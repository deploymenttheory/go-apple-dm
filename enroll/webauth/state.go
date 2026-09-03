package webauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/clock"
)

// Bound is what the caller ties to the state: the device that opened the
// web view. The ADE handler fills Serial and UDID from MachineInfo; the
// account-driven handler may only know the user identifier and puts it in
// LoginHint and Extra.
type Bound struct {
	Serial string
	UDID   string
	// LoginHint, when set, is sent to the provider as login_hint.
	LoginHint string
	// Extra carries any other values the caller wants back at Complete.
	Extra map[string]string
}

// State is one pending authorization, stored under its state parameter.
type State struct {
	Bound     Bound
	Verifier  string
	Nonce     string
	ExpiresAt time.Time
}

// StateStore persists pending authorizations. Implementations must make
// Take consume the entry so a state parameter is single-use, and may
// drop entries past ExpiresAt on their own.
type StateStore interface {
	// Put stores st under key. An existing key is an error.
	Put(ctx context.Context, key string, st State) error
	// Take returns and removes the entry, or ErrStateNotFound.
	Take(ctx context.Context, key string) (State, error)
}

// Store errors.
var (
	ErrStateNotFound = errors.New("webauth: state not found")
	ErrStateExists   = errors.New("webauth: state already stored")
	ErrStoreFull     = errors.New("webauth: state store full")
	ErrStateKey      = errors.New("webauth: empty state key")
)

// MemoryStore keeps states in memory with a periodic sweep of expired
// entries. It is safe for concurrent use.
type MemoryStore struct {
	mu        sync.Mutex
	clock     clock.Clock
	max       int
	sweepEach time.Duration
	lastSweep time.Time
	states    map[string]State
}

// MemoryOption configures NewMemoryStore.
type MemoryOption func(*MemoryStore)

// WithMemoryClock sets the clock used for expiry (tests).
func WithMemoryClock(c clock.Clock) MemoryOption { return func(m *MemoryStore) { m.clock = c } }

// WithMemoryMaxEntries caps live entries; Put fails with ErrStoreFull once
// the cap is reached after a sweep. The default is 10000.
func WithMemoryMaxEntries(n int) MemoryOption { return func(m *MemoryStore) { m.max = n } }

// WithMemorySweepInterval sets how often Put sweeps expired entries; the
// default is one minute.
func WithMemorySweepInterval(d time.Duration) MemoryOption {
	return func(m *MemoryStore) { m.sweepEach = d }
}

// NewMemoryStore returns an empty store.
func NewMemoryStore(opts ...MemoryOption) *MemoryStore {
	m := &MemoryStore{clock: clock.Real{}, max: 10000, sweepEach: time.Minute, states: map[string]State{}}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Put implements StateStore.
func (m *MemoryStore) Put(_ context.Context, key string, st State) error {
	if key == "" {
		return ErrStateKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	if now.Sub(m.lastSweep) >= m.sweepEach {
		m.sweepLocked(now)
	}
	if _, ok := m.states[key]; ok {
		return ErrStateExists
	}
	if m.max > 0 && len(m.states) >= m.max {
		m.sweepLocked(now)
		if len(m.states) >= m.max {
			return fmt.Errorf("%w: %d entries", ErrStoreFull, len(m.states))
		}
	}
	m.states[key] = st
	return nil
}

// Take implements StateStore.
func (m *MemoryStore) Take(_ context.Context, key string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.states[key]
	if !ok {
		return State{}, ErrStateNotFound
	}
	delete(m.states, key)
	return st, nil
}

// Sweep removes expired entries now and reports how many it removed.
func (m *MemoryStore) Sweep() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sweepLocked(m.clock.Now())
}

// Len reports the live entries, expired ones included until swept.
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.states)
}

func (m *MemoryStore) sweepLocked(now time.Time) int {
	m.lastSweep = now
	n := 0
	for k, st := range m.states {
		if !now.Before(st.ExpiresAt) {
			delete(m.states, k)
			n++
		}
	}
	return n
}
