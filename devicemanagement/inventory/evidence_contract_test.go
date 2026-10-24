package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// linkedFixture creates an explicitly checked physical relationship for failure
// and lifecycle contracts. Source IDs remain independent before and after linking.
func linkedFixture(t *testing.T) (*Repository, ResourceRecord, ResourceRecord) {
	t.Helper()
	r := repository(t)
	at := time.Now().UTC()
	apple := observe(t, r, "S", "axm.device", "a", "apple", `{"id":"apple","type":"orgDevices","attributes":{"serialNumber":"S"}}`, at)
	o, err := ResourceObservation(source.Reference{Kind: "mdm.DeviceInformation", ResourceID: "device:native"}, json.RawMessage(`{"QueryResponses":{"Flag":false}}`), at, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	o.Native = &NativeIdentity{Method: correlation.AutomatedDeviceEnrollment, Authenticated: true}
	native, err := r.Observe(t.Context(), "S", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Link(t.Context(), apple.Source, native.Source, at); err != nil {
		t.Fatal(err)
	}
	return r, apple, native
}

// TestNativeRefreshLinksAfterMigration preserves unchecked legacy history while
// allowing a later eligible authenticated report to establish an explicit link.
func TestNativeRefreshLinksAfterMigration(t *testing.T) {
	m, apple, native, at := legacyFixture(t)
	r, err := New(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Migrate(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	for _, method := range []correlation.Method{correlation.AutomatedDeviceEnrollment, correlation.AccountDrivenDeviceEnrollment, correlation.ProfileBasedDeviceEnrollment} {
		o, err := ResourceObservation(native, json.RawMessage(`{"QueryResponses":{"SerialNumber":"ACTUAL","Flag":false,"Zero":0,"Empty":[],"Null":null}}`), at.Add(time.Hour), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		o.Native = &NativeIdentity{Serial: "ACTUAL", Method: method, Authenticated: true}
		if _, err := r.Observe(t.Context(), "", o); err != nil {
			t.Fatal(err)
		}
		link, err := r.Link(t.Context(), apple, native, at)
		if err != nil || link.Serial != "ACTUAL" {
			t.Fatal("refresh could not repair unchecked provenance", link, err)
		}
	}
	history, err := r.History(t.Context(), native)
	if err != nil || len(history) != 4 {
		t.Fatal(history, err)
	}
	envelopes, err := r.Envelopes(t.Context(), native)
	if err != nil || len(envelopes) != 1 {
		t.Fatal("legacy original lost", envelopes, err)
	}
}

// TestSourceLifecycleInvalidatesLinks prevents an earlier checked association
// from implying current membership after absence, disconnect or new evidence.
func TestSourceLifecycleInvalidatesLinks(t *testing.T) {
	for _, change := range []struct{ absent, disconnected bool }{{true, false}, {false, true}, {true, true}} {
		r, apple, native := linkedFixture(t)
		if err := r.ChangeSource(t.Context(), apple.Source, change.absent, change.disconnected); err != nil {
			t.Fatal(err)
		}
		links, err := r.Links(t.Context(), native.Source)
		if err != nil || len(links) != 0 {
			t.Fatal("lifecycle kept active checked link", links, err)
		}
		if _, err := r.Link(t.Context(), apple.Source, native.Source, time.Now()); !errors.Is(err, ErrInvalid) {
			t.Fatal("inactive source relinked", err)
		}
		history, err := r.History(t.Context(), apple.Source)
		if err != nil || len(history) != 1 || history[0].Absent || history[0].Disconnected {
			t.Fatal("original history mutated", history, err)
		}
	}
}

// TestLiteralSourcePaths keeps literal dots and backslashes disjoint from nested paths.
func TestLiteralSourcePaths(t *testing.T) {
	raw := json.RawMessage(`{"a\\.b":1,"a\\":{"b":2},"a.b":3,"a":{"b":4},"array":[false,null,0,[]]}`)
	fields := Flatten(raw)
	for path, want := range map[string]string{`a\\\.b`: "1", `a\\.b`: "2", `a\.b`: "3", `a.b`: "4", "array": "[false,null,0,[]]"} {
		if string(fields[path]) != want {
			t.Fatal(path, string(fields[path]), want)
		}
	}
	if !compare(json.RawMessage(`{"a":[false,null,9007199254740993]}`), Condition{Operator: "eq", Value: json.RawMessage(`{"a":[false,null,9007199254740993]}`)}) {
		t.Fatal("object/array exact equality")
	}
	if compare(json.RawMessage(`{"a":1}`), Condition{Operator: "eq", Value: json.RawMessage(`{"a":2}`)}) {
		t.Fatal("different object matched")
	}
	if got, err := canonicalJSON(json.RawMessage(`{"a":[1.0]}`)); err != nil || len(got) == 0 {
		t.Fatal(string(got), err)
	}
}

// TestEvidenceFailureAtomicity covers every storage boundary of explicit links,
// history, lifecycle invalidation and refresh outcomes with real memory rollback.
func TestEvidenceFailureAtomicity(t *testing.T) {
	r, apple, native := linkedFixture(t)
	ctx := t.Context()
	at := time.Now().UTC()
	seed := cloneMemory(testMemory(t, r))
	operations := []struct {
		name string
		run  func(*Repository) error
	}{
		{"link", func(r *Repository) error { _, err := r.Link(ctx, apple.Source, native.Source, at); return err }},
		{"history", func(r *Repository) error { _, err := r.History(ctx, native.Source); return err }},
		{"attempts", func(r *Repository) error { _, err := r.Attempts(ctx, native.Source); return err }},
		{"links", func(r *Repository) error { _, err := r.Links(ctx, apple.Source); return err }},
		{"envelopes", func(r *Repository) error { _, err := r.Envelopes(ctx, native.Source); return err }},
		{"recovery", func(r *Repository) error { _, err := r.RecoveryNeeds(ctx); return err }},
		{"lifecycle", func(r *Repository) error { return r.ChangeSource(ctx, apple.Source, true, true) }},
		{"observe", func(r *Repository) error {
			o := native.Snapshot
			o.ObservedAt = at.Add(time.Hour)
			_, err := r.Observe(ctx, "S", o)
			return err
		}},
		{"acknowledged replay", func(r *Repository) error { _, err := r.Observe(ctx, "", native.Snapshot); return err }},
		{"delayed conflicting original", func(r *Repository) error {
			o := native.Snapshot
			o.ObservedAt = at.Add(-time.Hour)
			identity := *o.Native
			identity.Serial = "DIFFERENT"
			o.Native = &identity
			_, err := r.Observe(ctx, "", o)
			return err
		}},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			baseline := &faultBackend{Memory: cloneMemory(seed)}
			if err := op.run(&Repository{Backend: baseline}); err != nil {
				t.Fatal("baseline", err)
			}
			for n := 1; n <= baseline.calls; n++ {
				t.Run(fmt.Sprint(n), func(t *testing.T) {
					backend := &faultBackend{Memory: cloneMemory(seed), failAt: n}
					if err := op.run(&Repository{Backend: backend}); !errors.Is(err, errStorageFailure) {
						t.Fatal("error hidden", err)
					}
					if !reflect.DeepEqual(seed.data, backend.data) {
						t.Fatal("partial evidence operation survived")
					}
				})
			}
		})
	}
}

// TestMalformedEvidenceAndQueries exercises invalid originals and explicit
// relationship boundaries without turning malformed evidence into source values.
func TestMalformedEvidenceAndQueries(t *testing.T) {
	r, apple, native := linkedFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	for _, o := range []Observation{
		{Source: native.Source, ObservedAt: now, Raw: json.RawMessage(`{}`), Fields: map[string]json.RawMessage{"invalid": json.RawMessage(`{`)}},
		{Source: native.Source, ObservedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), Raw: json.RawMessage(`{}`)},
	} {
		if _, err := r.Observe(ctx, "", o); err == nil {
			t.Fatal("malformed evidence accepted")
		}
	}
	if err := r.Attempt(ctx, native.Source, time.Time{}, "failure"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := r.Attempt(ctx, native.Source, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), "failure"); err == nil {
		t.Fatal("invalid outcome time")
	}
	if _, err := r.Link(ctx, source.Reference{Kind: "missing", ResourceID: "missing"}, native.Source, now); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.Link(ctx, apple.Source, source.Reference{Kind: "missing", ResourceID: "missing"}, now); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	wrong := observe(t, r, "S", "axm.detail", "a", "detail", `{}`, now)
	if _, err := r.Link(ctx, wrong.Source, native.Source, now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	malformed := observe(t, r, "S", "axm.device", "a", "bad", `{"id":"bad","type":"wrong","attributes":{}}`, now)
	if _, err := r.Link(ctx, malformed.Source, native.Source, now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, q := range []ResourceQuery{{Source: "absent"}, {AccountID: "absent"}, {Focus: "apple", Source: "mdm.DeviceInformation"}, {Focus: "managed", Source: "axm.device"}, {Search: "absent"}, {Conditions: []Condition{{"data.missing", "eq", json.RawMessage(`1`)}}}, {Conditions: []Condition{{"meta.id", "exists", json.RawMessage(`false`)}}}} {
		page, err := r.Resources(ctx, q)
		if err != nil || len(page.Items) != 0 {
			t.Fatal(q, page, err)
		}
	}
	for _, path := range DefaultColumns {
		if raw, ok := native.Value(path); !ok || !json.Valid(raw) {
			t.Fatal("metadata column", path, string(raw), ok)
		}
	}
	if String(json.RawMessage(`false`)) != "" {
		t.Fatal("boolean coerced to string")
	}
	if at, ok := Time(json.RawMessage(`"2026-10-24T12:00:00Z"`)); !ok || at.IsZero() {
		t.Fatal(at, ok)
	}
	if _, ok := Time(json.RawMessage(`null`)); ok {
		t.Fatal("null became timestamp")
	}
}

// TestOriginalJSONBytes survives JSON document marshaling without compaction or
// HTML escaping changing the original source byte representation.
func TestOriginalJSONBytes(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	raw := "{\n  \"future\": \"<>&\", \"counter\": 9007199254740993, \"lexeme\": 1e+09\n}"
	d := observe(t, r, "", "axm.detail", "a", "opaque", raw, at)
	current, err := r.Resource(t.Context(), d.ID)
	if err != nil || string(current.Snapshot.Raw) != raw {
		t.Fatal("original bytes changed", string(current.Snapshot.Raw), err)
	}
	history, err := r.History(t.Context(), d.Source)
	if err != nil || len(history) != 1 || string(history[0].Raw) != raw {
		t.Fatal("historical bytes changed", history, err)
	}
	var invalid Observation
	if err := json.Unmarshal([]byte(`{"original_json":"ew=="}`), &invalid); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid original bytes accepted", err)
	}
	if err := json.Unmarshal([]byte(`42`), &invalid); err == nil {
		t.Fatal("malformed envelope accepted")
	}
	exported, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(exported, []byte("original_json")) {
		t.Fatal("internal byte bookkeeping leaked")
	}
	public, err := json.Marshal(PublicRecord(current))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, []byte("9007199254740993")) {
		t.Fatal("original byte export leaked without raw authority")
	}
}

// TestNativeStatusChannelsAndItemAge retains reported item ages while excluding
// user-channel data from the device-status source namespace.
func TestNativeStatusChannelsAndItemAge(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	itemAt := at.Add(-time.Hour)
	update := ddm.StatusUpdate{Raw: []byte(`{"StatusItems":{}}`), ReceivedAt: at, Values: []ddm.StatusValue{{Path: "future", Value: []byte(`false`), LastSeen: itemAt}}}
	if err := r.ObserveStatus(t.Context(), "", mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "user"}, update); err != nil {
		t.Fatal(err)
	}
	page, err := r.Resources(t.Context(), ResourceQuery{})
	if err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "device"}
	if err := r.ObserveStatus(t.Context(), "", id, update); err != nil {
		t.Fatal(err)
	}
	record, err := r.SourceResource(t.Context(), EnrollmentReference("ddm.status", id))
	if err != nil || !record.Snapshot.FieldTimes["future"].Equal(itemAt) || !record.Incomplete {
		t.Fatal(record, err)
	}
}

// TestUncheckedNativeProvenance remains incomplete rather than inferring a method.
func TestUncheckedNativeProvenance(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	for i, identity := range []NativeIdentity{{Method: correlation.AutomatedDeviceEnrollment}, {Method: "unknown", Authenticated: true}} {
		o, err := ResourceObservation(source.Reference{Kind: "mdm.DeviceInformation", ResourceID: fmt.Sprint(i)}, json.RawMessage(`{}`), at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		o.Native = &identity
		record, err := r.Observe(t.Context(), "S", o)
		if err != nil || !record.Incomplete {
			t.Fatal("provenance inferred", record, err)
		}
	}
}

// TestDelayedConflictInvalidatesCheckedLink retains a delayed original without
// replacing the newest snapshot, while preventing a contradictory active link.
func TestDelayedConflictInvalidatesCheckedLink(t *testing.T) {
	r, apple, native := linkedFixture(t)
	if _, err := r.Observe(t.Context(), "", native.Snapshot); err != nil {
		t.Fatal(err)
	}
	links, err := r.Links(t.Context(), apple.Source)
	if err != nil || len(links) != 1 {
		t.Fatal("acknowledged replay invalidated checked evidence", links, err)
	}
	delayed, err := ResourceObservation(native.Source, json.RawMessage(`{"QueryResponses":{"SerialNumber":"DIFFERENT"}}`), native.Snapshot.ObservedAt.Add(-time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	delayed.Native = &NativeIdentity{Serial: "DIFFERENT", Method: correlation.AutomatedDeviceEnrollment, Authenticated: true}
	if _, err := r.Observe(t.Context(), "", delayed); err != nil {
		t.Fatal(err)
	}
	latest, err := r.SourceResource(t.Context(), native.Source)
	if err != nil || string(latest.Snapshot.Raw) != string(native.Snapshot.Raw) || !latest.Snapshot.ObservedAt.Equal(native.Snapshot.ObservedAt) {
		t.Fatal("delayed original replaced latest snapshot", latest, err)
	}
	links, err = r.Links(t.Context(), apple.Source)
	if err != nil || len(links) != 0 {
		t.Fatal("delayed serial conflict left a checked link", links, err)
	}
	history, err := r.History(t.Context(), native.Source)
	if err != nil || len(history) != 2 {
		t.Fatal("delayed evidence lost", history, err)
	}
	if _, err := r.Link(t.Context(), apple.Source, native.Source, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting retained history linked", err)
	}
}

// TestSameTimeHistoricalReplayKeepsCurrentSnapshot exercises A then B at the
// same timestamp, a checked B link, and an exact replay of the older A original.
func TestSameTimeHistoricalReplayKeepsCurrentSnapshot(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	a := observe(t, r, "", "axm.device", "a", "apple", `{"id":"apple","type":"orgDevices","attributes":{"serialNumber":"A"}}`, at)
	b := observe(t, r, "", "axm.device", "a", "apple", `{"id":"apple","type":"orgDevices","attributes":{"serialNumber":"B"}}`, at)
	native, err := ResourceObservation(source.Reference{Kind: "mdm.DeviceInformation", ResourceID: "device:native"}, json.RawMessage(`{"QueryResponses":{"SerialNumber":"B"}}`), at, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	native.Native = &NativeIdentity{Serial: "B", Method: correlation.AutomatedDeviceEnrollment, Authenticated: true}
	if _, err := r.Observe(t.Context(), "", native); err != nil {
		t.Fatal(err)
	}
	checked, err := r.Link(t.Context(), b.Source, native.Source, at)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := r.Observe(t.Context(), "", a.Snapshot)
	if err != nil || string(replayed.Snapshot.Raw) != string(b.Snapshot.Raw) {
		t.Fatal("historical replay replaced equal-time winner", replayed, err)
	}
	links, err := r.Links(t.Context(), b.Source)
	if err != nil || len(links) != 1 || links[0] != checked {
		t.Fatal("historical replay changed checked B link", links, err)
	}
	current, err := r.SourceResource(t.Context(), b.Source)
	if err != nil || string(current.Snapshot.Raw) != string(b.Snapshot.Raw) {
		t.Fatal("checked link and current source diverged", current, err)
	}
	if err := r.ChangeSource(t.Context(), b.Source, true, true); err != nil {
		t.Fatal(err)
	}
	replayed, err = r.Observe(t.Context(), "", a.Snapshot)
	if err != nil || !replayed.Snapshot.Absent || !replayed.Snapshot.Disconnected {
		t.Fatal("acknowledged replay changed lifecycle", replayed, err)
	}
}

// TestHistoricalReplayMissingCurrentRecord fails closed rather than fabricating
// current state from an acknowledged historical document after inconsistent loss.
func TestHistoricalReplayMissingCurrentRecord(t *testing.T) {
	r, apple, _ := linkedFixture(t)
	if err := r.Backend.Update(t.Context(), func(tx Tx) error { return tx.Delete(t.Context(), "resource/"+apple.ID) }); err != nil {
		t.Fatal(err)
	}
	if record, err := r.Observe(t.Context(), "", apple.Snapshot); !errors.Is(err, ErrConflict) || record.ID != "" {
		t.Fatal("missing current record fabricated a replay success", record, err)
	}
	if _, err := r.SourceResource(t.Context(), apple.Source); !errors.Is(err, ErrNotFound) {
		t.Fatal("inconsistent replay wrote a current record", err)
	}
	if err := r.Backend.Update(t.Context(), func(tx Tx) error { return tx.Put(t.Context(), "resource/"+apple.ID, json.RawMessage(`{}`)) }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Observe(t.Context(), "", apple.Snapshot); !errors.Is(err, ErrConflict) {
		t.Fatal("corrupt current identity accepted", err)
	}
}
