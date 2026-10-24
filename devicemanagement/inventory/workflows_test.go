package inventory

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// TestAccountLifecycle verifies revision fencing, verification and secret separation.
func TestAccountLifecycle(t *testing.T) {
	r, _, _, a := syncFixture(t)
	ctx, now := t.Context(), time.Now().UTC()
	accounts, err := r.Accounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].ID != a.ID {
		t.Fatal(accounts, err)
	}
	if err := r.VerifyAccount(ctx, a.ID, a.Revision, now); err != nil {
		t.Fatal(err)
	}
	a, err = r.Account(ctx, a.ID)
	if err != nil || a.VerifiedAt == nil || !a.VerifiedAt.Equal(now) {
		t.Fatal(a, err)
	}
	a.Name = "Renamed"
	updated, err := r.SaveAccount(ctx, a, nil, now.Add(time.Minute))
	if err != nil || updated.VerifiedAt == nil || updated.Revision != a.Revision+1 {
		t.Fatal(updated, err)
	}
	if err := r.VerifyAccount(ctx, a.ID, a.Revision, now); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := r.VerifyAccount(ctx, "missing", 1, now); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.SaveAccount(ctx, a, nil, now); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for _, change := range []func(*Account){func(a *Account) { a.Name = "" }, func(a *Account) { a.ID = "../bad" }, func(a *Account) { a.CoverageBudget = -1 }, func(a *Account) { a.ClientID = "BUSINESSAPI.other" }} {
		bad := updated
		change(&bad)
		if _, err := r.SaveAccount(ctx, bad, nil, now); !errors.Is(err, ErrInvalid) {
			t.Fatal(bad, err)
		}
	}
	if _, err := r.SaveAccount(ctx, updated, []byte("invalid PEM"), now); err == nil {
		t.Fatal("accepted invalid key")
	}
	for _, raw := range []string{`{`, `{"device_ttl":"bad"}`, `{"coverage_ttl":true}`} {
		var a Account
		if err := json.Unmarshal([]byte(raw), &a); err == nil {
			t.Fatal(raw)
		}
	}
	if _, err := json.Marshal(Account{CreatedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}); err == nil {
		t.Fatal("invalid timestamp encoded")
	}
}

// TestScheduleQueueAndRetention checks due-run coalescing and terminal-only pruning.
func TestScheduleQueueAndRetention(t *testing.T) {
	r, _, _, a := syncFixture(t)
	ctx, now := t.Context(), time.Now().UTC().Truncate(time.Minute)
	s, err := r.SaveSchedule(ctx, Schedule{AccountID: a.ID, Expression: "*/5 * * * *", Enabled: true}, now)
	if err != nil || s.TimeZone != "UTC" || !s.Next.After(now) {
		t.Fatal(s, err)
	}
	if err := r.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	due := s.Next.Add(2 * time.Hour)
	if err := r.Tick(ctx, due); err != nil {
		t.Fatal(err)
	}
	jobs, err := r.Jobs(ctx, "", 100)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err := r.Tick(ctx, due.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	jobs, err = r.Jobs(ctx, "", 100)
	if err != nil || len(jobs) != 1 {
		t.Fatal("did not coalesce", jobs, err)
	}
	for _, tc := range []struct {
		action string
		want   error
	}{{"resume", ErrConflict}, {"bogus", ErrInvalid}, {"pause", nil}, {"pause", ErrConflict}, {"resume", nil}, {"cancel", nil}, {"cancel", ErrConflict}} {
		if err := r.Control(ctx, jobs[0].ID, tc.action, now); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v", tc.action, err)
		}
	}
	if err := r.Control(ctx, "missing", "pause", now); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := r.Backend.Update(ctx, func(tx Tx) error { return put(ctx, tx, "jobpage/"+jobs[0].ID+"/seen", true) }); err != nil {
		t.Fatal(err)
	}
	n, err := r.PruneJobs(ctx, now.Add(48*time.Hour), 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := r.Job(ctx, jobs[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if pages, err := r.Backend.Scan(ctx, "jobpage/", "", 100); err != nil || len(pages) != 0 {
		t.Fatal(pages, err)
	}
	if _, err := r.PruneJobs(ctx, now, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.SaveSchedule(ctx, Schedule{AccountID: "missing", Expression: "* * * * *"}, now); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.SaveSchedule(ctx, Schedule{AccountID: a.ID, Expression: "bad"}, now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	a.Enabled = false
	if _, err := r.SaveAccount(ctx, a, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := r.Tick(ctx, due.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	schedules, err := r.Schedules(ctx)
	if err != nil || len(schedules) != 1 || schedules[0].Last == nil {
		t.Fatal(schedules, err)
	}
}

// TestScheduleInvalidInputs exercises calendar limits and malformed cron syntax.
func TestScheduleInvalidInputs(t *testing.T) {
	now := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	for _, expr := range []string{"* *", "*/0 * * * *", "1/2/3 * * * *", "a * * * *", "1-a * * * *", "1-2-3 * * * *", "60 * * * *", "0 0 31 2 *"} {
		if _, err := NextRun(expr, "", now); !errors.Is(err, ErrInvalid) {
			t.Fatal(expr, err)
		}
	}
	if _, err := NextRun("* * * * *", "invalid/zone", now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if next, err := NextRun("5/10 * * * *", "", now); err != nil || next.Minute() != 5 {
		t.Fatal(next, err)
	}
	for _, s := range []Schedule{{RepeatEvery: 1}, {RepeatUnit: "hours", RepeatEvery: 1}, {RepeatUnit: "hours", RepeatEvery: 1, Anchor: now, Expression: "* * * * *"}, {RepeatUnit: "hours", RepeatEvery: 1, Anchor: now, TimeZone: "bad"}, {RepeatUnit: "years", RepeatEvery: 1, Anchor: now}, {RepeatUnit: "months", RepeatEvery: 1000, Anchor: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		if _, err := s.NextOccurrence(now); !errors.Is(err, ErrInvalid) {
			t.Fatal(s, err)
		}
	}
	for _, unit := range []string{"minutes", "hours", "days", "weeks", "months"} {
		s := Schedule{RepeatUnit: unit, RepeatEvery: 1, Anchor: now}
		next, err := s.NextOccurrence(now.Add(-48 * time.Hour))
		if err != nil || !next.After(now) {
			t.Fatal(unit, next, err)
		}
	}
}

// TestTypedQueryPredicates verifies type-sensitive comparisons, dates and validation limits.
func TestTypedQueryPredicates(t *testing.T) {
	for _, tc := range []struct {
		a, op, b string
		want     bool
	}{
		{`1`, "lt", `2`, true},
		{`2`, "lte", `2`, true},
		{`3`, "gt", `2`, true},
		{`2`, "gte", `2`, true},
		{`1`, "gt", `"0"`, false},
		{`"a"`, "lt", `"b"`, true},
		{`"a"`, "lt", `1`, false},
		{`false`, "gt", `true`, false},
		{`"2026-01-01T00:00:00Z"`, "lt", `"2026-02-01T00:00:00Z"`, true},
		{`"2026-03-01T00:00:00Z"`, "gt", `"2026-02-01T00:00:00Z"`, true},
		{`"2026-03-01T00:00:00Z"`, "gte", `"2026-03-01T00:00:00Z"`, true},
		{`[1,2]`, "contains", `3`, false},
		{`1`, "contains", `1`, false},
		{`[1,2]`, "contains", `2`, true},
		{`null`, "eq", `null`, true},
		{`1`, "bad", `2`, false},
		{`{`, "eq", `2`, false},
		{`1`, "eq", `{`, false},
		{`1e5000`, "gt", `2`, false},
	} {
		if got := compare(json.RawMessage(tc.a), Condition{"field", tc.op, json.RawMessage(tc.b)}); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, q := range []ResourceQuery{{Limit: -1}, {Limit: 1001}, {Focus: "bad"}, {Conditions: make([]Condition, 65)}, {Conditions: []Condition{{Field: ""}}}, {Conditions: []Condition{{Field: strings.Repeat("a", 1025)}}}, {Conditions: []Condition{{"meta.id", "exists", json.RawMessage(`1`)}}}, {Conditions: []Condition{{"meta.id", "eq", json.RawMessage(`{`)}}}, {Conditions: []Condition{{"meta.id", "unknown", nil}}}} {
		if !errors.Is(q.Validate(), ErrInvalid) {
			t.Fatal(q)
		}
	}
	for _, number := range []json.Number{json.Number(strings.Repeat("9", 129)), "bad", "1eX", "1e5000"} {
		if rational(number) != nil {
			t.Fatal(number)
		}
	}
	if _, err := canonicalJSON([]byte(`{`)); err == nil {
		t.Fatal("invalid JSON")
	}
	if !equalJSON(json.Number("1e5000"), json.Number("1e5000")) {
		t.Fatal("bounded exponent equality")
	}
}

// TestReportsExportsAndDiscovery checks that every presentation uses the same source and query scope.
func TestReportsExportsAndDiscovery(t *testing.T) {
	r := repository(t)
	now := time.Now()
	for i := range 6 {
		observe(t, r, "S", "axm.device", "a", fmt.Sprint(i), `{"attributes":{"futureSecret":"hidden","empty":[],"flag":false}}`, now)
	}
	report, err := r.Report(t.Context(), ResourceQuery{})
	if err != nil || report.Resources != 6 || report.Facets["sources"]["axm.device"] != 6 {
		t.Fatal(report, err)
	}
	for _, raw := range []bool{false, true} {
		fields, err := r.Fields(t.Context(), ResourceQuery{}, raw)
		if err != nil || slices.Contains(fields, "data.attributes.futureSecret") != raw {
			t.Fatal(fields, err)
		}
		for _, format := range []string{"json", "ndjson", "csv"} {
			var b bytes.Buffer
			if err := r.Export(t.Context(), &b, ResourceQuery{}, format, nil, raw); err != nil {
				t.Fatal(err)
			}
			if format != "csv" && strings.Contains(b.String(), "hidden") != raw {
				t.Fatal("raw authority", raw, b.String())
			}
			if format == "json" {
				var resources []ResourceRecord
				if err := json.Unmarshal(b.Bytes(), &resources); err != nil || len(resources) != 6 {
					t.Fatal(err, len(resources))
				}
			}
			if format == "csv" {
				rows, err := csv.NewReader(&b).ReadAll()
				if err != nil || len(rows) != 7 {
					t.Fatal(rows, err)
				}
			}
		}
	}
	preset, err := r.SavePreset(t.Context(), ExportPreset{Name: "Evidence", Format: "csv", Columns: []string{"meta.source", "meta.id"}})
	if err != nil {
		t.Fatal(err)
	}
	presets, err := r.Presets(t.Context())
	if err != nil || len(presets) != 1 || !reflect.DeepEqual(preset, presets[0]) {
		t.Fatal(presets, err)
	}
	if _, err := r.SavePreset(t.Context(), ExportPreset{Name: "bad", Format: "yaml"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := r.Export(t.Context(), io.Discard, ResourceQuery{}, "bad", nil, false); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := r.Export(t.Context(), io.Discard, ResourceQuery{}, "csv", []string{"data.attributes.futureSecret"}, false); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// writeFailure fails a selected write so streaming callers cannot report partial output as success.
type writeFailure struct{ remaining int }

// Write injects a deterministic output failure.
func (w *writeFailure) Write(p []byte) (int, error) {
	if w.remaining == 0 {
		return 0, io.ErrClosedPipe
	}
	w.remaining--
	return len(p), nil
}

// TestExportWriteFailures checks errors at stream opening, separators, records and closing.
func TestExportWriteFailures(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	observe(t, r, "A", "axm.device", "a", "a", `{"attributes":{"deviceName":"=formula"}}`, at)
	observe(t, r, "B", "axm.device", "a", "b", `{}`, at)
	for _, format := range []string{"json", "ndjson", "csv"} {
		for n := 0; n < 6; n++ {
			err := r.Export(t.Context(), &writeFailure{n}, ResourceQuery{}, format, nil, false)
			if err != nil && !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal(err)
			}
			if n == 0 && err == nil {
				t.Fatal("ignored output failure", format)
			}
		}
	}
	var b bytes.Buffer
	if err := r.Export(t.Context(), &b, ResourceQuery{}, "csv", []string{"data.attributes.deviceName", "meta.id"}, true); err != nil || !strings.Contains(b.String(), "'=formula") {
		t.Fatal(b.String(), err)
	}
	columns := make([]string, 1000)
	for i := range columns {
		columns[i] = "meta.id"
	}
	if err := r.Export(t.Context(), &writeFailure{}, ResourceQuery{}, "csv", columns, true); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

// TestMemoryIsolationAndCancellation verifies read copies, rollback and transactional scans.
func TestMemoryIsolationAndCancellation(t *testing.T) {
	m := NewMemory()
	ctx := t.Context()
	if _, err := New(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := m.Update(ctx, func(tx Tx) error { return tx.Put(ctx, "item/a", json.RawMessage(`1`)) }); err != nil {
		t.Fatal(err)
	}
	raw, err := m.Get(ctx, "item/a")
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '9'
	again, _ := m.Get(ctx, "item/a")
	if string(again) != "1" {
		t.Fatal("shared bytes")
	}
	if err := m.Update(ctx, func(tx Tx) error {
		if err := tx.Delete(ctx, "item/a"); err != nil {
			return err
		}
		if err := tx.Put(ctx, "item/b", json.RawMessage(`2`)); err != nil {
			return err
		}
		rows, err := tx.Scan(ctx, "item/", "", 1)
		if err != nil || len(rows) != 1 || rows[0].Key != "item/b" {
			t.Fatal(rows, err)
		}
		_, err = tx.Get(ctx, "item/a")
		if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, op := range []func() error{
		func() error { _, e := m.Get(cancelled, "item/b"); return e },
		func() error { _, e := m.Scan(cancelled, "item/", "", 10); return e },
		func() error { return m.Update(cancelled, func(Tx) error { return nil }) },
		func() error {
			return m.Update(ctx, func(tx Tx) error { return tx.Put(cancelled, "key", json.RawMessage(`1`)) })
		},
		func() error { return m.Update(ctx, func(tx Tx) error { return tx.Delete(cancelled, "key") }) },
	} {
		if err := op(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if _, err := m.Scan(ctx, "", "", 0); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, key := range []string{"", "\x00", strings.Repeat("x", 513)} {
		if err := m.Update(ctx, func(tx Tx) error { return tx.Put(ctx, key, json.RawMessage(`1`)) }); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := m.Update(ctx, func(tx Tx) error { return put(ctx, tx, "bad", make(chan int)) }); err == nil {
		t.Fatal("encoded channel")
	}
}

// TestRecordEvidenceLifecycle verifies successful history, failed outcomes and source lifecycle.
func TestRecordEvidenceLifecycle(t *testing.T) {
	r := repository(t)
	now := time.Now().UTC()
	ref := source.Reference{Kind: "mdm.DeviceInformation", ResourceID: "device"}
	first := observe(t, r, "S", ref.Kind, ref.AccountID, ref.ResourceID, `{"QueryResponses":{"OSVersion":"27.0","Flag":false}}`, now)
	observe(t, r, "S", ref.Kind, ref.AccountID, ref.ResourceID, `{"QueryResponses":{"Model":"Mac"}}`, now.Add(time.Hour))
	observe(t, r, "S", ref.Kind, ref.AccountID, ref.ResourceID, `{"QueryResponses":{"Old":true}}`, now.Add(-time.Hour))
	if err := r.Attempt(t.Context(), ref, now.Add(2*time.Hour), "failed"); err != nil {
		t.Fatal(err)
	}
	current, err := r.Resource(t.Context(), first.ID)
	if err != nil || string(current.Snapshot.Fields["QueryResponses.Model"]) != `"Mac"` {
		t.Fatal(current, err)
	}
	history, err := r.History(t.Context(), ref)
	if err != nil || len(history) != 3 {
		t.Fatal(history, err)
	}
	attempts, err := r.Attempts(t.Context(), ref)
	if err != nil || len(attempts) != 1 {
		t.Fatal(attempts, err)
	}
	if err := r.ChangeSource(t.Context(), ref, true, true); err != nil {
		t.Fatal(err)
	}
	current, err = r.Resource(t.Context(), first.ID)
	if err != nil || !current.Snapshot.Absent || !current.Snapshot.Disconnected {
		t.Fatal(current, err)
	}
	for _, q := range []ResourceQuery{{Focus: "managed"}, {Conditions: []Condition{{"data.QueryResponses.Model", "exists", nil}}}, {Conditions: []Condition{{"data.missing", "exists", json.RawMessage(`false`)}}}} {
		page, err := r.Resources(t.Context(), q)
		if err != nil || len(page.Items) != 1 {
			t.Fatal(q, page, err)
		}
	}
	if _, err := ResourceObservation(ref, json.RawMessage(`{`), now, time.Hour); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.Observe(t.Context(), "", Observation{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.Resource(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if PublicField("unknown") {
		t.Fatal("unreviewed field exposed")
	}
}

// TestDeviceStreamingCrossesPages checks continuation without duplicate or omitted records.
func TestDeviceStreamingCrossesPages(t *testing.T) {
	r := repository(t)
	ctx := t.Context()
	for i := range 260 {
		d := ResourceRecord{ID: fmt.Sprintf("%03d", i)}
		if err := r.Backend.Update(ctx, func(tx Tx) error { return put(ctx, tx, "resource/"+d.ID, d) }); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	if err := r.EachResource(ctx, ResourceQuery{}, func(d ResourceRecord) error {
		if d.ID != fmt.Sprintf("%03d", count) {
			t.Fatal(d.ID, count)
		}
		count++
		return nil
	}); err != nil || count != 260 {
		t.Fatal(count, err)
	}
}
