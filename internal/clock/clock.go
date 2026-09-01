// Package clock abstracts time so that protocol code (signature time-skew
// checks, NotNow backoff, token freshness, push retry timers) can be tested
// deterministically. Production code uses Real; tests use Fake.
package clock

import (
	"sync"
	"time"
)

// Clock is the subset of the time package that the library depends on.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Since returns the elapsed time since t.
	Since(t time.Time) time.Duration
	// After waits for the duration to elapse and then sends the current time
	// on the returned channel.
	After(d time.Duration) <-chan time.Time
}

// Real is a Clock backed by the time package.
type Real struct{}

// Now implements Clock.
func (Real) Now() time.Time { return time.Now() }

// Since implements Clock.
func (Real) Since(t time.Time) time.Duration { return time.Since(t) }

// After implements Clock.
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a manually advanced Clock for tests. It is safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

// NewFake returns a Fake clock set to now.
func NewFake(now time.Time) *Fake {
	return &Fake{now: now}
}

// Now implements Clock.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Since implements Clock.
func (f *Fake) Since(t time.Time) time.Duration {
	return f.Now().Sub(t)
}

// After implements Clock. The returned channel fires once Advance or Set
// moves the clock to or past now+d. A non-positive duration fires
// immediately.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	at := f.now.Add(d)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: at, ch: ch})
	return ch
}

// Advance moves the clock forward by d and fires any waiters that are due.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.fireLocked()
	f.mu.Unlock()
}

// Set moves the clock to t (forward or backward) and fires any waiters that
// are due.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	f.now = t
	f.fireLocked()
	f.mu.Unlock()
}

// Pending reports how many After waiters have not fired yet.
func (f *Fake) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

func (f *Fake) fireLocked() {
	remaining := f.waiters[:0]
	for _, w := range f.waiters {
		if !w.at.After(f.now) {
			w.ch <- f.now
			continue
		}
		remaining = append(remaining, w)
	}
	f.waiters = remaining
}
