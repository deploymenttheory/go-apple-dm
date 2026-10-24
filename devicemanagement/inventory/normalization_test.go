package inventory

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// TestIdentifierNormalization verifies that serial matching never normalizes source values.
func TestIdentifierNormalization(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	apple := observe(t, r, "WRONG", "axm.device", "account", "opaque", `{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":" ser ","imei":["",null,"original"],"counter":9007199254740993}}`, at)
	nativeRef := source.Reference{Kind: "mdm.DeviceInformation", ResourceID: "device:enrollment"}
	o, err := ResourceObservation(nativeRef, json.RawMessage(`{"QueryResponses":{"SerialNumber":"SER","Flag":false}}`), at, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	o.Native = &NativeIdentity{Serial: "SER", Method: correlation.ProfileBasedDeviceEnrollment, Authenticated: true}
	native, err := r.Observe(t.Context(), "", o)
	if err != nil {
		t.Fatal(err)
	}
	if native.ID == apple.ID {
		t.Fatal("sources merged before explicit link")
	}
	link, err := r.Link(t.Context(), apple.Source, native.Source, at)
	if err != nil || link.Serial != "SER" {
		t.Fatal(link, err)
	}
	if string(apple.Snapshot.Fields["attributes.serialNumber"]) != `" ser "` || string(apple.Snapshot.Fields["attributes.imei"]) != `["",null,"original"]` {
		t.Fatal("source normalized")
	}
	links, err := r.Links(t.Context(), native.Source)
	if err != nil || len(links) != 1 {
		t.Fatal(links, err)
	}
	// A fresh report requires an explicit new correlation; storage never silently
	// transfers a checked link when an opaque source changes its serial evidence.
	o.ObservedAt = at.Add(time.Hour)
	o.Native.Serial = "DIFFERENT"
	if _, err := r.Observe(t.Context(), "", o); err != nil {
		t.Fatal(err)
	}
	links, err = r.Links(t.Context(), apple.Source)
	if err != nil || len(links) != 0 {
		t.Fatal(links, err)
	}
	if _, err := r.Link(t.Context(), apple.Source, native.Source, at); !errors.Is(err, ErrConflict) {
		t.Fatal("serial history conflict accepted", err)
	}
}

// TestDDMNormalizedEvidence verifies retained partial originals without aliases or conversion.
func TestDDMNormalizedEvidence(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	ref := source.Reference{Kind: "ddm.status", ResourceID: "device:enrollment"}
	raw := json.RawMessage(`{"device.model.marketing-name":"Mac","diskmanagement.filevault.enabled":false,"future":null}`)
	d := observe(t, r, "S", ref.Kind, "", ref.ResourceID, string(raw), at)
	if _, ok := d.Snapshot.Fields["model"]; ok {
		t.Fatal("implicit alias")
	}
	if _, ok := d.Snapshot.Fields["filevault_enabled"]; ok {
		t.Fatal("implicit equivalence")
	}
	if len(d.Snapshot.Raw) != len(raw) || string(d.Snapshot.Raw) != string(raw) {
		t.Fatal("original changed")
	}
	// A user-enrollment report remains inspectable but cannot enrich physical hardware.
	apple := observe(t, r, "S", "axm.device", "a", "apple", `{"id":"apple","type":"orgDevices","attributes":{"serialNumber":"S"}}`, at)
	for _, method := range []correlation.Method{correlation.UserEnrollment, ""} {
		o := d.Snapshot
		o.Native = &NativeIdentity{Serial: "S", Method: method, Authenticated: true}
		o.ObservedAt = o.ObservedAt.Add(time.Minute)
		if _, err := r.Observe(t.Context(), "", o); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Link(t.Context(), apple.Source, ref, at); err == nil {
			t.Fatal("ineligible method correlated", err)
		}
	}
}

// TestLegacyZeroEnrollmentDates verifies that source omission remains distinct from zero.
func TestLegacyZeroEnrollmentDates(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	d := observe(t, r, "", "enrollment", "", "device:serialless", `{"managed":false,"enrolled_at":"0001-01-01T00:00:00Z","values":[],"zero":0}`, at)
	if string(d.Snapshot.Fields["managed"]) != "false" || string(d.Snapshot.Fields["zero"]) != "0" || string(d.Snapshot.Fields["values"]) != "[]" {
		t.Fatal("valid values lost")
	}
	if _, ok := d.Snapshot.Fields["disabled_at"]; ok {
		t.Fatal("omitted value fabricated")
	}
	if string(d.Snapshot.Fields["enrolled_at"]) != `"0001-01-01T00:00:00Z"` {
		t.Fatal("original zero discarded")
	}
	if _, err := r.SourceResource(t.Context(), d.Source); err != nil {
		t.Fatal("serialless enrollment unavailable", err)
	}
}
