package ddm_test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/ddm/ddmtest"
	ddminmem "github.com/deploymenttheory/go-apple-mdm/ddm/inmem"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/push"
	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
	"github.com/deploymenttheory/go-apple-mdm/schema/ddmproto"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

var notifierT0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

type enqueueCall struct {
	ids  []mdm.EnrollmentID
	cmd  *mdm.Command
	opts storage.EnqueueOptions
}

// fakeEnqueuer records commands and can fail or skip enrollments.
type fakeEnqueuer struct {
	mu    sync.Mutex
	calls []enqueueCall
	err   error
	skip  map[mdm.EnrollmentID]error
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, o storage.EnqueueOptions) (storage.EnqueueResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return storage.EnqueueResult{}, f.err
	}
	f.calls = append(f.calls, enqueueCall{ids: ids, cmd: cmd, opts: o})
	res := storage.EnqueueResult{Skipped: map[mdm.EnrollmentID]error{}}
	for _, id := range ids {
		if err, ok := f.skip[id]; ok {
			res.Skipped[id] = err
			continue
		}
		res.Queued = append(res.Queued, id)
	}
	return res, nil
}

func (f *fakeEnqueuer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakePusher records batches and can fail as a whole or per target.
type fakePusher struct {
	mu      sync.Mutex
	batches [][]mdm.EnrollmentID
	err     error
	results map[mdm.EnrollmentID]push.Result
}

func (f *fakePusher) Notify(_ context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]push.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, ids)
	if f.err != nil {
		return nil, f.err
	}
	out := map[mdm.EnrollmentID]push.Result{}
	for _, id := range ids {
		if r, ok := f.results[id]; ok {
			out[id] = r
			continue
		}
		out[id] = push.Result{Sent: true}
	}
	return out, nil
}

func (f *fakePusher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

type notifierFixture struct {
	store    *ddminmem.Store
	engine   *ddm.Engine
	clock    *clock.Fake
	enq      *fakeEnqueuer
	pusher   *fakePusher
	bus      *event.Bus
	notifier *ddm.Notifier
	events   []event.Event
	evMu     sync.Mutex
}

func newNotifierFixture(t *testing.T, mutate func(*ddm.NotifierConfig)) *notifierFixture {
	t.Helper()
	f := &notifierFixture{store: ddminmem.New(), clock: clock.NewFake(notifierT0), enq: &fakeEnqueuer{}, pusher: &fakePusher{}, bus: event.New()}
	f.bus.Subscribe(event.DDMChanged, func(_ context.Context, e event.Event) error {
		f.evMu.Lock()
		defer f.evMu.Unlock()
		f.events = append(f.events, e)
		return nil
	})
	var err error
	f.engine, err = ddm.New(ddm.Config{Store: f.store, Clock: f.clock})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	cfg := ddm.NotifierConfig{Store: f.store, Tokens: f.engine, Enqueuer: f.enq, Pusher: f.pusher, Bus: f.bus, Clock: f.clock}
	if mutate != nil {
		mutate(&cfg)
	}
	if cfg.Clock != f.clock {
		// The engine must stamp changes with the same clock the notifier reads
		// (a synctest bubble's real clock, for the Run tests).
		sameTokens := cfg.Tokens == ddm.TokenSource(f.engine)
		if f.engine, err = ddm.New(ddm.Config{Store: f.store, Clock: cfg.Clock}); err != nil {
			t.Fatalf("engine: %v", err)
		}
		if sameTokens {
			cfg.Tokens = f.engine
		}
	}
	f.notifier, err = ddm.NewNotifier(cfg)
	if err != nil {
		t.Fatalf("notifier: %v", err)
	}
	return f
}

// assignFresh uploads a properties declaration and assigns it directly to id.
func (f *notifierFixture) assignFresh(t *testing.T, id mdm.EnrollmentID, name string) {
	t.Helper()
	raw := fmt.Appendf(nil, `{"Type":"com.apple.management.properties","Identifier":%q,"Payload":{"a":1}}`, name)
	if _, _, err := f.engine.PutDeclaration(context.Background(), raw); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := f.engine.AssignDeclaration(context.Background(), id, name); err != nil {
		t.Fatalf("assign: %v", err)
	}
}

func (f *notifierFixture) drain(t *testing.T) ddm.DrainResult {
	t.Helper()
	res, err := f.notifier.DrainOnce(context.Background())
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	return res
}

func (f *notifierFixture) pending(t *testing.T) []ddm.Change {
	t.Helper()
	rows, err := f.store.PendingChanges(context.Background(), f.clock.Now().Add(365*24*time.Hour), 10000)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestNewNotifier(t *testing.T) {
	t.Run("RequiresStoreTokensEnqueuer", func(t *testing.T) {
		st := ddminmem.New()
		eng, err := ddm.New(ddm.Config{Store: st})
		if err != nil {
			t.Fatal(err)
		}
		cases := map[string]ddm.NotifierConfig{
			"no store":    {Tokens: eng, Enqueuer: &fakeEnqueuer{}},
			"no tokens":   {Store: st, Enqueuer: &fakeEnqueuer{}},
			"no enqueuer": {Store: st, Tokens: eng},
		}
		for name, cfg := range cases {
			if _, err := ddm.NewNotifier(cfg); !errors.Is(err, ddm.ErrNotifierConfig) {
				t.Errorf("%s: err = %v, want ErrNotifierConfig", name, err)
			}
		}
	})
	t.Run("Defaults", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		if f.notifier == nil {
			t.Fatal("nil notifier")
		}
		// A zero-value drain over an empty store succeeds with defaults.
		if res := f.drain(t); res != (ddm.DrainResult{}) {
			t.Fatalf("drain = %+v, want zero", res)
		}
	})
}

func TestNotifier(t *testing.T) {
	ctx := context.Background()
	t.Run("CoalescesBurstWithinWindow", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		dev := ddmtest.Device(1)
		for i := range 5 {
			f.assignFresh(t, dev, fmt.Sprintf("com.example.p%d", i))
			f.clock.Advance(100 * time.Millisecond)
		}
		res := f.drain(t)
		if res.Deferred != 1 || res.Queued != 0 {
			t.Fatalf("within window: %+v, want 1 deferred", res)
		}
		f.clock.Advance(ddm.DefaultNotifyWindow)
		res = f.drain(t)
		if res.Queued != 1 || res.Pushed != 1 {
			t.Fatalf("after window: %+v, want 1 queued 1 pushed", res)
		}
		if f.enq.count() != 1 || f.pusher.count() != 1 {
			t.Fatalf("enqueues = %d pushes = %d, want 1 and 1", f.enq.count(), f.pusher.count())
		}
		if rows := f.pending(t); len(rows) != 0 {
			t.Fatalf("pending after drain = %d, want 0", len(rows))
		}
	})
	t.Run("OneCommandPerEnrollment", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		const n = 40
		ids := make([]mdm.EnrollmentID, 0, n)
		for i := range n {
			id := ddmtest.Device(i)
			ids = append(ids, id)
			f.assignFresh(t, id, fmt.Sprintf("com.example.only.%d", i))
		}
		f.clock.Advance(ddm.DefaultNotifyWindow)
		res := f.drain(t)
		if res.Queued != n || res.Pushed != n {
			t.Fatalf("drain = %+v, want %d queued and pushed", res, n)
		}
		seen := map[mdm.EnrollmentID]bool{}
		for _, c := range f.enq.calls {
			if len(c.ids) != 1 {
				t.Fatalf("call targets %d ids, want 1", len(c.ids))
			}
			id := c.ids[0]
			seen[id] = true
			if c.opts.DedupeKey != ddm.DefaultDedupeKey {
				t.Fatalf("dedupe key = %q", c.opts.DedupeKey)
			}
			dm, ok := c.cmd.Payload.(*commands.DeclarativeManagement)
			if !ok {
				t.Fatalf("payload %T", c.cmd.Payload)
			}
			want, err := f.engine.Tokens(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if string(dm.Data) != string(want) {
				t.Fatalf("%s: Data = %s, want %s", id.ID, dm.Data, want)
			}
			var tr ddmproto.TokensResponse
			if err := json.Unmarshal(dm.Data, &tr); err != nil || tr.SyncTokens["DeclarationsToken"] == "" {
				t.Fatalf("%s: Data not a TokensResponse: %v %s", id.ID, err, dm.Data)
			}
		}
		for _, id := range ids {
			if !seen[id] {
				t.Errorf("%s got no command", id.ID)
			}
		}
		if len(f.pusher.batches) != 1 || len(f.pusher.batches[0]) != n {
			t.Fatalf("push batches = %d, want one batch of %d", len(f.pusher.batches), n)
		}
	})
	t.Run("DedupeSkipCompletesAndPushes", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		dev := ddmtest.Device(1)
		f.enq.skip = map[mdm.EnrollmentID]error{dev: fmt.Errorf("%w: pending", storage.ErrConflict)}
		f.assignFresh(t, dev, "com.example.dedupe")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		res := f.drain(t)
		if res.Deduped != 1 || res.Pushed != 1 || res.Queued != 0 {
			t.Fatalf("drain = %+v, want 1 deduped and pushed", res)
		}
		if len(f.pending(t)) != 0 {
			t.Fatal("change not completed")
		}
	})
	t.Run("DisabledEnrollmentSkipped", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		dev := ddmtest.Device(1)
		f.enq.skip = map[mdm.EnrollmentID]error{dev: fmt.Errorf("%w: %s", storage.ErrDisabled, dev.ID)}
		f.assignFresh(t, dev, "com.example.disabled")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		res := f.drain(t)
		if res.Skipped != 1 || res.Pushed != 0 {
			t.Fatalf("drain = %+v, want 1 skipped 0 pushed", res)
		}
		if len(f.pending(t)) != 0 {
			t.Fatal("change not completed")
		}
		if f.pusher.count() != 0 {
			t.Fatal("disabled enrollment was pushed")
		}
	})
	t.Run("EnqueueFailureRecordedAndRetried", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		dev := ddmtest.Device(1)
		f.assignFresh(t, dev, "com.example.retry")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		f.enq.err = errors.New("queue down")
		res := f.drain(t)
		if res.Failed != 1 || res.Pushed != 0 {
			t.Fatalf("drain = %+v, want 1 failed", res)
		}
		rows := f.pending(t)
		if len(rows) != 1 || rows[0].Attempts != 1 || rows[0].LastError == "" {
			t.Fatalf("rows = %+v, want one failed attempt", rows)
		}
		if want := f.clock.Now().Add(storage.NotNowBackoff(1)); !rows[0].NextAttemptAt.Equal(want) {
			t.Fatalf("next = %v, want %v", rows[0].NextAttemptAt, want)
		}
		// Not due yet: nothing happens.
		if res := f.drain(t); res != (ddm.DrainResult{}) {
			t.Fatalf("early drain = %+v", res)
		}
		f.enq.err = nil
		f.clock.Advance(storage.NotNowBackoff(1))
		if res := f.drain(t); res.Queued != 1 {
			t.Fatalf("retry drain = %+v, want 1 queued", res)
		}
		if len(f.pending(t)) != 0 {
			t.Fatal("change still pending after retry")
		}
	})
	t.Run("TokensFailureRecorded", func(t *testing.T) {
		f := newNotifierFixture(t, func(c *ddm.NotifierConfig) {
			c.Tokens = tokensFunc(func(context.Context, mdm.EnrollmentID) ([]byte, error) { return nil, errors.New("boom") })
		})
		dev := ddmtest.Device(1)
		f.assignFresh(t, dev, "com.example.tokens")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		if res := f.drain(t); res.Failed != 1 {
			t.Fatalf("drain = %+v", res)
		}
		if f.enq.count() != 0 {
			t.Fatal("enqueued despite tokens failure")
		}
	})
	t.Run("PushFailureRecorded", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		dev := ddmtest.Device(1)
		f.assignFresh(t, dev, "com.example.push")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		f.pusher.err = errors.New("apns down")
		if res := f.drain(t); res.Failed != 1 || res.Pushed != 0 {
			t.Fatalf("drain = %+v, want 1 failed", res)
		}
		rows := f.pending(t)
		if len(rows) != 1 || rows[0].Attempts != 1 {
			t.Fatalf("rows = %+v", rows)
		}
		// A per-target error also fails; an invalid token completes.
		f.pusher.err = nil
		f.pusher.results = map[mdm.EnrollmentID]push.Result{dev: {Err: errors.New("bad device")}}
		f.enq.skip = map[mdm.EnrollmentID]error{dev: fmt.Errorf("%w: pending", storage.ErrConflict)}
		f.clock.Advance(storage.NotNowBackoff(1))
		if res := f.drain(t); res.Failed != 1 {
			t.Fatalf("per-target drain = %+v", res)
		}
		if rows := f.pending(t); len(rows) != 1 || rows[0].Attempts != 2 {
			t.Fatalf("rows = %+v, want attempts 2", rows)
		}
		f.pusher.results = map[mdm.EnrollmentID]push.Result{dev: {Invalid: true, Err: errors.New("BadDeviceToken")}}
		f.clock.Advance(storage.NotNowBackoff(2))
		if res := f.drain(t); res.Pushed != 1 || res.Failed != 0 {
			t.Fatalf("invalid-token drain = %+v", res)
		}
		if len(f.pending(t)) != 0 {
			t.Fatal("change still pending after invalid token")
		}
	})
	t.Run("NoPusherStillCompletes", func(t *testing.T) {
		f := newNotifierFixture(t, func(c *ddm.NotifierConfig) { c.Pusher = nil })
		dev := ddmtest.Device(1)
		f.assignFresh(t, dev, "com.example.nopush")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		if res := f.drain(t); res.Queued != 1 || res.Pushed != 1 {
			t.Fatalf("drain = %+v", res)
		}
		if len(f.pending(t)) != 0 {
			t.Fatal("change still pending")
		}
	})
	t.Run("StoreFailuresSurface", func(t *testing.T) {
		for _, method := range []string{"PendingChanges", "CompleteChanges", "FailChanges"} {
			t.Run(method, func(t *testing.T) {
				var failing *ddmtest.Failing
				f := newNotifierFixture(t, func(c *ddm.NotifierConfig) {
					failing = &ddmtest.Failing{Store: c.Store.(ddm.Store), Fail: map[string]error{}}
					c.Store = failing
				})
				dev := ddmtest.Device(1)
				f.assignFresh(t, dev, "com.example.store")
				f.clock.Advance(ddm.DefaultNotifyWindow)
				if method == "FailChanges" {
					f.enq.err = errors.New("queue down")
				}
				failing.Fail[method] = errors.New("disk on fire")
				_, err := f.notifier.DrainOnce(ctx)
				if !errors.Is(err, ddm.ErrNotifier) || err == nil || !errors.Is(err, failing.Fail[method]) {
					t.Fatalf("err = %v, want ErrNotifier wrapping the store error", err)
				}
			})
		}
	})
	t.Run("PublishesDDMChanged", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		dev := ddmtest.Device(1)
		f.assignFresh(t, dev, "com.example.ev1")
		f.assignFresh(t, dev, "com.example.ev2")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		f.drain(t)
		f.evMu.Lock()
		defer f.evMu.Unlock()
		if len(f.events) != 1 {
			t.Fatalf("events = %d, want 1 per drained enrollment", len(f.events))
		}
		e := f.events[0]
		if e.Enrollment != dev || e.Actor != "ddm" {
			t.Fatalf("event = %+v", e)
		}
		rows, ok := e.Data.([]ddm.Change)
		if !ok || len(rows) < 2 {
			t.Fatalf("event data = %#v, want the change rows", e.Data)
		}
	})
	t.Run("DeleteNotifies", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		dev := ddmtest.Device(1)
		f.assignFresh(t, dev, "com.example.gone")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		f.drain(t)
		if err := f.engine.DeleteDeclaration(ctx, "com.example.gone"); err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(ddm.DefaultNotifyWindow)
		if res := f.drain(t); res.Queued != 1 {
			t.Fatalf("delete drain = %+v, want 1 queued", res)
		}
		if f.enq.count() != 2 {
			t.Fatalf("enqueues = %d, want 2", f.enq.count())
		}
	})
	t.Run("NoBus", func(t *testing.T) {
		f := newNotifierFixture(t, func(c *ddm.NotifierConfig) { c.Bus = nil })
		f.assignFresh(t, ddmtest.Device(1), "com.example.nobus")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		if res := f.drain(t); res.Queued != 1 {
			t.Fatalf("drain = %+v", res)
		}
	})
	t.Run("PublishErrorLogged", func(t *testing.T) {
		f := newNotifierFixture(t, nil)
		f.assignFresh(t, ddmtest.Device(1), "com.example.closedbus")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		if err := f.bus.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if res := f.drain(t); res.Queued != 1 {
			t.Fatalf("drain = %+v", res)
		}
		if len(f.pending(t)) != 0 {
			t.Fatal("a closed bus must not block completion")
		}
	})
	t.Run("PushFailureStoreErrorSurfaces", func(t *testing.T) {
		var failing *ddmtest.Failing
		f := newNotifierFixture(t, func(c *ddm.NotifierConfig) {
			failing = &ddmtest.Failing{Store: c.Store.(ddm.Store), Fail: map[string]error{}}
			c.Store = failing
		})
		f.assignFresh(t, ddmtest.Device(1), "com.example.pushstore")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		f.pusher.err = errors.New("apns down")
		failing.Fail["FailChanges"] = errors.New("disk on fire")
		if _, err := f.notifier.DrainOnce(ctx); !errors.Is(err, ddm.ErrNotifier) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("KickWakesRunImmediately", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newNotifierFixture(t, func(c *ddm.NotifierConfig) {
				c.Clock = clock.Real{}
				c.Poll = time.Hour
				c.Window = 0
			})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- f.notifier.Run(ctx) }()
			defer func() {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Errorf("Run = %v", err)
				}
			}()
			synctest.Wait()
			if f.enq.count() != 0 {
				t.Fatal("unexpected enqueue")
			}
			dev := ddmtest.Device(1)
			if err := f.engine.Touch(ctx, []mdm.EnrollmentID{dev}, ddm.ReasonTouch); err != nil {
				t.Fatal(err)
			}
			// Window 0 falls back to the default, so wait for the change to age.
			time.Sleep(ddm.DefaultNotifyWindow + time.Millisecond)
			f.notifier.Kick()
			f.notifier.Kick() // a second kick never blocks
			synctest.Wait()
			if f.enq.count() != 1 {
				t.Fatalf("enqueues after kick = %d, want 1 (no poll for an hour)", f.enq.count())
			}
		})
	})
	t.Run("RunStopsOnContext", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newNotifierFixture(t, func(c *ddm.NotifierConfig) {
				c.Clock = clock.Real{}
				c.Poll = 50 * time.Millisecond
			})
			var failing *ddmtest.Failing
			f2 := newNotifierFixture(t, func(c *ddm.NotifierConfig) {
				c.Clock = clock.Real{}
				c.Poll = 50 * time.Millisecond
				failing = &ddmtest.Failing{Store: c.Store.(ddm.Store), Fail: map[string]error{"PendingChanges": errors.New("down")}}
				c.Store = failing
			})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 2)
			go func() { done <- f.notifier.Run(ctx) }()
			go func() { done <- f2.notifier.Run(ctx) }() // drain errors are logged, not fatal
			defer func() {
				cancel()
				for range 2 {
					if err := <-done; !errors.Is(err, context.Canceled) {
						t.Errorf("Run = %v", err)
					}
				}
			}()
			dev := ddmtest.Device(1)
			if err := f.engine.Touch(ctx, []mdm.EnrollmentID{dev}, ddm.ReasonTouch); err != nil {
				t.Fatal(err)
			}
			time.Sleep(ddm.DefaultNotifyWindow + 100*time.Millisecond)
			synctest.Wait()
			if f.enq.count() != 1 {
				t.Fatalf("poll did not drain: enqueues = %d", f.enq.count())
			}
		})
	})
}

type tokensFunc func(context.Context, mdm.EnrollmentID) ([]byte, error)

func (fn tokensFunc) Tokens(ctx context.Context, id mdm.EnrollmentID) ([]byte, error) {
	return fn(ctx, id)
}

// Suppression is the consumer's decision, not this package's. The default
// keeps a second DeclarativeManagement from queueing while one is pending,
// but a deployment that would rather see every change produce a command can
// say so.
func TestNotifierDedupeIsConfigurable(t *testing.T) {
	t.Parallel()

	t.Run("DefaultKeyIsSent", func(t *testing.T) {
		t.Parallel()
		f := newNotifierFixture(t, nil)
		f.assignFresh(t, ddmtest.Device(1), "com.example.a")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		f.drain(t)
		if len(f.enq.calls) == 0 || f.enq.calls[0].opts.DedupeKey != ddm.DefaultDedupeKey {
			t.Fatalf("calls = %+v", f.enq.calls)
		}
	})

	t.Run("EmptyKeyTurnsSuppressionOff", func(t *testing.T) {
		t.Parallel()
		off := ""
		f := newNotifierFixture(t, func(c *ddm.NotifierConfig) { c.DedupeKey = &off })
		f.assignFresh(t, ddmtest.Device(1), "com.example.a")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		if res := f.drain(t); res.Queued != 1 {
			t.Fatalf("first drain = %+v", res)
		}
		for _, c := range f.enq.calls {
			if c.opts.DedupeKey != "" {
				t.Fatalf("dedupe key = %q, want none", c.opts.DedupeKey)
			}
		}
	})

	t.Run("ACustomKeyIsUsedVerbatim", func(t *testing.T) {
		t.Parallel()
		key := "declarative-sync"
		f := newNotifierFixture(t, func(c *ddm.NotifierConfig) { c.DedupeKey = &key })
		f.assignFresh(t, ddmtest.Device(1), "com.example.a")
		f.clock.Advance(ddm.DefaultNotifyWindow)
		f.drain(t)
		if len(f.enq.calls) == 0 || f.enq.calls[0].opts.DedupeKey != key {
			t.Fatalf("calls = %+v", f.enq.calls)
		}
	})
}

// A suppressed command used to leave no trace: Run discarded the result, so
// nothing anywhere recorded that a device had not been woken.
func TestNotifierReportsEveryDrain(t *testing.T) {
	t.Parallel()
	var got []ddm.DrainResult
	var mu sync.Mutex
	f := newNotifierFixture(t, func(c *ddm.NotifierConfig) {
		c.OnDrain = func(_ context.Context, res ddm.DrainResult) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, res)
		}
	})
	f.assignFresh(t, ddmtest.Device(1), "com.example.a")
	f.clock.Advance(ddm.DefaultNotifyWindow)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.notifier.Run(ctx) }()
	waitForDrain(t, &mu, &got)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	var queued int
	for _, r := range got {
		queued += r.Queued
	}
	if queued != 1 {
		t.Fatalf("reported drains = %+v, want one queued command", got)
	}
}

func waitForDrain(t *testing.T, mu *sync.Mutex, got *[]ddm.DrainResult) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, r := range *got {
			if r.Queued > 0 {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no drain was reported")
}
