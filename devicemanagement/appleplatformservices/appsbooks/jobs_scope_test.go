package appsbooks_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

// licensingScopedReader models the same caller-owned scope used by sealed SQL
// contracts. The underlying memory backend enforces the actual final key limit.
type licensingScopedReader struct {
	state.Reader
	prefix string
}

// Get restores the logical record key after reading its scoped physical record.
func (s licensingScopedReader) Get(ctx context.Context, key string) (state.Record, error) {
	r, err := s.Reader.Get(ctx, s.prefix+key)
	r.Key = strings.TrimPrefix(r.Key, s.prefix)
	return r, err
}

// List preserves caller cursors while enumerating only this test's scope.
func (s licensingScopedReader) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	records, err := s.Reader.List(ctx, s.prefix+prefix, s.prefix+after, limit)
	for i := range records {
		records[i].Key = strings.TrimPrefix(records[i].Key, s.prefix)
	}
	return records, err
}

type licensingScopedStore struct {
	licensingScopedReader
	store state.Store
}

// Update scopes every lock and retains the underlying backend's atomicity.
func (s licensingScopedStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	scoped := make([]string, len(keys))
	for i, key := range keys {
		scoped[i] = s.prefix + key
	}
	return s.store.Update(ctx, scoped, func(tx state.Tx) error {
		return fn(licensingScopedTx{licensingScopedReader: licensingScopedReader{Reader: tx, prefix: s.prefix}, tx: tx})
	})
}

// Prune delegates to the owned memory backend; licensing records have no TTL.
func (s licensingScopedStore) Prune(ctx context.Context, limit int) (int, error) {
	return s.store.Prune(ctx, limit)
}

type licensingScopedTx struct {
	licensingScopedReader
	tx state.Tx
}

// Now retains the backend's authoritative transaction time.
func (s licensingScopedTx) Now() time.Time { return s.tx.Now() }

// Put validates and writes the final physical key through the underlying store.
func (s licensingScopedTx) Put(ctx context.Context, r state.Record) error {
	r.Key = s.prefix + r.Key
	return s.tx.Put(ctx, r)
}

// Delete removes only this scope's addressed physical record.
func (s licensingScopedTx) Delete(ctx context.Context, key string) error {
	return s.tx.Delete(ctx, s.prefix+key)
}

// TestLicensingScopedMemory reproduces sealed SQL's 35-byte caller namespace in
// fast memory contracts, including callback/event indexes and restart recovery.
func TestLicensingScopedMemory(t *testing.T) {
	storagetest.RunAppsBooksJobs(t, func(*testing.T) (state.Store, state.Store) {
		raw := state.NewMemory()
		scope := licensingScopedStore{licensingScopedReader: licensingScopedReader{Reader: raw, prefix: strings.Repeat("s", 35)}, store: raw}
		return scope, scope
	})
}
