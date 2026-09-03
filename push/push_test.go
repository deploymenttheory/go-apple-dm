package push_test

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/push"
	"github.com/deploymenttheory/go-apple-dm/push/pushtest"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/storage/storagetest"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func dev(n string) mdm.EnrollmentID { return mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: n} }

func enrolledStore(t *testing.T, ids ...string) *inmem.Store {
	t.Helper()
	s := inmem.New()
	ctx := context.Background()
	for i, id := range ids {
		if err := s.UpsertAuthenticate(ctx, dev(id), &checkin.Authenticate{Topic: "t"}, nil, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreTokenUpdate(ctx, dev(id), mdm.Push{Topic: "t", Token: []byte{byte(i + 1)}, Magic: "m-" + id}, nil, nil, t0); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestNotifierPublishesInvalidToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := enrolledStore(t, "A", "B")
	fake := &pushtest.Fake{Results: map[mdm.EnrollmentID]push.Result{dev("B"): {Outcome: push.OutcomeInvalidToken, Status: 410, Err: push.ErrInvalidToken}}}
	bus := event.New()
	var got []event.Event
	bus.Subscribe(event.PushTokenInvalid, func(_ context.Context, e event.Event) error { got = append(got, e); return nil })
	n := &push.Notifier{Store: store, Pusher: fake, Bus: bus, Clock: clock.NewFake(t0)}
	res, err := n.Notify(ctx, []mdm.EnrollmentID{dev("A"), dev("B"), dev("missing")})
	if err != nil {
		t.Fatal(err)
	}
	if !res[dev("A")].Sent() || !res[dev("B")].TokenInvalid() || !errors.Is(res[dev("missing")].Err, storage.ErrNotFound) {
		t.Fatalf("results %+v", res)
	}
	if len(got) != 1 || got[0].Enrollment != dev("B") || got[0].Actor != "apns" || !got[0].At.Equal(t0) {
		t.Fatalf("events %+v", got)
	}
	if len(fake.Pushed()) != 2 || fake.Pushed()[0].Push.Magic != "m-A" {
		t.Fatalf("pushed %+v", fake.Pushed())
	}
	// Nothing to push.
	res, err = n.Notify(ctx, []mdm.EnrollmentID{dev("missing")})
	if err != nil || len(res) != 1 || len(fake.Calls) != 1 {
		t.Fatalf("nothing to push: %+v %v", res, err)
	}
	// Pusher batch failure and storage failure propagate.
	fake.Err = pushtest.ErrScripted
	if _, err := n.Notify(ctx, []mdm.EnrollmentID{dev("A")}); !errors.Is(err, pushtest.ErrScripted) {
		t.Fatalf("batch failure: %v", err)
	}
	failing := &storagetest.Failing{Store: store, Fail: map[string]error{"PushInfo": errors.New("db")}}
	if _, err := (&push.Notifier{Store: failing, Pusher: fake}).Notify(ctx, []mdm.EnrollmentID{dev("A")}); err == nil {
		t.Fatal("storage failure should propagate")
	}
	if _, err := (&push.Notifier{}).Notify(ctx, nil); err == nil {
		t.Fatal("missing config")
	}
	// Without a bus and clock, invalid tokens are still reported.
	fake.Err = nil
	res, _ = (&push.Notifier{Store: store, Pusher: fake}).Notify(ctx, []mdm.EnrollmentID{dev("B")})
	if !res[dev("B")].TokenInvalid() {
		t.Fatal("invalid without bus")
	}
}

func TestCoalesce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := &pushtest.Fake{}
	fc := clock.NewFake(t0)
	c := push.Coalesce(fake, time.Minute, fc)
	targets := []push.Target{{ID: dev("A"), Push: mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}}}
	if res, err := c.Push(ctx, targets); err != nil || !res[dev("A")].Sent() {
		t.Fatalf("first push %+v %v", res, err)
	}
	fc.Advance(30 * time.Second)
	if res, _ := c.Push(ctx, targets); !errors.Is(res[dev("A")].Err, push.ErrCoalesced) || len(fake.Calls) != 1 {
		t.Fatalf("second push inside window %+v calls=%d", res, len(fake.Calls))
	}
	fc.Advance(31 * time.Second)
	if res, _ := c.Push(ctx, targets); !res[dev("A")].Sent() || len(fake.Calls) != 2 {
		t.Fatalf("push after window %+v", res)
	}
	// Errors from the inner pusher pass through; default clock works.
	fake.Err = pushtest.ErrScripted
	fc.Advance(time.Hour)
	if _, err := c.Push(ctx, targets); !errors.Is(err, pushtest.ErrScripted) {
		t.Fatalf("inner error: %v", err)
	}
	real := push.Coalesce(&pushtest.Fake{}, time.Minute, nil)
	if res, err := real.Push(ctx, targets); err != nil || !res[dev("A")].Sent() {
		t.Fatal("real clock")
	}
}

func TestStaticCertStore(t *testing.T) {
	t.Parallel()
	s := push.StaticCertStore{"t": {}}
	if _, err := s.PushCertificate(context.Background(), "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PushCertificate(context.Background(), "nope"); !errors.Is(err, push.ErrNoCertificate) {
		t.Fatal("missing topic")
	}
	_ = tls.Certificate{}
}

// A refused request and a dead token are different operational facts and so
// are different events. One says retire this enrollment; the other says go
// and look at the push certificate, because every device on the topic is
// about to answer the same way.
func TestNotifierPublishesRejectionSeparately(t *testing.T) {
	t.Parallel()
	store := enrolledStore(t, "A", "B")
	fake := &pushtest.Fake{Results: map[mdm.EnrollmentID]push.Result{
		dev("A"): {Outcome: push.OutcomeRejected, Status: 400, Reason: "DeviceTokenNotForTopic", Err: push.ErrRejected},
		dev("B"): {Outcome: push.OutcomeInvalidToken, Status: 410, Reason: "Unregistered", Err: push.ErrInvalidToken},
	}}
	bus := event.New()
	seen := map[event.Type][]mdm.EnrollmentID{}
	for _, tp := range []event.Type{event.PushRejected, event.PushTokenInvalid} {
		bus.Subscribe(tp, func(_ context.Context, e event.Event) error {
			seen[e.Type] = append(seen[e.Type], e.Enrollment)
			return nil
		})
	}
	n := &push.Notifier{Store: store, Pusher: fake, Bus: bus, Clock: clock.NewFake(t0)}
	res, err := n.Notify(context.Background(), []mdm.EnrollmentID{dev("A"), dev("B")})
	if err != nil {
		t.Fatal(err)
	}
	// The rejected enrollment is emphatically not reported as a dead token.
	if res[dev("A")].TokenInvalid() || res[dev("A")].Outcome != push.OutcomeRejected {
		t.Fatalf("rejected result = %+v", res[dev("A")])
	}
	if got := seen[event.PushRejected]; len(got) != 1 || got[0] != dev("A") {
		t.Fatalf("push-rejected events = %+v", got)
	}
	if got := seen[event.PushTokenInvalid]; len(got) != 1 || got[0] != dev("B") {
		t.Fatalf("push-token-invalid events = %+v", got)
	}
}

// Every outcome a Pusher can report is one the projection registry knows
// how to render, and the list is closed.
func TestOutcomesAreClosed(t *testing.T) {
	t.Parallel()
	seen := map[push.Outcome]bool{}
	for _, o := range push.Outcomes {
		if o == "" {
			t.Error("the zero value is not a valid outcome")
		}
		if seen[o] {
			t.Errorf("duplicate outcome %q", o)
		}
		seen[o] = true
	}
	if len(push.Outcomes) != 6 {
		t.Fatalf("Outcomes = %d entries", len(push.Outcomes))
	}
	// Sent and TokenInvalid are derived from Outcome, so they cannot drift
	// from it the way two independent booleans could.
	for _, o := range push.Outcomes {
		r := push.Result{Outcome: o}
		if r.Sent() != (o == push.OutcomeSent) || r.TokenInvalid() != (o == push.OutcomeInvalidToken) {
			t.Errorf("%q: Sent=%v TokenInvalid=%v", o, r.Sent(), r.TokenInvalid())
		}
	}
}
