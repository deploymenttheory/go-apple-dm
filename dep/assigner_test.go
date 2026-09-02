package dep_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/dep"
	"github.com/deploymenttheory/go-apple-mdm/dep/deptest"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// assignFixture is a fixture with a defined profile the account targets.
type assignFixture struct {
	*fixture
	profile string
	syncer  *dep.Syncer
}

func newAssignFixture(t *testing.T, opts ...func(*fixtureOptions)) *assignFixture {
	t.Helper()
	f := newFixture(t, opts...)
	resp, err := f.client.DefineProfile(context.Background(), acct, &dep.Profile{ProfileName: "Corp", URL: "https://mdm.example.com", OrgMagic: "m"})
	if err != nil {
		t.Fatal(err)
	}
	f.putAccount(func(a *dep.Account) { a.ProfileUUID = resp.ProfileUUID })
	return &assignFixture{fixture: f, profile: resp.ProfileUUID, syncer: newSyncer(t, f)}
}

func (a *assignFixture) sync(t *testing.T) {
	t.Helper()
	if _, err := a.syncer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func newAssigner(t *testing.T, f *assignFixture, mutate ...func(*dep.AssignerConfig)) *dep.Assigner {
	t.Helper()
	cfg := dep.AssignerConfig{
		Client: f.client, Store: f.store, Account: acct, Clock: f.clk, Bus: f.bus,
		NotAccessibleBackoff: dep.Backoff{Base: time.Hour, Max: 4 * time.Hour, Jitter: -1},
		FailedBackoff:        dep.Backoff{Base: 10 * time.Minute, Max: time.Hour, Jitter: -1},
		AccountBackoff:       dep.Backoff{Base: time.Minute, Max: 10 * time.Minute, Jitter: -1},
	}
	for _, m := range mutate {
		m(&cfg)
	}
	a, err := dep.NewAssigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func assignment(t *testing.T, s dep.Store, serial string) *dep.Assignment {
	t.Helper()
	a, err := s.GetAssignment(context.Background(), acct, serial)
	if err != nil {
		t.Fatalf("assignment %s: %v", serial, err)
	}
	return a
}

func TestAssigner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("StaleProfileReassigned", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		f.srv.AddDevices(device("A"))
		f.sync(t)
		a := newAssigner(t, f)
		res, err := a.RunOnce(ctx)
		if err != nil || res.Candidates != 1 || res.Assigned != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		if d, _ := f.srv.Device("A"); d.ProfileUUID != f.profile || d.ProfileStatus != dep.ProfileStatusAssigned {
			t.Fatalf("service device %+v", d)
		}
		asg := assignment(t, f.store, "A")
		if asg.Status != dep.StatusSuccess || asg.ProfileUUID != f.profile || !asg.NextAttemptAt.IsZero() || !asg.AttemptedAt.Equal(t0) {
			t.Fatalf("assignment %+v", asg)
		}
		if evs := f.eventsOf(dep.EventDeviceAssigned); len(evs) != 1 || evs[0].Data.(dep.AssignmentEvent).Assignment.SerialNumber != "A" {
			t.Fatalf("events %+v", evs)
		}
		// Until Apple reports the device again the success is trusted: the
		// device is not even a candidate although the stored record still
		// lacks the profile.
		if res, err := a.RunOnce(ctx); err != nil || res.Candidates != 0 || res.Deferred != 0 || res.Assigned != 0 {
			t.Fatalf("second run %+v %v", res, err)
		}
		// The sync brings the modified op with the profile: nothing to do.
		f.sync(t)
		if d, _ := f.store.GetDevice(ctx, acct, "A"); d.ProfileUUID != f.profile || d.OpType != dep.OpModified {
			t.Fatalf("synced device %+v", d)
		}
		if res, err := a.RunOnce(ctx); err != nil || res.Candidates != 0 {
			t.Fatalf("converged %+v %v", res, err)
		}
		// A server move: Apple reports the device modified with the profile
		// gone. State says it is off the profile, so it is re-assigned
		// (nanodep and Fleet act on op_type=added only and would not).
		f.srv.ModifyDevice("A", func(d *dep.Device) { d.ProfileUUID, d.ProfileStatus = "", dep.ProfileStatusRemoved })
		f.clock.Advance(time.Minute)
		f.sync(t)
		f.srv.ResetRequests()
		res, err = a.RunOnce(ctx)
		if err != nil || res.Assigned != 1 || f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 1 {
			t.Fatalf("after server move %+v %v", res, err)
		}
		// Assigned to another profile: re-assigned too.
		f.srv.ModifyDevice("A", func(d *dep.Device) { d.ProfileUUID, d.ProfileStatus = "PROFILE-OTHER", dep.ProfileStatusPushed })
		f.clock.Advance(time.Minute)
		f.sync(t)
		if res, err := a.RunOnce(ctx); err != nil || res.Assigned != 1 {
			t.Fatalf("other profile %+v %v", res, err)
		}
		if !dep.NeedsAssignment(dep.Device{ProfileUUID: "P", ProfileStatus: dep.ProfileStatusEmpty}, "P") || dep.NeedsAssignment(dep.Device{ProfileUUID: "P", ProfileStatus: dep.ProfileStatusPushed}, "P") {
			t.Fatal("NeedsAssignment")
		}
	})

	t.Run("AfterRefetchAssigns", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		f.srv.AddDevices(device("A"), device("B"))
		// A full fetch carries no op_type; nanodep's assigner would see
		// nothing to do.
		f.sync(t)
		if d, _ := f.store.GetDevice(ctx, acct, "A"); d.OpType != "" {
			t.Fatalf("fetch record carries op_type %q", d.OpType)
		}
		a := newAssigner(t, f)
		res, err := a.RunOnce(ctx)
		if err != nil || res.Assigned != 2 {
			t.Fatalf("%+v %v", res, err)
		}
		// A later re-fetch (expired cursor) that shows the profile gone
		// again re-assigns from state.
		f.srv.ModifyDevice("B", func(d *dep.Device) { d.ProfileUUID, d.ProfileStatus = "", dep.ProfileStatusEmpty })
		f.clock.Advance(8 * 24 * time.Hour)
		cur, _ := f.store.Cursor(ctx, acct)
		cur.UpdatedAt = f.clk.Now()
		_ = f.store.SetCursor(ctx, acct, cur)
		res2, err := f.syncer.RunOnce(ctx)
		if err != nil || !res2.Restarted {
			t.Fatalf("refetch %+v %v", res2, err)
		}
		res, err = a.RunOnce(ctx)
		if err != nil || res.Assigned != 1 || res.Candidates != 1 {
			t.Fatalf("after refetch %+v %v", res, err)
		}
		if asg := assignment(t, f.store, "B"); asg.Status != dep.StatusSuccess {
			t.Fatalf("B %+v", asg)
		}
	})

	t.Run("RetryWithBackoff", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		f.srv.AddDevices(device("F"), device("N"))
		f.srv.Fail("F", true)
		f.srv.NotAccessible("N", true)
		f.sync(t)
		a := newAssigner(t, f)
		res, err := a.RunOnce(ctx)
		if err != nil || res.Failed != 1 || res.NotAccessible != 1 || res.Assigned != 0 {
			t.Fatalf("%+v %v", res, err)
		}
		fa, na := assignment(t, f.store, "F"), assignment(t, f.store, "N")
		if fa.Status != dep.StatusFailed || fa.Attempts != 1 || fa.LastError != dep.StatusFailed || !fa.NextAttemptAt.Equal(t0.Add(10*time.Minute)) {
			t.Fatalf("F %+v", fa)
		}
		if na.Status != dep.StatusNotAccessible || na.Attempts != 1 || !na.NextAttemptAt.Equal(t0.Add(time.Hour)) {
			t.Fatalf("N %+v", na)
		}
		// Before the next attempt nothing is sent.
		f.srv.ResetRequests()
		if res, err := a.RunOnce(ctx); err != nil || res.Deferred != 2 || f.srv.Count(http.MethodPost, dep.PathProfileDevs) != 0 {
			t.Fatalf("deferred %+v %v", res, err)
		}
		// After it the delay doubles: F at 20m, N untouched until its hour.
		f.clock.Advance(10 * time.Minute)
		if res, err := a.RunOnce(ctx); err != nil || res.Failed != 1 || res.Deferred != 1 {
			t.Fatalf("retry %+v %v", res, err)
		}
		if fa = assignment(t, f.store, "F"); fa.Attempts != 2 || !fa.NextAttemptAt.Equal(f.clk.Now().Add(20*time.Minute)) {
			t.Fatalf("F retry %+v", fa)
		}
		f.clock.Advance(50 * time.Minute)
		if res, err := a.RunOnce(ctx); err != nil || res.Failed != 1 || res.NotAccessible != 1 {
			t.Fatalf("both retried %+v %v", res, err)
		}
		if na = assignment(t, f.store, "N"); na.Attempts != 2 || !na.NextAttemptAt.Equal(f.clk.Now().Add(2*time.Hour)) {
			t.Fatalf("N retry %+v", na)
		}
		// The cap holds (10m, 20m, 40m, then 1h), and a success resets the count.
		f.clock.Advance(3 * time.Hour)
		if _, err := a.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if fa = assignment(t, f.store, "F"); fa.Attempts != 4 || !fa.NextAttemptAt.Equal(f.clk.Now().Add(time.Hour)) {
			t.Fatalf("F fourth %+v", fa)
		}
		f.srv.Fail("F", false)
		f.clock.Advance(time.Hour)
		if _, err := a.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if fa = assignment(t, f.store, "F"); fa.Status != dep.StatusSuccess || fa.Attempts != 0 || fa.LastError != "" {
			t.Fatalf("F success %+v", fa)
		}
		// An unknown outcome value counts as FAILED and is kept as the error.
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 200, Body: `{"profile_uuid":"` + f.profile + `","devices":{"N":"WEIRD_STATUS"}}`})
		f.clock.Advance(24 * time.Hour)
		if _, err := a.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if na = assignment(t, f.store, "N"); na.Status != dep.StatusFailed || na.LastError != "WEIRD_STATUS" {
			t.Fatalf("N weird %+v", na)
		}
	})

	t.Run("ThrottledHonoursRetryAfterSeconds", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		f.srv.AddDevices(device("T"), device("U"))
		f.srv.Throttle("T", 120)
		f.sync(t)
		a := newAssigner(t, f, func(c *dep.AssignerConfig) { c.ThrottleDelay = 5 * time.Minute })
		res, err := a.RunOnce(ctx)
		if err != nil || res.Throttled != 1 || res.Assigned != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		asg := assignment(t, f.store, "T")
		if asg.Status != dep.StatusThrottled || !asg.NextAttemptAt.Equal(t0.Add(120*time.Second)) || asg.Attempts != 1 {
			t.Fatalf("T %+v", asg)
		}
		f.clock.Advance(119 * time.Second)
		if res, err := a.RunOnce(ctx); err != nil || res.Deferred != 1 {
			t.Fatalf("before retry_after %+v %v", res, err)
		}
		f.clock.Advance(time.Second)
		f.srv.Throttle("T", 0)
		if res, err := a.RunOnce(ctx); err != nil || res.Assigned != 1 {
			t.Fatalf("after retry_after %+v %v", res, err)
		}
		// THROTTLED without retry_after_seconds (protocol < 10) uses the
		// configured delay.
		f.srv.ModifyDevice("U", func(d *dep.Device) { d.ProfileUUID, d.ProfileStatus = "", dep.ProfileStatusEmpty })
		f.clock.Advance(time.Minute)
		f.sync(t)
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 200, Body: `{"profile_uuid":"` + f.profile + `","devices":{"U":"THROTTLED"}}`})
		if _, err := a.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if asg := assignment(t, f.store, "U"); asg.Status != dep.StatusThrottled || !asg.NextAttemptAt.Equal(f.clk.Now().Add(5*time.Minute)) {
			t.Fatalf("U %+v", asg)
		}
	})

	t.Run("MissingSerialIsFailed", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		f.srv.AddDevices(device("M"), device("P"))
		f.sync(t)
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 200, Body: `{"profile_uuid":"` + f.profile + `","devices":{"P":"SUCCESS"}}`})
		a := newAssigner(t, f)
		res, err := a.RunOnce(ctx)
		if err != nil || res.Failed != 1 || res.Assigned != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		asg := assignment(t, f.store, "M")
		if asg.Status != dep.StatusFailed || !strings.Contains(asg.LastError, "missing") || !asg.NextAttemptAt.Equal(t0.Add(10*time.Minute)) {
			t.Fatalf("M %+v", asg)
		}
	})

	t.Run("Http429BacksOffAccount", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		f.srv.AddDevices(device("A"))
		f.sync(t)
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 429, RetryAfter: "90"})
		a := newAssigner(t, f)
		res, err := a.RunOnce(ctx)
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Status != 429 || !res.NotBefore.Equal(t0.Add(90*time.Second)) {
			t.Fatalf("%+v %v", res, err)
		}
		if _, err := f.store.GetAssignment(ctx, acct, "A"); !errors.Is(err, dep.ErrNotFound) {
			t.Fatal("a 429 recorded a per-serial outcome")
		}
		f.srv.ResetRequests()
		res, err = a.RunOnce(ctx)
		if !errors.Is(err, dep.ErrBackoff) || !res.NotBefore.Equal(t0.Add(90*time.Second)) || len(f.srv.Requests()) != 0 {
			t.Fatalf("during backoff %+v %v requests=%d", res, err, len(f.srv.Requests()))
		}
		f.clock.Advance(90 * time.Second)
		if res, err := a.RunOnce(ctx); err != nil || res.Assigned != 1 {
			t.Fatalf("after backoff %+v %v", res, err)
		}
		// Without Retry-After the account backoff grows per failure and
		// resets on success.
		f.srv.ModifyDevice("A", func(d *dep.Device) { d.ProfileUUID, d.ProfileStatus = "", dep.ProfileStatusEmpty })
		f.clock.Advance(time.Minute)
		f.sync(t)
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 429})
		if res, err := a.RunOnce(ctx); !errors.As(err, &derr) || !res.NotBefore.Equal(f.clk.Now().Add(time.Minute)) {
			t.Fatalf("no Retry-After %+v %v", res, err)
		}
		f.clock.Advance(time.Minute)
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 429})
		if res, err := a.RunOnce(ctx); !errors.As(err, &derr) || !res.NotBefore.Equal(f.clk.Now().Add(2*time.Minute)) {
			t.Fatalf("second 429 %+v %v", res, err)
		}
		// Other service errors surface without an account backoff.
		f.clock.Advance(2 * time.Minute)
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 500})
		if res, err := a.RunOnce(ctx); !errors.As(err, &derr) || derr.Status != 500 || !res.NotBefore.IsZero() {
			t.Fatalf("500 %+v %v", res, err)
		}
		if res, err := a.RunOnce(ctx); err != nil || res.Assigned != 1 {
			t.Fatalf("after 500 %+v %v", res, err)
		}
	})

	t.Run("FilterHook", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		f.srv.AddDevices(device("A"), dep.Device{SerialNumber: "B", DeviceFamily: "Mac"})
		f.sync(t)
		a := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Filter = func(d dep.Device) bool { return d.DeviceFamily == "Mac" } })
		res, err := a.RunOnce(ctx)
		if err != nil || res.Candidates != 1 || res.Assigned != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		if _, err := f.store.GetAssignment(ctx, acct, "A"); !errors.Is(err, dep.ErrNotFound) {
			t.Fatal("filtered device assigned")
		}
		if asg := assignment(t, f.store, "B"); asg.Status != dep.StatusSuccess {
			t.Fatalf("B %+v", asg)
		}
		// Tombstoned devices are never candidates.
		f.srv.DeleteDevice("B")
		f.srv.ModifyDevice("A", func(d *dep.Device) { d.DeviceFamily = "Mac" })
		f.clock.Advance(time.Minute)
		f.sync(t)
		if res, err := a.RunOnce(ctx); err != nil || res.Candidates != 1 {
			t.Fatalf("after delete %+v %v", res, err)
		}
	})

	t.Run("ReadBackAndBatches", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		f.srv.AddDevices(device("A"), device("B"), device("C"))
		f.srv.NotAccessible("C", true)
		f.sync(t)
		a := newAssigner(t, f, func(c *dep.AssignerConfig) { c.ReadBack = true; c.BatchSize = 2; c.UsePUT = true; c.PageSize = 1 })
		res, err := a.RunOnce(ctx)
		if err != nil || res.Assigned != 2 || res.NotAccessible != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		if f.srv.Count(http.MethodPut, dep.PathProfileDevs) != 2 || f.srv.Count(http.MethodPost, dep.PathDeviceDetails) != 2 {
			t.Fatalf("batches: put=%d details=%d", f.srv.Count(http.MethodPut, dep.PathProfileDevs), f.srv.Count(http.MethodPost, dep.PathDeviceDetails))
		}
		d, _ := f.store.GetDevice(ctx, acct, "A")
		if d.ProfileUUID != f.profile || d.ProfileStatus != dep.ProfileStatusAssigned || d.ResponseStatus != "" || d.OpType != "" {
			t.Fatalf("read back %+v", d)
		}
		if res, err := a.RunOnce(ctx); err != nil || res.Candidates != 1 || res.Deferred != 1 {
			t.Fatalf("converged %+v %v", res, err)
		}
		// Read-back failures surface; an unreadable serial is skipped.
		f.srv.ModifyDevice("A", func(d *dep.Device) { d.ProfileUUID, d.ProfileStatus = "", dep.ProfileStatusEmpty })
		f.clock.Advance(time.Minute)
		f.sync(t)
		f.srv.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 500})
		var derr *dep.Error
		if _, err := a.RunOnce(ctx); !errors.As(err, &derr) || derr.Status != 500 {
			t.Fatalf("details 500: %v", err)
		}
		f.srv.ModifyDevice("A", func(d *dep.Device) { d.ProfileUUID, d.ProfileStatus = "", dep.ProfileStatusEmpty })
		f.clock.Advance(time.Minute)
		f.sync(t)
		f.srv.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 200, Body: `{"devices":{"A":{"response_status":"NOT_ACCESSIBLE"}}}`})
		if res, err := a.RunOnce(ctx); err != nil || res.Assigned != 1 {
			t.Fatalf("unreadable serial %+v %v", res, err)
		}
	})

	t.Run("RunLoop", func(t *testing.T) {
		// The loop waits on the fake clock, so the test advances time
		// rather than sleeping; HTTP to the fake service stays real.
		f := newAssignFixture(t)
		f.srv.AddDevices(device("A"))
		f.sync(t)
		a := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Interval = time.Hour })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx) }()
		waitFor(t, "first run", func() bool {
			asg, err := f.store.GetAssignment(context.Background(), acct, "A")
			return err == nil && asg.Status == dep.StatusSuccess && f.clock.Pending() >= 1 // the loop is parked on its interval
		})
		// A 429 backoff shortens the wait; a Kick during it is refused
		// and the loop waits for the backoff to end.
		f.srv.AddDevices(device("B"))
		f.sync(t)
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 429, RetryAfter: "30"})
		before, pending := len(f.srv.Requests()), f.clock.Pending()
		a.Kick()
		a.Kick() // never blocks
		waitFor(t, "429 seen", func() bool { return len(f.srv.Requests()) > before && f.clock.Pending() > pending })
		if _, err := f.store.GetAssignment(context.Background(), acct, "B"); !errors.Is(err, dep.ErrNotFound) {
			t.Fatal("assigned through a 429")
		}
		f.clock.Advance(31 * time.Second)
		waitFor(t, "after backoff", func() bool {
			asg, err := f.store.GetAssignment(context.Background(), acct, "B")
			return err == nil && asg.Status == dep.StatusSuccess
		})
		// A non-429 failure with the interval wait, then the tick.
		f.srv.AddDevices(device("C"))
		f.sync(t)
		f.srv.Script(dep.PathProfileDevs, deptest.Scripted{Status: 500})
		before, pending = len(f.srv.Requests()), f.clock.Pending()
		a.Kick()
		waitFor(t, "500 seen", func() bool { return len(f.srv.Requests()) > before && f.clock.Pending() > pending })
		f.clock.Advance(time.Hour + time.Second)
		waitFor(t, "after interval", func() bool {
			asg, err := f.store.GetAssignment(context.Background(), acct, "C")
			return err == nil && asg.Status == dep.StatusSuccess
		})
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v", err)
		}
	})

	t.Run("ConfigAndStoreFailures", func(t *testing.T) {
		t.Parallel()
		f := newAssignFixture(t)
		for _, cfg := range []dep.AssignerConfig{{}, {Client: f.client}, {Client: f.client, Store: f.store}, {Store: f.store, Account: acct}} {
			if _, err := dep.NewAssigner(cfg); !errors.Is(err, dep.ErrConfig) {
				t.Fatalf("%+v: %v", cfg, err)
			}
		}
		// No profile on the account: nothing happens.
		f.putAccount()
		f.srv.AddDevices(device("A"))
		f.sync(t)
		if res, err := newAssigner(t, f).RunOnce(ctx); err != nil || res.Candidates != 0 {
			t.Fatalf("no profile %+v %v", res, err)
		}
		f.putAccount(func(a *dep.Account) { a.ProfileUUID = f.profile })
		failing := &deptest.Failing{Store: f.store}
		a := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Store = failing; c.ReadBack = true })
		for _, method := range []string{"GetAccount", "ListDevices", "GetAssignment", "Update", "PutAssignment"} {
			failing.Fail = map[string]error{method: errors.New("down " + method)}
			if _, err := a.RunOnce(ctx); err == nil || !strings.Contains(err.Error(), "down "+method) {
				t.Fatalf("%s: %v", method, err)
			}
		}
		// The read-back transaction failing surfaces after the outcome was
		// recorded.
		failing.Fail = map[string]error{"GetDevice": errors.New("down GetDevice")}
		if _, err := a.RunOnce(ctx); err == nil || !strings.Contains(err.Error(), "down GetDevice") {
			t.Fatalf("read-back GetDevice: %v", err)
		}
		if asg := assignment(t, f.store, "A"); asg.Status != dep.StatusSuccess {
			t.Fatalf("outcome lost: %+v", asg)
		}
		// Without a bus, and with an assignment recorded for a serial whose
		// device record is newer, the serial is re-assigned.
		quiet := newAssigner(t, f, func(c *dep.AssignerConfig) { c.Bus = nil })
		if err := f.store.PutDevices(ctx, acct, []dep.Device{{SerialNumber: "A"}}, f.clk.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if res, err := quiet.RunOnce(ctx); err != nil || res.Assigned != 1 {
			t.Fatalf("newer device %+v %v", res, err)
		}
		r, _ := f.store.ListAssignments(ctx, acct, dep.AssignmentQuery{Status: dep.StatusSuccess}, storage.Page{})
		if len(r.Items) != 1 {
			t.Fatalf("assignments %d", len(r.Items))
		}
	})
}

// waitFor polls cond for up to five seconds of real time.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
