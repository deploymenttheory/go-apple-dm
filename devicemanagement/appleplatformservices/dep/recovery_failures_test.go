package dep_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
)

// TestSyncCommitFailureRecovery checks that a failed final fetch neither publishes its
// page nor deletes missing inventory, and that the same generation remains retryable.
func TestSyncCommitFailureRecovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method string
		after  int
	}{
		{"LockAccount", 2},
		{"GetAccount", 2},
		{"Cursor", 2},
		{"GetDevice", 1},
		{"ListDevices", 1},
		{"PutDevices", 2},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			ctx := t.Context()
			before := dep.Cursor{Phase: dep.PhaseFetch, Generation: "retryable", Revision: 7, UpdatedAt: t0}
			if err := f.store.SetCursor(ctx, acct, before); err != nil {
				t.Fatal(err)
			}
			if err := f.store.PutDevices(ctx, acct, []dep.Device{device("ABSENT")}, t0); err != nil {
				t.Fatal(err)
			}
			f.srv.AddDevices(device("NEW"))
			fault := errors.New("final fetch storage unavailable")
			store := &deptest.Failing{Store: f.store, Fail: map[string]error{tc.method: fault}, After: map[string]int{tc.method: tc.after}}
			worker := newSyncer(t, f, func(c *dep.SyncerConfig) { c.Store = store })
			if res, err := worker.RunOnce(ctx); !errors.Is(err, fault) || res.Pages != 0 {
				t.Fatalf("failed commit: %+v %v", res, err)
			}
			if got, err := f.store.Cursor(ctx, acct); err != nil || !reflect.DeepEqual(got, before) {
				t.Fatalf("cursor escaped rollback: %+v %v", got, err)
			}
			if _, err := f.store.GetDevice(ctx, acct, "NEW"); !errors.Is(err, dep.ErrNotFound) {
				t.Fatalf("new inventory escaped rollback: %v", err)
			}
			if old, err := f.store.GetDevice(ctx, acct, "ABSENT"); err != nil || old.Deleted {
				t.Fatalf("missing inventory deleted before commit: %+v %v", old, err)
			}
			if len(f.eventsOf(dep.EventDeviceAdded))+len(f.eventsOf(dep.EventDeviceDeleted)) != 0 {
				t.Fatal("uncommitted fetch published events")
			}
			store.SetFail(nil)
			if res, err := worker.RunOnce(ctx); err != nil || res.Added != 1 || res.Deleted != 1 {
				t.Fatalf("recovered fetch: %+v %v", res, err)
			}
			if old, err := f.store.GetDevice(ctx, acct, "ABSENT"); err != nil || !old.Deleted {
				t.Fatalf("successful fetch did not reconcile inventory: %+v %v", old, err)
			}
			if got := serials(t, f.store, dep.DeviceQuery{}); !reflect.DeepEqual(got, []string{"NEW"}) {
				t.Fatalf("recovered inventory: %v", got)
			}
			if len(f.eventsOf(dep.EventDeviceAdded)) != 1 || len(f.eventsOf(dep.EventDeviceDeleted)) != 1 {
				t.Fatal("recovery did not publish exactly the committed changes")
			}
		})
	}
}

// TestSyncRestartFailureRecovery keeps the old snapshot when persisting a replacement
// generation fails, whether the restart follows age, initial setup, or Apple's rejection.
func TestSyncRestartFailureRecovery(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"initial", "aged", "rejected"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			ctx := t.Context()
			var before dep.Cursor
			if reason != "initial" {
				before = dep.Cursor{Phase: dep.PhaseSync, Value: "old-cursor", Revision: 9, UpdatedAt: t0}
				if reason == "aged" {
					before.UpdatedAt = t0.Add(-48 * time.Hour)
				}
				if err := f.store.SetCursor(ctx, acct, before); err != nil {
					t.Fatal(err)
				}
			}
			if reason == "rejected" {
				f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: http.StatusBadRequest, Code: dep.CodeInvalidCursor})
			}
			f.srv.AddDevices(device("A"))
			fault := errors.New("cannot lock replacement generation")
			store := &deptest.Failing{Store: f.store, Fail: map[string]error{"LockAccount": fault}, After: map[string]int{"LockAccount": 2}}
			worker := newSyncer(t, f, func(c *dep.SyncerConfig) { c.Store = store; c.MaxCursorAge = time.Hour })
			if _, err := worker.RunOnce(ctx); !errors.Is(err, fault) {
				t.Fatalf("restart failure: %v", err)
			}
			if got, err := f.store.Cursor(ctx, acct); err != nil || !reflect.DeepEqual(got, before) {
				t.Fatalf("failed restart changed cursor: %+v %v", got, err)
			}
			if n := f.srv.Count(http.MethodPost, dep.PathFetchDevices); n != 0 {
				t.Fatalf("fetch started before generation persisted: %d", n)
			}
			store.SetFail(nil)
			if res, err := worker.RunOnce(ctx); err != nil || res.Added != 1 {
				t.Fatalf("restart recovery: %+v %v", res, err)
			}
		})
	}
}

// TestSyncIncompleteFetchAndLargeReconciliation checks that an exhausted initial fetch
// cannot erase inventory and that a later complete snapshot reconciles every absent page.
func TestSyncIncompleteFetchAndLargeReconciliation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	old := make([]dep.Device, 0, 503)
	for i := range 501 {
		old = append(old, device(fmt.Sprintf("ABSENT-%03d", i)))
	}
	old = append(old, device("EXISTING"))
	revived := device("REVIVED")
	revived.OpType = dep.OpDeleted
	old = append(old, revived)
	if err := f.store.PutDevices(ctx, acct, old, t0); err != nil {
		t.Fatal(err)
	}
	f.srv.AddDevices(device("EXISTING"), device("REVIVED"))
	f.srv.Script(dep.PathFetchDevices, deptest.Scripted{Status: http.StatusBadRequest, Code: dep.CodeExhaustedCursor})
	worker := newSyncer(t, f)
	if _, err := worker.RunOnce(ctx); !errors.Is(err, dep.ErrInvalid) {
		t.Fatalf("accepted an exhausted fetch with no cursor: %v", err)
	}
	if d, err := f.store.GetDevice(ctx, acct, "ABSENT-000"); err != nil || d.Deleted {
		t.Fatalf("incomplete fetch deleted inventory: %+v %v", d, err)
	}
	if len(f.eventsOf(dep.EventDeviceDeleted)) != 0 {
		t.Fatal("incomplete fetch published deletions")
	}
	if res, err := worker.RunOnce(ctx); err != nil || res.Deleted != 501 || res.Modified != 1 || res.Added != 1 {
		t.Fatalf("complete fetch: %+v %v", res, err)
	}
	for i := range 501 {
		if d, err := f.store.GetDevice(ctx, acct, fmt.Sprintf("ABSENT-%03d", i)); err != nil || !d.Deleted {
			t.Fatalf("missing tombstone %d: %+v %v", i, d, err)
		}
	}
	if got := serials(t, f.store, dep.DeviceQuery{}); !reflect.DeepEqual(got, []string{"EXISTING", "REVIVED"}) {
		t.Fatalf("reconciled inventory: %v", got)
	}
	if len(f.eventsOf(dep.EventDeviceDeleted)) != 501 || len(f.eventsOf(dep.EventDeviceAdded)) != 1 || len(f.eventsOf(dep.EventDeviceModified)) != 1 {
		t.Fatal("fetch changes were not published exactly once")
	}
}

// TestAssignmentLeaseFailureRecovery verifies fail-closed lease acquisition, renewal,
// and cleanup, followed by recovery without a second remote assignment before lease expiry.
func TestAssignmentLeaseFailureRecovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		method  string
		after   int
		profile bool
		leased  bool
	}{
		{"claim lock", "LockAccount", 1, true, false},
		{"claim account", "GetAccount", 1, true, false},
		{"claim state", "AssignmentState", 1, true, false},
		{"renew lock", "LockAccount", 2, true, true},
		{"renew account", "GetAccount", 2, true, false},
		{"renew state", "AssignmentState", 2, true, true},
		{"release lock", "LockAccount", 2, false, true},
		{"release state", "AssignmentState", 2, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newAssignFixture(t)
			ctx := t.Context()
			f.srv.AddDevices(device("A"))
			f.sync(t)
			if !tc.profile {
				a := f.account()
				a.ProfileUUID = ""
				if err := f.store.PutAccount(ctx, a); err != nil {
					t.Fatal(err)
				}
			}
			fault := errors.New("assignment lease store unavailable")
			store := &deptest.Failing{Store: f.store, Fail: map[string]error{tc.method: fault}, After: map[string]int{tc.method: tc.after}}
			worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Store = store })
			if _, err := worker.RunOnce(ctx); !errors.Is(err, fault) {
				t.Fatalf("lease failure: %v", err)
			}
			if n := f.srv.Count(http.MethodPost, dep.PathProfileDevs); n != 0 {
				t.Fatalf("assignment escaped failed lease: %d", n)
			}
			if _, err := f.store.GetAssignment(ctx, acct, "A"); !errors.Is(err, dep.ErrNotFound) {
				t.Fatalf("lease failure wrote an outcome: %v", err)
			}
			state, err := f.store.AssignmentState(ctx, acct)
			if err != nil || (state.Owner != "") != tc.leased {
				t.Fatalf("lease after failure: %+v %v", state, err)
			}
			store.SetFail(nil)
			if tc.leased {
				if res, err := worker.RunOnce(ctx); !errors.Is(err, dep.ErrBackoff) || !res.NotBefore.Equal(state.LeaseUntil) {
					t.Fatalf("failed cleanup did not retain ownership: %+v %v", res, err)
				}
				f.clock.Advance(3 * time.Minute)
			}
			a := f.account()
			a.ProfileUUID = f.profile
			if err := f.store.PutAccount(ctx, a); err != nil {
				t.Fatal(err)
			}
			if res, err := worker.RunOnce(ctx); err != nil || res.Assigned != 1 {
				t.Fatalf("lease recovery: %+v %v", res, err)
			}
			if state, err := f.store.AssignmentState(ctx, acct); err != nil || state.Owner != "" || !state.LeaseUntil.IsZero() {
				t.Fatalf("successful run retained lease: %+v %v", state, err)
			}
		})
	}
}

// TestAssignmentOutcomeAndReadbackFailureRecovery distinguishes an uncommitted outcome
// from a committed success whose optional readback could not reacquire its lease.
func TestAssignmentOutcomeAndReadbackFailureRecovery(t *testing.T) {
	t.Parallel()
	for _, readback := range []bool{false, true} {
		t.Run(fmt.Sprintf("readback_%t", readback), func(t *testing.T) {
			t.Parallel()
			f := newAssignFixture(t)
			ctx := t.Context()
			f.srv.AddDevices(device("A"))
			f.sync(t)
			method, after := "GetAssignment", 2
			if readback {
				method, after = "LockAccount", 4
			}
			fault := errors.New("assignment persistence unavailable")
			store := &deptest.Failing{Store: f.store, Fail: map[string]error{method: fault}, After: map[string]int{method: after}}
			worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Store = store; c.ReadBack = readback })
			if _, err := worker.RunOnce(ctx); !errors.Is(err, fault) {
				t.Fatalf("assignment failure: %v", err)
			}
			if n := f.srv.Count(http.MethodPost, dep.PathDeviceDetails); n != 0 {
				t.Fatalf("readback passed a failed lease renewal: %d", n)
			}
			if readback {
				if got := assignment(t, f.store, "A"); got.Status != dep.StatusSuccess || got.ProfileUUID != f.profile {
					t.Fatalf("readback failure lost committed outcome: %+v", got)
				}
				if len(f.eventsOf(dep.EventDeviceAssigned)) != 1 {
					t.Fatal("committed assignment did not publish its event")
				}
			} else {
				if _, err := f.store.GetAssignment(ctx, acct, "A"); !errors.Is(err, dep.ErrNotFound) {
					t.Fatalf("failed outcome escaped rollback: %v", err)
				}
				if len(f.eventsOf(dep.EventDeviceAssigned)) != 0 {
					t.Fatal("uncommitted outcome published an event")
				}
			}
			store.SetFail(nil)
			f.clock.Advance(3 * time.Minute)
			res, err := worker.RunOnce(ctx)
			if err != nil {
				t.Fatalf("outcome recovery: %v", err)
			}
			if readback && (res.Candidates != 0 || f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 1) {
				t.Fatalf("readback recovery repeated committed assignment: %+v", res)
			}
			if !readback && res.Assigned != 1 {
				t.Fatalf("failed outcome was not retried: %+v", res)
			}
			if len(f.eventsOf(dep.EventDeviceAssigned)) != 1 {
				t.Fatal("recovery did not publish exactly one committed outcome")
			}
		})
	}
}

// TestAssignmentCooldownFailureRecovery checks that vendor cooldowns never shorten a
// stored deadline, and that failed or obsolete writes cannot publish assignment outcomes.
func TestAssignmentCooldownFailureRecovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		stale     string
		http429   bool
		failStore bool
		keepLater bool
		omitRetry bool
	}{
		{name: "HTTP throttle storage failure", http429: true, failStore: true},
		{name: "HTTP throttle retains later deadline", http429: true, keepLater: true},
		{name: "stale credential cooldown storage failure", stale: "credentials", failStore: true, omitRetry: true},
		{name: "stale target retains later deadline", stale: "target", keepLater: true, omitRetry: true},
		{name: "replaced identity rejects cooldown", stale: "identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newAssignFixture(t)
			ctx := t.Context()
			account := f.account()
			account.ServerUUID = "original-server"
			if err := f.store.PutAccount(ctx, account); err != nil {
				t.Fatal(err)
			}
			f.srv.AddDevices(device("A"))
			f.sync(t)
			answer := deptest.Scripted{Status: http.StatusOK, Body: `{"devices":{"A":"THROTTLED"},"retry_after":90}`}
			if tc.omitRetry {
				answer.Body = `{"devices":{"A":"THROTTLED"}}`
			}
			if tc.http429 {
				answer = deptest.Scripted{Status: http.StatusTooManyRequests, RetryAfter: "90"}
			}
			f.srv.Script(dep.PathProfileDevs, answer)
			client, entered, release := gateClient(t, f.fixture, dep.PathProfileDevs)
			store := &deptest.Failing{Store: f.store}
			worker := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Client = client; c.Store = store })
			done := make(chan error, 1)
			go func() { _, err := worker.RunOnce(ctx); done <- err }()
			awaitRequest(t, entered)
			account = f.account()
			switch tc.stale {
			case "credentials":
				account.AccessSecret += "-renewed"
			case "target":
				account.ProfileUUID = "new-target"
			case "identity":
				account.ServerUUID = "replacement-server"
			}
			if err := f.store.PutAccount(ctx, account); err != nil {
				t.Fatal(err)
			}
			deadline := t0.Add(30 * time.Minute)
			if tc.keepLater {
				state, err := f.store.AssignmentState(ctx, acct)
				if err != nil {
					t.Fatal(err)
				}
				state.NotBefore = deadline
				if err := f.store.PutAssignmentState(ctx, acct, state); err != nil {
					t.Fatal(err)
				}
			}
			fault := errors.New("cooldown persistence unavailable")
			if tc.failStore {
				store.SetFail(map[string]error{"LockAccount": fault})
			}
			release()
			err := <-done
			if err == nil || (tc.failStore && !errors.Is(err, fault)) || (tc.stale != "" && !errors.Is(err, dep.ErrConflict)) {
				t.Fatalf("cooldown error: %v", err)
			}
			if _, err := f.store.GetAssignment(ctx, acct, "A"); !errors.Is(err, dep.ErrNotFound) {
				t.Fatalf("obsolete or failed batch recorded an outcome: %v", err)
			}
			if len(f.eventsOf(dep.EventDeviceAssigned)) != 0 {
				t.Fatal("obsolete or failed batch published an assignment")
			}
			state, err := f.store.AssignmentState(ctx, acct)
			if err != nil {
				t.Fatal(err)
			}
			if tc.keepLater {
				if !state.NotBefore.Equal(deadline) || state.Owner != "" {
					t.Fatalf("cooldown shortened or lease retained: %+v", state)
				}
				if res, err := worker.RunOnce(ctx); !errors.Is(err, dep.ErrBackoff) || !res.NotBefore.Equal(deadline) {
					t.Fatalf("persisted cooldown was ignored: %+v %v", res, err)
				}
			} else if !state.NotBefore.IsZero() || state.Failures != 0 {
				t.Fatalf("failed or obsolete cooldown wrote retry state: %+v", state)
			}
			store.SetFail(nil)
			f.clock.Advance(time.Hour)
			// Restore accepted credentials and desired policy without deleting retry state.
			account = f.account()
			account.SetTokens(f.srv.Tokens())
			account.ProfileUUID = f.profile
			if err := f.store.PutAccount(ctx, account); err != nil {
				t.Fatal(err)
			}
			if res, err := worker.RunOnce(ctx); err != nil || res.Assigned != 1 {
				t.Fatalf("cooldown recovery: %+v %v", res, err)
			}
		})
	}
}

// failureTransport lets a test fail a selected response without changing the fake service.
type failureTransport func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f failureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failedResponseBody struct {
	err    error
	closed *bool
}

// Read reports the injected transport stream failure.
func (b failedResponseBody) Read([]byte) (int, error) { return 0, b.err }

// Close records that the client released the failed response stream.
func (b failedResponseBody) Close() error { *b.closed = true; return nil }

// TestClientResponseFailureRecovery verifies that an unreadable response cannot replace
// credentials or sessions, that its body is closed, and that a later request recovers.
func TestClientResponseFailureRecovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		path      string
		validate  bool
		transport bool
	}{
		{name: "account body", path: dep.PathAccount},
		{name: "session body", path: dep.PathSession},
		{name: "token validation body", path: dep.PathAccount, validate: true},
		{name: "token validation transport", path: dep.PathAccount, validate: true, transport: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			ctx := t.Context()
			if tc.path != dep.PathSession {
				if _, err := f.client.Account(ctx, acct); err != nil {
					t.Fatal(err)
				}
			}
			before := f.account()
			session, err := f.store.Session(ctx, acct)
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("response connection lost")
			broken, closed := true, false
			transport := failureTransport(func(r *http.Request) (*http.Response, error) {
				if broken && r.URL.Path == tc.path && tc.transport {
					return nil, fault
				}
				resp, err := f.srv.Client().Transport.RoundTrip(r)
				if err == nil && broken && r.URL.Path == tc.path {
					_ = resp.Body.Close()
					resp.Body = failedResponseBody{err: fault, closed: &closed}
				}
				return resp, err
			})
			client, err := dep.NewClient(dep.ClientConfig{Store: f.store, BaseURL: f.srv.URL(), Clock: f.clk, HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			call := func() error {
				if tc.validate {
					_, err := client.StoreTokens(ctx, acct, f.srv.Tokens())
					return err
				}
				_, err := client.Account(ctx, acct)
				return err
			}
			if err := call(); !errors.Is(err, fault) {
				t.Fatalf("response failure: %v", err)
			}
			if !tc.transport && !closed {
				t.Fatal("failed response body was not closed")
			}
			if !reflect.DeepEqual(f.account(), before) {
				t.Fatal("failed response replaced account data")
			}
			if got, err := f.store.Session(ctx, acct); err != nil || got != session {
				t.Fatalf("failed response replaced session: %q %v", got, err)
			}
			broken = false
			if err := call(); err != nil {
				t.Fatalf("response recovery: %v", err)
			}
		})
	}
}

// TestClientStateClearFailureRecovery exercises both authentication and ordinary API
// success: a failed state write must remain visible, then clear after storage recovers.
func TestClientStateClearFailureRecovery(t *testing.T) {
	t.Parallel()
	for _, storedSession := range []bool{false, true} {
		t.Run(fmt.Sprintf("stored_session_%t", storedSession), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			ctx := t.Context()
			if storedSession {
				if _, err := f.client.Account(ctx, acct); err != nil {
					t.Fatal(err)
				}
			}
			before := dep.AccountState{TokenInvalid: true, TermsExpired: true}
			if err := f.store.SetAccountState(ctx, acct, before); err != nil {
				t.Fatal(err)
			}
			session, err := f.store.Session(ctx, acct)
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("cannot persist authentication recovery")
			store := &deptest.Failing{Store: f.store, Fail: map[string]error{"SetAccountState": fault}}
			client, err := dep.NewClient(dep.ClientConfig{Store: store, BaseURL: f.srv.URL(), Clock: f.clk, HTTPClient: f.srv.Client()})
			if err != nil {
				t.Fatal(err)
			}
			if out, err := client.Account(ctx, acct); !errors.Is(err, fault) || out != nil {
				t.Fatalf("state failure reported as success: %+v %v", out, err)
			}
			if got := f.account().State; got != before {
				t.Fatalf("failed state update escaped rollback: %+v", got)
			}
			if got, err := f.store.Session(ctx, acct); err != nil || got != session {
				t.Fatalf("failed state update replaced session: %q %v", got, err)
			}
			store.SetFail(nil)
			if _, err := client.Account(ctx, acct); err != nil {
				t.Fatalf("state recovery: %v", err)
			}
			if got := f.account().State; got != (dep.AccountState{}) {
				t.Fatalf("successful recovery retained failure state: %+v", got)
			}
		})
	}
}

// TestTokenCreationLockFailureRecovery ensures validation alone cannot create an
// account when its transaction cannot obtain the creation fence.
func TestTokenCreationLockFailureRecovery(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withoutAccount)
	ctx := t.Context()
	fault := errors.New("creation fence unavailable")
	store := &deptest.Failing{Store: f.store, Fail: map[string]error{"LockAccount": fault}}
	client, err := dep.NewClient(dep.ClientConfig{Store: store, BaseURL: f.srv.URL(), Clock: f.clk, HTTPClient: f.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.StoreTokens(ctx, acct, f.srv.Tokens()); !errors.Is(err, fault) {
		t.Fatalf("creation failure: %v", err)
	}
	if _, err := f.store.GetAccount(ctx, acct); !errors.Is(err, dep.ErrNotFound) {
		t.Fatalf("validation created account without a fence: %v", err)
	}
	store.SetFail(nil)
	if _, err := client.StoreTokens(ctx, acct, f.srv.Tokens()); err != nil {
		t.Fatalf("creation recovery: %v", err)
	}
	if got := f.account(); !got.HasTokens() || got.ServerUUID == "" {
		t.Fatalf("recovered account is incomplete: %+v", got)
	}
}

// TestClientRequestAndExpiryFailures confirms local validation prevents network calls
// while a failed optional expiry notification does not interrupt a valid request.
func TestClientRequestAndExpiryFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	foreign, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://different.example/account", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []*http.Request{nil, {Method: http.MethodGet, Body: io.NopCloser(http.NoBody)}, foreign} {
		if err := f.client.Do(ctx, acct, req, nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("invalid request: %v", err)
		}
	}
	f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(t0) })
	if _, err := f.client.Account(ctx, acct); !errors.Is(err, dep.ErrTokenExpired) {
		t.Fatalf("expired account request: %v", err)
	}
	if len(f.srv.Requests()) != 0 {
		t.Fatal("local validation performed network I/O")
	}
	f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(t0.Add(time.Hour)) })
	f.bus.Subscribe(dep.EventTokenExpiring, func(context.Context, event.Event) error { return errors.New("notification unavailable") })
	if _, err := f.client.Account(ctx, acct); err != nil {
		t.Fatalf("optional notification broke request: %v", err)
	}
	if len(f.eventsOf(dep.EventTokenExpiring)) != 1 {
		t.Fatal("expiry warning was not attempted")
	}
}
