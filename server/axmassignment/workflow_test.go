package axmassignment

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/server/inventorystore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

type wire struct {
	base      http.RoundTripper
	posts     atomic.Int32
	mu        sync.Mutex
	afterPost func()
	failPath  string
	loseReply bool
	csv       string
}

// RoundTrip injects failures at actual HTTP boundaries without replacing the Apple client.
func (w *wire) RoundTrip(r *http.Request) (*http.Response, error) {
	w.mu.Lock()
	path, lose, csv, after := w.failPath, w.loseReply, w.csv, w.afterPost
	w.mu.Unlock()
	if path != "" && strings.Contains(r.URL.Path, path) {
		return nil, errors.New("fixture network failure")
	}
	response, e := w.base.RoundTrip(r)
	if e != nil {
		return response, e
	}
	if r.Method == "POST" && r.URL.Path == "/v1/orgDeviceActivities" {
		w.posts.Add(1)
		if after != nil {
			after()
		}
		if lose {
			_ = response.Body.Close()
			return nil, errors.New("accepted response lost")
		}
	}
	if csv != "" && strings.HasSuffix(r.URL.Path, "/download") {
		_ = response.Body.Close()
		response.Body = io.NopCloser(strings.NewReader(csv))
		response.ContentLength = int64(len(csv))
	}
	return response, nil
}

type harness struct {
	c       *Controller
	fake    *axmtest.Server
	tr      *wire
	account inventory.Account
	service string
	key     []byte
}

// newHarness wires real OAuth, resource, mutation, activity, log and linkage exchanges to durable state.
func newHarness(t *testing.T, b inventory.Backend) *harness {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	fake := axmtest.NewServer()
	t.Cleanup(fake.Close)
	fake.RegisterKey("BUSINESSAPI.test", "kid", &key.PublicKey)
	service := fake.AddMDMServer("this-mdm", nil)
	fake.AddOrgDeviceResource("opaque-one", "SER1", nil)
	fake.AddOrgDeviceResource("opaque-two", "SER2", nil)
	repo, e := inventory.New(b)
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.Migrate(t.Context(), time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	a, e := repo.SaveAccount(t.Context(), inventory.Account{ID: "named", Name: "Named", ClientID: "BUSINESSAPI.test", KeyID: "kid", Enabled: true, BaseURL: fake.URL, TokenURL: fake.TokenURL}, pemKey, time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	httpClient := fake.Client()
	tr := &wire{base: httpClient.Transport}
	httpClient.Transport = tr
	c := &Controller{Repository: repo, Client: func(ctx context.Context, a inventory.Account, key []byte) (*axm.Client, error) {
		return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, BaseURL: a.BaseURL, TokenURL: a.TokenURL, HTTPClient: httpClient})
	}}
	if _, e = c.Configure(t.Context(), a.ID, a.Revision, service, axm.ActivityLogColumns{Serial: "serialNumber", Outcome: "status", Success: "SUCCESS"}); e != nil {
		t.Fatal(e)
	}
	return &harness{c, fake, tr, a, service, pemKey}
}

// request creates an explicit operation for the two opaque addressed fixture resources.
func (h *harness) request(t *testing.T, id string) Workflow {
	t.Helper()
	w, e := h.c.Request(t.Context(), h.account.ID, id, []string{"SER1", "SER2"}, time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	return w
}

// due advances only local scheduling metadata, leaving all protocol outcomes unchanged.
func (h *harness) due(t *testing.T, id string) {
	t.Helper()
	if e := h.c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error {
		w, e := read[Workflow](t.Context(), tx, "assignment/"+id)
		if e != nil {
			return e
		}
		w.NextAttempt = time.Time{}
		return write(t.Context(), tx, "assignment/"+id, w)
	}); e != nil {
		t.Fatal(e)
	}
}

// step observes one persisted attempt and returns the stored result.
func (h *harness) step(t *testing.T, id string) Workflow {
	t.Helper()
	h.due(t, id)
	if e := h.c.Step(t.Context(), id); e != nil {
		t.Fatal(e)
	}
	w, e := h.c.Get(t.Context(), h.account.ID, id)
	if e != nil {
		t.Fatal(e)
	}
	return w
}

// TestWorkflowBackends verifies restart, partial outcomes, delayed visibility and idempotent requests.
func TestWorkflowBackends(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var backend inventory.Backend = inventory.NewMemory()
			var db *sql.DB
			var dbPath string
			if kind == "sqlite" {
				var e error
				dbPath = filepath.Join(t.TempDir(), "assignment.db")
				db, e = sql.Open("sqlite", sqlite.DSN(dbPath, sqlite.Options{}))
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = db.Close() })
				backend, e = inventorystore.Open(t.Context(), db, sqlite.Dialect, nil)
				if e != nil {
					t.Fatal(e)
				}
			}
			h := newHarness(t, backend)
			h.fake.SetConsistencyLag(time.Hour)
			h.fake.SetOutcome("SER2", "retained rejection detail")
			queued := h.request(t, "request")
			if queued.State != "queued" || h.tr.posts.Load() != 0 {
				t.Fatal("request performed remote mutation")
			}
			same, e := h.c.Request(t.Context(), h.account.ID, "request", []string{"SER1", "SER2"}, time.Now())
			if e != nil || same.ID != queued.ID {
				t.Fatal(same, e)
			}
			if _, e = h.c.Request(t.Context(), h.account.ID, "overlap", []string{"SER1"}, time.Now()); !errors.Is(e, inventory.ErrConflict) {
				t.Fatal(e)
			}
			w := h.step(t, "request")
			if w.State != "accepted" || w.AcceptedAt == nil || w.ActivityID == "" || w.TerminalStatus != "" || w.Targets[0].DeviceID != "opaque-one" {
				t.Fatalf("acceptance conflated: %+v", w)
			}
			h.fake.Complete()
			// A fresh controller resumes the same document store; SQLite is reopened through another pool.
			if kind == "sqlite" {
				db2, e := sql.Open("sqlite", sqlite.DSN(dbPath, sqlite.Options{}))
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = db2.Close() })
				store, e := inventorystore.Open(t.Context(), db2, sqlite.Dialect, nil)
				if e != nil {
					t.Fatal(e)
				}
				h.c.Repository, e = inventory.New(store)
				if e != nil {
					t.Fatal(e)
				}
			}
			h.c = &Controller{Repository: h.c.Repository, Client: h.c.Client}
			w = h.step(t, "request")
			if w.State != "terminal" || !w.LogComplete || w.LogSucceeded || w.Targets[0].Converged || w.Targets[1].Outcome != "FAILED" {
				t.Fatalf("terminal or partial failure lost: %+v", w)
			}
			evidence, e := h.c.Evidence(t.Context(), h.account.ID, w.ID)
			if e != nil || !strings.Contains(string(evidence.CSV), "retained rejection detail") || evidence.Log.Rows[1]["reason"] != "retained rejection detail" {
				t.Fatal(evidence, e)
			}
			h.fake.SetConsistencyLag(0)
			h.fake.Assign("opaque-one", h.service)
			w = h.step(t, "request")
			if w.State != "complete" || !w.Targets[0].Converged || w.LogSucceeded || w.Targets[1].Outcome != "FAILED" || h.tr.posts.Load() != 1 {
				t.Fatalf("convergence erased partial failure: %+v", w)
			}
			if e = h.c.Step(t.Context(), w.ID); !errors.Is(e, inventory.ErrStopped) {
				t.Fatal(e)
			}
		})
	}
}

// TestLostSubmissionNeverRetries covers transport loss and a crash before accepted ID persistence.
func TestLostSubmissionNeverRetries(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost_reply", true: "crash_before_commit"}[crash], func(t *testing.T) {
			h := newHarness(t, inventory.NewMemory())
			h.request(t, "lost")
			if crash {
				h.tr.afterPost = func() {
					if e := h.c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error {
						return write(t.Context(), tx, "assignment-lease/lost", lease{"expired", time.Time{}})
					}); e != nil {
						t.Error(e)
					}
				}
			} else {
				h.tr.loseReply = true
			}
			err := h.c.Step(t.Context(), "lost")
			if crash && !errors.Is(err, inventory.ErrLease) {
				t.Fatal(err)
			}
			if !crash && err != nil {
				t.Fatal(err)
			}
			h.tr.afterPost = nil
			h.tr.loseReply = false
			h.fake.Complete()
			h.fake.Assign("opaque-one", h.service)
			h.fake.Assign("opaque-two", h.service)
			w := h.step(t, "lost")
			if w.State != "uncertain" || w.ActivityID != "" || w.AcceptedAt != nil || w.TerminalStatus != "" || !w.Targets[0].Converged || !w.Targets[1].Converged || h.tr.posts.Load() != 1 {
				t.Fatalf("uncertainty invented success or retried: %+v", w)
			}
			h.step(t, "lost")
			if h.tr.posts.Load() != 1 {
				t.Fatal("duplicate mutation")
			}
		})
	}
}

// TestFencesAndReplicaLeases rejects old revisions, owners, expired leases and deleted-account incarnations.
func TestFencesAndReplicaLeases(t *testing.T) {
	for _, change := range []string{"key", "binding", "disabled", "delete_recreate"} {
		t.Run(change, func(t *testing.T) {
			h := newHarness(t, inventory.NewMemory())
			h.request(t, "fenced")
			w, owner, e := h.c.claim(t.Context(), "fenced", time.Now())
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = h.c.claim(t.Context(), "fenced", time.Now()); !errors.Is(e, inventory.ErrLease) {
				t.Fatal(e)
			}
			switch change {
			case "binding":
				_, e = h.c.Configure(t.Context(), h.account.ID, h.account.Revision, "other-service", w.Binding.Columns)
			case "disabled":
				h.account.Enabled = false
				_, e = h.c.Repository.SaveAccount(t.Context(), h.account, nil, time.Now())
			case "key":
				_, e = h.c.Repository.SaveAccount(t.Context(), h.account, h.key, time.Now())
			case "delete_recreate":
				e = h.c.Repository.DeleteAccount(t.Context(), h.account.ID, h.account.Revision, time.Now())
				if e == nil {
					h.account.Revision = 0
					_, e = h.c.Repository.SaveAccount(t.Context(), h.account, h.key, time.Now())
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = h.c.commit(t.Context(), &w, owner, nil, false); e == nil {
				t.Fatal("stale worker published")
			}
			if e = h.c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error { return tx.Delete(t.Context(), "assignment-lease/fenced") }); e != nil {
				t.Fatal(e)
			}
			w = h.step(t, "fenced")
			if w.State != "fenced" || h.tr.posts.Load() != 0 {
				t.Fatal(w)
			}
		})
	}
}

// TestReadFailuresRetainEvidence covers delayed, unavailable and incomplete activity evidence.
func TestReadFailuresRetainEvidence(t *testing.T) {
	for _, fault := range []string{"service", "device_lookup", "activity", "download", "partial_log", "readback", "identity"} {
		t.Run(fault, func(t *testing.T) {
			h := newHarness(t, inventory.NewMemory())
			h.request(t, "fault")
			switch fault {
			case "service":
				h.tr.failPath = "/mdmServers/"
			case "device_lookup":
				h.tr.failPath = "/orgDevices"
			}
			w := h.step(t, "fault")
			if fault == "service" || fault == "device_lookup" {
				if w.State != "queued" || h.tr.posts.Load() != 0 || w.Error == "" {
					t.Fatal(w)
				}
				return
			}
			h.fake.Complete()
			switch fault {
			case "activity":
				h.tr.failPath = "/orgDeviceActivities/"
			case "download":
				h.tr.failPath = "/download"
			case "partial_log":
				h.tr.csv = "serialNumber,status,reason\nSER1,SUCCESS,first\n"
			case "readback":
				h.tr.failPath = "/assignedServer"
			case "identity":
				h.fake.AddOrgDeviceResource("opaque-one", "CHANGED", nil)
			}
			w = h.step(t, "fault")
			if w.Error == "" || w.AcceptedAt == nil || h.tr.posts.Load() != 1 {
				t.Fatal(w)
			}
			if fault == "partial_log" {
				evidence, e := h.c.Evidence(t.Context(), h.account.ID, w.ID)
				if e != nil || evidence.Log.Complete || len(evidence.Log.Rows) != 1 {
					t.Fatal(evidence, e)
				}
			}
		})
	}
}

// TestUnassignScopes prevents removing devices owned by a different MDM service.
func TestUnassignScopes(t *testing.T) {
	h := newHarness(t, inventory.NewMemory())
	h.fake.Assign("opaque-one", "other")
	if _, e := h.c.RequestUnassign(t.Context(), h.account.ID, "unassign", []string{"SER1"}, time.Now()); e != nil {
		t.Fatal(e)
	}
	w := h.step(t, "unassign")
	if w.Error != "outside_configured_service" || h.tr.posts.Load() != 0 {
		t.Fatal(w)
	}
	h.fake.Assign("opaque-one", h.service)
	w = h.step(t, "unassign")
	if w.State != "accepted" {
		t.Fatal(w)
	}
	h.fake.Complete()
	w = h.step(t, "unassign")
	if w.State != "complete" || !w.Targets[0].Converged {
		t.Fatal(w)
	}
}

// TestValidationAndRun checks request boundaries, scoped reads and supervised resumption.
func TestValidationAndRun(t *testing.T) {
	h := newHarness(t, inventory.NewMemory())
	ctx := t.Context()
	for _, input := range []struct {
		id      string
		serials []string
	}{{"", []string{"S"}}, {"bad/id", []string{"S"}}, {"x", nil}, {"x", []string{""}}, {"x", []string{"S", "S"}}} {
		if _, e := h.c.Request(ctx, h.account.ID, input.id, input.serials, time.Now()); !errors.Is(e, inventory.ErrInvalid) {
			t.Fatal(e)
		}
	}
	if _, e := h.c.Configure(ctx, h.account.ID, 1, "", axm.ActivityLogColumns{}); !errors.Is(e, inventory.ErrInvalid) {
		t.Fatal(e)
	}
	if _, e := h.c.Configure(ctx, h.account.ID, 99, h.service, axm.ActivityLogColumns{Serial: "s", Outcome: "o", Success: "y"}); !errors.Is(e, inventory.ErrConflict) {
		t.Fatal(e)
	}
	if _, e := h.c.Binding(ctx, h.account.ID); e != nil {
		t.Fatal(e)
	}
	h.request(t, "run")
	if _, e := h.c.Get(ctx, "other", "run"); !errors.Is(e, inventory.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := h.c.Evidence(ctx, "other", "run"); !errors.Is(e, inventory.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := h.c.Request(ctx, h.account.ID, "run", []string{"SER2", "SER1"}, time.Now()); !errors.Is(e, inventory.ErrConflict) {
		t.Fatal(e)
	}
	if _, e := h.c.RequestUnassign(ctx, h.account.ID, "run", []string{"SER1", "SER2"}, time.Now()); !errors.Is(e, inventory.ErrConflict) {
		t.Fatal(e)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	h.tr.afterPost = cancel
	err := h.c.Run(cancelCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if h.tr.posts.Load() != 1 {
		t.Fatal("worker did not submit queued intent")
	}
}

// TestDocumentCorruption keeps invalid durable state from silently becoming successful work.
func TestDocumentCorruption(t *testing.T) {
	h := newHarness(t, inventory.NewMemory())
	h.request(t, "broken")
	if e := h.c.Repository.Backend.Update(t.Context(), func(tx inventory.Tx) error { return tx.Put(t.Context(), "assignment/broken", json.RawMessage(`[]`)) }); e != nil {
		t.Fatal(e)
	}
	if e := h.c.Step(t.Context(), "broken"); e == nil {
		t.Fatal("corrupt workflow accepted")
	}
}

// TestSQLiteReplicaMutation proves concurrent independent controllers submit only once.
func TestSQLiteReplicaMutation(t *testing.T) {
	db, e := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "replicas.db"), sqlite.Options{}))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	checkAssignmentSQL(t, db, sqlite.Dialect)
}

// checkAssignmentSQL exercises durable leases, competing workers, account fencing and restart.
func checkAssignmentSQL(t *testing.T, db *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	backend, e := inventorystore.Open(t.Context(), db, dialect, nil)
	if e != nil {
		t.Fatal(e)
	}
	h := newHarness(t, backend)
	h.request(t, "replicas")
	backend2, e := inventorystore.Open(t.Context(), db, dialect, nil)
	if e != nil {
		t.Fatal(e)
	}
	repo2, e := inventory.New(backend2)
	if e != nil {
		t.Fatal(e)
	}
	second := &Controller{Repository: repo2, Client: h.c.Client}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for _, c := range []*Controller{h.c, second} {
		group.Go(func() { results <- c.Step(t.Context(), "replicas") })
	}
	group.Wait()
	close(results)
	for e := range results {
		if e != nil && !errors.Is(e, inventory.ErrLease) && !errors.Is(e, inventory.ErrStopped) {
			t.Fatal(e)
		}
	}
	if h.tr.posts.Load() != 1 {
		t.Fatal("replica duplicate mutation", h.tr.posts.Load())
	}
	h.c = second
	h.fake.Complete()
	w := h.step(t, "replicas")
	if w.State != "complete" || !w.LogSucceeded {
		t.Fatal(w)
	}
	h.request(t, "rotate")
	h.tr.afterPost = func() {
		_, e := h.c.Repository.SaveAccount(t.Context(), h.account, h.key, time.Now())
		if e != nil {
			t.Error(e)
		}
	}
	if e = h.c.Step(t.Context(), "rotate"); !errors.Is(e, inventory.ErrStopped) {
		t.Fatal(e)
	}
	w, e = h.c.Get(t.Context(), h.account.ID, "rotate")
	if e != nil || w.AcceptedAt != nil || w.State != "submitting" {
		t.Fatal("stale acceptance published", w, e)
	}
	if e = backend.Update(t.Context(), func(tx inventory.Tx) error { return tx.Delete(t.Context(), "assignment-lease/rotate") }); e != nil {
		t.Fatal(e)
	}
	w = h.step(t, "rotate")
	if w.State != "fenced" || h.tr.posts.Load() != 2 {
		t.Fatal(w)
	}
}

// TestOpaqueAddressingAndLogCompleteness rejects ambiguous discovery and retains unknown outcomes.
func TestOpaqueAddressingAndLogCompleteness(t *testing.T) {
	for _, kind := range []string{"duplicate_resource", "missing_resource", "duplicate_log", "unknown_outcome", "unexpected_log"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, inventory.NewMemory())
			h.request(t, "address")
			if kind == "duplicate_resource" {
				h.fake.AddOrgDeviceResource("foreign-opaque", "SER1", nil)
			}
			if kind == "missing_resource" {
				h.fake.AddOrgDeviceResource("opaque-one", "DIFFERENT-SERIAL", nil)
			}
			w := h.step(t, "address")
			if kind == "duplicate_resource" || kind == "missing_resource" {
				if w.State != "queued" || w.Error != "device_lookup_failed" || h.tr.posts.Load() != 0 {
					t.Fatal(w)
				}
				return
			}
			h.fake.Complete()
			switch kind {
			case "duplicate_log":
				h.tr.csv = "serialNumber,status,reason\nSER1,SUCCESS,first\nSER1,SUCCESS,second\n"
			case "unexpected_log":
				h.tr.csv = "serialNumber,status,reason\nSER1,SUCCESS,first\nFOREIGN,SUCCESS,unexpected\n"
			case "unknown_outcome":
				h.tr.csv = "serialNumber,status,reason\nSER1,NEW_VENDOR_STATUS,unknown\nSER2,SUCCESS,second\n"
			}
			w = h.step(t, "address")
			evidence, e := h.c.Evidence(t.Context(), h.account.ID, w.ID)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "unknown_outcome" {
				if !w.LogComplete || w.LogSucceeded || w.Targets[0].Outcome != "NEW_VENDOR_STATUS" || evidence.Log.Rows[0]["reason"] != "unknown" {
					t.Fatal(w, evidence)
				}
			} else {
				if w.LogComplete || w.LogSucceeded || evidence.Log.Complete || w.Error != "log_incomplete" {
					t.Fatal(w, evidence)
				}
			}
		})
	}
}
