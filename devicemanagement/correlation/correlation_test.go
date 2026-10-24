package correlation_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

var at = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// apple supplies an actual collected resource with forward-compatible evidence.
func apple() correlation.AppleDevice {
	return correlation.AppleDevice{AccountID: "account", ObservedAt: at.Add(time.Hour), Resource: collection.Resource{ID: "opaque-id", Type: "orgDevices", Raw: json.RawMessage(`{"id":"opaque-id","type":"orgDevices","attributes":{"serialNumber":" Serial-1 ","model":"Apple model","false":true,"zero":1,"empty":[1],"large":9007199254740993,"null":null,"capacityGB":256},"relationships":{"service":{"data":null}},"future":{"unknown":false}}`)}}
}

// native supplies caller-authenticated successful protocol evidence.
func native(id string) correlation.NativeReport {
	return correlation.NativeReport{Source: source.Reference{Kind: "mdm.DeviceInformation", ResourceID: "device:" + id}, Serial: "serial-1", Method: correlation.ProfileBasedDeviceEnrollment, Authenticated: true, ObservedAt: at, Fields: map[string]json.RawMessage{"model": json.RawMessage(`"Native model"`), "false": json.RawMessage(`false`), "zero": json.RawMessage(`0`), "empty": json.RawMessage(`[]`), "capacityBytes": json.RawMessage(`256000000000`)}, Raw: json.RawMessage(`{"complete":{"SerialNumber":"serial-1","unknown":null}}`)}
}

// correlate constructs checked evidence and fails setup on invalid fixtures.
func correlate(t *testing.T, a correlation.AppleDevice, reports ...correlation.NativeReport) correlation.Correlation {
	t.Helper()
	c, err := correlation.Correlate(a, reports)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// mapping declares equivalent fixture fields explicitly.
func mapping(c correlation.Correlation, r correlation.NativeReport, name string) correlation.Equivalent {
	return correlation.Equivalent{Name: name, Inputs: []correlation.Field{{Source: c.AppleReference(), Name: name}, {Source: r.Source, Name: name}}}
}

// TestEligibilityAndConflicts checks authentication, enrollment and serial boundaries.
func TestEligibilityAndConflicts(t *testing.T) {
	for _, method := range []correlation.Method{correlation.AutomatedDeviceEnrollment, correlation.AccountDrivenDeviceEnrollment, correlation.ProfileBasedDeviceEnrollment} {
		t.Run(string(method), func(t *testing.T) {
			r := native("one")
			r.Method = method
			c := correlate(t, apple(), r, native("second"))
			if len(c.Matched()) != 2 || len(c.Rejected()) != 0 {
				t.Fatal(c.Matched(), c.Rejected())
			}
		})
	}
	tests := []struct {
		name, reason string
		change       func(*correlation.NativeReport)
	}{
		{"missing", "missing_serial", func(r *correlation.NativeReport) { r.Serial = " " }},
		{"mismatch", "serial_conflict", func(r *correlation.NativeReport) { r.Serial = "OTHER" }},
		{"user enrollment", "ineligible_method", func(r *correlation.NativeReport) { r.Method = correlation.UserEnrollment }},
		{"unknown method", "ineligible_method", func(r *correlation.NativeReport) { r.Method = "unknown" }},
		{"unauthenticated", "unauthenticated", func(r *correlation.NativeReport) { r.Authenticated = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := native("one")
			tt.change(&r)
			c := correlate(t, apple(), r)
			rejected := c.Rejected()
			if len(c.Matched()) != 0 || len(rejected) != 1 || rejected[0].Reason != tt.reason {
				t.Fatal(rejected)
			}
			if _, err := c.Resolve(nil); !errors.Is(err, correlation.ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	r := native("one")
	unauthenticated := r
	unauthenticated.Authenticated = false
	unauthenticated.Serial = "untrusted"
	clean := correlate(t, apple(), r, unauthenticated)
	if len(clean.Matched()) != 1 || clean.Rejected()[0].Reason != "unauthenticated" {
		t.Fatal(clean.Matched(), clean.Rejected())
	}
	later := r
	later.Serial = "OTHER"
	later.ObservedAt = at.Add(time.Second)
	c := correlate(t, apple(), r, later)
	if len(c.Matched()) != 0 || len(c.Rejected()) != 2 {
		t.Fatal(c.Matched(), c.Rejected())
	}
	for _, raw := range []string{`{"id":"opaque-id","type":"orgDevices","attributes":{}}`, `{"id":"opaque-id","type":"orgDevices","attributes":{"serialNumber":null}}`} {
		a := apple()
		a.Resource.Raw = json.RawMessage(raw)
		c := correlate(t, a, r)
		if c.Rejected()[0].Reason != "missing_serial" {
			t.Fatal(c.Rejected())
		}
	}
}

// TestResolutionHistoryProvenanceAndFidelity checks native precedence and independent originals.
func TestResolutionHistoryProvenanceAndFidelity(t *testing.T) {
	a := apple()
	r := native("one")
	later := native("one")
	later.ObservedAt = at.Add(2 * time.Hour)
	later.Fields = map[string]json.RawMessage{"model": json.RawMessage(`"Later native"`)}
	c := correlate(t, a, r, later)
	mappings := []correlation.Equivalent{}
	for _, name := range []string{"model", "false", "zero", "empty", "large", "null", "missing", "capacityGB", "capacityBytes"} {
		mappings = append(mappings, mapping(c, r, name))
	}
	result, err := c.Resolve(mappings)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"model": `"Later native"`, "false": `false`, "zero": `0`, "empty": `[]`, "large": `9007199254740993`, "null": `null`, "capacityGB": `256`, "capacityBytes": `256000000000`} {
		v := result[name].Selected
		if v == nil || string(v.Value) != want {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	if result["missing"].Selected != nil || len(result["missing"].Candidates) != 0 {
		t.Fatal(result["missing"])
	}
	if !result["false"].Selected.Native || !result["false"].Selected.ObservedAt.Equal(at) || result["false"].Selected.Field.Source != r.Source || !result["false"].Disagrees {
		t.Fatal(result["false"])
	}
	if len(result["model"].Candidates) != 3 || !result["model"].Disagrees {
		t.Fatal(result["model"])
	}
	if result["capacityGB"].Selected.Native || !result["capacityBytes"].Selected.Native {
		t.Fatal("incompatible units merged")
	}
	if string(c.Apple().Resource.Raw) != string(a.Resource.Raw) || !strings.Contains(string(c.Apple().Resource.Raw), `"relationships"`) || string(c.Matched()[0].Raw) != string(r.Raw) {
		t.Fatal("lost source evidence")
	}
	// Inputs, accessor results and resolver results never share mutable evidence.
	r.Fields["model"][1] = 'X'
	a.Resource.Raw[0] = '['
	returned := c.Matched()
	returned[0].Raw[0] = '['
	returned[0].Fields["model"][1] = 'Y'
	ar := c.Apple()
	ar.Resource.Raw[0] = '['
	result["false"].Selected.Value[0] = 'X'
	result["model"].Candidates[0].Value[1] = 'X'
	again, err := c.Resolve(mappings)
	if err != nil || string(again["false"].Selected.Value) != "false" || string(c.Matched()[0].Fields["model"]) != `"Native model"` || c.Apple().Resource.Raw[0] != '{' {
		t.Fatal("shared bytes", err)
	}
}

// TestResolutionMultipleEnrollmentsAndTies checks explicit tie policy and concurrent reads.
func TestResolutionMultipleEnrollmentsAndTies(t *testing.T) {
	r := native("one")
	second := native("two")
	second.ObservedAt = at.Add(time.Second)
	second.Fields["model"] = json.RawMessage(`"Second"`)
	c := correlate(t, apple(), second, r)
	m := mapping(c, r, "model")
	m.Inputs = append(m.Inputs, correlation.Field{Source: second.Source, Name: "model"})
	result, err := c.Resolve([]correlation.Equivalent{m})
	if err != nil || string(result["model"].Selected.Value) != `"Second"` {
		t.Fatal(result, err)
	}
	second.ObservedAt = r.ObservedAt
	c = correlate(t, apple(), second, r)
	result, err = c.Resolve([]correlation.Equivalent{m})
	if err != nil || result["model"].Selected.Field.Source != r.Source {
		t.Fatal(result, err)
	}
	// Whitespace and object member order do not create false disagreements.
	r.Fields["same"] = json.RawMessage(`{"a":1,"b":false}`)
	second.Fields["same"] = json.RawMessage(`{ "b": false, "a":1 }`)
	c = correlate(t, apple(), r, second)
	m = correlation.Equivalent{Name: "same", Inputs: []correlation.Field{{Source: r.Source, Name: "same"}, {Source: second.Source, Name: "same"}}}
	result, err = c.Resolve([]correlation.Equivalent{m})
	if err != nil || result["same"].Disagrees {
		t.Fatal(result, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				if _, err := c.Resolve([]correlation.Equivalent{m}); err != nil {
					t.Error(err)
				}
				_ = c.Matched()
				_ = c.Rejected()
				_ = c.Apple()
			}
		})
	}
	wg.Wait()
}

// TestInvalidEvidenceAndMappings verifies malformed inputs fail without partial results.
func TestInvalidEvidenceAndMappings(t *testing.T) {
	for _, change := range []func(*correlation.AppleDevice){func(a *correlation.AppleDevice) { a.AccountID = "" }, func(a *correlation.AppleDevice) { a.ObservedAt = time.Time{} }, func(a *correlation.AppleDevice) { a.Resource.Raw = json.RawMessage(`!`) }, func(a *correlation.AppleDevice) { a.Resource.ID = "different" }, func(a *correlation.AppleDevice) { a.Resource.Type = "other" }, func(a *correlation.AppleDevice) {
		a.Resource.Raw = json.RawMessage(`{"id":"opaque-id","type":"orgDevices","attributes":{"serialNumber":3}}`)
	}, func(a *correlation.AppleDevice) {
		a.Resource.Raw = json.RawMessage(`{"id":"opaque-id","type":"orgDevices"}`)
	}} {
		a := apple()
		change(&a)
		if _, err := correlation.Correlate(a, nil); !errors.Is(err, correlation.ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*correlation.NativeReport){
		func(r *correlation.NativeReport) { r.Source.Kind = "" },
		func(r *correlation.NativeReport) {
			r.Source = source.Reference{Kind: "axm.device", AccountID: "account", ResourceID: "opaque-id"}
		}, func(r *correlation.NativeReport) { r.Source.ResourceID = "" }, func(r *correlation.NativeReport) { r.ObservedAt = time.Time{} }, func(r *correlation.NativeReport) { r.Raw = nil }, func(r *correlation.NativeReport) { r.Fields["bad"] = json.RawMessage(`!`) },
	} {
		r := native("one")
		change(&r)
		if _, err := correlation.Correlate(apple(), []correlation.NativeReport{r}); !errors.Is(err, correlation.ErrInvalid) {
			t.Fatal(err)
		}
	}
	r := native("one")
	c := correlate(t, apple(), r)
	m := mapping(c, r, "model")
	for _, mappings := range [][]correlation.Equivalent{{{Name: ""}}, {{Name: "empty"}}, {m, m}, {{Name: "unknown", Inputs: []correlation.Field{{Source: source.Reference{Kind: "other", ResourceID: "unknown"}, Name: "model"}}}}, {{Name: "empty field", Inputs: []correlation.Field{{Source: r.Source}}}}, {{Name: "duplicate field", Inputs: []correlation.Field{{Source: r.Source, Name: "model"}, {Source: r.Source, Name: "model"}}}}} {
		if _, err := c.Resolve(mappings); !errors.Is(err, correlation.ErrInvalid) {
			t.Fatal(mappings, err)
		}
	}
}
