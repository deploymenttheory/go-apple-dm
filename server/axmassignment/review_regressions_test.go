package axmassignment

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

// TestReviewFencedUncertaintyBlocksDuplicate keeps remote uncertainty locked after local authority changes.
func TestReviewFencedUncertaintyBlocksDuplicate(t *testing.T) {
	h := newHarness(t, inventory.NewMemory())
	h.request(t, "old")
	h.tr.loseReply = true
	w := h.step(t, "old")
	if w.State != "uncertain" {
		t.Fatal(w)
	}
	h.tr.loseReply = false
	h.fake.Complete()
	w = h.step(t, "old")
	if !w.Targets[0].Converged || w.ActivityID != "" || w.AcceptedAt != nil || w.TerminalStatus != "" {
		t.Fatal("readback invented terminal evidence", w)
	}
	a, e := h.c.Repository.SaveAccount(t.Context(), h.account, h.key, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	h.tr.loseReply = false
	h.due(t, "old")
	if e = h.c.Step(t.Context(), "old"); e != nil {
		t.Fatal(e)
	}
	w, e = h.c.Get(t.Context(), a.ID, "old")
	if e != nil || w.State != "fenced" {
		t.Fatal(w, e)
	}
	if _, e = h.c.Configure(t.Context(), a.ID, a.Revision, h.service, w.Binding.Columns); e != nil {
		t.Fatal(e)
	}
	if _, e = h.c.Request(t.Context(), a.ID, "new", []string{"SER1", "SER2"}, time.Now()); !errors.Is(e, inventory.ErrConflict) {
		t.Fatalf("unresolved fenced submission allowed overlapping intent: %v", e)
	}
	if h.tr.posts.Load() != 1 {
		t.Fatal("duplicate mutation", h.tr.posts.Load())
	}
}

// TestReviewBindingABAFencesOldOwner rejects obsolete authority even when service values are restored.
func TestReviewBindingABAFencesOldOwner(t *testing.T) {
	h := newHarness(t, inventory.NewMemory())
	h.request(t, "old")
	w, owner, e := h.c.claim(t.Context(), "old", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.c.Configure(t.Context(), h.account.ID, h.account.Revision, "other-service", w.Binding.Columns); e != nil {
		t.Fatal(e)
	}
	if _, e = h.c.Configure(t.Context(), h.account.ID, h.account.Revision, h.service, w.Binding.Columns); e != nil {
		t.Fatal(e)
	}
	if e = h.c.commit(t.Context(), &w, owner, nil, true); !errors.Is(e, inventory.ErrStopped) {
		t.Fatalf("obsolete binding regained authority: %v", e)
	}
	// Once its old lease expires, unsubmitted work can be fenced without leaving remote uncertainty.
	if e = h.c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error { return tx.Delete(t.Context(), "assignment-lease/old") }); e != nil {
		t.Fatal(e)
	}
	w = h.step(t, "old")
	if w.State != "fenced" || w.SubmissionStartedAt != nil {
		t.Fatal(w)
	}
	if _, e = h.c.Request(t.Context(), h.account.ID, "replacement", []string{"SER1", "SER2"}, time.Now()); e != nil {
		t.Fatal("unsubmitted fenced work blocked replacement", e)
	}
	if h.tr.posts.Load() != 0 {
		t.Fatal("local reconfiguration mutated Apple")
	}
}
