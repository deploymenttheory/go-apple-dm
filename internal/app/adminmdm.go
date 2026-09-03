package app

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// mdmAdminRoutes are the classic-MDM admin routes: the enrollments the
// server manages, the commands queued for them, the push certificates that
// wake them, and the export and import that move them between deployments.
//
//	GET    /enrollments                                 list, filtered and paged
//	GET    /enrollments/{channel}/{id}                  one enrollment
//	DELETE /enrollments/{channel}/{id}                  disable it
//	POST   /enrollments/{channel}/{id}/commands         enqueue a command
//	GET    /enrollments/{channel}/{id}/commands         read the queue
//	DELETE /enrollments/{channel}/{id}/commands         clear the queue
//	POST   /enrollments/{channel}/{id}/push             wake the device now
//	GET    /pushcerts                                   topics and expiry
//	PUT    /pushcerts                                   upload or renew one
//	GET    /export                                      paged enrollment export
//	POST   /import                                      import one record
//
// DDM rides on MDM in the protocol, so the enrollment is the root object and
// DDM's /enrollments/{channel}/{id}/sets and /declarations are sub-resources
// of it (decision record 0039).
func (a *App) mdmAdminRoutes() []adminRoute {
	var routes []adminRoute
	add := func(action, pattern string, fn http.HandlerFunc) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: action, Family: "mdm", Handler: fn})
	}

	add(ActionReadEnrollment, "GET /enrollments", a.listEnrollments)
	add(ActionReadEnrollment, "GET /enrollments/{channel}/{id}", a.getEnrollment)
	add(ActionDisableEnrollment, "DELETE /enrollments/{channel}/{id}", a.disableEnrollment)
	add(ActionEnqueueCommand, "POST /enrollments/{channel}/{id}/commands", a.enqueueCommand)
	add(ActionReadCommands, "GET /enrollments/{channel}/{id}/commands", a.listCommands)
	add(ActionClearCommands, "DELETE /enrollments/{channel}/{id}/commands", a.clearCommands)
	add(ActionPushEnrollment, "POST /enrollments/{channel}/{id}/push", a.pushEnrollment)
	add(ActionManagePushCerts, "GET /pushcerts", a.listPushCerts)
	add(ActionManagePushCerts, "PUT /pushcerts", a.putPushCert)
	add(ActionExportEnrollments, "GET /export", a.exportEnrollments)
	add(ActionImportEnrollments, "POST /import", a.importEnrollment)
	return routes
}

// page reads the shared cursor and limit parameters every listing accepts.
func page(r *http.Request) (storage.Page, error) {
	p := storage.Page{Cursor: r.URL.Query().Get("cursor")}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return storage.Page{}, fmt.Errorf("%w: limit %q", storage.ErrInvalid, v)
		}
		p.Limit = n
	}
	return p, nil
}

// storageStatus maps the storage sentinels to statuses, keeping the cause out
// of the body: a backend failure is not the caller's business.
func (a *App) storageStatus(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, storage.ErrInvalid), errors.Is(err, ErrBadChannel):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, storage.ErrConflict):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, storage.ErrDisabled):
		writeError(w, http.StatusGone, err)
	default:
		a.cfg.Logger.WarnContext(r.Context(), "app: admin mdm", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("app: storage unavailable"))
	}
}

func (a *App) listEnrollments(w http.ResponseWriter, r *http.Request) {
	p, err := page(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	q := storage.EnrollmentQuery{
		ParentID: r.URL.Query().Get("parent"),
		Serial:   r.URL.Query().Get("serial"),
	}
	if v := r.URL.Query().Get("channel"); v != "" {
		ch, err := channelFromName(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		q.Channel = ch
	}
	if v := r.URL.Query().Get("enabled"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%w: enabled %q", storage.ErrInvalid, v))
			return
		}
		q.Enabled = &b
	}
	res, err := a.Store.List(r.Context(), q, p)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	items := make([]enrollmentView, 0, len(res.Items))
	for _, e := range res.Items {
		items = append(items, viewEnrollment(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "NextCursor": res.NextCursor})
}

func (a *App) getEnrollment(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	e, err := a.Store.Get(r.Context(), id)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewEnrollment(*e))
}

func (a *App) disableEnrollment(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.Store.Disable(r.Context(), id, a.cfg.Clock.Now()); err != nil {
		a.storageStatus(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// enqueueCommand takes the command's plist exactly as it would go to the
// device, so an operator can send anything the schema describes without this
// package growing a case per RequestType.
func (a *App) enqueueCommand(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(body) > MaxAdminBody {
		writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
		return
	}
	cmd, err := mdm.DecodeCommand(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("app: command: %w", err))
		return
	}
	res, err := a.Core.Enqueue(r.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{})
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	// A target the server refused is reported rather than silently dropped:
	// Core.Enqueue screens against schema/support, so "queued: 0" with a
	// reason is the useful answer.
	out := map[string]any{"CommandUUID": cmd.UUID, "Queued": len(res.Queued)}
	if reason, ok := res.Skipped[id]; ok {
		out["Skipped"] = reason.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) listCommands(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, err := page(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	res, err := a.Store.Commands(r.Context(), id,
		storage.CommandQuery{RequestType: r.URL.Query().Get("type")}, p)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	items := make([]queuedView, 0, len(res.Items))
	for _, q := range res.Items {
		items = append(items, viewQueued(q))
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "NextCursor": res.NextCursor})
}

func (a *App) clearCommands(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	n, err := a.Store.Clear(r.Context(), id,
		storage.ClearFilter{RequestType: r.URL.Query().Get("type")})
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Cleared": n})
}

// pushEnrollment wakes one device now. The queue is unchanged: this is the
// "the device has not checked in" lever, not a way to send anything.
func (a *App) pushEnrollment(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if a.Push == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("app: no push source configured"))
		return
	}
	results, err := a.Push.Notify(r.Context(), []mdm.EnrollmentID{id})
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	res := results[id]
	out := map[string]any{"Sent": res.Sent, "Invalid": res.Invalid}
	if res.Status != 0 {
		out["Status"] = res.Status
	}
	if res.Reason != "" {
		out["Reason"] = res.Reason
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) listPushCerts(w http.ResponseWriter, r *http.Request) {
	certs, err := a.Store.PushCerts(r.Context())
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	// The private key never leaves the server, so the view is the topic and
	// what an operator needs to plan a renewal.
	items := make([]pushCertView, 0, len(certs))
	for _, c := range certs {
		items = append(items, pushCertView{
			Topic: c.Topic, NotAfter: c.NotAfter, Version: c.Version, UpdatedAt: c.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items})
}

func (a *App) putPushCert(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(body) > MaxAdminBody {
		writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
		return
	}
	var in struct{ Topic, CertPEM, KeyPEM string }
	if err := json.Unmarshal(body, &in); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("app: push certificate: %w", err))
		return
	}
	if in.CertPEM == "" || in.KeyPEM == "" {
		writeError(w, http.StatusBadRequest, errors.New("app: push certificate needs CertPEM and KeyPEM"))
		return
	}
	cert, err := a.Store.StorePushCert(r.Context(), in.Topic, []byte(in.CertPEM), []byte(in.KeyPEM), a.cfg.Clock.Now())
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pushCertView{
		Topic: cert.Topic, NotAfter: cert.NotAfter, Version: cert.Version, UpdatedAt: cert.UpdatedAt,
	})
}

func (a *App) exportEnrollments(w http.ResponseWriter, r *http.Request) {
	p, err := page(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	res, err := a.Core.ExportEnrollments(r.Context(), p)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": res.Items, "NextCursor": res.NextCursor})
}

func (a *App) importEnrollment(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(body) > MaxAdminBody {
		writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
		return
	}
	var rec storage.EnrollmentExport
	if err := json.Unmarshal(body, &rec); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("app: enrollment record: %w", err))
		return
	}
	if err := a.Core.ImportEnrollment(r.Context(), rec); err != nil {
		a.storageStatus(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// channelFromName parses the channel filter, accepting the same names the
// path uses.
func channelFromName(s string) (mdm.Channel, error) {
	for _, c := range []mdm.Channel{
		mdm.ChannelDevice, mdm.ChannelUser, mdm.ChannelSharedIPadUser,
		mdm.ChannelUserEnrollmentDevice, mdm.ChannelUserEnrollmentUser,
	} {
		if c.String() == s {
			return c, nil
		}
	}
	return mdm.ChannelUnknown, fmt.Errorf("%w: %q", ErrBadChannel, s)
}

// enrollmentView is the wire shape. It carries no unlock token, no bootstrap
// token and no raw check-in plists: those are secrets and evidence, not
// inventory.
type enrollmentView struct {
	Channel        string
	ID             string
	ParentID       string `json:",omitempty"`
	Enabled        bool
	Topic          string    `json:",omitempty"`
	SerialNumber   string    `json:",omitempty"`
	Model          string    `json:",omitempty"`
	ProductName    string    `json:",omitempty"`
	OSVersion      string    `json:",omitempty"`
	BuildVersion   string    `json:",omitempty"`
	UserShortName  string    `json:",omitempty"`
	LastSeenAt     time.Time `json:",omitempty"`
	EnrolledAt     time.Time `json:",omitempty"`
	TokenUpdatedAt time.Time `json:",omitempty"`
	DisabledAt     time.Time `json:",omitempty"`
}

func viewEnrollment(e storage.Enrollment) enrollmentView {
	return enrollmentView{
		Channel: e.ID.Channel.String(), ID: e.ID.ID, ParentID: e.ID.ParentID,
		Enabled: e.Enabled, Topic: e.Push.Topic,
		SerialNumber: e.Device.SerialNumber, Model: e.Device.Model,
		ProductName: e.Device.ProductName, OSVersion: e.Device.OSVersion,
		BuildVersion: e.Device.BuildVersion, UserShortName: e.UserShortName,
		LastSeenAt: e.LastSeenAt, EnrolledAt: e.EnrolledAt,
		TokenUpdatedAt: e.TokenUpdatedAt, DisabledAt: e.DisabledAt,
	}
}

// queuedView omits the command plist and the device's raw response: the
// identifiers and the outcome are what a queue listing is for.
type queuedView struct {
	CommandUUID string
	RequestType string
	State       string
	EnqueuedAt  time.Time
	LastSentAt  time.Time `json:",omitempty"`
	CompletedAt time.Time `json:",omitempty"`
	Attempts    int
	NotNowCount int
	Status      string  `json:",omitempty"`
	ErrorCodes  []int64 `json:",omitempty"`
}

func viewQueued(q storage.QueuedCommand) queuedView {
	v := queuedView{
		CommandUUID: q.Command.UUID, RequestType: q.Command.RequestType,
		State: string(q.State), EnqueuedAt: q.EnqueuedAt, LastSentAt: q.LastSentAt,
		CompletedAt: q.CompletedAt, Attempts: q.Attempts, NotNowCount: q.NotNowCount,
	}
	if q.Result != nil {
		v.Status = string(q.Result.Status)
		for _, e := range q.Result.ErrorChain {
			v.ErrorCodes = append(v.ErrorCodes, e.ErrorCode)
		}
	}
	return v
}

type pushCertView struct {
	Topic     string
	NotAfter  time.Time
	Version   int64
	UpdatedAt time.Time
}
