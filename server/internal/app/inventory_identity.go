package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
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
// Serial here is the certificate issuance binding for consistency checks, never
// an assertion that stored DeviceInfo is an authenticated native serial report.
func (a *App) inventoryIdentity(ctx context.Context, e *storage.Enrollment) (*inventory.NativeIdentity, error) {
	identity, err := a.inventoryCertificateIdentity(ctx, e)
	if err != nil || identity == nil {
		return identity, err
	}
	if identity.Serial != "" && e.Device.SerialNumber != "" && inventory.CanonicalSerial(identity.Serial) != inventory.CanonicalSerial(e.Device.SerialNumber) {
		return nil, inventory.ErrConflict
	}
	return identity, nil
}

// inventoryCertificateIdentity loads issued method and certificate binding only.
// Reported native serial qualification is independent of merged device indexes.
func (a *App) inventoryCertificateIdentity(ctx context.Context, e *storage.Enrollment) (*inventory.NativeIdentity, error) {
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
	if evidence.UDID != "" && evidence.UDID != e.ID.ID || evidence.EnrollmentID != "" && evidence.EnrollmentID != e.ID.ID {
		return nil, inventory.ErrConflict
	}
	return &inventory.NativeIdentity{Serial: evidence.Serial, Method: evidence.EnrollmentMethod, Authenticated: true}, nil
}

// inventoryReportIdentity uses only a serial supplied by this receipt or a retained
// authenticated serial report for this enrollment. Certificate method provenance
// and merged DeviceInfo alone never establish a reported hardware serial.
func (a *App) inventoryReportIdentity(ctx context.Context, e *storage.Enrollment, reported string, present bool) (*inventory.NativeIdentity, error) {
	identity, err := a.inventoryCertificateIdentity(ctx, e)
	if err != nil || identity == nil {
		return identity, err
	}
	certificateSerial := identity.Serial
	identity.Serial = reported
	if !present {
		identity.Serial, err = a.retainedInventorySerial(ctx, e.ID)
		if err != nil {
			return nil, err
		}
	}
	if certificateSerial != "" && identity.Serial != "" && inventory.CanonicalSerial(certificateSerial) != inventory.CanonicalSerial(identity.Serial) {
		identity.Authenticated = false
	}
	return identity, nil
}

// retainedInventorySerial follows authenticated enrollment addressing only to
// previously captured actual serial fields. Missing, unchecked and conflicting
// originals never supply a binding for a later serial-omitting receipt.
func (a *App) retainedInventorySerial(ctx context.Context, id mdm.EnrollmentID) (string, error) {
	serial := ""
	var latest time.Time
	for _, selector := range []struct{ kind, path string }{{"mdm.identity", "SerialNumber"}, {"mdm.DeviceInformation", "QueryResponses.SerialNumber"}} {
		kind, path := selector.kind, selector.path
		ref := inventory.EnrollmentReference(kind, id)
		record, err := a.Inventory.SourceResource(ctx, ref)
		if errors.Is(err, inventory.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		if record.Incomplete || record.Snapshot.Absent || record.Snapshot.Disconnected {
			continue
		}
		history, err := a.Inventory.History(ctx, ref)
		if err != nil {
			return "", err
		}
		for _, original := range history {
			if original.Native == nil || !original.Native.Authenticated || original.Native.Serial == "" {
				continue
			}
			value := inventory.String(original.Fields[path])
			if value == "" || inventory.CanonicalSerial(value) != inventory.CanonicalSerial(original.Native.Serial) {
				continue
			}
			if serial != "" && inventory.CanonicalSerial(serial) != inventory.CanonicalSerial(value) {
				return "", nil
			}
			if serial == "" || original.ObservedAt.After(latest) {
				serial = value
				latest = original.ObservedAt
			}
		}
	}
	return serial, nil
}
