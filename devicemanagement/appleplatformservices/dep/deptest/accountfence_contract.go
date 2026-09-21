package deptest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
)

// responseGate pauses one completed response without holding a server or store lock.
type responseGate struct {
	base             http.RoundTripper
	path             string
	used             atomic.Bool
	entered, release chan struct{}
}

// RoundTrip exposes the interval between Apple's response and the local commit.
func (g *responseGate) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := g.base.RoundTrip(r)
	if err == nil && r.URL.Path == g.path && g.used.CompareAndSwap(false, true) {
		close(g.entered)
		select {
		case <-g.release:
		case <-r.Context().Done():
		}
	}
	return res, err
}

type fenceFixture struct {
	store  dep.Store
	client *dep.Client
	server *Server
	clock  *clock.Fake
	name   string
}

// postResponseGate exposes the gap between a response fence and session adoption.
type postResponseGate struct {
	dep.Store
	armed            atomic.Bool
	updates          atomic.Int64
	entered, release chan struct{}
}

// Update delays return after the first response-validation transaction has completed.
func (s *postResponseGate) Update(ctx context.Context, fn func(dep.Tx) error) error {
	err := s.Store.Update(ctx, fn)
	if s.armed.Load() && s.updates.Add(1) == 1 {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

type armResponseTransport struct {
	base  http.RoundTripper
	store *postResponseGate
	path  string
}

// RoundTrip arms the store gate only after the assignment response arrives.
func (r armResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := r.base.RoundTrip(req)
	path := r.path
	if path == "" {
		path = dep.PathProfileDevs
	}
	if err == nil && req.URL.Path == path {
		r.store.armed.Store(true)
	}
	return res, err
}

// newFenceFixture creates a validated account and inventory on the selected backend.
func newFenceFixture(t *testing.T, factory Factory) *fenceFixture {
	t.Helper()
	f := &fenceFixture{store: factory(t, nil), clock: clock.NewFake(t0), name: "fenced"}
	f.server = NewServer(Options{Clock: f.clock})
	t.Cleanup(f.server.Close)
	f.client = f.newClient(t, f.server.Client(), f.store)
	if _, err := f.client.StoreTokens(t.Context(), f.name, f.server.Tokens()); err != nil {
		t.Fatal(err)
	}
	devices := []dep.Device{{SerialNumber: "A"}, {SerialNumber: "B"}}
	f.server.AddDevices(devices...)
	if err := f.store.PutDevices(t.Context(), f.name, devices, t0); err != nil {
		t.Fatal(err)
	}
	return f
}

// newClient shares persisted account state while retaining an independent session cache.
func (f *fenceFixture) newClient(t *testing.T, hc *http.Client, st dep.Store) *dep.Client {
	t.Helper()
	c, err := dep.NewClient(dep.ClientConfig{Store: st, BaseURL: f.server.URL(), HTTPClient: hc, Clock: f.clock})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// gatedClient returns a client and controls for exactly one delayed response.
func (f *fenceFixture) gatedClient(t *testing.T, path string) (*dep.Client, func(), func()) {
	t.Helper()
	g := &responseGate{base: f.server.Client().Transport, path: path, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(g.release) }) }
	t.Cleanup(release)
	wait := func() {
		t.Helper()
		select {
		case <-g.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("response did not reach gate")
		}
	}
	return f.newClient(t, &http.Client{Transport: g, Timeout: 10 * time.Second}, f.store), wait, release
}

// changeAccount applies an operator edit with the same lock used by worker commits.
func (f *fenceFixture) changeAccount(t *testing.T, fn func(*dep.Account)) {
	t.Helper()
	err := f.store.Update(t.Context(), func(tx dep.Tx) error {
		if err := tx.LockAccount(t.Context(), f.name); err != nil {
			return err
		}
		a, err := tx.GetAccount(t.Context(), f.name)
		if err != nil {
			return err
		}
		fn(a)
		return tx.PutAccount(t.Context(), a)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// profile defines and saves a profile without making it the desired target.
func (f *fenceFixture) profile(t *testing.T) string {
	t.Helper()
	p := &dep.Profile{ProfileName: "fence", URL: "https://mdm.example.test/enroll", OrgMagic: "fence"}
	r, err := f.client.DefineProfile(t.Context(), f.name, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.PutProfile(t.Context(), f.name, p); err != nil {
		t.Fatal(err)
	}
	return r.ProfileUUID
}

// tokenFile stages a key and obtains the fake service's matching encrypted token.
func (f *fenceFixture) tokenFile(t *testing.T) []byte {
	t.Helper()
	kp, err := dep.GenerateTokenPKI("fence", time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.PutKeypair(t.Context(), f.name, dep.StageStaged, kp); err != nil {
		t.Fatal(err)
	}
	cert, err := kp.Certificate()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := f.server.TokenP7M(cert)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// runAccountFences verifies races against every memory and SQL contract backend.
func runAccountFences(t *testing.T, factory Factory) {
	t.Helper()
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests} {
		t.Run("RenewalDuringSessionAdoption/"+fmt.Sprint(status), func(t *testing.T) {
			f := newFenceFixture(t, factory)
			profile := f.profile(t)
			f.changeAccount(t, func(a *dep.Account) { a.ProfileUUID = profile })
			body := `{"devices":{"A":"THROTTLED"},"retry_after_seconds":600}`
			f.server.Script(dep.PathProfileDevs, Scripted{Status: status, Body: body, RetryAfter: "600", Session: "obsolete-rotated"})
			gate := &postResponseGate{Store: f.store, entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			t.Cleanup(release)
			client := f.newClient(t, &http.Client{Transport: armResponseTransport{base: f.server.Client().Transport, store: gate}}, gate)
			worker, _ := dep.NewAssigner(dep.AssignerConfig{Client: client, Store: gate, Account: f.name, Clock: f.clock})
			done := make(chan error, 1)
			go func() { _, err := worker.RunOnce(t.Context()); done <- err }()
			select {
			case <-gate.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("session adoption gate not reached")
			}
			tokens := f.server.Tokens()
			tokens.AccessToken = "renewed"
			f.server.set(func() { f.server.o.Tokens = tokens })
			if _, err := f.client.StoreTokens(t.Context(), f.name, tokens); err != nil {
				t.Fatal(err)
			}
			session, _ := f.store.Session(t.Context(), f.name)
			release()
			if err := <-done; !errors.Is(err, dep.ErrConflict) {
				t.Fatalf("adoption race: %v", err)
			} else if status == http.StatusOK && !strings.Contains(err.Error(), "cooldown") {
				t.Fatalf("throttle context missing from rejection: %v", err)
			}
			state, _ := f.store.AssignmentState(t.Context(), f.name)
			if !state.NotBefore.Equal(t0.Add(600 * time.Second)) {
				t.Fatalf("lost late cooldown: %+v", state)
			}
			if got, _ := f.store.Session(t.Context(), f.name); got != session {
				t.Fatal("stale session adopted")
			}
		})
	}
	for _, path := range []string{dep.PathFetchDevices, dep.PathProfileDevs} {
		t.Run("RenewalBeforeWorkerCommit"+path, func(t *testing.T) {
			f := newFenceFixture(t, factory)
			profile := f.profile(t)
			f.changeAccount(t, func(a *dep.Account) { a.ProfileUUID = profile })
			f.server.AddDevices(dep.Device{SerialNumber: "NEW"})
			gate := &postResponseGate{Store: f.store, entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			t.Cleanup(release)
			client := f.newClient(t, &http.Client{Transport: armResponseTransport{base: f.server.Client().Transport, store: gate, path: path}}, gate)
			done := make(chan error, 1)
			go func() {
				var err error
				if path == dep.PathFetchDevices {
					worker, _ := dep.NewSyncer(dep.SyncerConfig{Client: client, Store: gate, Account: f.name, Clock: f.clock})
					_, err = worker.RunOnce(t.Context())
				} else {
					worker, _ := dep.NewAssigner(dep.AssignerConfig{Client: client, Store: gate, Account: f.name, Clock: f.clock})
					_, err = worker.RunOnce(t.Context())
				}
				done <- err
			}()
			select {
			case <-gate.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker commit gate not reached")
			}
			before, _ := f.store.Cursor(t.Context(), f.name)
			tokens := f.server.Tokens()
			tokens.AccessToken = "renewed"
			f.server.set(func() { f.server.o.Tokens = tokens })
			if _, err := f.client.StoreTokens(t.Context(), f.name, tokens); err != nil {
				t.Fatal(err)
			}
			release()
			if err := <-done; !errors.Is(err, dep.ErrConflict) {
				t.Fatalf("obsolete worker committed: %v", err)
			}
			if _, err := f.store.GetAssignment(t.Context(), f.name, "A"); !errors.Is(err, dep.ErrNotFound) {
				t.Fatalf("obsolete assignment: %v", err)
			}
			if _, err := f.store.GetDevice(t.Context(), f.name, "NEW"); !errors.Is(err, dep.ErrNotFound) {
				t.Fatalf("obsolete inventory: %v", err)
			}
			after, _ := f.store.Cursor(t.Context(), f.name)
			if before.Revision != after.Revision || before.Value != after.Value {
				t.Fatal("obsolete cursor advanced")
			}
		})
	}
	t.Run("CompetingCreationRejectsOldValidation", func(t *testing.T) {
		f := newFenceFixture(t, factory)
		name := "new-account"
		client, wait, release := f.gatedClient(t, dep.PathAccount)
		done := make(chan error, 1)
		go func() { _, err := client.StoreTokens(t.Context(), name, f.server.Tokens()); done <- err }()
		wait()
		if _, err := f.client.StoreTokens(t.Context(), name, f.server.Tokens()); err != nil {
			t.Fatal(err)
		}
		a, _ := f.store.GetAccount(t.Context(), name)
		a.ProfileUUID = "new-policy"
		if err := f.store.PutAccount(t.Context(), a); err != nil {
			t.Fatal(err)
		}
		release()
		if err := <-done; !errors.Is(err, dep.ErrConflict) {
			t.Fatalf("competing creation accepted: %v", err)
		}
		a, _ = f.store.GetAccount(t.Context(), name)
		if a.ProfileUUID != "new-policy" {
			t.Fatal("competing creation lost policy")
		}
	})
	for _, mode := range []string{"changed", "disabled", "429", "throttled", "readback", "renewal-429", "renewal-throttled"} {
		t.Run("Assignment/"+mode, func(t *testing.T) {
			f := newFenceFixture(t, factory)
			cooldown := mode == "429" || mode == "throttled" || strings.HasPrefix(mode, "renewal-")
			p1, p2 := f.profile(t), f.profile(t)
			f.changeAccount(t, func(a *dep.Account) { a.ProfileUUID = p1 })
			path := dep.PathProfileDevs
			if mode == "readback" {
				path = dep.PathDeviceDetails
			}
			if mode == "429" || mode == "renewal-429" {
				f.server.Script(path, Scripted{Status: 429, RetryAfter: "600"})
			}
			if mode == "throttled" || mode == "renewal-throttled" {
				f.server.Throttle("A", 600)
			}
			client, wait, release := f.gatedClient(t, path)
			worker, err := dep.NewAssigner(dep.AssignerConfig{Client: client, Store: f.store, Account: f.name, Clock: f.clock, BatchSize: 1, ReadBack: mode == "readback"})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := worker.RunOnce(t.Context()); done <- err }()
			wait()
			target := p2
			if mode == "disabled" {
				target = ""
			}
			f.changeAccount(t, func(a *dep.Account) { a.ProfileUUID = target })
			if strings.HasPrefix(mode, "renewal-") {
				tokens := f.server.Tokens()
				tokens.AccessToken = "renewed-token"
				f.server.set(func() { f.server.o.Tokens = tokens })
				if _, err := f.client.StoreTokens(t.Context(), f.name, tokens); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "readback" {
				if err := f.store.PutDevices(t.Context(), f.name, []dep.Device{{SerialNumber: "A", ProfileUUID: p2}}, t0.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			release()
			err = <-done
			if err == nil || (mode != "429" && !errors.Is(err, dep.ErrConflict)) {
				t.Fatalf("stale run: %v", err)
			}
			if n := f.server.Count(http.MethodPost, dep.PathProfileDevs); n != 1 {
				t.Fatalf("old target sent %d batches", n)
			}
			if mode != "readback" {
				if _, err := f.store.GetAssignment(t.Context(), f.name, "A"); !errors.Is(err, dep.ErrNotFound) {
					t.Fatalf("stale outcome saved: %v", err)
				}
			} else {
				d, err := f.store.GetDevice(t.Context(), f.name, "A")
				if err != nil || d.ProfileUUID != p2 {
					t.Fatalf("stale readback: %+v %v", d, err)
				}
			}
			state, err := f.store.AssignmentState(t.Context(), f.name)
			if err != nil || state.Owner != "" {
				t.Fatalf("lease not released: %+v %v", state, err)
			}
			next, _ := dep.NewAssigner(dep.AssignerConfig{Client: f.client, Store: f.store, Account: f.name, Clock: f.clock})
			res, err := next.RunOnce(t.Context())
			if cooldown {
				if !errors.Is(err, dep.ErrBackoff) || !res.NotBefore.Equal(t0.Add(600*time.Second)) {
					t.Fatalf("lost cooldown: %+v %v", res, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("RenewalPreservesConcurrentPolicy", func(t *testing.T) {
		f := newFenceFixture(t, factory)
		client, wait, release := f.gatedClient(t, dep.PathAccount)
		done := make(chan error, 1)
		go func() { _, err := client.StoreTokens(t.Context(), f.name, f.server.Tokens()); done <- err }()
		wait()
		f.changeAccount(t, func(a *dep.Account) { a.ProfileUUID = "new-policy"; a.ProtocolVersion = 9 })
		release()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		a, _ := f.store.GetAccount(t.Context(), f.name)
		if a.ProfileUUID != "new-policy" || a.ProtocolVersion != 9 {
			t.Fatalf("renewal lost policy: %+v", a)
		}
	})
	t.Run("ConcurrentTokenChangeRejectsOldValidation", func(t *testing.T) {
		f := newFenceFixture(t, factory)
		client, wait, release := f.gatedClient(t, dep.PathAccount)
		done := make(chan error, 1)
		go func() { _, err := client.StoreTokens(t.Context(), f.name, f.server.Tokens()); done <- err }()
		wait()
		f.changeAccount(t, func(a *dep.Account) { a.AccessToken = "newer-token" })
		release()
		if err := <-done; !errors.Is(err, dep.ErrConflict) {
			t.Fatalf("stale import accepted: %v", err)
		}
		a, _ := f.store.GetAccount(t.Context(), f.name)
		if a.AccessToken != "newer-token" {
			t.Fatal("new credentials overwritten")
		}
	})
	t.Run("StagedKeyChangedDuringValidation", func(t *testing.T) {
		f := newFenceFixture(t, factory)
		raw := f.tokenFile(t)
		client, wait, release := f.gatedClient(t, dep.PathAccount)
		done := make(chan error, 1)
		go func() { _, err := client.ImportToken(t.Context(), f.name, raw, dep.ImportOptions{}); done <- err }()
		wait()
		_ = f.tokenFile(t)
		release()
		if err := <-done; !errors.Is(err, dep.ErrConflict) {
			t.Fatalf("replaced staged key promoted: %v", err)
		}
		if _, err := f.store.Keypair(t.Context(), f.name, dep.StageCurrent); !errors.Is(err, dep.ErrNotFound) {
			t.Fatalf("current key changed: %v", err)
		}
	})
	t.Run("OmittedIdentityMetadataRetainsBinding", func(t *testing.T) {
		f := newFenceFixture(t, factory)
		f.server.SetAccount(dep.AccountDetail{OrgName: "Renamed"})
		if _, err := f.client.StoreTokens(t.Context(), f.name, f.server.Tokens()); err != nil {
			t.Fatal(err)
		}
		a, _ := f.store.GetAccount(t.Context(), f.name)
		if a.ServerUUID != "SERVER-UUID-DEPTEST" || a.OrgID != "ORG-1" || a.OrgName != "Renamed" {
			t.Fatal("omitted metadata erased identity")
		}
	})
	for _, mode := range []string{"ordinary", "same", "same-new-key", "enrichment", "reset", "missing-uuid", "rollback"} {
		t.Run("Identity/"+mode, func(t *testing.T) {
			f := newFenceFixture(t, factory)
			profile := f.profile(t)
			f.changeAccount(t, func(a *dep.Account) { a.ProfileUUID = profile; a.ProtocolVersion = 9 })
			if err := f.store.SetCursor(t.Context(), f.name, dep.Cursor{Phase: dep.PhaseSync, Value: "old-cursor", Revision: 7, UpdatedAt: t0}); err != nil {
				t.Fatal(err)
			}
			if err := f.store.PutAssignment(t.Context(), &dep.Assignment{Account: f.name, SerialNumber: "A", ProfileUUID: profile, Status: dep.StatusSuccess}); err != nil {
				t.Fatal(err)
			}
			if err := f.store.PutAssignmentState(t.Context(), f.name, dep.AssignmentState{Owner: "old-worker", LeaseUntil: t0.Add(time.Minute), NotBefore: t0.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			raw := f.tokenFile(t)
			if mode == "same-new-key" {
				f.changeAccount(t, func(a *dep.Account) { a.ConsumerKey = "old-consumer" })
			}
			if mode == "enrichment" {
				f.changeAccount(t, func(a *dep.Account) { a.ServerUUID = ""; a.OrgID = "" })
			}
			if mode != "same" && mode != "same-new-key" && mode != "enrichment" {
				f.server.SetAccount(dep.AccountDetail{ServerUUID: "NEW-SERVER", OrgID: "ORG-2"})
			}
			if mode == "missing-uuid" {
				f.server.SetAccount(dep.AccountDetail{OrgID: "ORG-2"})
			}
			client := f.client
			var fault error
			if mode == "rollback" {
				fault = errors.New("promotion unavailable")
				client = f.newClient(t, f.server.Client(), &Failing{Store: f.store, Fail: map[string]error{"UpstageKeypair": fault}})
			}
			if mode == "ordinary" {
				if _, err := client.StoreTokens(t.Context(), f.name, f.server.Tokens()); !errors.Is(err, dep.ErrConflict) {
					t.Fatalf("ordinary raw replacement: %v", err)
				}
			}
			_, err := client.ImportToken(t.Context(), f.name, raw, dep.ImportOptions{Force: mode != "ordinary"})
			switch mode {
			case "ordinary":
				if !errors.Is(err, dep.ErrConflict) {
					t.Fatalf("ordinary replacement: %v", err)
				}
			case "missing-uuid":
				if !errors.Is(err, dep.ErrInvalid) {
					t.Fatalf("unidentified replacement: %v", err)
				}
			case "rollback":
				if !errors.Is(err, fault) {
					t.Fatalf("failed reset: %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			a, _ := f.store.GetAccount(t.Context(), f.name)
			cursor, _ := f.store.Cursor(t.Context(), f.name)
			devices, err := f.store.ListDevices(t.Context(), f.name, dep.DeviceQuery{IncludeDeleted: true}, paging.Page{})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "reset" {
				if a.ProfileUUID != "" || a.ServerUUID != "NEW-SERVER" || a.ProtocolVersion != 9 || len(devices.Items) != 0 {
					t.Fatalf("reset retained old state: account=%+v devices=%d", a, len(devices.Items))
				}
				if cursor.Phase != dep.PhaseFetch || cursor.Value != "" || cursor.Generation == "" || cursor.Revision <= 7 {
					t.Fatalf("reset cursor: %+v", cursor)
				}
				if _, err := f.store.GetProfile(t.Context(), f.name, profile); !errors.Is(err, dep.ErrNotFound) {
					t.Fatalf("old profile retained: %v", err)
				}
				if _, err := f.store.GetAssignment(t.Context(), f.name, "A"); !errors.Is(err, dep.ErrNotFound) {
					t.Fatalf("old assignment retained: %v", err)
				}
				state, _ := f.store.AssignmentState(t.Context(), f.name)
				if state != (dep.AssignmentState{}) {
					t.Fatalf("old schedule retained: %+v", state)
				}
			} else if a.ProfileUUID != profile || a.ServerUUID != "SERVER-UUID-DEPTEST" || cursor.Value != "old-cursor" || len(devices.Items) != 2 {
				t.Fatal("renewal or rejected reset changed derived state")
			}
		})
	}
	t.Run("RejectedCandidateDoesNotPoisonCurrentCredentials", func(t *testing.T) {
		f := newFenceFixture(t, factory)
		tokens := f.server.Tokens()
		tokens.AccessToken = "invalid-candidate"
		if _, err := f.client.StoreTokens(t.Context(), f.name, tokens); !errors.Is(err, dep.ErrTokenInvalid) {
			t.Fatalf("candidate: %v", err)
		}
		a, _ := f.store.GetAccount(t.Context(), f.name)
		if a.State != (dep.AccountState{}) || a.AccessToken == tokens.AccessToken {
			t.Fatal("candidate rejection changed current credentials")
		}
	})
	t.Run("DelayedAuthenticationFailureDoesNotPoisonRenewal", func(t *testing.T) {
		f := newFenceFixture(t, factory)
		f.server.Script(dep.PathSession, Scripted{Status: http.StatusUnauthorized})
		client, wait, release := f.gatedClient(t, dep.PathSession)
		done := make(chan error, 1)
		old := f.server.Tokens()
		go func() { _, err := client.StoreTokens(t.Context(), f.name, old); done <- err }()
		wait()
		renewed := old
		renewed.AccessToken = "renewed"
		f.server.set(func() { f.server.o.Tokens = renewed })
		if _, err := f.client.StoreTokens(t.Context(), f.name, renewed); err != nil {
			t.Fatal(err)
		}
		release()
		if err := <-done; !errors.Is(err, dep.ErrTokenInvalid) || !errors.Is(err, dep.ErrConflict) {
			t.Fatalf("old authentication: %v", err)
		}
		a, _ := f.store.GetAccount(t.Context(), f.name)
		if a.State != (dep.AccountState{}) || a.AccessToken != renewed.AccessToken {
			t.Fatal("old failure poisoned renewed credentials")
		}
	})
	for _, path := range []string{dep.PathFetchDevices, dep.PathProfileDevs, dep.PathAccount} {
		t.Run("ResetRejectsInFlight"+path, func(t *testing.T) {
			f := newFenceFixture(t, factory)
			profile := f.profile(t)
			f.changeAccount(t, func(a *dep.Account) { a.ProfileUUID = profile })
			raw := f.tokenFile(t)
			client, wait, release := f.gatedClient(t, path)
			if path == dep.PathAccount {
				f.server.Script(path, Scripted{Status: 200, Body: `{}`, Session: "obsolete-rotated-session"})
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch path {
				case dep.PathFetchDevices:
					worker, _ := dep.NewSyncer(dep.SyncerConfig{Client: client, Store: f.store, Account: f.name, Clock: f.clock})
					_, err = worker.RunOnce(t.Context())
				case dep.PathProfileDevs:
					worker, _ := dep.NewAssigner(dep.AssignerConfig{Client: client, Store: f.store, Account: f.name, Clock: f.clock})
					_, err = worker.RunOnce(t.Context())
				default:
					_, err = client.Account(t.Context(), f.name)
				}
				done <- err
			}()
			wait()
			f.server.SetAccount(dep.AccountDetail{ServerUUID: "NEW-SERVER", OrgID: "ORG-2"})
			if _, err := f.client.ImportToken(t.Context(), f.name, raw, dep.ImportOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			session, _ := f.store.Session(t.Context(), f.name)
			release()
			if err := <-done; !errors.Is(err, dep.ErrConflict) {
				t.Fatalf("old response accepted: %v", err)
			}
			if got, _ := f.store.Session(t.Context(), f.name); got != session {
				t.Fatal("old response replaced session")
			}
			if _, err := f.store.GetDevice(t.Context(), f.name, "A"); !errors.Is(err, dep.ErrNotFound) {
				t.Fatalf("old response repopulated inventory: %v", err)
			}
			// Reuse the old client: it must use the new persisted session, not its old cache.
			f.server.ResetRequests()
			if _, err := client.Account(t.Context(), f.name); err != nil {
				t.Fatal(err)
			}
			requests := f.server.Requests()
			if len(requests) != 1 || requests[0].Session != session {
				t.Fatal("old client reused its previous identity's session")
			}
		})
	}
	for _, tc := range []struct {
		method string
		after  int
	}{
		{"LockAccount", 1},
		{"GetAccount", 2},
		{"Keypair", 2},
		{"Keypair", 3},
		{"Cursor", 1},
		{"DeleteAccount", 1},
		{"PutAccount", 1},
		{"PutAccount", 2},
		{"PutKeypair", 1},
		{"SetCursor", 1},
		{"SetSession", 1},
	} {
		t.Run("ResetRollback/"+tc.method+"/"+fmt.Sprint(tc.after), func(t *testing.T) {
			f := newFenceFixture(t, factory)
			raw := f.tokenFile(t)
			before, _ := f.store.Session(t.Context(), f.name)
			f.server.SetAccount(dep.AccountDetail{ServerUUID: "NEW-SERVER", OrgID: "NEW-ORG"})
			fault := errors.New("injected reset failure")
			client := f.newClient(t, f.server.Client(), &Failing{Store: f.store, Fail: map[string]error{tc.method: fault}, After: map[string]int{tc.method: tc.after}})
			if _, err := client.ImportToken(t.Context(), f.name, raw, dep.ImportOptions{Force: true}); !errors.Is(err, fault) {
				t.Fatalf("reset error: %v", err)
			}
			a, err := f.store.GetAccount(t.Context(), f.name)
			if err != nil || a.ServerUUID != "SERVER-UUID-DEPTEST" {
				t.Fatalf("reset escaped rollback: %+v %v", a, err)
			}
			if _, err := f.store.GetDevice(t.Context(), f.name, "A"); err != nil {
				t.Fatal("inventory escaped rollback")
			}
			if got, _ := f.store.Session(t.Context(), f.name); got != before {
				t.Fatal("session escaped rollback")
			}
			if _, err := f.store.Keypair(t.Context(), f.name, dep.StageStaged); err != nil {
				t.Fatal("staged key escaped rollback")
			}
		})
	}
}
