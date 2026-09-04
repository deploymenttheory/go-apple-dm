package clock_test

import (
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
)

func TestRealClock(t *testing.T) {
	t.Parallel()
	var c clock.Clock = clock.Real{}
	before := time.Now()
	now := c.Now()
	if now.Before(before) {
		t.Fatalf("Now went backwards: %v < %v", now, before)
	}
	if c.Since(before) < 0 {
		t.Fatal("Since returned negative duration")
	}
	select {
	case <-c.After(time.Millisecond):
	case <-time.After(time.Second):
		t.Fatal("After did not fire")
	}
}

func TestFakeNowSinceAdvanceSet(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)
	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now = %v, want %v", got, start)
	}
	f.Advance(90 * time.Second)
	if got := f.Since(start); got != 90*time.Second {
		t.Fatalf("Since = %v, want 90s", got)
	}
	back := start.Add(-time.Hour)
	f.Set(back)
	if got := f.Now(); !got.Equal(back) {
		t.Fatalf("Set: Now = %v, want %v", got, back)
	}
}

func TestFakeAfterFiresWhenDue(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)
	ch := f.After(5 * time.Minute)
	if f.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1", f.Pending())
	}
	f.Advance(4 * time.Minute)
	select {
	case <-ch:
		t.Fatal("fired early")
	default:
	}
	f.Advance(time.Minute)
	select {
	case got := <-ch:
		if !got.Equal(start.Add(5 * time.Minute)) {
			t.Fatalf("fired with %v", got)
		}
	default:
		t.Fatal("did not fire when due")
	}
	if f.Pending() != 0 {
		t.Fatalf("Pending = %d after firing, want 0", f.Pending())
	}
}

func TestFakeAfterNonPositiveFiresImmediately(t *testing.T) {
	t.Parallel()
	f := clock.NewFake(time.Unix(0, 0))
	for _, d := range []time.Duration{0, -time.Second} {
		select {
		case <-f.After(d):
		default:
			t.Fatalf("After(%v) did not fire immediately", d)
		}
	}
	if f.Pending() != 0 {
		t.Fatal("non-positive After registered a waiter")
	}
}

func TestFakeSetFiresDueWaitersAndKeepsOthers(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)
	soon := f.After(time.Minute)
	later := f.After(time.Hour)
	f.Set(start.Add(30 * time.Minute))
	select {
	case <-soon:
	default:
		t.Fatal("soon waiter did not fire on Set")
	}
	select {
	case <-later:
		t.Fatal("later waiter fired too early")
	default:
	}
	if f.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1", f.Pending())
	}
}
