package statestore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/ratelimit"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
	"github.com/deploymenttheory/go-apple-dm/state"
)

func sqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "state.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func TestSQLiteSharedState(t *testing.T) { exercise(t, sqliteDB(t), sqlite.Dialect) }

func exercise(t *testing.T, db *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	ctx := t.Context()
	a, err := statestore.Open(ctx, db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	b, err := statestore.Open(ctx, db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("test/%d/", time.Now().UnixNano())
	if err := a.Update(ctx, []string{key}, func(tx state.Tx) error {
		if time.Since(tx.Now()) > time.Second || tx.Now().After(time.Now().Add(time.Second)) {
			t.Fatal("database time", tx.Now())
		}
		for _, suffix := range []string{"a", "a%", "a_", "b"} {
			if err := tx.Put(ctx, state.Record{Key: key + suffix, Value: []byte(suffix)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := b.List(ctx, key+"a", key+"a", 2)
	if err != nil || len(rows) != 2 || rows[0].Key != key+"a%" || rows[1].Key != key+"a_" {
		t.Fatal(rows, err)
	}
	boom := errors.New("rollback")
	if err := a.Update(ctx, []string{key}, func(tx state.Tx) error {
		_ = tx.Delete(ctx, key+"a")
		_ = tx.Put(ctx, state.Record{Key: key + "new"})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, key+"a"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, key+"new"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	if err := a.Update(ctx, []string{key}, func(tx state.Tx) error {
		return tx.Put(ctx, state.Record{Key: key + "expired", ExpiresAt: tx.Now().Add(-time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Prune(ctx, 100); err != nil || n < 1 {
		t.Fatal(n, err)
	}
	if _, err := a.Get(ctx, key+"expired"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	// Separate limiter instances see one burst, regardless of concurrent arrival.
	limits := []*ratelimit.Limiter{{Store: a, Namespace: key}, {Store: b, Namespace: key}}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			dec, err := limits[i%2].Check(ctx, []ratelimit.Bucket{{Key: "global", Interval: time.Hour, Burst: 7}, {Key: "peer", Interval: time.Hour, Burst: 20}})
			if err != nil {
				t.Error(err)
			}
			if dec.Allowed {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 7 {
		t.Fatalf("shared burst admitted %d", accepted.Load())
	}
	// Single-use token consumption also shares a transaction across instances.
	stores := []*accountdriven.StateTokenStore{{Backend: a}, {Backend: b}}
	now := time.Now()
	token := key + "code"
	if err := stores[0].Put(ctx, token, accountdriven.Record{Kind: accountdriven.KindCode, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	accepted.Store(0)
	for i := range 20 {
		wg.Go(func() {
			err := stores[i%2].Exchange(ctx, token, now, func(accountdriven.Record) error { return nil }, nil)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, accountdriven.ErrTokenUsed) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("single-use token accepted", accepted.Load())
	}
	// Rejecting metadata leaves the token available for its rightful client.
	if err := stores[0].Put(ctx, token+"2", accountdriven.Record{Kind: accountdriven.KindCode, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := stores[0].Exchange(ctx, token+"2", now, func(accountdriven.Record) error { return boom }, nil); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := stores[1].MarkUsed(ctx, token+"2", now); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidAndDatabaseFailures(t *testing.T) {
	ctx := t.Context()
	db := sqliteDB(t)
	if _, err := statestore.Open(ctx, nil, sqlite.Dialect); err == nil {
		t.Fatal("nil db")
	}
	if _, err := statestore.Open(ctx, db, sqlcommon.Dialect{Name: "oracle", Upsert: sqlcommon.UpsertOnConflict}); err == nil {
		t.Fatal("unknown dialect")
	}
	s, err := statestore.Open(ctx, db, sqlite.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range [][]string{nil, {""}} {
		if err := s.Update(ctx, keys, func(state.Tx) error { return nil }); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := s.Update(ctx, []string{"a"}, nil); !errors.Is(err, state.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ""); !errors.Is(err, state.ErrInvalid) {
		t.Fatal(err)
	}
	for _, n := range []int{0, 10001} {
		if _, err := s.List(ctx, "", "", n); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := s.Prune(ctx, n); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := s.Update(ctx, []string{"a"}, func(tx state.Tx) error {
		if err := tx.Put(ctx, state.Record{}); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		if err := tx.Delete(ctx, ""); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Update(cancelled, []string{"a"}, func(state.Tx) error { return nil }); err == nil {
		t.Fatal("cancelled update")
	}
	if n, err := s.Prune(ctx, 10); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := statestore.Open(ctx, db, sqlite.Dialect); err == nil {
		t.Fatal("closed open")
	}
	if _, err := s.Get(ctx, "a"); err == nil {
		t.Fatal("closed read")
	}
	if _, err := s.List(ctx, "", "", 1); err == nil {
		t.Fatal("closed list")
	}
	if _, err := s.Prune(ctx, 10); err == nil {
		t.Fatal("closed prune")
	}
	if err := s.Update(ctx, []string{"a"}, func(state.Tx) error { return nil }); err == nil {
		t.Fatal("closed update")
	}
}
