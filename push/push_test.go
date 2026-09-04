package push_test

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/push"
	"github.com/deploymenttheory/go-apple-dm/push/pushtest"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/storage/inmem"
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
