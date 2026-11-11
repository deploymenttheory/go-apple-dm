package inventory

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// inventoryThrottleFixture creates source collection with in-memory provider exchanges and real timer semantics.
func inventoryThrottleFixture(t *testing.T, handler transportFunc) (*Repository, *Syncer, Account) {
	t.Helper()
	r := repository(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	a, err := r.SaveAccount(t.Context(), Account{ID: "apple", Name: "Apple", ClientID: "BUSINESSAPI.test", KeyID: "key", Enabled: true}, keyPEM, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := &Syncer{Repository: r, Client: func(ctx context.Context, a Account, key []byte) (*axm.Client, error) {
		return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, HTTPClient: &http.Client{Transport: handler}, Retry: axm.Retry{Base: time.Millisecond, Cap: time.Millisecond, MaxWait: time.Millisecond}})
	}}
	return r, s, a
}

// inventoryThrottleResponse returns an independently owned response with a long provider cooldown.
func inventoryThrottleResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

// inventorySourceResponse models independent opaque device, assignment and Apple MDM resources.
func inventorySourceResponse(req *http.Request) *http.Response {
	body := `{"data":[]}`
	switch req.URL.Path {
	case "/auth/oauth2/token":
		body = `{"access_token":"private-token","expires_in":3600}`
	case "/v1/orgDevices":
		body = `{"data":[{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"SER"}}]}`
	case "/v1/orgDevices/opaque":
		body = `{"data":{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"SER"}}}`
	case "/v1/orgDevices/opaque/assignedServer":
		body = `{"data":null}`
	case "/v1/mdmServers":
		body = `{"data":[{"id":"service","attributes":{"serverType":"APPLE_MDM"}}]}`
	case "/v1/mdmDevices":
		body = `{"data":[{"id":"builtin","attributes":{"serialNumber":"SER"}}]}`
	case "/v1/mdmDevices/builtin/details":
		body = `{"data":{"id":"builtin","attributes":{"serialNumber":"SER"}}}`
	}
	return inventoryThrottleResponse(req, 200, body)
}

// TestInventoryThrottleDefersEveryReadStage resumes durable progress after each provider surface recovers.
func TestInventoryThrottleDefersEveryReadStage(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/auth/oauth2/token", "/v1/orgDevices", "/v1/orgDevices/opaque", "/v1/orgDevices/opaque/assignedServer", "/v1/orgDevices/opaque/appleCareCoverage", "/v1/mdmServers", "/v1/mdmDevices", "/v1/mdmDevices/builtin/details"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				blocked := true
				r, s, a := inventoryThrottleFixture(t, func(req *http.Request) (*http.Response, error) {
					calls++
					if blocked && req.URL.Path == path {
						return inventoryThrottleResponse(req, 429, `{"error":"invalid_request","errors":[{"detail":"private-diagnostic"}]}`), nil
					}
					return inventorySourceResponse(req), nil
				})
				j, err := r.Enqueue(t.Context(), a.ID, true, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				run, err := s.RunOne(t.Context())
				if !axm.IsRateLimited(err) || run.State != "deferred" || run.FinishedAt != nil || run.Error != "apple_rate_limited" || run.RetryAt != time.Now().Add(time.Hour) {
					t.Fatalf("defer: %+v %v", run, err)
				}
				saved, err := r.Job(t.Context(), j.ID)
				if err != nil || saved.State != "deferred" || !saved.RetryAt.Equal(run.RetryAt) {
					t.Fatal("durable state missing", err)
				}
				before := calls
				restarted := &Syncer{Repository: r, Client: s.Client}
				if _, err := restarted.RunOne(t.Context()); !errors.Is(err, ErrNotFound) || calls != before {
					t.Fatalf("early retry: %v calls=%d", err, calls)
				}
				same, err := r.Enqueue(t.Context(), a.ID, true, time.Now())
				if err != nil || same.ID != j.ID {
					t.Fatal("deferred sync not coalesced", err)
				}
				blocked = false
				time.Sleep(time.Hour)
				run, err = restarted.RunOne(t.Context())
				if err != nil || run.State != "success" || run.FinishedAt == nil || !run.RetryAt.IsZero() {
					t.Fatalf("resume: %+v %v", run, err)
				}
			})
		})
	}
}

// TestInventoryThrottlePreservesSuccessorAndSource keeps prior evidence and callback generations through deferral.
func TestInventoryThrottlePreservesSuccessorAndSource(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var r *Repository
		var a Account
		var successor Job
		var request RefreshRequest
		blocked := true
		calls := 0
		repo, s, account := inventoryThrottleFixture(t, func(req *http.Request) (*http.Response, error) {
			calls++
			if blocked && strings.HasSuffix(req.URL.Path, "/assignedServer") {
				var err error
				successor, err = r.EnqueueRefresh(t.Context(), request, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				return inventoryThrottleResponse(req, 429, `{}`), nil
			}
			return inventorySourceResponse(req), nil
		})
		r, a = repo, account
		request = RefreshRequest{AccountID: a.ID, ResourceID: "opaque", Operations: []collection.Operation{collection.Detail, collection.Assignment}}
		old := observe(t, r, "SER", "axm.assignment", a.ID, "opaque", `{"data":{"id":"prior"}}`, time.Now())
		first, err := r.EnqueueRefresh(t.Context(), request, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		run, err := s.RunOne(t.Context())
		if !axm.IsRateLimited(err) || run.State != "deferred" || successor.ID == first.ID || successor.ID == "" {
			t.Fatalf("lost successor: %+v %v", run, err)
		}
		current, err := r.SourceResource(t.Context(), old.Source)
		if err != nil || string(current.Snapshot.Raw) != string(old.Snapshot.Raw) || current.Snapshot.Absent {
			t.Fatal("throttle replaced successful evidence", err)
		}
		before := calls
		if _, err := s.RunOne(t.Context()); !errors.Is(err, ErrNotFound) || calls != before {
			t.Fatal("successor ignored account cooldown", err)
		}
		blocked = false
		time.Sleep(time.Hour)
		for range 2 {
			run, err = s.RunOne(t.Context())
			if err != nil || run.State != "success" || run.Devices != 1 || run.Failed != 0 {
				t.Fatalf("pending refresh lost or miscounted: %+v %v", run, err)
			}
		}
		detail, err := r.SourceResource(t.Context(), source.Reference{Kind: "axm.detail", AccountID: a.ID, ResourceID: "opaque"})
		if err != nil {
			t.Fatal(err)
		}
		assignment, err := r.SourceResource(t.Context(), old.Source)
		if err != nil || assignment.Snapshot.Generation != detail.Snapshot.Generation {
			t.Fatal("mixed completed generation", err)
		}
	})
}

// TestInventoryThrottleAccountReplacementAndControl fences cooldowns and preserves pause/resume timing.
func TestInventoryThrottleAccountReplacementAndControl(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		blocked := true
		calls := 0
		r, s, a := inventoryThrottleFixture(t, func(req *http.Request) (*http.Response, error) {
			calls++
			if blocked {
				return inventoryThrottleResponse(req, 429, `{}`), nil
			}
			return inventorySourceResponse(req), nil
		})
		j, err := r.Enqueue(t.Context(), a.ID, false, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.RunOne(t.Context()); !axm.IsRateLimited(err) {
			t.Fatal(err)
		}
		if err = r.Control(t.Context(), j.ID, "pause", time.Now()); err != nil {
			t.Fatal(err)
		}
		if err = r.Control(t.Context(), j.ID, "resume", time.Now()); err != nil {
			t.Fatal(err)
		}
		before := calls
		if _, err = s.RunOne(t.Context()); !errors.Is(err, ErrNotFound) || before != calls {
			t.Fatal("resume bypassed cooldown", err)
		}
		key, err := r.PrivateKey(t.Context(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.DeleteAccount(t.Context(), a.ID, a.Revision, time.Now()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		a.Revision = 0
		a.CreatedAt = time.Time{}
		a, err = r.SaveAccount(t.Context(), a, key, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.Enqueue(t.Context(), a.ID, false, time.Now()); err != nil {
			t.Fatal(err)
		}
		blocked = false
		run, err := s.RunOne(t.Context())
		if err != nil || run.State != "success" || calls == before {
			t.Fatalf("old cooldown crossed incarnation: %+v %v", run, err)
		}
	})
}

// TestInventoryThrottleCancelledBookkeepingAndFence retains retry timing only under the active lease.
func TestInventoryThrottleCancelledBookkeepingAndFence(t *testing.T) {
	t.Parallel()
	for _, fenced := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "lease replaced"}[fenced], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var r *Repository
				repo, s, a := inventoryThrottleFixture(t, func(req *http.Request) (*http.Response, error) {
					cancel()
					if fenced {
						if err := r.Backend.Update(t.Context(), func(tx Tx) error {
							return put(t.Context(), tx, "lease/axm", lease{JobID: "other", Owner: "other", Until: time.Now().Add(time.Hour)})
						}); err != nil {
							t.Fatal(err)
						}
					}
					return inventoryThrottleResponse(req, 429, `{}`), nil
				})
				r = repo
				j, err := r.Enqueue(t.Context(), a.ID, false, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.RunOne(ctx)
				if !axm.IsRateLimited(err) {
					t.Fatal(err)
				}
				saved, e := r.Job(t.Context(), j.ID)
				if e != nil {
					t.Fatal(e)
				}
				if fenced {
					if !errors.Is(err, ErrLease) || saved.State != "running" {
						t.Fatal("obsolete worker deferred another lease", err, saved.State)
					}
				} else if saved.State != "deferred" {
					t.Fatal("cancelled parent lost retry timing", saved)
				}
			})
		})
	}
}
