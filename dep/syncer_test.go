package dep_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/dep/inmem"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/paging"
)

// newSyncer builds a syncer on the fixture with jitter off.
func newSyncer(t *testing.T, f *fixture, mutate ...func(*dep.SyncerConfig)) *dep.Syncer {
	t.Helper()
	cfg := dep.SyncerConfig{Client: f.client, Store: f.store, Account: acct, Clock: f.clk, Bus: f.bus, Backoff: dep.Backoff{Base: time.Second, Max: 4 * time.Second, Jitter: -1}}
	for _, m := range mutate {
		m(&cfg)
	}
	s, err := dep.NewSyncer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// serials lists the live serials in the store.
func serials(t *testing.T, s dep.Store, q dep.DeviceQuery) []string {
	t.Helper()
	r, err := s.ListDevices(context.Background(), acct, q, paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range r.Items {
		out = append(out, d.SerialNumber)
	}
	return out
}

// cursorsSent lists the cursor values of the logged requests to path.
func cursorsSent(f *fixture, path string) []string {
	var out []string
	for _, r := range f.srv.Requests() {
		if r.Path != path {
			continue
		}
		var body struct {
			Cursor string `json:"cursor"`
			Limit  int    `json:"limit"`
		}
		_ = dep.Unmarshal(r.Body, &body)
		out = append(out, body.Cursor)
	}
	return out
}

func TestSyncer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("FetchThenSync", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"), device("B"), device("C"), device("D"), device("E"))
		var wakes atomic.Int32
		s := newSyncer(t, f, func(c *dep.SyncerConfig) { c.Limit = 2; c.Wake = func() { wakes.Add(1) } })
		res, err := s.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if res.Added != 5 || res.Pages != 4 || res.Phase != dep.PhaseSync || res.Restarted {
			t.Fatalf("result %+v", res)
		}
		if f.srv.Count(http.MethodPost, dep.PathFetchDevices) != 3 || f.srv.Count(http.MethodPost, dep.PathSyncDevices) != 1 {
			t.Fatalf("fetch=%d sync=%d", f.srv.Count(http.MethodPost, dep.PathFetchDevices), f.srv.Count(http.MethodPost, dep.PathSyncDevices))
		}
		if got := serials(t, f.store, dep.DeviceQuery{}); strings.Join(got, "") != "ABCDE" {
			t.Fatalf("stored %v", got)
		}
		if evs := f.eventsOf(dep.EventDeviceAdded); len(evs) != 5 {
			t.Fatalf("added events %d", len(evs))
		}
		if wakes.Load() != 4 {
			t.Fatalf("wakes %d", wakes.Load())
		}
		ev := f.eventsOf(dep.EventDeviceAdded)[0]
		data, ok := ev.Data.(dep.DeviceEvent)
		if !ok || data.Account != acct || data.Device.SerialNumber != "A" || data.Phase != dep.PhaseFetch || ev.Actor != dep.Actor {
			t.Fatalf("event %+v", ev)
		}
		cur, _ := f.store.Cursor(ctx, acct)
		if cur.Phase != dep.PhaseSync || cur.Value == "" || !cur.UpdatedAt.Equal(t0) || cur.FetchedUntil == nil {
			t.Fatalf("cursor %+v", cur)
		}
		// Changes arrive through sync with op types.
		f.resetEvents()
		f.srv.AddDevices(device("F"))
		f.srv.ModifyDevice("B", func(d *dep.Device) { d.AssetTag = "tagged" })
		f.srv.DeleteDevice("C")
		// Three changes at a page size of two arrive as two sync pages.
		res, err = s.RunOnce(ctx)
		if err != nil || res.Added != 1 || res.Modified != 1 || res.Deleted != 1 || res.Pages != 2 {
			t.Fatalf("sync %+v %v", res, err)
		}
		if got := serials(t, f.store, dep.DeviceQuery{}); strings.Join(got, "") != "ABDEF" {
			t.Fatalf("after sync %v", got)
		}
		c, err := f.store.GetDevice(ctx, acct, "C")
		if err != nil || !c.Deleted || c.OpType != dep.OpDeleted {
			t.Fatalf("tombstone %+v %v", c, err)
		}
		if b, _ := f.store.GetDevice(ctx, acct, "B"); b.AssetTag != "tagged" || b.OpType != dep.OpModified {
			t.Fatalf("modified %+v", b)
		}
		if len(f.eventsOf(dep.EventDeviceModified)) != 1 || len(f.eventsOf(dep.EventDeviceDeleted)) != 1 || len(f.eventsOf(dep.EventDeviceAdded)) != 1 {
			t.Fatal("sync events")
		}
		// Nothing new: one sync call, no events, cursor moves on.
		f.resetEvents()
		res, err = s.RunOnce(ctx)
		if err != nil || res.Pages != 1 || res.Added+res.Modified+res.Deleted != 0 {
			t.Fatalf("quiet sync %+v %v", res, err)
		}
		// A sync answered EXHAUSTED_CURSOR is a quiet run too.
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 400, Code: dep.CodeExhaustedCursor})
		if res, err := s.RunOnce(ctx); err != nil || res.Pages != 0 {
			t.Fatalf("exhausted sync %+v %v", res, err)
		}
	})

	t.Run("StaleCursorDiscarded", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		s := newSyncer(t, f)
		if _, err := s.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		cur, _ := f.store.Cursor(ctx, acct)
		// The service still knows the cursor; the store says it was
		// received 8 days ago, so it is discarded before the first call.
		cur.UpdatedAt = t0.Add(-8 * 24 * time.Hour)
		if err := f.store.SetCursor(ctx, acct, cur); err != nil {
			t.Fatal(err)
		}
		f.srv.ResetRequests()
		f.resetEvents()
		res, err := s.RunOnce(ctx)
		if err != nil || !res.Restarted || res.Modified != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		if got := cursorsSent(f, dep.PathFetchDevices); len(got) != 1 || got[0] != "" {
			t.Fatalf("fetch cursors %q", got)
		}
		if got := cursorsSent(f, dep.PathSyncDevices); len(got) != 1 || got[0] == cur.Value {
			t.Fatalf("sync cursors %q", got)
		}
		if after, _ := f.store.Cursor(ctx, acct); !after.UpdatedAt.Equal(t0) {
			t.Fatalf("cursor age not refreshed: %+v", after)
		}
		// Exactly 7 days is still fresh.
		cur, _ = f.store.Cursor(ctx, acct)
		cur.UpdatedAt = t0.Add(-7 * 24 * time.Hour)
		_ = f.store.SetCursor(ctx, acct, cur)
		f.srv.ResetRequests()
		if res, err := s.RunOnce(ctx); err != nil || res.Restarted {
			t.Fatalf("7 days: %+v %v", res, err)
		}
		if f.srv.Count(http.MethodPost, dep.PathFetchDevices) != 0 {
			t.Fatal("a 7-day-old cursor triggered a fetch")
		}
	})

	t.Run("ExpiredCursorRefetches", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"), device("B"))
		s := newSyncer(t, f)
		if _, err := s.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		// The service ages the cursor out while the store believes it is
		// fresh: EXPIRED_CURSOR restarts the fetch once.
		f.clock.Advance(8 * 24 * time.Hour)
		cur, _ := f.store.Cursor(ctx, acct)
		cur.UpdatedAt = f.clk.Now()
		_ = f.store.SetCursor(ctx, acct, cur)
		f.srv.ResetRequests()
		f.resetEvents()
		res, err := s.RunOnce(ctx)
		if err != nil || !res.Restarted || res.Modified != 2 {
			t.Fatalf("%+v %v", res, err)
		}
		if f.srv.Count(http.MethodPost, dep.PathFetchDevices) != 1 || len(f.eventsOf(dep.EventDeviceModified)) != 2 {
			t.Fatal("no re-fetch after EXPIRED_CURSOR")
		}
		// INVALID_CURSOR and CURSOR_REQUIRED restart as well.
		for _, code := range []string{dep.CodeInvalidCursor, dep.CodeCursorRequired} {
			f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 400, Code: code})
			if res, err := s.RunOnce(ctx); err != nil || !res.Restarted {
				t.Fatalf("%s: %+v %v", code, res, err)
			}
		}
		// A second rejection in one run is an error, not a loop.
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 400, Code: dep.CodeExpiredCursor})
		f.srv.Script(dep.PathFetchDevices, deptest.Scripted{Status: 400, Code: dep.CodeInvalidCursor})
		_, err = s.RunOnce(ctx)
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Code != dep.CodeInvalidCursor {
			t.Fatalf("second rejection: %v", err)
		}
		// EXHAUSTED_CURSOR during the fetch phase flips to sync.
		_ = f.store.SetCursor(ctx, acct, dep.Cursor{Value: "fetch-cursor", Phase: dep.PhaseFetch, UpdatedAt: f.clk.Now()})
		f.srv.Script(dep.PathFetchDevices, deptest.Scripted{Status: 400, Code: dep.CodeExhaustedCursor})
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 200, Body: `{"cursor":"next","devices":[],"more_to_follow":false}`})
		res, err = s.RunOnce(ctx)
		if err != nil || res.Phase != dep.PhaseSync {
			t.Fatalf("exhausted fetch: %+v %v", res, err)
		}
		if got := cursorsSent(f, dep.PathSyncDevices); got[len(got)-1] != "fetch-cursor" {
			t.Fatalf("sync did not reuse the exhausted fetch cursor: %q", got)
		}
	})

	t.Run("SameCursorIsError", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		s := newSyncer(t, f)
		if _, err := s.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		f.srv.SetRepeatCursor(true)
		f.srv.ResetRequests()
		if _, err := s.RunOnce(ctx); !errors.Is(err, dep.ErrSameCursor) {
			t.Fatalf("err = %v", err)
		}
		if f.srv.Count(http.MethodPost, dep.PathSyncDevices) != 1 {
			t.Fatalf("sync called %d times on a repeated cursor", f.srv.Count(http.MethodPost, dep.PathSyncDevices))
		}
		// A page without a cursor is refused too.
		f.srv.SetRepeatCursor(false)
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 200, Body: `{"devices":[],"more_to_follow":false}`})
		if _, err := s.RunOnce(ctx); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("no cursor: %v", err)
		}
	})

	t.Run("TransientErrorRetriesSoon", func(t *testing.T) {
		// Backoffs wait on the fake clock; a driver advances it whenever
		// the syncer parks, so the test asserts fake elapsed time.
		f := newFixture(t)
		stop := driveClock(f.clock)
		t.Cleanup(stop)
		f.srv.AddDevices(device("A"))
		s := newSyncer(t, f, func(c *dep.SyncerConfig) { c.MaxAttempts = 3 })
		f.srv.Script(dep.PathFetchDevices, deptest.Scripted{Status: 503}, deptest.Scripted{Status: 429, RetryAfter: "3"})
		start := f.clock.Now()
		res, err := s.RunOnce(context.Background())
		if err != nil || res.Added != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		// 1s backoff after the 503, then Retry-After 3s beats the 2s backoff.
		if elapsed := f.clock.Now().Sub(start); elapsed != 4*time.Second {
			t.Fatalf("retried after %v, want 4s", elapsed)
		}
		if f.srv.Count(http.MethodPost, dep.PathFetchDevices) != 3 {
			t.Fatalf("fetch calls %d", f.srv.Count(http.MethodPost, dep.PathFetchDevices))
		}
		// Attempts are bounded.
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 500}, deptest.Scripted{Status: 500}, deptest.Scripted{Status: 500})
		_, err = s.RunOnce(context.Background())
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Status != 500 {
			t.Fatalf("exhausted attempts: %v", err)
		}
		// A non-transient answer is not retried.
		f.srv.ResetRequests()
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 400, Code: dep.CodeUserAgentInvalid})
		if _, err := s.RunOnce(context.Background()); !errors.As(err, &derr) || derr.Code != dep.CodeUserAgentInvalid || f.srv.Count(http.MethodPost, dep.PathSyncDevices) != 1 {
			t.Fatalf("non-transient: %v", err)
		}
		// Cancellation during a backoff returns promptly: the driver is
		// stopped so the syncer stays parked on the clock.
		stop()
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 503})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		pending := f.clock.Pending()
		go func() { _, err := s.RunOnce(ctx); done <- err }()
		waitFor(t, "backoff park", func() bool { return f.clock.Pending() > pending })
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled: %v", err)
		}
	})

	t.Run("DedupeByOpDate", func(t *testing.T) {
		t.Parallel()
		later, earlier := dep.Time(t0.Add(time.Hour)), dep.Time(t0)
		devs := []dep.Device{
			{SerialNumber: "X", OpType: dep.OpModified, OpDate: later, AssetTag: "new"},
			{SerialNumber: "X", OpType: dep.OpAdded, OpDate: earlier, AssetTag: "old"},
			{SerialNumber: "Y", OpType: dep.OpDeleted, OpDate: earlier},
			{SerialNumber: "Y", OpType: dep.OpModified, OpDate: earlier},
			{SerialNumber: "Z", OpType: dep.OpModified, OpDate: earlier, AssetTag: "first"},
			{SerialNumber: "Z", OpType: dep.OpDeleted, OpDate: earlier},
			{SerialNumber: "W", OpType: dep.OpModified, OpDate: earlier, AssetTag: "first"},
			{SerialNumber: "W", OpType: dep.OpModified, OpDate: earlier, AssetTag: "second"},
			{SerialNumber: "V", OpType: dep.OpDeleted, OpDate: earlier},
			{SerialNumber: "V", OpType: dep.OpAdded, OpDate: later},
			{SerialNumber: "U", OpType: dep.OpModified, AssetTag: "a"},
			{SerialNumber: "U", OpType: dep.OpModified, AssetTag: "b"},
		}
		got := dep.Dedupe(devs)
		want := map[string]string{"X": "modified new", "Y": "deleted ", "Z": "deleted ", "W": "modified second", "V": "added ", "U": "modified b"}
		if len(got) != len(want) {
			t.Fatalf("deduped %d, want %d", len(got), len(want))
		}
		for _, d := range got {
			if w := want[d.SerialNumber]; w != d.OpType+" "+d.AssetTag {
				t.Errorf("%s: got %q %q, want %q", d.SerialNumber, d.OpType, d.AssetTag, w)
			}
		}
		if got[0].SerialNumber != "X" || got[1].SerialNumber != "Y" {
			t.Fatal("first-seen order not kept")
		}
		// Through the syncer: a scripted page with a duplicate.
		f := newFixture(t)
		s := newSyncer(t, f)
		f.srv.Script(dep.PathFetchDevices, deptest.Scripted{Status: 200, Body: `{"cursor":"c1","more_to_follow":false,"fetched_until":"2026-09-02T12:00:00Z","devices":[{"serial_number":"D","asset_tag":"a"},{"serial_number":"D","asset_tag":"b"}]}`})
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 200, Body: `{"cursor":"c2","more_to_follow":false,"devices":[{"serial_number":"D","op_type":"deleted","op_date":"2026-09-02T13:00:00Z"},{"serial_number":"D","op_type":"modified","op_date":"2026-09-02T13:00:00Z","asset_tag":"late"}]}`})
		res, err := s.RunOnce(ctx)
		if err != nil || res.Added != 1 || res.Deleted != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		d, _ := f.store.GetDevice(ctx, acct, "D")
		if !d.Deleted || d.OpType != dep.OpDeleted {
			t.Fatalf("deleted did not win the tie: %+v", d)
		}
		if len(f.eventsOf(dep.EventDeviceAdded)) != 1 || len(f.eventsOf(dep.EventDeviceDeleted)) != 1 || len(f.eventsOf(dep.EventDeviceModified)) != 0 {
			t.Fatal("duplicate events")
		}
	})

	t.Run("CursorStoredAfterCommit", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		failing := &deptest.Failing{Store: f.store}
		s := newSyncer(t, f, func(c *dep.SyncerConfig) { c.Store = failing })
		if _, err := s.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		before, _ := f.store.Cursor(ctx, acct)
		f.srv.AddDevices(device("B"))
		f.resetEvents()
		f.srv.ResetRequests()
		failing.Fail = map[string]error{"SetCursor": errors.New("disk full")}
		if _, err := s.RunOnce(ctx); !errors.Is(err, dep.ErrSync) || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("commit failure: %v", err)
		}
		if after, _ := f.store.Cursor(ctx, acct); after.Value != before.Value || after.Phase != before.Phase || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("cursor advanced although the page did not commit: %+v -> %+v", before, after)
		}
		if _, err := f.store.GetDevice(ctx, acct, "B"); !errors.Is(err, dep.ErrNotFound) {
			t.Fatal("device written although the page did not commit")
		}
		if n := len(f.eventsOf(dep.EventDeviceAdded)); n != 0 {
			t.Fatalf("%d events published for an uncommitted page", n)
		}
		// The same cursor is requested again and the page commits once.
		failing.Fail = nil
		res, err := s.RunOnce(ctx)
		if err != nil || res.Added != 1 {
			t.Fatalf("retry: %+v %v", res, err)
		}
		if got := cursorsSent(f, dep.PathSyncDevices); len(got) != 2 || got[0] != before.Value || got[1] != before.Value {
			t.Fatalf("sync cursors %q, want the same cursor twice", got)
		}
		if n := len(f.eventsOf(dep.EventDeviceAdded)); n != 1 {
			t.Fatalf("%d added events, want exactly 1", n)
		}
		// Other store failures inside the commit surface the same way (a
		// sync page carries op types, so GetDevice is not consulted).
		for _, method := range []string{"PutDevices", "Update"} {
			failing.Fail = map[string]error{method: errors.New("down")}
			f.srv.AddDevices(device("C" + method))
			if _, err := s.RunOnce(ctx); !errors.Is(err, dep.ErrSync) {
				t.Fatalf("%s: %v", method, err)
			}
		}
		failing.Fail = map[string]error{"GetAccount": errors.New("down")}
		if _, err := s.RunOnce(ctx); err == nil || errors.Is(err, dep.ErrSync) {
			t.Fatalf("GetAccount: %v", err)
		}
		failing.Fail = map[string]error{"Cursor": errors.New("down")}
		if _, err := s.RunOnce(ctx); err == nil || errors.Is(err, dep.ErrSync) {
			t.Fatalf("Cursor: %v", err)
		}
		// SetCursor after EXHAUSTED_CURSOR in the fetch phase can fail too.
		failing.Fail = map[string]error{"SetCursor": errors.New("down")}
		_ = f.store.SetCursor(ctx, acct, dep.Cursor{Value: "fc", Phase: dep.PhaseFetch, UpdatedAt: f.clk.Now()})
		f.srv.Script(dep.PathFetchDevices, deptest.Scripted{Status: 400, Code: dep.CodeExhaustedCursor})
		if _, err := s.RunOnce(ctx); err == nil || !strings.Contains(err.Error(), "down") {
			t.Fatalf("SetCursor on exhausted: %v", err)
		}
	})

	t.Run("LimitFromAccountDetail", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("A"), device("B"), device("C"))
		f.putAccount(func(a *dep.Account) {
			a.Limits = map[string]dep.Limit{dep.PathFetchDevices: {Default: 1, Maximum: 2}, dep.PathSyncDevices: {Default: 5}}
		})
		s := newSyncer(t, f)
		if _, err := s.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		limits := map[string]int{}
		for _, r := range f.srv.Requests() {
			var body struct {
				Limit int `json:"limit"`
			}
			_ = dep.Unmarshal(r.Body, &body)
			if r.Path == dep.PathFetchDevices || r.Path == dep.PathSyncDevices {
				limits[r.Path] = body.Limit
			}
		}
		if limits[dep.PathFetchDevices] != 2 || limits[dep.PathSyncDevices] != 5 {
			t.Fatalf("limits %v", limits)
		}
		if f.srv.Count(http.MethodPost, dep.PathFetchDevices) != 2 {
			t.Fatal("maximum from the account detail not applied")
		}
		// Without a detail the documented fallback applies; the config
		// overrides both.
		f.putAccount()
		f.srv.ResetRequests()
		_ = f.store.SetCursor(ctx, acct, dep.Cursor{})
		if _, err := s.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Limit int `json:"limit"`
		}
		_ = dep.Unmarshal(f.srv.Requests()[0].Body, &body)
		if body.Limit != dep.FallbackLimit {
			t.Fatalf("fallback limit %d", body.Limit)
		}
		f.srv.ResetRequests()
		_ = f.store.SetCursor(ctx, acct, dep.Cursor{})
		if _, err := newSyncer(t, f, func(c *dep.SyncerConfig) { c.Limit = 7 }).RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		_ = dep.Unmarshal(f.srv.Requests()[0].Body, &body)
		if body.Limit != 7 {
			t.Fatalf("configured limit %d", body.Limit)
		}
	})

	t.Run("RunLoop", func(t *testing.T) {
		// The loop parks on the fake clock; the test advances it.
		f := newFixture(t)
		f.srv.AddDevices(device("A"))
		s := newSyncer(t, f, func(c *dep.SyncerConfig) { c.Interval = time.Hour })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()
		waitFor(t, "first run", func() bool { return len(serials(t, f.store, dep.DeviceQuery{})) == 1 && f.clock.Pending() >= 1 })
		f.srv.AddDevices(device("B"))
		s.SyncNow()
		s.SyncNow() // never blocks
		waitFor(t, "after SyncNow", func() bool { return len(serials(t, f.store, dep.DeviceQuery{})) == 2 })
		// A failing run is retried after the backoff, not the interval.
		f.srv.Script(dep.PathSyncDevices, deptest.Scripted{Status: 400, Code: dep.CodeUserAgentInvalid})
		f.srv.AddDevices(device("C"))
		before, pending := f.srv.Count(http.MethodPost, dep.PathSyncDevices), f.clock.Pending()
		s.SyncNow()
		waitFor(t, "failure parked", func() bool {
			return f.srv.Count(http.MethodPost, dep.PathSyncDevices) > before && f.clock.Pending() > pending
		})
		// The kick channel may still hold an earlier SyncNow, so the loop
		// can park once more before the failing run; advance in small steps
		// until the retry lands and check it took the backoff, not the hour.
		start := f.clock.Now()
		waitFor(t, "after backoff retry", func() bool {
			f.clock.Advance(2 * time.Second)
			return len(serials(t, f.store, dep.DeviceQuery{})) == 3
		})
		if f.clock.Now().Sub(start) >= time.Hour {
			t.Fatal("retry waited for the interval, not the backoff")
		}
		// The interval tick syncs as well.
		f.srv.AddDevices(device("D"))
		// The fired backoff waiter is gone; the loop parks on a new interval.
		waitFor(t, "parked on the interval", func() bool { return f.clock.Pending() >= pending+1 })
		f.clock.Advance(time.Hour + time.Second)
		waitFor(t, "after interval", func() bool { return len(serials(t, f.store, dep.DeviceQuery{})) == 4 })
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v", err)
		}
	})

	t.Run("Config", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		for _, cfg := range []dep.SyncerConfig{{}, {Client: f.client}, {Client: f.client, Store: f.store}, {Store: f.store, Account: acct}} {
			if _, err := dep.NewSyncer(cfg); !errors.Is(err, dep.ErrConfig) {
				t.Fatalf("%+v: %v", cfg, err)
			}
		}
		// Sentinel client errors are not retried as transient.
		f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(t0.Add(-time.Hour)) })
		if _, err := newSyncer(t, f).RunOnce(ctx); !errors.Is(err, dep.ErrTokenExpired) {
			t.Fatalf("expired: %v", err)
		}
		// Without a bus events are simply not published.
		f.putAccount()
		f.srv.AddDevices(device("A"))
		quiet, err := dep.NewSyncer(dep.SyncerConfig{Client: f.client, Store: inmem.New(), Account: acct})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := quiet.RunOnce(ctx); !errors.Is(err, dep.ErrNotFound) {
			t.Fatalf("empty store: %v", err)
		}
		if _, err := newSyncer(t, f, func(c *dep.SyncerConfig) { c.Bus = nil }).RunOnce(ctx); err != nil {
			t.Fatalf("no bus: %v", err)
		}
	})
}

// driveClock advances a fake clock by one second whenever something is
// waiting on it, until the returned stop function is called.
func driveClock(c *clock.Fake) func() {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			default:
			}
			if c.Pending() > 0 {
				c.Advance(time.Second)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); <-finished }) }
}
