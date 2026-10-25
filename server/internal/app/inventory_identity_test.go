package app

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// TestInventoryAuthenticatedIdentity rejects identifier-only assertions, missing
// provenance and mismatched certificate bindings without losing native evidence.
func TestInventoryAuthenticatedIdentity(t *testing.T) {
	a, id := nativeInventoryApp(t)
	e, err := a.Store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := a.inventoryIdentity(t.Context(), e)
	if err != nil || identity != nil {
		t.Fatal(identity, err)
	}
	cert := &x509.Certificate{Raw: []byte("issued")}
	e.CertHash = cms.Fingerprint(cert)
	identity, err = a.inventoryIdentity(t.Context(), e)
	if err != nil || identity != nil {
		t.Fatal(identity, err)
	}
	save := func(value []byte) {
		t.Helper()
		if err := a.protocol.Update(t.Context(), []string{"issued-identity:" + e.CertHash}, func(tx state.Tx) error {
			return tx.Put(t.Context(), state.Record{Key: "issued-identity:" + e.CertHash, Value: value})
		}); err != nil {
			t.Fatal(err)
		}
	}
	save([]byte(`{`))
	if _, err := a.inventoryIdentity(t.Context(), e); err == nil {
		t.Fatal("corrupt issuance accepted")
	}
	for _, tc := range []struct {
		evidence      identityEvidence
		want          error
		authenticated bool
	}{
		{identityEvidence{}, nil, false},
		{identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, Serial: "other"}, inventory.ErrConflict, false},
		{identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, UDID: "other"}, inventory.ErrConflict, false},
		{identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, EnrollmentID: "other"}, inventory.ErrConflict, false},
		{identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, Serial: "SER", UDID: id.ID}, nil, true},
		{identityEvidence{EnrollmentMethod: correlation.UserEnrollment, Serial: "SER"}, nil, true},
	} {
		raw, err := json.Marshal(tc.evidence)
		if err != nil {
			t.Fatal(err)
		}
		save(raw)
		identity, err := a.inventoryIdentity(t.Context(), e)
		if !errors.Is(err, tc.want) || (identity != nil) != tc.authenticated {
			t.Fatal(identity, err, tc)
		}
	}
	base := a.protocol
	a.protocol = issuanceStateFault{Store: base, readErr: errInventoryStorage}
	if _, err := a.inventoryIdentity(t.Context(), e); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	// All native adapters propagate provenance storage outages rather than treating
	// unchecked reports as successful authenticated enrichment.
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	authenticateRaw, err := plist.Marshal(map[string]any{"MessageType": "Authenticate", "UDID": id.ID, "SerialNumber": "SER"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.observeInventoryAuthenticate(t.Context(), &mdm.Request{ID: id, Certificate: cert}, &mdm.Checkin{Raw: authenticateRaw}, time.Now().UTC()); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	a.protocol = base
	raw, err := plist.Marshal(map[string]any{"Status": "Acknowledged", "QueryResponses": map[string]any{"SerialNumber": "CONTRADICTORY"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.projectInventoryCommand(t.Context(), id, "DeviceInformation", raw, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	contradictory, err := a.Inventory.SourceResource(t.Context(), inventory.EnrollmentReference("mdm.DeviceInformation", id))
	if err != nil || !contradictory.Incomplete || contradictory.Snapshot.Native.Authenticated || inventory.String(contradictory.Snapshot.Fields["QueryResponses.SerialNumber"]) != "CONTRADICTORY" {
		t.Fatal(contradictory, err)
	}
}

// TestInventoryIssuanceMethodBinding verifies complete issuance tuples and refuses
// ambiguous pending-flow changes without treating a different UDID as the same device.
func TestInventoryIssuanceMethodBinding(t *testing.T) {
	a, _ := nativeInventoryApp(t)
	enrollment := &enrollment{app: a}
	grant := AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)}
	binding := acme.Binding{CommonName: "shared-serial", Serial: "SER", MDMUDID: "one"}
	ctx := context.WithValue(t.Context(), inventoryMethodKey{}, correlation.AutomatedDeviceEnrollment)
	for range 2 {
		if err := enrollment.retainInventoryMethod(ctx, binding, grant); err != nil {
			t.Fatal(err)
		}
	}
	other := binding
	other.MDMUDID = "two"
	if err := enrollment.retainInventoryMethod(ctx, other, grant); err != nil {
		t.Fatal("serial wrongly used as protocol identity", err)
	}
	if err := enrollment.retainInventoryMethod(t.Context(), binding, grant); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("changed flow reused issued binding", err)
	}
	replacement := binding
	replacement.CommonName = replacementSubjectPrefix + "replacement"
	if err := enrollment.retainInventoryMethod(t.Context(), replacement, grant); err != nil {
		t.Fatal(err)
	}
	base := a.protocol
	for _, fault := range []issuanceStateFault{{Store: base, txReadErr: errInventoryStorage}, {Store: base, writeErr: errInventoryStorage}} {
		a.protocol = fault
		fresh := binding
		fresh.CommonName = "fresh"
		if err := enrollment.retainInventoryMethod(ctx, fresh, grant); !errors.Is(err, errInventoryStorage) {
			t.Fatal(err)
		}
	}
	a.protocol = base
	key := inventoryProfileMethodKey(binding.CommonName, binding.Serial, binding.EnrollmentUDID())
	if err := base.Update(t.Context(), []string{key}, func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: []byte(`{`)}) }); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.retainInventoryMethod(ctx, binding, grant); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
	// A device channel alone remains insufficient proof of an enrollment method.
	identity, err := a.inventoryIdentity(t.Context(), &storage.Enrollment{ID: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "not-issued"}, Device: storage.DeviceInfo{SerialNumber: "SER"}})
	if err != nil || identity != nil {
		t.Fatal(identity, err)
	}
}

// TestInventoryCertificateRetryKeepsOriginalMethod proves a cached certificate
// cannot adopt a later pending issuance's method for the same complete binding.
func TestInventoryCertificateRetryKeepsOriginalMethod(t *testing.T) {
	a, _ := nativeInventoryApp(t)
	binding := acme.Binding{CommonName: "same", Serial: "SER", MDMUDID: "one"}
	cert := &x509.Certificate{Raw: []byte("issued certificate"), Subject: pkix.Name{CommonName: "same"}, NotAfter: time.Now().Add(time.Hour)}
	ctx := context.WithValue(t.Context(), issuanceBindingKey{}, binding)
	ctx = context.WithValue(ctx, inventoryMethodKey{}, correlation.AutomatedDeviceEnrollment)
	if err := a.recordIssuedIdentity(ctx, cert); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, inventoryMethodKey{}, correlation.ProfileBasedDeviceEnrollment)
	if err := a.recordIssuedIdentity(ctx, cert); err != nil {
		t.Fatal(err)
	}
	record, err := a.protocol.Get(t.Context(), "issued-identity:"+cms.Fingerprint(cert))
	if err != nil {
		t.Fatal(err)
	}
	var evidence identityEvidence
	if err := json.Unmarshal(record.Value, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.EnrollmentMethod != correlation.AutomatedDeviceEnrollment {
		t.Fatal("certificate adopted another method", evidence)
	}
}
