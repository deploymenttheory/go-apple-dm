package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// TestTargetedRefreshAndSuccessor checks coalescing and retained work during active refresh.
func TestTargetedRefreshAndSuccessor(t *testing.T) {
	r, s, remote, a := syncFixture(t)
	remote.AddOrgDevice("opaque", map[string]any{"serialNumber": "SER", "future": false})
	req := RefreshRequest{AccountID: a.ID, ResourceID: "opaque", Operations: []collection.Operation{collection.Detail}}
	first, err := r.EnqueueRefresh(t.Context(), req, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	same, err := r.EnqueueRefresh(t.Context(), req, time.Now())
	if err != nil || same.ID != first.ID {
		t.Fatal("queued work did not coalesce", err)
	}
	running, err := r.claim(t.Context(), "owner", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	next, err := r.EnqueueRefresh(t.Context(), req, time.Now())
	if err != nil || next.ID == running.ID {
		t.Fatal("lost in-flight successor", err)
	}
	coalesced, err := r.EnqueueRefresh(t.Context(), req, time.Now())
	if err != nil || coalesced.ID != next.ID {
		t.Fatal("successors did not coalesce", err)
	}
	if err := r.release(t.Context(), running); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		j, err := s.RunOne(t.Context())
		if err != nil || j.State != "success" {
			t.Fatalf("refresh %+v: %v", j, err)
		}
	}
	for _, kind := range []string{"axm.device", "axm.detail"} {
		d, err := r.SourceResource(t.Context(), source.Reference{Kind: kind, AccountID: a.ID, ResourceID: "opaque"})
		if err != nil || d.Snapshot.Fields["attributes.future"] == nil {
			t.Fatal("lost source evidence", kind, err)
		}
	}
	for _, request := range remote.Requests() {
		if request.Path == "/v1/orgDevices" {
			t.Fatal("targeted work enumerated the account")
		}
	}
}

// TestTargetedRefreshFailurePreservesEvidence keeps original observations after a failed targeted read.
func TestTargetedRefreshFailurePreservesEvidence(t *testing.T) {
	r, s, _, a := syncFixture(t)
	old := observe(t, r, "SER", "axm.detail", a.ID, "missing", `{"id":"missing","attributes":{"serialNumber":"SER","future":true}}`, time.Now())
	_, err := r.EnqueueRefresh(t.Context(), RefreshRequest{AccountID: a.ID, ResourceID: "missing", Operations: []collection.Operation{collection.Detail}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.RunOne(t.Context())
	if err != nil || j.State != "failed" {
		t.Fatal(j, err)
	}
	current, err := r.SourceResource(t.Context(), old.Source)
	if err != nil || current.Snapshot.Absent || string(current.Snapshot.Raw) != string(old.Snapshot.Raw) {
		t.Fatal("failed callback refresh replaced evidence", err)
	}
	if _, err := r.EnqueueRefresh(t.Context(), RefreshRequest{AccountID: a.ID}, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// TestTargetedQueueDoesNotReachOlderWorkers prevents old workers consuming unknown job kinds.
func TestTargetedQueueDoesNotReachOlderWorkers(t *testing.T) {
	r, _, _, a := syncFixture(t)
	_, err := r.EnqueueRefresh(t.Context(), RefreshRequest{AccountID: a.ID, ResourceID: "opaque", Operations: []collection.Operation{collection.Detail}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := r.Backend.Scan(t.Context(), "job/", "", 100)
	if err != nil || len(legacy) != 0 {
		t.Fatal("an older worker can consume targeted work", len(legacy), err)
	}
}
