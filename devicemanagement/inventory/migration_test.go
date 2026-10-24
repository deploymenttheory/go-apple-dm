package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// legacyFixture supplies original observations alongside deliberately false
// normalized aliases. Only the former may survive as source evidence.
func legacyFixture(t *testing.T) (*Memory, source.Reference, source.Reference, time.Time) {
	t.Helper()
	m := NewMemory()
	at := time.Now().UTC()
	apple := source.Reference{Kind: "axm.device", AccountID: "original-account", ResourceID: "opaque/apple"}
	native := source.Reference{Kind: "mdm.DeviceInformation", ResourceID: "device:old"}
	missing := source.Reference{Kind: "axm.coverage", AccountID: apple.AccountID, ResourceID: apple.ResourceID}
	observations := map[string]any{
		apple.Key():   map[string]any{"source": apple, "observed_at": at, "attempted_at": at.Add(time.Hour), "error": "apple_http_500", "raw": json.RawMessage(`{"id":"opaque/apple","type":"orgDevices","attributes":{"serialNumber":"ACTUAL","unknown":[null,false,0,[],9007199254740993]},"relationships":{"assigned":{"data":null}}}`)},
		native.Key():  Observation{Source: native, ObservedAt: at, Fields: map[string]json.RawMessage{"QueryResponses.SerialNumber": json.RawMessage(`"ACTUAL"`), "invented.normalized": json.RawMessage(`"fabricated"`)}, FieldTimes: map[string]time.Time{"QueryResponses.SerialNumber": at}, Raw: json.RawMessage(`{"QueryResponses":{"SerialNumber":"ACTUAL"}}`)},
		missing.Key(): map[string]any{"source": missing, "attempted_at": at, "error": "timeout"},
	}
	if err := m.Update(t.Context(), func(tx Tx) error {
		encodedSources, err := json.Marshal(observations)
		if err != nil {
			return err
		}
		var sourceEnvelopes map[string]json.RawMessage
		if err := json.Unmarshal(encodedSources, &sourceEnvelopes); err != nil {
			return err
		}
		var nativeEnvelope map[string]json.RawMessage
		if err := json.Unmarshal(sourceEnvelopes[native.Key()], &nativeEnvelope); err != nil {
			return err
		}
		nativeEnvelope["unknown_provenance"] = json.RawMessage(`{"future":null,"precise":9007199254740993}`)
		sourceEnvelopes[native.Key()], err = json.Marshal(nativeEnvelope)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(map[string]any{"id": "legacy", "serial_number": "FABRICATED", "fields": map[string]any{"model": "fabricated", "serial_number": "FABRICATED"}, "sources": sourceEnvelopes})
		if err != nil {
			return err
		}
		if err := tx.Put(t.Context(), "device/legacy", raw); err != nil {
			return err
		}
		for _, key := range []string{"source/old", "alias/provisional", "serial/false", "serialconflict/false", "field/false", "indexes/legacy"} {
			if err := put(t.Context(), tx, key, "legacy"); err != nil {
				return err
			}
		}
		if err := put(t.Context(), tx, "device/no-original", map[string]any{"id": "no-original", "serial_number": "FABRICATED", "fields": map[string]any{"model": "fabricated"}}); err != nil {
			return err
		}
		for _, key := range []string{"credential/account", "account/account", "schedule/account", "audit/old", "queue/old", "enrollment/serialless"} {
			if err := put(t.Context(), tx, key, map[string]any{"preserved": key}); err != nil {
				return err
			}
		}
		if err := put(t.Context(), tx, "job/running", Job{ID: "running", State: "running", Stage: "enrich", Cursor: "legacy", Checkpoint: json.RawMessage(`{"old":true}`), Devices: 3}); err != nil {
			return err
		}
		if err := put(t.Context(), tx, "job/paused", Job{ID: "paused", State: "paused", Stage: "apple_mdm", Cursor: "legacy"}); err != nil {
			return err
		}
		if err := put(t.Context(), tx, "lease/axm", lease{"running", "old-worker", at.Add(2 * time.Minute)}); err != nil {
			return err
		}
		return put(t.Context(), tx, "jobpage/running/old", true)
	}); err != nil {
		t.Fatal(err)
	}
	return m, apple, native, at
}

// TestMigrationOriginalsAndWorkerFence covers repeatability, exact original
// fidelity, incomplete evidence, independent operational state and old responses.
func TestMigrationOriginalsAndWorkerFence(t *testing.T) {
	m, apple, native, at := legacyFixture(t)
	before := cloneMemory(m)
	r, err := New(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Migrate(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	for key, value := range before.data {
		if RetiredKey(key) {
			if _, exists := m.data[key]; exists {
				t.Fatal("retired key survived", key)
			}
		}
		if key == "enrollment/serialless" || key == "queue/old" || key == "credential/account" || key == "schedule/account" || key == "account/account" || key == "audit/old" {
			if string(m.data[key]) != string(value) {
				t.Fatal("operational state changed", key)
			}
		}
	}
	d, err := r.SourceResource(t.Context(), apple)
	if err != nil || d.Incomplete || string(d.Snapshot.Fields["attributes.serialNumber"]) != `"ACTUAL"` || string(d.Snapshot.Fields["attributes.unknown"]) != `[null,false,0,[],9007199254740993]` {
		t.Fatal(d, err)
	}
	if d.Source.AccountID != "original-account" || d.Source.ResourceID != "opaque/apple" {
		t.Fatal("source identity changed")
	}
	attempts, err := r.Attempts(t.Context(), apple)
	if err != nil || len(attempts) != 1 || attempts[0].Code != "apple_http_500" {
		t.Fatal(attempts, err)
	}
	n, err := r.SourceResource(t.Context(), native)
	if err != nil || !n.Incomplete || n.Snapshot.Native != nil {
		t.Fatal("native provenance invented or lost", n, err)
	}
	envelopes, err := r.Envelopes(t.Context(), native)
	if err != nil || len(envelopes) != 1 {
		t.Fatal(envelopes, err)
	}
	var nativeEnvelope Observation
	if err := json.Unmarshal(envelopes[0], &nativeEnvelope); err != nil || !nativeEnvelope.FieldTimes["QueryResponses.SerialNumber"].Equal(at) {
		t.Fatal("original field provenance lost", nativeEnvelope, err)
	}
	if _, exists := n.Snapshot.Fields["invented.normalized"]; exists {
		t.Fatal("legacy derived field became source fact")
	}
	var unknownEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(envelopes[0], &unknownEnvelope); err != nil || string(unknownEnvelope["unknown_provenance"]) != `{"future":null,"precise":9007199254740993}` {
		t.Fatal("unknown observation provenance lost", unknownEnvelope, err)
	}
	incomplete, err := r.SourceResource(t.Context(), source.Reference{Kind: "axm.coverage", AccountID: apple.AccountID, ResourceID: apple.ResourceID})
	if err != nil || !incomplete.Incomplete || len(incomplete.Snapshot.Raw) != 0 {
		t.Fatal(incomplete, err)
	}
	needs, err := r.RecoveryNeeds(t.Context())
	if err != nil || len(needs) != 1 || needs[0].LegacyID != "no-original" {
		t.Fatal(needs, err)
	}
	links, err := r.Links(t.Context(), apple)
	if err != nil || len(links) != 0 {
		t.Fatal("fabricated cross-source link", links, err)
	}
	running, err := r.Job(t.Context(), "running")
	if err != nil || running.State != "queued" || running.Stage != "devices" || running.Cursor != "" || len(running.Checkpoint) != 0 || running.Devices != 0 {
		t.Fatal(running, err)
	}
	paused, err := r.Job(t.Context(), "paused")
	if err != nil || paused.State != "paused" || paused.Stage != "devices" {
		t.Fatal(paused, err)
	}
	old := Job{ID: "running", Owner: "old-worker"}
	if err := r.commitJob(t.Context(), &old, func(tx Tx) error { return put(t.Context(), tx, "device/late", true) }); !errors.Is(err, ErrLease) {
		t.Fatal("old response committed", err)
	}
	if err := r.release(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	leaseAfter, err := get[lease](t.Context(), m, "lease/axm")
	if err != nil || leaseAfter.Owner == old.Owner || !leaseAfter.Until.Equal(at.Add(2*time.Minute)) {
		t.Fatal("outbound deadline lost", leaseAfter, err)
	}
	if err := m.Update(t.Context(), func(tx Tx) error { return put(t.Context(), tx, "device/late-native", true) }); !errors.Is(err, ErrConflict) {
		t.Fatal("old observer committed", err)
	}
	snapshot := cloneMemory(m)
	if err := r.Migrate(t.Context(), at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.data, m.data) {
		t.Fatal("repeat migration changed evidence")
	}
	// Refreshing a missing original is ordinary collection, without aliases.
	ref := incomplete.Source
	observe(t, r, "FABRICATED", ref.Kind, ref.AccountID, ref.ResourceID, `[]`, at.Add(time.Hour))
	current, err := r.SourceResource(t.Context(), ref)
	if err != nil || current.Incomplete || string(current.Snapshot.Raw) != "[]" {
		t.Fatal(current, err)
	}
}

// TestMigrationInterruptedAtEveryBoundary verifies complete rollback and safe retry
// for each read, scan, write and deletion, including the final model fence.
func TestMigrationInterruptedAtEveryBoundary(t *testing.T) {
	seed, _, _, at := legacyFixture(t)
	baseline := &faultBackend{Memory: cloneMemory(seed)}
	if err := (&Repository{Backend: baseline}).Migrate(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= baseline.calls; n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			backend := &faultBackend{Memory: cloneMemory(seed), failAt: n}
			r := &Repository{Backend: backend}
			if err := r.Migrate(t.Context(), at); !errors.Is(err, errStorageFailure) {
				t.Fatal("failure hidden", err)
			}
			if !reflect.DeepEqual(seed.data, backend.data) {
				t.Fatal("partial migration survived")
			}
			backend.failAt = 0
			if err := r.Migrate(t.Context(), at); err != nil {
				t.Fatal("retry", err)
			}
			for key := range backend.data {
				if RetiredKey(key) {
					t.Fatal("retry retained aggregate", key)
				}
			}
		})
	}
}

// TestMigrationMissingResourceOriginals treats valid JSON null and malformed
// organization-resource shapes as incomplete evidence, retaining original envelopes.
func TestMigrationMissingResourceOriginals(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"id":"opaque","type":"orgDevices"}`, `{"id":"different","type":"orgDevices","attributes":{}}`, `{"id":"opaque","type":"orgDevices","attributes":null}`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			r := repository(t)
			at := time.Now().UTC()
			ref := source.Reference{Kind: "axm.device", AccountID: "a", ResourceID: "opaque"}
			envelope := map[string]any{"source": ref, "observed_at": at, "raw": json.RawMessage(raw), "fields": map[string]json.RawMessage{"attributes.serialNumber": json.RawMessage(`"FALSE-ALIAS"`)}, "unknown_metadata": json.RawMessage(`{"precise":9007199254740993}`)}
			legacy, err := json.Marshal(map[string]any{"id": "legacy", "serial_number": "FALSE-ALIAS", "sources": map[string]any{ref.Key(): envelope}})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Backend.Update(t.Context(), func(tx Tx) error { return tx.Put(t.Context(), "device/legacy", legacy) }); err != nil {
				t.Fatal(err)
			}
			if err := r.Migrate(t.Context(), at); err != nil {
				t.Fatal(err)
			}
			current, err := r.SourceResource(t.Context(), ref)
			if err != nil || !current.Incomplete || len(current.Snapshot.Fields) != 0 {
				t.Fatal("missing original fabricated facts", current, err)
			}
			envelopes, err := r.Envelopes(t.Context(), ref)
			if err != nil || len(envelopes) != 1 {
				t.Fatal(envelopes, err)
			}
			if !bytes.Contains(envelopes[0], []byte(`"precise":9007199254740993`)) {
				t.Fatal("unknown incomplete envelope lost")
			}
			complete := observe(t, r, "FALSE-ALIAS", ref.Kind, ref.AccountID, ref.ResourceID, `{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"ACTUAL"}}`, at.Add(time.Hour))
			if complete.Incomplete || String(complete.Snapshot.Fields["attributes.serialNumber"]) != "ACTUAL" {
				t.Fatal("not refreshable", complete)
			}
		})
	}
}

// TestMigrationMalformedMarkerAndTerminalHistory fails closed on corrupt model
// markers and preserves terminal job history even when older optional dates are absent.
func TestMigrationMalformedMarkerAndTerminalHistory(t *testing.T) {
	for _, marker := range []string{`false`, `42`} {
		r := repository(t)
		if err := r.Backend.Update(t.Context(), func(tx Tx) error { return tx.Put(t.Context(), EvidenceModelKey, json.RawMessage(marker)) }); err != nil {
			t.Fatal(err)
		}
		if err := r.Migrate(t.Context(), time.Now()); err == nil {
			t.Fatal("corrupt model marker skipped migration")
		}
	}
	r := repository(t)
	raw := json.RawMessage(`{"id":"terminal","state":"success","unknown_audit":{"counter":9007199254740993}}`)
	if err := r.Backend.Update(t.Context(), func(tx Tx) error { return tx.Put(t.Context(), "job/terminal", raw) }); err != nil {
		t.Fatal(err)
	}
	if err := r.Migrate(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	current, err := r.Backend.Get(t.Context(), "job/terminal")
	if err != nil || string(current) != string(raw) {
		t.Fatal("terminal audit metadata changed", string(current), err)
	}
}

// TestMigrationUnidentifiedOriginal preserves opaque observations even when no
// trustworthy source identity remains; it cannot turn aliases into provenance.
func TestMigrationUnidentifiedOriginal(t *testing.T) {
	r := repository(t)
	raw := json.RawMessage(`{"id":"legacy","sources":{"old":{"source":{"account_id":"retained"},"raw":{"future":null},"unknown_metadata":9007199254740993}}}`)
	if err := r.Backend.Update(t.Context(), func(tx Tx) error { return tx.Put(t.Context(), "device/legacy", raw) }); err != nil {
		t.Fatal(err)
	}
	if err := r.Migrate(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	orphans, err := r.Backend.Scan(t.Context(), "orphan/", "", 100)
	if err != nil || len(orphans) != 1 || !bytes.Contains(orphans[0].Value, []byte(`"unknown_metadata":9007199254740993`)) {
		t.Fatal("unidentified original lost", orphans, err)
	}
	needs, err := r.RecoveryNeeds(t.Context())
	if err != nil || len(needs) != 1 {
		t.Fatal(needs, err)
	}
	resources, err := r.Resources(t.Context(), ResourceQuery{})
	if err != nil || len(resources.Items) != 0 {
		t.Fatal("source identity fabricated", resources, err)
	}
}

// TestLegacyAssignmentAndStatusOriginals unwraps only retained original source
// JSON; synthesized absence and effective-only status require a refresh.
func TestLegacyAssignmentAndStatusOriginals(t *testing.T) {
	server := `{"id":"server","type":"mdmServers","attributes":{"future":null,"flag":false}}`
	for _, tc := range []struct {
		name, kind, raw, want string
		incomplete            bool
	}{
		{"original server", "axm.assignment", `{"mdm_assigned":true,"mdm_server_raw":` + server + `}`, server, false},
		{"ambiguous false", "axm.assignment", `{"mdm_assigned":false}`, "", true},
		{"missing original", "axm.assignment", `{"mdm_assigned":true,"mdm_server":{"id":"server"}}`, "", true},
		{"ambiguous null", "axm.assignment", `{"mdm_assigned":false,"mdm_server_raw":null}`, "", true},
		{"legacy null", "axm.assignment", `null`, "", true},
		{"wrong shape", "axm.assignment", `[]`, "", true},
		{"direct server", "axm.assignment", server, server, false},
		{"status original", "ddm.status", `{"report":{"StatusItems":{"future":false}},"effective":{"fabricated_alias":true}}`, `{"StatusItems":{"future":false}}`, true},
		{"effective-only report", "ddm.status", `{"report":{"backfilled_effective_values":[]},"effective":{"fabricated_alias":true}}`, "", true},
		{"effective-only snapshot", "ddm.status", `{"backfilled_effective_values":[]}`, "", true},
		{"missing report", "ddm.status", `{"report":null,"effective":{}}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := repository(t)
			at := time.Now().UTC()
			ref := source.Reference{Kind: tc.kind, AccountID: "a", ResourceID: "device"}
			legacy, err := json.Marshal(map[string]any{"id": "legacy", "sources": map[string]Observation{ref.Key(): {Source: ref, ObservedAt: at, Raw: json.RawMessage(tc.raw)}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Backend.Update(t.Context(), func(tx Tx) error { return tx.Put(t.Context(), "device/legacy", legacy) }); err != nil {
				t.Fatal(err)
			}
			if err := r.Migrate(t.Context(), at); err != nil {
				t.Fatal(err)
			}
			record, err := r.SourceResource(t.Context(), ref)
			if err != nil || record.Incomplete != tc.incomplete || string(record.Snapshot.Raw) != tc.want {
				t.Fatal(record, string(record.Snapshot.Raw), err)
			}
			if _, ok := record.Snapshot.Fields["mdm_assigned"]; ok {
				t.Fatal("normalized assignment persisted")
			}
			if _, ok := record.Snapshot.Fields["fabricated_alias"]; ok {
				t.Fatal("derived status alias persisted")
			}
			envelopes, err := r.Envelopes(t.Context(), ref)
			if err != nil || len(envelopes) != 1 {
				t.Fatal(envelopes, err)
			}
		})
	}
}
