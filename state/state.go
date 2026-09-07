package state

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("state: record not found")
	ErrInvalid  = errors.New("state: invalid key or transaction")
)

// Record is opaque state. A zero ExpiresAt means retain until explicitly deleted.
type Record struct {
	Key       string
	Value     []byte
	ExpiresAt time.Time
}

// Reader reads records, including expired ones; protocol callers validate expiry.
type Reader interface {
	Get(context.Context, string) (Record, error)
	// List returns up to limit records in key order, strictly after after.
	List(ctx context.Context, prefix, after string, limit int) ([]Record, error)
}

// Tx is valid only during Update. Now is authoritative store time sampled after
// acquiring locks. A callback must not re-enter the Store or retain its Tx.
type Tx interface {
	Reader
	Now() time.Time
	Put(context.Context, Record) error
	Delete(context.Context, string) error
}

// Store commits all writes in a callback or none. Updates sharing any lock key
// serialize; callers must use the same lock keys for every writer of a record.
// Keys may designate a whole logical group (for example an issuer's CRL).
type Store interface {
	Reader
	Update(ctx context.Context, keys []string, fn func(Tx) error) error
	// Prune deletes at most limit expired records. It never deletes immortal data.
	Prune(ctx context.Context, limit int) (int, error)
}

// ValidKey permits printable ASCII keys of at most 255 bytes across all backends.
func ValidKey(k string) bool {
	if len(k) == 0 || len(k) > 255 {
		return false
	}
	for _, c := range []byte(k) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

// Memory is a serializable store. Now may be set before use for deterministic tests.
type Memory struct {
	mu      sync.Mutex
	records map[string]Record
	Now     func() time.Time
}

// NewMemory returns an empty store.
func NewMemory() *Memory    { return &Memory{records: make(map[string]Record)} }
func clone(r Record) Record { r.Value = slices.Clone(r.Value); return r }
func (m *Memory) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

// Get implements Reader.
func (m *Memory) Get(ctx context.Context, key string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return (&memoryTx{records: m.records}).Get(ctx, key)
}

// List implements Reader.
func (m *Memory) List(ctx context.Context, prefix, after string, limit int) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return (&memoryTx{records: m.records}).List(ctx, prefix, after, limit)
}

// Update implements Store. A private write set makes callback failures atomic.
func (m *Memory) Update(ctx context.Context, keys []string, fn func(Tx) error) error {
	if len(keys) == 0 || fn == nil {
		return ErrInvalid
	}
	for _, k := range keys {
		if !ValidKey(k) {
			return ErrInvalid
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	t := &memoryTx{records: m.records, writes: map[string]*Record{}, now: m.now()}
	if err := fn(t); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for k, r := range t.writes {
		if r == nil {
			delete(m.records, k)
		} else {
			m.records[k] = clone(*r)
		}
	}
	return nil
}

// Prune implements Store.
func (m *Memory) Prune(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 10000 {
		return 0, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	now, n := m.now(), 0
	for k, r := range m.records {
		if !r.ExpiresAt.IsZero() && !now.Before(r.ExpiresAt) {
			delete(m.records, k)
			n++
			if n == limit {
				break
			}
		}
	}
	return n, nil
}

type memoryTx struct {
	records map[string]Record
	writes  map[string]*Record
	now     time.Time
}

func (t *memoryTx) Now() time.Time { return t.now }
func (t *memoryTx) Get(ctx context.Context, k string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if !ValidKey(k) {
		return Record{}, ErrInvalid
	}
	if r, ok := t.writes[k]; ok {
		if r == nil {
			return Record{}, ErrNotFound
		}
		return clone(*r), nil
	}
	r, ok := t.records[k]
	if !ok {
		return Record{}, ErrNotFound
	}
	return clone(r), nil
}
func (t *memoryTx) List(ctx context.Context, prefix, after string, limit int) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 10000 {
		return nil, ErrInvalid
	}
	keys := map[string]bool{}
	for k := range t.records {
		keys[k] = true
	}
	for k, r := range t.writes {
		keys[k] = r != nil
	}
	var sorted []string
	for k, live := range keys {
		if live && strings.HasPrefix(k, prefix) && k > after {
			sorted = append(sorted, k)
		}
	}
	slices.Sort(sorted)
	var out []Record
	for _, k := range sorted[:min(limit, len(sorted))] {
		r, err := t.Get(ctx, k)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (t *memoryTx) Put(ctx context.Context, r Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidKey(r.Key) {
		return ErrInvalid
	}
	r = clone(r)
	t.writes[r.Key] = &r
	return nil
}
func (t *memoryTx) Delete(ctx context.Context, k string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidKey(k) {
		return ErrInvalid
	}
	t.writes[k] = nil
	return nil
}
