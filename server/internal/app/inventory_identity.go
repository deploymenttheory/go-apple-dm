package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

type (
	inventoryMethodKey     struct{}
	inventoryProfileMethod struct {
		Method  correlation.Method `json:"method"`
		Binding acme.Binding       `json:"binding"`
	}
)

// retainInventoryMethod records the actual issuance flow, never a channel guess.
// A reused subject with a different flow/binding is ambiguous and fails closed.
func (e *enrollment) retainInventoryMethod(ctx context.Context, b acme.Binding, grant AdmissionGrant) error {
	if e.app == nil || e.app.protocol == nil {
		return nil
	}
	method, recordedMethod := ctx.Value(inventoryMethodKey{}).(correlation.Method)
	if association, ok := accountdriven.AssociationFromContext(ctx); ok {
		switch association.Origin {
		case accountdriven.VersionADDE:
			method = correlation.AccountDrivenDeviceEnrollment
		case accountdriven.VersionBYOD:
			method = correlation.UserEnrollment
		}
	}
	// Replacement and credential renewal carry the previous issued method;
	// missing historical provenance never becomes profile-based by default.
	if recordedMethod && method == "" || !recordedMethod && strings.HasPrefix(b.CommonName, replacementSubjectPrefix) {
		return nil
	}
	if method == "" {
		method = correlation.ProfileBasedDeviceEnrollment
	}
	value := inventoryProfileMethod{Method: method, Binding: b}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	key := inventoryProfileMethodKey(b.CommonName, b.Serial, b.EnrollmentUDID())
	return e.app.protocol.Update(ctx, []string{key}, func(tx state.Tx) error {
		previous, err := tx.Get(ctx, key)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if err == nil {
			var old inventoryProfileMethod
			if json.Unmarshal(previous.Value, &old) != nil || old.Method != value.Method || old.Binding.EnrollmentUDID() != b.EnrollmentUDID() || old.Binding.Serial != b.Serial {
				return inventory.ErrConflict
			}
		}
		return tx.Put(ctx, state.Record{Key: key, Value: raw, ExpiresAt: grant.ExpiresAt})
	})
}

// inventoryProfileMethodKey scopes pending provenance to the complete issued binding.
func inventoryProfileMethodKey(commonName, serial, udid string) string {
	raw, _ := json.Marshal([]string{commonName, serial, udid})
	return "inventory/profile-method/" + issuanceHash(raw)
}

// inventoryIdentity requires a pinned issued certificate and its recorded flow.
// Missing historical provenance remains incomplete until an authenticated refresh
// using a newly issued identity; source identifiers never authenticate a device.
func (a *App) inventoryIdentity(ctx context.Context, e *storage.Enrollment) (*inventory.NativeIdentity, error) {
	if e.CertHash == "" {
		return nil, nil
	}
	record, err := a.protocol.Get(ctx, "issued-identity:"+e.CertHash)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var evidence identityEvidence
	if err := json.Unmarshal(record.Value, &evidence); err != nil {
		return nil, err
	}
	if evidence.EnrollmentMethod == "" {
		return nil, nil
	}
	if evidence.Serial != "" && inventory.CanonicalSerial(evidence.Serial) != inventory.CanonicalSerial(e.Device.SerialNumber) {
		return nil, inventory.ErrConflict
	}
	if evidence.UDID != "" && evidence.UDID != e.ID.ID || evidence.EnrollmentID != "" && evidence.EnrollmentID != e.ID.ID {
		return nil, inventory.ErrConflict
	}
	return &inventory.NativeIdentity{Serial: e.Device.SerialNumber, Method: evidence.EnrollmentMethod, Authenticated: true}, nil
}
