package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// TestTargetedAssignmentAndCoverage checks real source reads for each refresh stream.
func TestTargetedAssignmentAndCoverage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		op       collection.Operation
		assigned bool
	}{
		{"unassigned", collection.Assignment, false}, {"assigned", collection.Assignment, true}, {"coverage", collection.Coverage, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, remote, a := syncFixture(t)
			remote.AddOrgDevice("opaque", map[string]any{"serialNumber": "SER"})
			if tc.name == "unassigned" {
				base := remote.Client().Transport
				remote.Client().Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
					if strings.HasSuffix(req.URL.Path, "/assignedServer") {
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":null}`)), Request: req}, nil
					}
					return base.RoundTrip(req)
				})
			}
			if tc.assigned {
				server := remote.AddMDMServer("Managed", nil)
				remote.Assign("opaque", server)
			}
			if tc.op == collection.Coverage {
				for i := range 1001 {
					remote.AddAppleCareCoverage("opaque", fmt.Sprintf("plan-%04d", i), map[string]any{"future": false})
				}
			}
			job, err := r.EnqueueRefresh(t.Context(), RefreshRequest{AccountID: a.ID, ResourceID: "opaque", Operations: []collection.Operation{tc.op}}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			ran, err := s.RunOne(t.Context())
			if err != nil || ran.State != "success" || ran.ID != job.ID {
				t.Fatal(ran, err)
			}
			resource, err := r.SourceResource(t.Context(), source.Reference{Kind: "axm." + string(tc.op), AccountID: a.ID, ResourceID: "opaque"})
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "unassigned" && string(resource.Snapshot.Raw) != "null" {
				t.Fatal("unassigned was not retained as null")
			}
			if tc.op == collection.Coverage {
				var plans []json.RawMessage
				if json.Unmarshal(resource.Snapshot.Raw, &plans) != nil || len(plans) != 1001 {
					t.Fatal("coverage traversal incomplete")
				}
			}
		})
	}
}
