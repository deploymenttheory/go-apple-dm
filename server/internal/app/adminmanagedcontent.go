package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

const (
	ActionReadAppsBooks              = "readAppsBooks"
	ActionManageAppsBooks            = "manageAppsBooks"
	ActionReadAppsBooksEvidence      = "readAppsBooksEvidence"
	ActionReadManagedContent         = "readManagedContent"
	ActionManageManagedContent       = "manageManagedContent"
	ActionReadManagedContentEvidence = "readManagedContentEvidence"
)

// managedContentActions requires individually authored grants for every capability.
func managedContentActions() []adminauth.Action {
	var out []adminauth.Action
	for _, s := range []struct {
		id, help  string
		sensitive bool
	}{
		{ActionReadAppsBooks, "Inspect safe Apps and Books location and licensing job status.", false},
		{ActionManageAppsBooks, "Register tokens, explicitly configure ownership and submit or reconcile licensing operations.", true},
		{ActionReadAppsBooksEvidence, "Read private source configuration, licensing requests and notification evidence.", true},
		{ActionReadManagedContent, "Inspect managed content intent and safe delivery observations.", false},
		{ActionManageManagedContent, "Queue, advance, cancel or reconcile explicitly selected managed content operations.", true},
		{ActionReadManagedContentEvidence, "Read private licensing, wire payload and native delivery evidence.", true},
	} {
		out = append(out, adminauth.Action{ID: s.id, Help: s.help, Resource: adminauth.EntitySystem, Sensitive: s.sensitive})
	}
	return out
}

// contentError returns stable errors without exposing remote bodies or credentials.
func contentError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, state.ErrNotFound), errors.Is(err, storage.ErrNotFound), errors.Is(err, ddm.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, appsbooks.ErrNotificationAuth):
		status = http.StatusUnauthorized
	case errors.Is(err, appsbooks.ErrInput), errors.Is(err, appsbooks.ErrConfig), errors.Is(err, managedcontent.ErrInput), errors.Is(err, managedcontent.ErrUnsupported):
		status = http.StatusBadRequest
	case errors.Is(err, ddm.ErrConflict), errors.Is(err, appsbooks.ErrPollEarly), errors.Is(err, appsbooks.ErrRevision), errors.Is(err, appsbooks.ErrOwnership), errors.Is(err, appsbooks.ErrConfigurationPending), errors.Is(err, appsbooks.ErrJobBusy), errors.Is(err, appsbooks.ErrReconciliation), errors.Is(err, managedcontent.ErrBusy), errors.Is(err, managedcontent.ErrConflict), errors.Is(err, managedcontent.ErrReconciliation):
		status = http.StatusConflict
	}
	writeError(w, status, errors.New(http.StatusText(status)))
}

type contentWorkerInput struct {
	Worker       string `json:"worker"`
	LeaseSeconds int    `json:"leaseSeconds"`
	Note         string `json:"note"`
	RetryAbsent  bool   `json:"retryAbsent"`
}

// managedContentRoutes composes SDK operations without remote calls inside local audit transactions.
func (a *App) managedContentRoutes() []adminRoute {
	if a.AppsBooks == nil {
		return nil
	}
	var routes []adminRoute
	add := func(pattern, action string, local bool, fn func(*http.Request) (any, error)) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: action, Family: "managed-content", LocalMutation: local, MaxResponseBytes: 64 << 20, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			v, err := fn(r)
			if err != nil {
				contentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, v)
		})})
	}
	add("PUT /managed-content/users", ActionManageManagedContent, true, func(r *http.Request) (any, error) {
		var in struct {
			Enrollment   mdm.EnrollmentID `json:"enrollment"`
			ClientUserID string           `json:"clientUserId"`
		}
		if err := decodeContentBody(r, &in); err != nil {
			return nil, err
		}
		return nil, a.bindContentUser(r.Context(), in.Enrollment, in.ClientUserID)
	})
	add("GET /managed-content/operations", ActionReadManagedContent, false, func(r *http.Request) (any, error) {
		items, next, err := a.ManagedContent.Operations(r.Context(), r.URL.Query().Get("cursor"), 100)
		return struct {
			Items      []managedcontent.Operation
			NextCursor string
		}{items, next}, err
	})
	base := "/appsbooks/locations/{location}"
	add("GET "+base, ActionReadAppsBooks, false, func(r *http.Request) (any, error) { return a.AppsBooks.Load(r.Context(), r.PathValue("location")) })
	add("GET "+base+"/evidence", ActionReadAppsBooksEvidence, false, func(r *http.Request) (any, error) {
		_, e, err := a.AppsBooks.LoadEvidence(r.Context(), r.PathValue("location"))
		return e, err
	})
	for _, verb := range []string{"register", "recover-configuration", "recover-licensing"} {
		pattern := "POST " + base + "/" + verb
		if verb == "register" {
			pattern = "PUT " + base
		}
		add(pattern, ActionManageAppsBooks, false, func(r *http.Request) (any, error) {
			var in struct {
				Revision uint64 `json:"revision"`
				SToken   string `json:"sToken"`
				JobID    string `json:"jobId"`
			}
			if err := decodeContentBody(r, &in); err != nil {
				return nil, err
			}
			switch verb {
			case "recover-configuration":
				return a.AppsBooks.RecoverConfiguration(r.Context(), r.PathValue("location"), in.Revision, in.SToken)
			case "recover-licensing":
				return a.AppsBooks.RecoverLicensingToken(r.Context(), r.PathValue("location"), in.Revision, in.JobID, in.SToken)
			default:
				return a.AppsBooks.Register(r.Context(), r.PathValue("location"), in.Revision, in.SToken)
			}
		})
	}
	add("PUT "+base+"/receiver", ActionManageAppsBooks, true, func(r *http.Request) (any, error) {
		var in struct {
			Token string `json:"token"`
		}
		if err := decodeContentBody(r, &in); err != nil {
			return nil, err
		}
		return nil, a.saveReceiver(r.Context(), r.PathValue("location"), in.Token)
	})
	for _, verb := range []string{"configure", "refresh", "probe"} {
		add("POST "+base+"/"+verb, ActionManageAppsBooks, false, func(r *http.Request) (any, error) {
			var in struct {
				Revision      uint64                               `json:"revision"`
				MaxPages      int                                  `json:"maxPages"`
				Configuration appsbooks.ClientConfigurationRequest `json:"configuration"`
			}
			if err := decodeContentBody(r, &in); err != nil {
				return nil, err
			}
			id := r.PathValue("location")
			switch verb {
			case "configure":
				// Receiver rotation is separately audited local intent; configuration cannot silently replace it.
				if in.Configuration.NotificationAuthToken != "" {
					rec, err := a.protocol.Get(r.Context(), receiverKey(id))
					if err != nil {
						return nil, err
					}
					if string(rec.Value) != in.Configuration.NotificationAuthToken {
						return nil, appsbooks.ErrInput
					}
				}
				return a.AppsBooks.Configure(r.Context(), id, in.Revision, in.Configuration)
			case "refresh":
				return a.AppsBooks.Refresh(r.Context(), id, in.Revision, in.MaxPages)
			default:
				return a.AppsBooks.Probe(r.Context(), id, in.Revision)
			}
		})
	}
	add("GET "+base+"/notifications/{notification}/evidence", ActionReadAppsBooksEvidence, false, func(r *http.Request) (any, error) {
		return a.AppsBooks.LoadNotification(r.Context(), r.PathValue("location"), r.PathValue("notification"))
	})
	add("GET "+base+"/jobs", ActionReadAppsBooks, false, func(r *http.Request) (any, error) {
		items, next, err := a.AppsBooks.Jobs(r.Context(), r.PathValue("location"), r.URL.Query().Get("cursor"), 100)
		safe := make([]any, 0, len(items))
		for _, item := range items {
			safe = append(safe, safeJob(item))
		}
		return struct {
			Items      []any
			NextCursor string
		}{safe, next}, err
	})
	jobs := base + "/jobs/{job}"
	add("PUT "+jobs, ActionManageAppsBooks, true, func(r *http.Request) (any, error) {
		var in struct {
			Revision uint64               `json:"revision"`
			Request  appsbooks.JobRequest `json:"request"`
		}
		if err := decodeContentBody(r, &in); err != nil {
			return nil, err
		}
		j, err := a.AppsBooks.CreateJob(r.Context(), r.PathValue("location"), in.Revision, r.PathValue("job"), in.Request)
		return safeJob(j), err
	})
	add("GET "+jobs, ActionReadAppsBooks, false, func(r *http.Request) (any, error) {
		j, err := a.AppsBooks.LoadJob(r.Context(), r.PathValue("location"), r.PathValue("job"))
		return safeJob(j), err
	})
	add("GET "+jobs+"/evidence", ActionReadAppsBooksEvidence, false, func(r *http.Request) (any, error) {
		return a.AppsBooks.LoadJob(r.Context(), r.PathValue("location"), r.PathValue("job"))
	})
	for _, verb := range []string{"submit", "poll"} {
		add("POST "+jobs+"/"+verb, ActionManageAppsBooks, false, func(r *http.Request) (any, error) {
			var in contentWorkerInput
			if err := decodeContentBody(r, &in); err != nil {
				return nil, err
			}
			var j appsbooks.LicensingJob
			var err error
			if verb == "submit" {
				j, err = a.AppsBooks.SubmitJob(r.Context(), r.PathValue("location"), r.PathValue("job"), in.Worker, time.Duration(in.LeaseSeconds)*time.Second)
			} else {
				j, err = a.AppsBooks.PollJob(r.Context(), r.PathValue("location"), r.PathValue("job"), in.Worker, time.Duration(in.LeaseSeconds)*time.Second)
			}
			return safeJob(j), err
		})
	}
	add("POST "+jobs+"/reconcile", ActionManageAppsBooks, true, func(r *http.Request) (any, error) {
		var in struct {
			EventID string `json:"eventId"`
			Note    string `json:"note"`
			Abandon bool   `json:"abandon"`
		}
		if err := decodeContentBody(r, &in); err != nil {
			return nil, err
		}
		j, err := a.AppsBooks.ReconcileJob(r.Context(), r.PathValue("location"), r.PathValue("job"), in.EventID, in.Note, in.Abandon)
		return safeJob(j), err
	})
	ops := "/managed-content/operations/{operation}"
	add("GET "+ops, ActionReadManagedContent, false, func(r *http.Request) (any, error) {
		return a.ManagedContent.Load(r.Context(), r.PathValue("operation"))
	})
	add("GET "+ops+"/evidence", ActionReadManagedContentEvidence, false, func(r *http.Request) (any, error) {
		_, e, err := a.ManagedContent.LoadEvidence(r.Context(), r.PathValue("operation"))
		return e, err
	})
	add("PUT "+ops, ActionManageManagedContent, true, func(r *http.Request) (any, error) {
		var in struct {
			Mode managedcontent.Mode `json:"mode"`
			Plan managedcontent.Plan `json:"plan"`
		}
		if err := decodeContentBody(r, &in); err != nil {
			return nil, err
		}
		return a.queueContent(r.Context(), r.PathValue("operation"), in.Mode, in.Plan)
	})
	add("POST "+ops+"/cancel", ActionManageManagedContent, true, func(r *http.Request) (any, error) {
		return a.ManagedContent.Cancel(r.Context(), r.PathValue("operation"))
	})
	for _, verb := range []string{"step", "reconcile"} {
		add("POST "+ops+"/"+verb, ActionManageManagedContent, false, func(r *http.Request) (any, error) {
			var in contentWorkerInput
			if err := decodeContentBody(r, &in); err != nil {
				return nil, err
			}
			ctx, err := a.contentExecutionContext(r.Context(), r.PathValue("operation"))
			if err != nil {
				return nil, err
			}
			if verb == "step" {
				return a.ManagedContent.Step(ctx, r.PathValue("operation"), in.Worker, time.Duration(in.LeaseSeconds)*time.Second)
			}
			return a.ManagedContent.Reconcile(ctx, r.PathValue("operation"), in.Worker, time.Duration(in.LeaseSeconds)*time.Second, in.Note, in.RetryAbsent)
		})
	}
	return routes
}

// bindContentUser binds an explicit Apple user ID to a current authenticated
// enrollment principal. Hardware disclosure and Apple Business Manager are not
// prerequisites. Re-enrollment invalidates the binding through its certificate pin.
func (a *App) bindContentUser(ctx context.Context, id mdm.EnrollmentID, clientUser string) error {
	if clientUser == "" {
		return managedcontent.ErrInput
	}
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !e.Enabled {
		return managedcontent.ErrTarget
	}
	device := e
	if id.Channel.IsUser() {
		ch := mdm.ChannelDevice
		if id.Channel == mdm.ChannelUserEnrollmentUser {
			ch = mdm.ChannelUserEnrollmentDevice
		}
		device, err = a.Store.Get(ctx, mdm.EnrollmentID{Channel: ch, ID: id.ParentID})
		if err != nil {
			return err
		}
	}
	if !device.Enabled || device.CertHash == "" {
		return managedcontent.ErrTarget
	}
	raw, err := json.Marshal(contentUserBinding{Enrollment: id, ClientUserID: clientUser, CertHash: device.CertHash, Principal: e.EnrollmentUserID + "/" + e.UserShortName})
	if err != nil {
		return err
	}
	key := contentUserKey(id)
	return a.protocol.Update(ctx, []string{key}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: raw}) })
}
