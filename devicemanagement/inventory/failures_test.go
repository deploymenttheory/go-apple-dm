package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

var errStorageFailure = errors.New("inventory test storage failure")

// faultBackend injects failures at persistence boundaries while keeping real transaction semantics.
type faultBackend struct {
	*Memory
	calls, failAt int
}

// step fails precisely one persistence operation in a workflow.
func (b *faultBackend) step() error {
	b.calls++
	if b.calls == b.failAt {
		return errStorageFailure
	}
	return nil
}

// Get exercises repository behavior when a document cannot be read.
func (b *faultBackend) Get(ctx context.Context, k string) (json.RawMessage, error) {
	if e := b.step(); e != nil {
		return nil, e
	}
	return b.Memory.Get(ctx, k)
}

// Scan exercises repository behavior when a page cannot be read.
func (b *faultBackend) Scan(ctx context.Context, p, a string, n int) ([]Entry, error) {
	if e := b.step(); e != nil {
		return nil, e
	}
	return b.Memory.Scan(ctx, p, a, n)
}

// Update keeps injected write failures inside the real rollback boundary.
func (b *faultBackend) Update(ctx context.Context, fn func(Tx) error) error {
	if e := b.step(); e != nil {
		return e
	}
	return b.Memory.Update(ctx, func(tx Tx) error { return fn(faultTx{tx, b}) })
}

type faultTx struct {
	Tx
	b *faultBackend
}

// Get injects transaction read failures.
func (t faultTx) Get(ctx context.Context, k string) (json.RawMessage, error) {
	if e := t.b.step(); e != nil {
		return nil, e
	}
	return t.Tx.Get(ctx, k)
}

// Scan injects transaction range failures.
func (t faultTx) Scan(ctx context.Context, p, a string, n int) ([]Entry, error) {
	if e := t.b.step(); e != nil {
		return nil, e
	}
	return t.Tx.Scan(ctx, p, a, n)
}

// Put injects transaction write failures.
func (t faultTx) Put(ctx context.Context, k string, v json.RawMessage) error {
	if e := t.b.step(); e != nil {
		return e
	}
	return t.Tx.Put(ctx, k, v)
}

// Delete injects transaction removal failures.
func (t faultTx) Delete(ctx context.Context, k string) error {
	if e := t.b.step(); e != nil {
		return e
	}
	return t.Tx.Delete(ctx, k)
}

// cloneMemory isolates each failure position from mutations in other test cases.
func cloneMemory(source *Memory) *Memory {
	m := NewMemory()
	for k, v := range source.data {
		m.data[k] = append(json.RawMessage(nil), v...)
	}
	return m
}

// TestRepositoryFailureAtomicity checks every persistence boundary in public workflows.
func TestRepositoryFailureAtomicity(t *testing.T) {
	r, _, _, a := syncFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	key, e := r.PrivateKey(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	observe(t, r, "SERIAL", "axm.device", a.ID, "apple", `{"attributes":{"deviceModel":"Mac"}}`, now)
	observe(t, r, "", "enrollment", "", "enrolled", `{"managed":true}`, now.Add(-time.Hour))
	job, e := r.Enqueue(ctx, a.ID, false, now)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := r.claim(ctx, "worker", now)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := r.SaveSchedule(ctx, Schedule{AccountID: a.ID, Expression: "* * * * *"}, now); e != nil {
		t.Fatal(e)
	}
	if _, e := r.SavePreset(ctx, ExportPreset{Name: "test", Format: "csv"}); e != nil {
		t.Fatal(e)
	}
	old := now.Add(-72 * time.Hour)
	if e := r.Backend.Update(ctx, func(tx Tx) error {
		if e := put(ctx, tx, "job/finished", Job{ID: "finished", State: "success", FinishedAt: &old}); e != nil {
			return e
		}
		return put(ctx, tx, "jobpage/finished/first", true)
	}); e != nil {
		t.Fatal(e)
	}
	observation := Observation{Source: SourceReference{"axm.device", a.ID, "apple"}, Raw: json.RawMessage(`{"attributes":{"deviceModel":"New"}}`), ObservedAt: now.Add(time.Hour)}
	cases := []struct {
		name   string
		atomic bool
		op     func(*Repository) error
	}{
		{"save account", true, func(r *Repository) error { _, e := r.SaveAccount(ctx, a, key, now); return e }},
		{"verify", true, func(r *Repository) error { return r.VerifyAccount(ctx, a.ID, a.Revision, now) }},
		{"accounts", true, func(r *Repository) error { _, e := r.Accounts(ctx); return e }},
		{"observe update", true, func(r *Repository) error { _, e := r.Observe(ctx, "SERIAL", observation); return e }},
		{"observe new", true, func(r *Repository) error {
			o := observation
			o.Source.ResourceID = "new"
			_, e := r.Observe(ctx, "NEW", o)
			return e
		}},
		{"observe duplicate serial", true, func(r *Repository) error {
			o := observation
			o.Source.ResourceID = "duplicate"
			_, e := r.Observe(ctx, "SERIAL", o)
			return e
		}},
		{"observe mismatch", true, func(r *Repository) error { _, e := r.Observe(ctx, "WRONG", observation); return e }},
		{"merge provisional", true, func(r *Repository) error {
			o := observation
			o.Source = SourceReference{Kind: "mdm.DeviceInformation", ResourceID: "enrolled"}
			_, e := r.Observe(ctx, "SERIAL", o)
			return e
		}},
		{"source lookup", true, func(r *Repository) error { _, e := r.SourceDevice(ctx, observation.Source); return e }},
		{"attempt", true, func(r *Repository) error { return r.Attempt(ctx, observation.Source, now, "failed") }},
		{"attempt native", true, func(r *Repository) error {
			return r.Attempt(ctx, SourceReference{Kind: "mdm.SecurityInfo", ResourceID: "enrolled"}, now, "failed")
		}},
		{"lifecycle", true, func(r *Repository) error { return r.ChangeSource(ctx, observation.Source, true, false) }},
		{"enqueue coalesced", true, func(r *Repository) error { _, e := r.Enqueue(ctx, a.ID, false, now); return e }},
		{"jobs", true, func(r *Repository) error { _, e := r.Jobs(ctx, "", 100); return e }},
		{"pause", true, func(r *Repository) error { return r.Control(ctx, job.ID, "pause", now) }},
		{"commit", true, func(r *Repository) error {
			j := claimed
			return r.commitJob(ctx, &j, func(tx Tx) error { return put(ctx, tx, "checkpoint/test", true) })
		}},
		{"release", true, func(r *Repository) error { return r.release(ctx, claimed) }},
		{"claim expired", true, func(r *Repository) error { _, e := r.claim(ctx, "new-worker", now.Add(time.Hour)); return e }},
		{"delete account", true, func(r *Repository) error { return r.DeleteAccount(ctx, a.ID, a.Revision, now) }},
		{"cancel all", true, func(r *Repository) error { _, e := r.CancelAll(ctx, now); return e }},
		{"prune", true, func(r *Repository) error { _, e := r.PruneJobs(ctx, now, 24*time.Hour); return e }},
		{"schedule", true, func(r *Repository) error {
			_, e := r.SaveSchedule(ctx, Schedule{AccountID: a.ID, Expression: "* * * * *"}, now)
			return e
		}},
		{"schedules", true, func(r *Repository) error { _, e := r.Schedules(ctx); return e }},
		{"preset", true, func(r *Repository) error {
			_, e := r.SavePreset(ctx, ExportPreset{Name: "new", Format: "json"})
			return e
		}},
		{"presets", true, func(r *Repository) error { _, e := r.Presets(ctx); return e }},
		{"records", true, func(r *Repository) error { _, e := r.Devices(ctx, DeviceQuery{}); return e }},
		{"indexed records", true, func(r *Repository) error {
			_, e := r.Devices(ctx, DeviceQuery{Conditions: []Condition{{"model", "eq", json.RawMessage(`"Mac"`)}}})
			return e
		}},
		{"fields", true, func(r *Repository) error { _, e := r.Fields(ctx, DeviceQuery{}, false); return e }},
		{"report", true, func(r *Repository) error { _, e := r.Report(ctx, DeviceQuery{}); return e }},
	}
	seed := testMemory(t, r)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseline := &faultBackend{Memory: cloneMemory(seed)}
			if err := tc.op(&Repository{Backend: baseline}); err != nil {
				t.Fatal("baseline", err)
			}
			for n := 1; n <= baseline.calls; n++ {
				t.Run(fmt.Sprint(n), func(t *testing.T) {
					b := &faultBackend{Memory: cloneMemory(seed), failAt: n}
					err := tc.op(&Repository{Backend: b})
					if !errors.Is(err, errStorageFailure) {
						t.Fatalf("operation %d swallowed storage failure: %v", n, err)
					}
					if tc.atomic && !reflect.DeepEqual(seed.data, b.Memory.data) {
						t.Fatal("partial transaction survived failure")
					}
				})
			}
		})
	}
}

// TestCorruptDocumentsFailClosed checks decoding errors before any destructive mutation.
func TestCorruptDocumentsFailClosed(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	for _, tc := range []struct {
		key string
		op  func(*Repository) error
	}{
		{"account/a", func(r *Repository) error { _, e := r.Accounts(ctx); return e }},
		{"job/a", func(r *Repository) error { _, e := r.Jobs(ctx, "", 100); return e }},
		{"job/a", func(r *Repository) error { _, e := r.claim(ctx, "owner", now); return e }},
		{"job/a", func(r *Repository) error { _, e := r.CancelAll(ctx, now); return e }},
		{"job/a", func(r *Repository) error { _, e := r.PruneJobs(ctx, now, time.Hour); return e }},
		{"schedule/a", func(r *Repository) error { return r.Tick(ctx, now) }},
		{"preset/a", func(r *Repository) error { _, e := r.Presets(ctx); return e }},
		{"device/a", func(r *Repository) error { _, e := r.Devices(ctx, DeviceQuery{}); return e }},
		{indexPrefix("model", json.RawMessage(`"Mac"`)) + "a", func(r *Repository) error {
			_, e := r.Devices(ctx, DeviceQuery{Conditions: []Condition{{"model", "eq", json.RawMessage(`"Mac"`)}}})
			return e
		}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			r := repository(t)
			testMemory(t, r).data[tc.key] = json.RawMessage(`42`)
			if err := tc.op(r); err == nil {
				t.Fatal("accepted corrupt document")
			}
		})
	}
	r := repository(t)
	for i := range 17 {
		testMemory(t, r).data[fmt.Sprintf("alias/%d", i)] = json.RawMessage(fmt.Sprintf(`"%d"`, i+1))
	}
	if _, e := r.Device(ctx, "0"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

// testMemory validates the fixture backend before accessing its corruption hooks.
func testMemory(t *testing.T, r *Repository) *Memory {
	t.Helper()
	m, ok := r.Backend.(*Memory)
	if !ok {
		t.Fatal("fixture requires memory backend")
	}
	return m
}
