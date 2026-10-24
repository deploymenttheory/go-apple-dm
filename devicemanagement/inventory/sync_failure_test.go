package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

type transportFunc func(*http.Request) (*http.Response, error)

// RoundTrip injects a fixture response or forwards the test request.
func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSyncCheckpointSurvivesInterruption proves that resume starts after the last committed page.
func TestSyncCheckpointSurvivesInterruption(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			repo, s, server, a := syncFixture(t)
			assignedServer := server.AddMDMServer("server", nil)
			for _, id := range []string{"one", "two", "three"} {
				server.AddOrgDevice(id, nil)
				server.Assign(id, assignedServer)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			interrupted := false
			base := server.Client().Transport
			s.Client = func(ctx context.Context, a Account, key []byte) (*axm.Client, error) {
				client := server.Client()
				client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
					copy := req.Clone(req.Context())
					u := *copy.URL
					copy.URL = &u
					if u.Path == "/v1/orgDevices" {
						q := u.Query()
						q.Set("limit", "1")
						u.RawQuery = q.Encode()
						calls++
						if calls == 2 && !interrupted {
							interrupted = true
							cancel()
							return nil, context.Canceled
						}
					}
					return base.RoundTrip(copy)
				})
				return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, BaseURL: a.BaseURL, TokenURL: a.TokenURL, HTTPClient: client})
			}
			job, e := repo.Enqueue(ctx, a.ID, false, time.Now())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.RunOne(ctx); e == nil {
				t.Fatal("interruption was hidden")
			}
			saved, e := repo.Job(t.Context(), job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if saved.Devices != 1 || len(saved.Checkpoint) == 0 || saved.State != "running" {
				t.Fatalf("checkpoint %+v", saved)
			}
			page, e := repo.Resources(t.Context(), ResourceQuery{Source: "axm.device"})
			if e != nil || len(page.Items) != 1 {
				t.Fatal("checkpoint advanced ahead of data", e)
			}
			if legacy {
				var checkpoint collection.Checkpoint
				if err := json.Unmarshal(saved.Checkpoint, &checkpoint); err != nil {
					t.Fatal(err)
				}
				saved.Checkpoint, e = json.Marshal(axm.Page[axm.OrgDevice]{Links: checkpoint.Links, Meta: checkpoint.Meta})
				if e != nil {
					t.Fatal(e)
				}
				if e := repo.Backend.Update(t.Context(), func(tx Tx) error { return put(t.Context(), tx, "job/"+saved.ID, saved) }); e != nil {
					t.Fatal(e)
				}
				if err := repo.Migrate(t.Context(), time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}

			resumed, e := s.RunOne(t.Context())
			if e != nil || resumed.State != "success" || resumed.Devices != 3 {
				t.Fatalf("resume %+v %v", resumed, e)
			}
			page, e = repo.Resources(t.Context(), ResourceQuery{Source: "axm.device"})
			if e != nil || len(page.Items) != 3 {
				t.Fatal("resume skipped devices", e)
			}
		})
	}
}

// TestCoverageFailureNeverMeansNoCoverage preserves unknown coverage and fences obsolete account revisions.
func TestCoverageFailureNeverMeansNoCoverage(t *testing.T) {
	repo, s, server, a := syncFixture(t)
	server.AddOrgDevice("serial", nil)
	assignedServer := server.AddMDMServer("server", nil)
	server.Assign("serial", assignedServer)
	fail := true
	base := server.Client().Transport
	s.Client = func(ctx context.Context, a Account, key []byte) (*axm.Client, error) {
		client := server.Client()
		client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
			if fail && strings.HasSuffix(req.URL.Path, "/appleCareCoverage") {
				return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"errors":[{"status":"404","code":"NOT_FOUND"}]}`)), Request: req}, nil
			}
			return base.RoundTrip(req)
		})
		return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, BaseURL: a.BaseURL, TokenURL: a.TokenURL, HTTPClient: client})
	}
	_, e := repo.Enqueue(t.Context(), a.ID, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.RunOne(t.Context())
	if e != nil || job.State != "partial" {
		t.Fatalf("result %+v %v", job, e)
	}
	page, e := repo.Resources(t.Context(), ResourceQuery{Source: "axm.device"})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	ref := source.Reference{Kind: "axm.coverage", AccountID: a.ID, ResourceID: "serial"}
	if _, err := repo.SourceResource(t.Context(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed coverage fabricated evidence", err)
	}
	attempts, err := repo.Attempts(t.Context(), ref)
	if err != nil || len(attempts) != 1 {
		t.Fatal(attempts, err)
	}

	fail = false
	_, e = repo.Enqueue(t.Context(), a.ID, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	job, e = s.RunOne(t.Context())
	if e != nil || job.State != "success" {
		t.Fatal(e, job)
	}
	page, e = repo.Resources(t.Context(), ResourceQuery{Source: "axm.device"})
	if e != nil {
		t.Fatal(e)
	}
	coverage, err := repo.SourceResource(t.Context(), ref)
	if err != nil || string(coverage.Snapshot.Raw) != "[]" {
		t.Fatal("empty successful original missing", coverage, err)
	}

	// Changing credentials/config fences an already claimed worker's next commit.
	_, e = repo.Enqueue(t.Context(), a.ID, true, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := repo.claim(t.Context(), "worker", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	a.Name = "Renamed"
	if _, e = repo.SaveAccount(t.Context(), a, nil, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = repo.commitJob(t.Context(), &claimed, nil); !errors.Is(e, ErrStopped) {
		t.Fatal("obsolete revision committed", e)
	}
}

// TestSyncPersistenceFailures checks every database boundary while real Apple responses are decoded.
func TestSyncPersistenceFailures(t *testing.T) {
	r, s, server, a := syncFixture(t)
	ctx := t.Context()
	server.AddOrgDevice("one", map[string]any{"serialNumber": "SER"})
	assignedServer := server.AddMDMServer("apple", map[string]any{"serverType": "APPLE_MDM"})
	server.Assign("one", assignedServer)
	server.AddMDMDevice("native", map[string]any{"serialNumber": "SER"}, map[string]any{"osVersion": "27.0"})
	if _, err := r.Enqueue(ctx, a.ID, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	claimed, err := r.claim(ctx, "worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key, err := r.PrivateKey(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	client, err := s.Client(ctx, a, key)
	if err != nil {
		t.Fatal(err)
	}
	seed := testMemory(t, r)
	baseline := &faultBackend{Memory: cloneMemory(seed)}
	trial := &Syncer{Repository: &Repository{Backend: baseline}}
	j := claimed
	if err := trial.sync(ctx, client, a, &j); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= baseline.calls; n++ {
		b := &faultBackend{Memory: cloneMemory(seed), failAt: n}
		trial.Repository = &Repository{Backend: b}
		j = claimed
		if err := trial.sync(ctx, client, a, &j); !errors.Is(err, errStorageFailure) {
			t.Fatalf("persistence operation %d: %v", n, err)
		}
	}
}

// TestSyncAppleFailureOutcomes distinguishes failed, partial and successful empty responses.
func TestSyncAppleFailureOutcomes(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
		want   string
	}{
		{"/v1/orgDevices", 403, "failed"},
		{"/v1/orgDevices/one", 403, "partial"},
		{"/v1/orgDevices/one/assignedServer", 403, "partial"},
		{"/v1/orgDevices/one/appleCareCoverage", 403, "partial"},
		{"/v1/mdmServers", 403, "partial"},
		{"/v1/mdmDevices", 403, "partial"},
		{"/v1/mdmDevices/native/details", 403, "partial"},
		{"/v1/orgDevices/one", 401, "partial"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r, s, server, a := syncFixture(t)
			server.AddOrgDevice("one", map[string]any{"serialNumber": "SER"})
			server.AddMDMServer("apple", map[string]any{"serverType": "APPLE_MDM"})
			server.AddMDMDevice("native", map[string]any{"serialNumber": "SER"}, nil)
			base := server.Client().Transport
			s.Client = func(ctx context.Context, a Account, key []byte) (*axm.Client, error) {
				cl := server.Client()
				cl.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == tc.path {
						return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"errors":[{"code":"DENIED"}]}`)), Request: req}, nil
					}
					return base.RoundTrip(req)
				})
				return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, BaseURL: a.BaseURL, TokenURL: a.TokenURL, HTTPClient: cl})
			}
			if _, err := r.Enqueue(t.Context(), a.ID, false, time.Now()); err != nil {
				t.Fatal(err)
			}
			j, err := s.RunOne(t.Context())
			if j.State != tc.want || j.Failed == 0 {
				t.Fatal(j, err)
			}
			stored, e := r.Job(t.Context(), j.ID)
			if e != nil || stored.State != j.State || stored.FinishedAt == nil {
				t.Fatal(stored, e)
			}
		})
	}
}

// TestSyncWorkerLifecycle checks default client construction, factory errors and shutdown.
func TestSyncWorkerLifecycle(t *testing.T) {
	r, s, _, a := syncFixture(t)
	ctx := t.Context()
	key, err := r.PrivateKey(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(ctx, a, key); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(ctx, a, []byte("bad")); err == nil {
		t.Fatal("invalid key")
	}
	if _, err := s.RunOne(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	s.Client = func(context.Context, Account, []byte) (*axm.Client, error) { return nil, context.DeadlineExceeded }
	if _, err := r.Enqueue(ctx, a.ID, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	j, err := s.RunOne(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || j.State != "failed" || j.Error != "timeout" {
		t.Fatal(j, err)
	}
	if _, err := r.Enqueue(ctx, a.ID, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	a.Enabled = false
	if _, err := r.SaveAccount(ctx, a, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	j, err = s.RunOne(ctx)
	if err != nil || j.State != "cancelled" {
		t.Fatal(j, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Run(cancelled); err != nil {
		t.Fatal(err)
	}
	timeout, stop := context.WithTimeout(ctx, 1100*time.Millisecond)
	defer stop()
	if err := s.Run(timeout); err != nil {
		t.Fatal(err)
	}
	if errorCode(&axm.AuthError{Status: 401}) != "apple_authentication" || errorCode(errors.New("host secret")) != "sync_error" {
		t.Fatal("unsafe error classification")
	}
}

// TestSyncMissingMembershipRequiresConfirmation preserves devices unless a complete run confirms absence.
func TestSyncMissingMembershipRequiresConfirmation(t *testing.T) {
	r, s, _, a := syncFixture(t)
	at := time.Now().UTC()
	old := observe(t, r, "OLD", "axm.device", a.ID, "removed", `{"attributes":{"serialNumber":"OLD"}}`, at.Add(-time.Hour))
	if _, err := r.Enqueue(t.Context(), a.ID, false, at); err != nil {
		t.Fatal(err)
	}
	job, err := s.RunOne(t.Context())
	if err != nil || job.State != "success" {
		t.Fatal(job, err)
	}
	d, err := r.Resource(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Snapshot.Absent {
		t.Fatal("confirmed absence not recorded")
	}
}

// TestAppleMDMPagination rejects repeated pages and follows ordinary multi-page device inventories.
func TestAppleMDMPagination(t *testing.T) {
	for _, loop := range []bool{false, true} {
		t.Run(fmt.Sprint(loop), func(t *testing.T) {
			r, s, server, a := syncFixture(t)
			server.AddMDMServer("apple", map[string]any{"serverType": "APPLE_MDM"})
			for _, id := range []string{"one", "two", "three"} {
				server.AddMDMDevice(id, map[string]any{"serialNumber": id}, nil)
			}
			base := server.Client().Transport
			s.Client = func(ctx context.Context, a Account, key []byte) (*axm.Client, error) {
				cl := server.Client()
				cl.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
					copy := req.Clone(req.Context())
					u := *req.URL
					copy.URL = &u
					if u.Path == "/v1/mdmDevices" {
						q := u.Query()
						q.Set("limit", "1")
						if loop {
							q.Del("cursor")
						}
						u.RawQuery = q.Encode()
					}
					return base.RoundTrip(copy)
				})
				return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, BaseURL: a.BaseURL, TokenURL: a.TokenURL, HTTPClient: cl})
			}
			if _, err := r.Enqueue(t.Context(), a.ID, false, time.Now()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			job, err := s.RunOne(ctx)
			if loop {
				if !errors.Is(err, axm.ErrNextLink) || job.State != "failed" {
					t.Fatal(job, err)
				}
			} else {
				if err != nil || job.State != "success" {
					t.Fatal(job, err)
				}
				page, err := r.Resources(t.Context(), ResourceQuery{Source: "axm.mdm"})
				if err != nil || len(page.Items) != 3 {
					t.Fatal(page, err)
				}
			}
		})
	}
}

// TestAssignmentOutcomesPreserveOriginalEvidence distinguishes a real successful
// null from 404, retaining the original server resource through later failures.
func TestAssignmentOutcomesPreserveOriginalEvidence(t *testing.T) {
	r, s, server, a := syncFixture(t)
	server.AddOrgDevice("one", map[string]any{"serialNumber": "S"})
	run := func() Job {
		t.Helper()
		if _, err := r.Enqueue(t.Context(), a.ID, true, time.Now()); err != nil {
			t.Fatal(err)
		}
		job, err := s.RunOne(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	ref := source.Reference{Kind: "axm.assignment", AccountID: a.ID, ResourceID: "one"}
	first := run()
	if first.State != "partial" {
		t.Fatal("404 became successful absence", first)
	}
	if _, err := r.SourceResource(t.Context(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatal("404 fabricated source", err)
	}
	base := server.Client().Transport
	server.Client().Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/assignedServer") {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":null}`)), Request: req}, nil
		}
		return base.RoundTrip(req)
	})
	successfulNull := run()
	if successfulNull.State != "success" {
		t.Fatal(successfulNull)
	}
	record, err := r.SourceResource(t.Context(), ref)
	if err != nil || record.Incomplete || string(record.Snapshot.Raw) != "null" {
		t.Fatal("real null not preserved", record, err)
	}
	server.Client().Transport = base
	if job := run(); job.State != "partial" {
		t.Fatal(job)
	}
	record, err = r.SourceResource(t.Context(), ref)
	if err != nil || string(record.Snapshot.Raw) != "null" {
		t.Fatal("failed refresh erased successful null", record, err)
	}
	assignedServer := server.AddMDMServer("server", map[string]any{"serverType": "THIRD_PARTY", "unknown": nil, "flag": false})
	server.Assign("one", assignedServer)
	if job := run(); job.State != "success" {
		t.Fatal(job)
	}
	original, err := r.SourceResource(t.Context(), ref)
	if err != nil || original.Incomplete || String(original.Snapshot.Fields["id"]) != assignedServer || string(original.Snapshot.Fields["attributes.flag"]) != "false" {
		t.Fatal(original, err)
	}
	server.Assign("one", "")
	if job := run(); job.State != "partial" {
		t.Fatal(job)
	}
	record, err = r.SourceResource(t.Context(), ref)
	if err != nil || string(record.Snapshot.Raw) != string(original.Snapshot.Raw) {
		t.Fatal("404 erased server original", record, err)
	}
	if _, exists := record.Snapshot.Fields["mdm_assigned"]; exists {
		t.Fatal("synthesized assignment projection")
	}
	history, err := r.History(t.Context(), ref)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
}

// TestFailedRequestSinkErrorsPropagate covers every local commit boundary while
// Apple returns 404. Storage failures must not become ordinary refresh outcomes.
func TestFailedRequestSinkErrorsPropagate(t *testing.T) {
	r, s, server, a := syncFixture(t)
	server.AddOrgDevice("one", nil)
	if _, err := r.Enqueue(t.Context(), a.ID, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err := r.claim(t.Context(), "owner", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key, err := r.PrivateKey(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	client, err := s.Client(t.Context(), a, key)
	if err != nil {
		t.Fatal(err)
	}
	seed := testMemory(t, r)
	baseline := &faultBackend{Memory: cloneMemory(seed)}
	trial := &Syncer{Repository: &Repository{Backend: baseline}}
	current := job
	if _, err := trial.collectResources(t.Context(), client, a, &current, collection.Assignment, "one"); !axm.IsNotFound(err) {
		t.Fatal(err)
	}
	for n := 1; n <= baseline.calls; n++ {
		backend := &faultBackend{Memory: cloneMemory(seed), failAt: n}
		trial.Repository = &Repository{Backend: backend}
		current = job
		if _, err := trial.collectResources(t.Context(), client, a, &current, collection.Assignment, "one"); !errors.Is(err, errStorageFailure) {
			t.Fatal(n, "sink failure hidden", err)
		}
	}
}
