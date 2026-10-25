package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/status"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// contentResolver uses the explicitly addressed native enrollment, without Apple
// inventory or cross-source identifier matching. Unknown identity fails closed.
type contentResolver struct{ a *App }

func (v contentResolver) Resolve(ctx context.Context, s managedcontent.Selection) ([]managedcontent.Target, error) {
	if expected, ok := ctx.Value(contentIdentityKey{}).(contentIdentity); ok {
		actual, err := v.a.nativeContentIdentity(ctx, s)
		if err != nil {
			return nil, err
		}
		if actual != expected {
			return nil, managedcontent.ErrTarget
		}
	}
	e, err := v.a.Store.Get(ctx, s.Enrollment)
	if err != nil {
		return nil, err
	}
	if !e.Enabled {
		return nil, managedcontent.ErrTarget
	}
	device := e
	if e.ID.Channel.IsUser() {
		ch := mdm.ChannelDevice
		if e.ID.Channel == mdm.ChannelUserEnrollmentUser {
			ch = mdm.ChannelUserEnrollmentDevice
		}
		device, err = v.a.Store.Get(ctx, mdm.EnrollmentID{Channel: ch, ID: e.ID.ParentID})
		if err != nil {
			return nil, err
		}
	}
	if !device.Enabled || device.CertHash == "" {
		return nil, managedcontent.ErrTarget
	}
	target, err := service.EnrollmentTarget(ctx, v.a.Store, e.ID)
	if err != nil {
		return nil, err
	}
	serial := device.Device.SerialNumber
	if target.UserEnrollment {
		serial = ""
	}
	if s.Serial != "" && s.Serial != serial {
		return nil, managedcontent.ErrTarget
	}
	scope := e.ID.ID
	clientUser := ""
	if s.ClientUserID != "" {
		record, err := v.a.protocol.Get(ctx, contentUserKey(e.ID))
		if err != nil {
			return nil, managedcontent.ErrTarget
		}
		var binding contentUserBinding
		if json.Unmarshal(record.Value, &binding) != nil || binding.Enrollment != e.ID || binding.CertHash != device.CertHash || binding.ClientUserID != s.ClientUserID || binding.Principal != e.EnrollmentUserID+"/"+e.UserShortName {
			return nil, managedcontent.ErrTarget
		}
		clientUser = binding.ClientUserID
	}
	_, capErr := v.a.Engine.ClientCapabilities(ctx, e.ID)
	if capErr != nil && !errors.Is(capErr, ddm.ErrNotFound) {
		return nil, capErr
	}
	return []managedcontent.Target{{Enrollment: e.ID, Serial: serial, ClientUserID: clientUser, ScopeID: scope, Authenticated: true, AllowAppInstall: true, DDMEnabled: capErr == nil, Support: target, Context: managedcontent.PlatformContext{OS: target.OS, Version: target.Version.String(), Channel: target.Channel, Supervised: target.Supervised, SharedIPad: target.SharedIPad, UserEnrollment: target.UserEnrollment, ADE: target.DEP, UserApproved: target.UserApproved}}}, nil
}

type contentUserBinding struct {
	Enrollment   mdm.EnrollmentID `json:"enrollment"`
	ClientUserID string           `json:"clientUserId"`
	Principal    string           `json:"principal"`
	CertHash     string           `json:"certHash"`
}

func contentUserKey(id mdm.EnrollmentID) string {
	b, _ := json.Marshal(id)
	return fmt.Sprintf("managedcontent/user/%x", sha256.Sum256(b))
}

// contentDelivery binds immutable SDK dispatch to the server's native queue and
// declaration publication in one shared SQL transaction. Durable receipts survive
// queue pruning and prevent a completed dispatch from being submitted again.
type contentDelivery struct{ a *App }

func deliveryKey(id string) string {
	return fmt.Sprintf("managedcontent/native/%x", sha256.Sum256([]byte(id)))
}

type contentDispatchRecord struct {
	Dispatch  managedcontent.Dispatch `json:"dispatch"`
	CreatedAt time.Time               `json:"createdAt"`
}

func (v contentDelivery) Submit(ctx context.Context, d managedcontent.Dispatch) (managedcontent.Observation, error) {
	var out managedcontent.Observation
	err := (sqlcommon.UnitOfWork{DB: v.a.db, Dialect: v.a.dialect}).Run(ctx, func(ctx context.Context) error {
		targets, err := (contentResolver{v.a}).Resolve(ctx, managedcontent.Selection{Enrollment: d.Enrollment, Serial: d.Serial, ClientUserID: d.ClientUserID})
		if err != nil {
			return err
		}
		if len(targets) != 1 || (d.Serial != "" && targets[0].Serial != d.Serial) || targets[0].ClientUserID != d.ClientUserID {
			return managedcontent.ErrTarget
		}
		key := deliveryKey(d.ID)
		return v.a.protocol.Update(ctx, []string{key}, func(tx state.Tx) error {
			prior, err := tx.Get(ctx, key)
			if err == nil {
				var saved contentDispatchRecord
				if json.Unmarshal(prior.Value, &saved) != nil || !reflect.DeepEqual(saved.Dispatch, d) {
					return managedcontent.ErrConflict
				}
				out = dispatchObservation(d)
				out.Found = true
				return nil
			}
			if !errors.Is(err, state.ErrNotFound) {
				return err
			}
			if err := v.rejectNativeContentConflict(ctx, tx, d); err != nil {
				return err
			}
			if d.Predecessor != nil {
				prior, err := tx.Get(ctx, deliveryKey(d.Predecessor.ID))
				if err != nil {
					return err
				}
				var saved contentDispatchRecord
				if json.Unmarshal(prior.Value, &saved) != nil || saved.Dispatch.Digest != d.Predecessor.Digest || saved.Dispatch.Enrollment != d.Enrollment || saved.Dispatch.Method != d.Method {
					return managedcontent.ErrConflict
				}
				observation, err := v.inspectNative(ctx, saved.Dispatch, saved.CreatedAt)
				if err != nil {
					return err
				}
				if !observation.Terminal {
					return managedcontent.ErrConflict
				}
			}
			if d.Method == managedcontent.MDM {
				cmd, err := mdm.DecodeCommand(d.MDM)
				if err != nil {
					return err
				}
				if cmd.UUID != d.ID {
					return managedcontent.ErrConflict
				}
				res, err := v.a.Core.Enqueue(ctx, []mdm.EnrollmentID{d.Enrollment}, cmd, storage.EnqueueOptions{DedupeKey: "managedcontent:" + d.ID})
				if err != nil {
					return err
				}
				if e := res.Skipped[d.Enrollment]; e != nil {
					return e
				}
			} else if d.Method == managedcontent.DDM {
				if len(d.Declarations) != 2 {
					return managedcontent.ErrProtocol
				}
				publication := ddm.SetPublication{Name: "managedcontent:" + d.Declarations[0].Identifier}
				for _, doc := range d.Declarations {
					publication.Declarations = append(publication.Declarations, jsontext.Value(doc.Raw))
				}
				if _, err := v.a.Engine.PublishSet(ctx, publication); err != nil {
					return err
				}
				if _, err := v.a.Engine.AssignSet(ctx, d.Enrollment, publication.Name); err != nil {
					return err
				}
			} else {
				return managedcontent.ErrUnsupported
			}
			raw, err := json.Marshal(contentDispatchRecord{Dispatch: d, CreatedAt: tx.Now()})
			if err != nil {
				return err
			}
			if err := tx.Put(ctx, state.Record{Key: key, Value: raw}); err != nil {
				return err
			}
			out = dispatchObservation(d)
			out.Found = true
			out.DeclarationPublished = d.Method == managedcontent.DDM
			return nil
		})
	})
	return out, err
}

func dispatchObservation(d managedcontent.Dispatch) managedcontent.Observation {
	return managedcontent.Observation{ID: d.ID, Enrollment: d.Enrollment, Method: d.Method, Digest: d.Digest, ManagementMethod: d.Method, Authoritative: true, OwnsIdentity: true, RetrySafe: true}
}

func (v contentDelivery) Inspect(ctx context.Context, d managedcontent.Dispatch) (managedcontent.Observation, error) {
	var out managedcontent.Observation
	if _, err := (contentResolver{v.a}).Resolve(ctx, managedcontent.Selection{Enrollment: d.Enrollment, Serial: d.Serial, ClientUserID: d.ClientUserID}); err != nil {
		return out, err
	}
	err := (sqlcommon.UnitOfWork{DB: v.a.db, Dialect: v.a.dialect}).Run(ctx, func(ctx context.Context) error {
		record, err := v.a.protocol.Get(ctx, deliveryKey(d.ID))
		if errors.Is(err, state.ErrNotFound) {
			out = dispatchObservation(d)
			return nil
		}
		if err != nil {
			return err
		}
		var saved contentDispatchRecord
		if json.Unmarshal(record.Value, &saved) != nil || !reflect.DeepEqual(saved.Dispatch, d) {
			return managedcontent.ErrConflict
		}
		out, err = v.inspectNative(ctx, d, saved.CreatedAt)
		return err
	})
	return out, err
}

// inspectNative reports native processing independently of actual installation.
// An MDM acknowledgment is never converted into Terminal or Installed evidence.
func (v contentDelivery) inspectNative(ctx context.Context, d managedcontent.Dispatch, created time.Time) (managedcontent.Observation, error) {
	out := dispatchObservation(d)
	out.Found = true
	out.ObservedAt = v.a.cfg.Clock.Now()
	if d.Method == managedcontent.DDM {
		for _, doc := range d.Declarations {
			decl, err := v.a.Engine.GetDeclaration(ctx, doc.Identifier)
			if err != nil {
				return out, err
			}
			if decl.ServerToken != doc.ServerToken {
				return out, managedcontent.ErrConflict
			}
		}
		out.DeclarationPublished = true
		statuses, err := v.a.Engine.DeclarationStatus(ctx, d.Enrollment)
		if err != nil {
			return out, err
		}
		var tokenSeen time.Time
		for _, s := range statuses {
			if s.Identifier == d.Declarations[0].Identifier && s.ServerToken == d.Declarations[0].ServerToken && !s.LastSeen.Before(created) {
				tokenSeen = s.LastSeen
			}
		}
		if tokenSeen.IsZero() {
			return out, nil
		}
		values, err := v.a.Engine.StatusValues(ctx, d.Enrollment, ddm.StatusValueQuery{PathPrefix: "app.managed.list"}, paging.Page{Limit: 1000})
		if err != nil {
			return out, err
		}
		historical, bundle, err := v.declarativeContentHistory(ctx, d, created)
		if err != nil {
			return out, err
		}
		out.Terminal = historical
		for _, value := range values.Items {
			if value.Path != "app.managed.list" || value.LastSeen.Before(tokenSeen) {
				continue
			}
			var apps status.AppManagedList
			if json.Unmarshal(value.Value, &apps) != nil {
				return out, managedcontent.ErrProtocol
			}
			out.ObservedAt = value.LastSeen
			out.Raw = value.Value
			for _, app := range apps.Value {
				if (app.DeclarationIdentifier == nil || *app.DeclarationIdentifier != d.Declarations[0].Identifier) && (bundle == "" || app.Identifier != bundle) {
					continue
				}
				out.ObservedAt = value.LastSeen
				out.Raw = value.Value
				if app.State != nil {
					out.Terminal = out.Terminal || *app.State == "managed" || *app.State == "failed" || *app.State == "managed-but-uninstalled"
					out.Installed = *app.State == "managed"
				}
				if app.Removed != nil && *app.Removed {
					out.Installed = false
				}
				if out.Installed && app.Version != nil {
					out.AppVersion = *app.Version
				}
			}
		}
		return out, nil
	}
	rows, err := v.nativeCommands(ctx, d.Enrollment)
	if err != nil {
		return out, err
	}
	for _, q := range rows {
		if q.Command.UUID != d.ID {
			continue
		}
		out.CommandAcknowledged = q.State == storage.StateAcknowledged
		if q.Result == nil {
			return out, nil
		}
		out.Raw = q.Result.Raw
		decoded, err := mdm.DecodeResponse(q.Result.Raw, q.Command.RequestType)
		if err != nil {
			return out, err
		}
		out.ObservedAt = q.CompletedAt
		switch payload := decoded.Payload.(type) {
		case *commands.InstallApplicationResponse:
			if payload.State != nil {
				out.Terminal = *payload.State == "Managed" || *payload.State == "ManagedButUninstalled" || *payload.State == "Failed" || *payload.State == "UserRejected"
			}
			if payload.Identifier != nil && *payload.Identifier != "" {
				return currentNativeApplication(out, *payload.Identifier, rows, q.CompletedAt)
			}
		case *commands.InstallMediaResponse:
			if payload.State != nil {
				out.Terminal = *payload.State == "Managed" || *payload.State == "Installed" || *payload.State == "Failed"
			}
		}
		return out, nil
	}
	// The immutable receipt proves previous acceptance even after queue pruning.
	return out, nil
}

func (v contentDelivery) nativeCommands(ctx context.Context, id mdm.EnrollmentID) ([]storage.QueuedCommand, error) {
	var out []storage.QueuedCommand
	after := ""
	for {
		page, err := v.a.Store.Commands(ctx, id, storage.CommandQuery{}, paging.Page{Limit: 100, Cursor: after})
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" {
			return out, nil
		}
		after = page.NextCursor
	}
}

// currentNativeApplication joins the returned bundle identity from the exact
// install command with an independently authenticated, applicable native query.
// Historical terminal processing survives a later empty/uninstalled report.
func currentNativeApplication(out managedcontent.Observation, bundle string, rows []storage.QueuedCommand, since time.Time) (managedcontent.Observation, error) {
	var latest *storage.QueuedCommand
	for i := range rows {
		q := &rows[i]
		if q.Command.RequestType != "InstalledApplicationList" || q.State != storage.StateAcknowledged || q.Result == nil || q.CompletedAt.Before(since) {
			continue
		}
		decoded, err := mdm.DecodeCommand(q.Command.Raw)
		if err != nil {
			return out, err
		}
		request, ok := decoded.Payload.(*commands.InstalledApplicationList)
		if !ok {
			return out, managedcontent.ErrProtocol
		}
		if len(request.Identifiers) > 0 && !slices.Contains(request.Identifiers, bundle) {
			continue
		}
		if latest == nil || latest.CompletedAt.Before(q.CompletedAt) {
			latest = q
		}
	}
	if latest == nil {
		return out, nil
	}
	response, err := mdm.DecodeResponse(latest.Result.Raw, "InstalledApplicationList")
	if err != nil {
		return out, err
	}
	payload, ok := response.Payload.(*commands.InstalledApplicationListResponse)
	if !ok {
		return out, managedcontent.ErrProtocol
	}
	out.ObservedAt = latest.CompletedAt
	for _, app := range payload.InstalledApplicationList {
		if app.Identifier == nil || *app.Identifier != bundle {
			continue
		}
		out.Installed = app.Installing != nil && !*app.Installing
		if out.Installed && app.Version != nil {
			out.AppVersion = *app.Version
		}
	}
	raw, err := json.Marshal(struct {
		BundleID             string `json:"bundleId"`
		InstallationResponse []byte `json:"installationResponse"`
		CurrentReport        []byte `json:"currentReport"`
	}{bundle, out.Raw, latest.Result.Raw})
	if err != nil {
		return out, err
	}
	out.Raw = raw
	return out, nil
}

// declarativeContentHistory retains terminal processing independently of later
// removals, using authenticated native reports from this dispatch generation.
func (v contentDelivery) declarativeContentHistory(ctx context.Context, d managedcontent.Dispatch, created time.Time) (bool, string, error) {
	after := ""
	terminal := false
	bundle := ""
	for {
		page, err := v.a.Engine.StatusReports(ctx, d.Enrollment, paging.Page{Limit: 100, Cursor: after})
		if err != nil {
			return false, "", err
		}
		for _, record := range page.Items {
			if record.ReceivedAt.Before(created) {
				continue
			}
			var report struct {
				StatusItems struct {
					App struct {
						Managed struct {
							List []status.AppManagedListApp `json:"list"`
						} `json:"managed"`
					} `json:"app"`
				} `json:"StatusItems"`
			}
			if json.Unmarshal(record.Raw, &report) != nil {
				return false, "", managedcontent.ErrProtocol
			}
			for _, app := range report.StatusItems.App.Managed.List {
				if app.DeclarationIdentifier == nil || *app.DeclarationIdentifier != d.Declarations[0].Identifier {
					continue
				}
				if bundle == "" {
					bundle = app.Identifier
				}
				if app.State != nil && (*app.State == "managed" || *app.State == "failed" || *app.State == "managed-but-uninstalled") {
					terminal = true
				}
			}
		}
		if page.NextCursor == "" {
			return terminal, bundle, nil
		}
		after = page.NextCursor
	}
}

// rejectNativeContentConflict refuses to adopt an existing foreign native
// command/declaration or change the management method behind explicit SDK intent.
func (v contentDelivery) rejectNativeContentConflict(ctx context.Context, reader state.Reader, d managedcontent.Dispatch) error {
	rows, err := v.nativeCommands(ctx, d.Enrollment)
	if err != nil {
		return err
	}
	for _, q := range rows {
		if q.Command.UUID == d.ID {
			return managedcontent.ErrConflict
		}
		if q.Command.RequestType != "InstallApplication" {
			continue
		}
		cmd, err := mdm.DecodeCommand(q.Command.Raw)
		if err != nil {
			return err
		}
		payload, ok := cmd.Payload.(*commands.InstallApplication)
		if !ok || payload.ITunesStoreID == nil || fmt.Sprint(*payload.ITunesStoreID) != d.Content.Asset.AdamID {
			continue
		}
		if _, err := reader.Get(ctx, deliveryKey(q.Command.UUID)); err != nil {
			return managedcontent.ErrConflict
		}
		if d.Method != managedcontent.MDM {
			return managedcontent.ErrConflict
		}
	}
	ids, err := v.a.Engine.EnrollmentDeclarations(ctx, d.Enrollment)
	if err != nil {
		return err
	}
	for _, id := range ids {
		decl, err := v.a.Engine.GetDeclaration(ctx, id)
		if err != nil {
			return err
		}
		if decl.Type != "com.apple.configuration.app.managed" {
			continue
		}
		var envelope struct {
			Payload struct {
				AppStoreID string `json:"AppStoreID"`
			} `json:"Payload"`
		}
		if json.Unmarshal(decl.Canonical, &envelope) != nil {
			return managedcontent.ErrProtocol
		}
		if envelope.Payload.AppStoreID != d.Content.Asset.AdamID {
			continue
		}
		if d.Method != managedcontent.DDM || d.Predecessor == nil {
			return managedcontent.ErrConflict
		}
		owned := false
		for _, doc := range d.Declarations {
			if doc.Identifier == id {
				owned = true
			}
		}
		if !owned {
			return managedcontent.ErrConflict
		}
	}
	return nil
}
