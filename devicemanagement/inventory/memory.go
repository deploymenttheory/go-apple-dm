package inventory

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Memory is an isolated, transactional inventory backend for embedding and tests.
type Memory struct {
	mu   sync.RWMutex
	data map[string]json.RawMessage
}

// NewMemory creates an empty store.
func NewMemory() *Memory { return &Memory{data: make(map[string]json.RawMessage)} }

// Get returns a defensive copy of a stored document.
func (m *Memory) Get(ctx context.Context, key string) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return (&memoryTx{base: m.data}).Get(ctx, key)
}

// Scan returns a bounded page ordered by key.
func (m *Memory) Scan(ctx context.Context, prefix, after string, limit int) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return (&memoryTx{base: m.data}).Scan(ctx, prefix, after, limit)
}

// Update commits a copy-on-write transaction only if fn and its context succeed.
func (m *Memory) Update(ctx context.Context, fn func(Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &memoryTx{base: m.data, writes: make(map[string]json.RawMessage)}
	if err := fn(t); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for k, v := range t.writes {
		if v == nil {
			delete(m.data, k)
		} else {
			m.data[k] = v
		}
	}
	return nil
}

// memoryTx overlays a transaction's writes on an immutable locked base.
type memoryTx struct{ base, writes map[string]json.RawMessage }

// Get checks transaction-local writes before the base.
func (t *memoryTx) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, ok := t.writes[key]
	if !ok {
		v, ok = t.base[key]
	}
	if !ok || v == nil {
		return nil, ErrNotFound
	}
	return append(json.RawMessage(nil), v...), nil
}

// Scan merges the two key spaces without exposing mutable buffers.
func (t *memoryTx) Scan(ctx context.Context, prefix, after string, limit int) ([]Entry, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	keys := map[string]bool{}
	for k := range t.base {
		if strings.HasPrefix(k, prefix) && k > after {
			keys[k] = true
		}
	}
	for k, v := range t.writes {
		if strings.HasPrefix(k, prefix) && k > after {
			if v == nil {
				delete(keys, k)
			} else {
				keys[k] = true
			}
		}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	out := make([]Entry, 0, len(ordered))
	for _, k := range ordered {
		v, e := t.Get(ctx, k)
		if e != nil {
			return nil, e
		}
		out = append(out, Entry{k, v})
	}
	return out, nil
}

// Put stages a validated document.
func (t *memoryTx) Put(ctx context.Context, k string, v json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidEntry(k, v) {
		return ErrInvalid
	}
	t.writes[k] = append(json.RawMessage(nil), v...)
	return nil
}

// Delete stages an idempotent removal.
func (t *memoryTx) Delete(ctx context.Context, k string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.writes[k] = nil
	return nil
}

// ValidEntry enforces backend-independent key and document limits.
func ValidEntry(k string, v json.RawMessage) bool {
	if len(k) == 0 || len(k) > 512 || len(v) > 16<<20 || !json.Valid(v) {
		return false
	}
	for _, c := range k {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}
