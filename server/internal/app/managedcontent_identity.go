package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

type (
	contentIdentityKey struct{}
	contentIdentity    struct {
		Enrollment  mdm.EnrollmentID `json:"Enrollment"`
		Principal   string           `json:"Principal"`
		Certificate string           `json:"Certificate"`
		UserBinding string           `json:"UserBinding"`
	}
)

type contentAdmission struct {
	Identity contentIdentity     `json:"Identity"`
	Plan     managedcontent.Plan `json:"Plan"`
}

// nativeContentIdentity identifies the actual authenticated principal generation,
// independently of optional serial disclosure and Apple licensing aliases.
func (a *App) nativeContentIdentity(ctx context.Context, s managedcontent.Selection) (contentIdentity, error) {
	e, device, err := a.authenticatedContentEnrollment(ctx, s.Enrollment)
	if e == nil {
		return contentIdentity{}, err
	}
	out := contentIdentity{Enrollment: e.ID, Principal: e.EnrollmentUserID + "/" + e.UserShortName}
	if err != nil {
		return out, err
	}
	out.Certificate = device.CertHash
	if s.ClientUserID != "" {
		raw, err := a.contentUserBindingValue(ctx, e, device, s.ClientUserID)
		if err != nil {
			return out, err
		}
		out.UserBinding = string(raw)
	}
	return out, nil
}

// authenticatedContentEnrollment reads the selected native principal and its
// certificate-bearing device parent without requiring hardware disclosure.
func (a *App) authenticatedContentEnrollment(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, *storage.Enrollment, error) {
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !e.Enabled {
		return nil, nil, managedcontent.ErrTarget
	}
	device := e
	if e.ID.Channel.IsUser() {
		device, err = a.Store.Get(ctx, e.ID.Device())
		if err != nil {
			return e, nil, err
		}
	}
	if !device.Enabled || device.CertHash == "" {
		return e, device, managedcontent.ErrTarget
	}
	return e, device, nil
}

// contentUserBindingValue verifies an explicit licensing alias against the
// current native principal and certificate, retaining its sealed admission bytes.
func (a *App) contentUserBindingValue(ctx context.Context, e, device *storage.Enrollment, clientUser string) ([]byte, error) {
	record, err := a.protocol.Get(ctx, contentUserKey(e.ID))
	if err != nil {
		return nil, err
	}
	var binding contentUserBinding
	if json.Unmarshal(record.Value, &binding) != nil || binding.Enrollment != e.ID || binding.CertHash != device.CertHash || binding.ClientUserID != clientUser || binding.Principal != e.EnrollmentUserID+"/"+e.UserShortName {
		return nil, managedcontent.ErrTarget
	}
	return record.Value, nil
}

// queueContent pins the native authenticated principal before durable local
// intent. Binding or certificate rotation cannot redirect an existing operation.
func (a *App) queueContent(ctx context.Context, id string, mode managedcontent.Mode, p managedcontent.Plan) (managedcontent.Operation, error) {
	identity, err := a.nativeContentIdentity(ctx, p.Target)
	if err != nil {
		return managedcontent.Operation{}, err
	}
	admission := contentAdmission{Identity: identity, Plan: p}
	key := contentServerKey("admission", id)
	err = a.protocol.Update(ctx, []string{key}, func(tx state.Tx) error {
		record, err := tx.Get(ctx, key)
		if err == nil {
			var old contentAdmission
			if json.Unmarshal(record.Value, &old) != nil || !reflect.DeepEqual(old, admission) {
				return managedcontent.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		raw, err := json.Marshal(admission)
		if err != nil {
			return err
		}
		return tx.Put(ctx, state.Record{Key: key, Value: raw})
	})
	if err != nil {
		return managedcontent.Operation{}, err
	}
	switch mode {
	case managedcontent.AssignOnly:
		return a.ManagedContent.Assign(ctx, id, p)
	case managedcontent.InstallOnly:
		return a.ManagedContent.Install(ctx, id, p)
	case managedcontent.AssignAndInstall:
		return a.ManagedContent.AssignAndInstall(ctx, id, p)
	default:
		return managedcontent.Operation{}, managedcontent.ErrInput
	}
}

// contentExecutionContext restores the immutable admitted principal generation for execution.
func (a *App) contentExecutionContext(ctx context.Context, id string) (context.Context, error) {
	record, err := a.protocol.Get(ctx, contentServerKey("admission", id))
	if err != nil {
		return ctx, err
	}
	var admission contentAdmission
	if json.Unmarshal(record.Value, &admission) != nil {
		return ctx, managedcontent.ErrProtocol
	}
	return context.WithValue(ctx, contentIdentityKey{}, admission.Identity), nil
}

// lockContentEnrollment protects the admitted principal generation with the same
// native row locks used by authentication/reset. Parent precedes user, matching
// the native store's ordering. The caller owns the shared local transaction.
func (a *App) lockContentEnrollment(ctx context.Context, id mdm.EnrollmentID) error {
	locker, ok := a.Store.(interface {
		GetForUpdate(context.Context, mdm.EnrollmentID) (*storage.Enrollment, error)
	})
	if !ok {
		return managedcontent.ErrProtocol
	}
	if id.Channel.IsUser() {
		if _, err := locker.GetForUpdate(ctx, id.Device()); err != nil {
			return err
		}
	}
	_, err := locker.GetForUpdate(ctx, id)
	return err
}
