package axmassignment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

var errDisk = errors.New("fixture persistence failure")

type failingBackend struct {
	inventory.Backend
	at, count int
	hit       bool
}

// check fails precisely one storage boundary, including reads inside atomic transactions.
func (f *failingBackend) check() error {
	f.count++
	if f.count == f.at {
		f.hit = true
		return errDisk
	}
	return nil
}

// Get injects a bounded read failure.
func (f *failingBackend) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if e := f.check(); e != nil {
		return nil, e
	}
	return f.Backend.Get(ctx, key)
}

// Scan injects a bounded ordered-read failure.
func (f *failingBackend) Scan(ctx context.Context, prefix, after string, limit int) ([]inventory.Entry, error) {
	if e := f.check(); e != nil {
		return nil, e
	}
	return f.Backend.Scan(ctx, prefix, after, limit)
}

// Update preserves rollback while exposing every transactional operation to failure injection.
func (f *failingBackend) Update(ctx context.Context, fn func(inventory.Tx) error) error {
	if e := f.check(); e != nil {
		return e
	}
	return f.Backend.Update(ctx, func(tx inventory.Tx) error { return fn(&failingTx{Tx: tx, f: f}) })
}

type failingTx struct {
	inventory.Tx
	f *failingBackend
}

// Get injects a transactional document-read failure.
func (t *failingTx) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if e := t.f.check(); e != nil {
		return nil, e
	}
	return t.Tx.Get(ctx, key)
}

// Scan injects a transactional index-read failure.
func (t *failingTx) Scan(ctx context.Context, prefix, after string, limit int) ([]inventory.Entry, error) {
	if e := t.f.check(); e != nil {
		return nil, e
	}
	return t.Tx.Scan(ctx, prefix, after, limit)
}

// Put injects a write failure without committing partial workflow evidence.
func (t *failingTx) Put(ctx context.Context, key string, value json.RawMessage) error {
	if e := t.f.check(); e != nil {
		return e
	}
	return t.Tx.Put(ctx, key, value)
}

// Delete injects a lease-release failure within the same atomic transaction.
func (t *failingTx) Delete(ctx context.Context, key string) error {
	if e := t.f.check(); e != nil {
		return e
	}
	return t.Tx.Delete(ctx, key)
}

// TestPersistenceBoundaries sweeps each durable boundary and requires rollback and restart safety.
func TestPersistenceBoundaries(t *testing.T) {
	for _, stage := range []string{"configure", "request", "submit", "terminal", "evidence", "run"} {
		for at := 1; at <= 200; at++ {
			hit := false
			t.Run(fmt.Sprintf("%s/%d", stage, at), func(t *testing.T) {
				h := newHarness(t, inventory.NewMemory())
				ctx := t.Context()
				if stage != "configure" && stage != "request" {
					h.request(t, "failure")
				}
				if stage == "terminal" || stage == "evidence" {
					h.step(t, "failure")
					h.fake.Complete()
					h.due(t, "failure")
				}
				if stage == "evidence" {
					h.step(t, "failure")
				}
				f := &failingBackend{Backend: h.c.Repository.Backend, at: at}
				h.c.Repository.Backend = f
				var e error
				switch stage {
				case "configure":
					_, e = h.c.Configure(ctx, h.account.ID, h.account.Revision, h.service, axm.ActivityLogColumns{Serial: "s", Outcome: "o", Success: "y"})
				case "request":
					_, e = h.c.Request(ctx, h.account.ID, "new", []string{"SER1"}, time.Now())
				case "submit", "terminal":
					e = h.c.Step(ctx, "failure")
				case "evidence":
					_, e = h.c.Evidence(ctx, h.account.ID, "failure")
				case "run":
					runCtx, cancel := context.WithCancel(ctx)
					cancel()
					e = h.c.Run(runCtx)
				}
				hit = f.hit
				if hit && e == nil {
					t.Fatal("storage failure ignored")
				}
				h.c.Repository.Backend = f.Backend
				// Failed submissions never turn into queued work that could blindly repeat a POST.
				if h.tr.posts.Load() > 0 && stage == "submit" {
					w, readErr := h.c.Get(ctx, h.account.ID, "failure")
					if readErr != nil || w.State == "queued" {
						t.Fatal(w, readErr)
					}
				}
			})
			if !hit {
				break
			}
		}
	}
}

// TestInvalidDocuments covers malformed persisted account, credential, lease and workflow inputs.
func TestInvalidDocuments(t *testing.T) {
	for _, key := range []string{"account/named", "credential/named", "assignment-binding/named", "assignment-lease/job", "assignment/job"} {
		t.Run(key, func(t *testing.T) {
			h := newHarness(t, inventory.NewMemory())
			h.request(t, "job")
			if e := h.c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error { return tx.Put(t.Context(), key, json.RawMessage(`[]`)) }); e != nil {
				t.Fatal(e)
			}
			if e := h.c.Step(t.Context(), "job"); e == nil {
				w, e := h.c.Get(t.Context(), h.account.ID, "job")
				if e != nil || w.State != "fenced" || h.tr.posts.Load() != 0 {
					t.Fatal("invalid durable input accepted", w, e)
				}
			}
		})
	}
	h := newHarness(t, inventory.NewMemory())
	if e := h.c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error { return write(t.Context(), tx, "bad", make(chan int)) }); e == nil {
		t.Fatal("unencodable document accepted")
	}
	h.request(t, "future")
	if _, _, e := h.c.claim(t.Context(), "future", time.Now().Add(-time.Hour)); !errors.Is(e, inventory.ErrStopped) {
		t.Fatal(e)
	}
	h.c.Client = func(context.Context, inventory.Account, []byte) (*axm.Client, error) {
		return nil, errors.New("unavailable")
	}
	w := h.step(t, "future")
	if w.Error != "client_unavailable" {
		t.Fatal(w)
	}
}
