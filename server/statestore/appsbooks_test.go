package statestore_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// TestSQLiteAppsBooks runs the published workflow contracts with strict sealing
// and independent caller-owned connection pools for each replica.
func TestSQLiteAppsBooks(t *testing.T) {
	path := sqlite.DSN(t.TempDir()+"/appsbooks.db", sqlite.Options{})
	runAppsBooksSQL(t, func(t *testing.T) *sql.DB {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		return ownAppsBooksPool(t, db)
	}, sqlite.Dialect)
}

// ownAppsBooksPool keeps pool lifetime with the caller rather than the adapter.
func ownAppsBooksPool(t *testing.T, db *sql.DB) *sql.DB {
	t.Helper()
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

// runAppsBooksSQL uses real sealed statestore instances and isolated test-only
// key scopes. It never resets tables or databases shared with other contracts.
func runAppsBooksSQL(t *testing.T, open func(*testing.T) *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	storagetest.RunAppsBooks(t, func(t *testing.T) (state.Store, state.Store) {
		t.Helper()
		aDB, bDB := open(t), open(t)
		ring, err := crypt.NewKeyring(t.Context(), crypt.Options{
			Keys:     crypt.Keys{Active: "appsbooks-test", Strict: true},
			Provider: secrets.Static{"appsbooks-test": bytes.Repeat([]byte{41}, 32)},
		})
		if err != nil {
			t.Fatal(err)
		}
		a, err := statestore.Open(t.Context(), aDB, dialect, ring)
		if err != nil {
			t.Fatal(err)
		}
		b, err := statestore.Open(t.Context(), bDB, dialect, ring)
		if err != nil {
			t.Fatal(err)
		}
		prefix := "apps320/" + rand.Text() + "/"
		t.Cleanup(func() {
			checkAppsBooksSQLSealing(t, aDB, dialect, a, prefix)
			if err := aDB.PingContext(context.Background()); err != nil {
				t.Error("adapter closed caller pool", err)
			}
			if err := bDB.PingContext(context.Background()); err != nil {
				t.Error("adapter closed replica pool", err)
			}
		})
		return &appsBooksScoped{Store: a, prefix: prefix}, &appsBooksScoped{Store: b, prefix: prefix}
	})
}

// checkAppsBooksSQLSealing inspects physical records, proves strict plaintext
// rejection, and rejects copied ciphertext with another record's authenticated key.
func checkAppsBooksSQLSealing(t *testing.T, db *sql.DB, d sqlcommon.Dialect, s state.Store, prefix string) {
	t.Helper()
	// Cleanup callbacks run after the subtest context has been cancelled.
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, d.Rebind("SELECT record_key,value FROM protocol_state WHERE record_key >= ? AND record_key < ? ORDER BY record_key"), prefix, prefix+"\x7f")
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = rows.Close() }()
	var records []state.Record
	for rows.Next() {
		var r state.Record
		if err := rows.Scan(&r.Key, &r.Value); err != nil {
			t.Error(err)
			return
		}
		if !crypt.IsSealed(r.Value) || bytes.Contains(r.Value, []byte("private-notification")) || bytes.Contains(r.Value, []byte("sToken")) {
			t.Error("location record was not sealed", r.Key)
		}
		opened, err := s.Get(ctx, r.Key)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.HasSuffix(r.Key, "/metadata") && bytes.Contains(opened.Value, []byte("sToken")) {
			t.Error("credential leaked into safe metadata")
		}
		records = append(records, r)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil || closeErr != nil {
		t.Error(readErr, closeErr)
		return
	}
	if len(records) < 3 {
		t.Error("missing separated location records", len(records))
		return
	}
	target := records[0]
	for _, invalid := range [][]byte{[]byte(`{"sToken":"unsealed"}`), records[1].Value} {
		if _, err := db.ExecContext(ctx, d.Rebind("UPDATE protocol_state SET value = ? WHERE record_key = ?"), invalid, target.Key); err != nil {
			t.Error(err)
			return
		}
		_, openErr := s.Get(ctx, target.Key)
		// Always restore this test-owned row before reporting an unexpected acceptance.
		if _, err := db.ExecContext(ctx, d.Rebind("UPDATE protocol_state SET value = ? WHERE record_key = ?"), target.Value, target.Key); err != nil {
			t.Error(err)
			return
		}
		if openErr == nil {
			t.Error("strict sealing accepted plaintext or another location row's ciphertext")
		}
	}
}

// appsBooksScoped isolates test scenarios while retaining the adapter's actual
// locking, transactions, database clock, sealing and row authentication.
type appsBooksScoped struct {
	state.Store
	prefix string
}

// Get addresses a record in this test's SQL scope.
func (s *appsBooksScoped) Get(ctx context.Context, key string) (state.Record, error) {
	return appsBooksScopedGet(ctx, s.Store, s.prefix, key)
}

// List returns original logical keys in the isolated SQL scope.
func (s *appsBooksScoped) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	return appsBooksScopedList(ctx, s.Store, s.prefix, prefix, after, limit)
}

// Update preserves all logical lock keys across the independent replica pools.
func (s *appsBooksScoped) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	scoped := make([]string, len(keys))
	for i, key := range keys {
		scoped[i] = s.prefix + key
	}
	return s.Store.Update(ctx, scoped, func(tx state.Tx) error { return fn(&appsBooksScopedTx{Tx: tx, prefix: s.prefix}) })
}

type appsBooksScopedTx struct {
	state.Tx
	prefix string
}

// Get reads through the actual SQL transaction.
func (s *appsBooksScopedTx) Get(ctx context.Context, key string) (state.Record, error) {
	return appsBooksScopedGet(ctx, s.Tx, s.prefix, key)
}

// List preserves logical cursors within the actual SQL transaction.
func (s *appsBooksScopedTx) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	return appsBooksScopedList(ctx, s.Tx, s.prefix, prefix, after, limit)
}

// Put lets statestore seal the complete record with its physical scoped row key.
func (s *appsBooksScopedTx) Put(ctx context.Context, r state.Record) error {
	r.Key = s.prefix + r.Key
	return s.Tx.Put(ctx, r)
}

// Delete addresses only this test's physical row.
func (s *appsBooksScopedTx) Delete(ctx context.Context, key string) error {
	return s.Tx.Delete(ctx, s.prefix+key)
}

// appsBooksScopedGet restores logical keys without altering opaque record values.
func appsBooksScopedGet(ctx context.Context, r state.Reader, prefix, key string) (state.Record, error) {
	record, err := r.Get(ctx, prefix+key)
	record.Key = strings.TrimPrefix(record.Key, prefix)
	return record, err
}

// appsBooksScopedList restores logical keys without changing their ordering.
func appsBooksScopedList(ctx context.Context, r state.Reader, scope, prefix, after string, limit int) ([]state.Record, error) {
	records, err := r.List(ctx, scope+prefix, scope+after, limit)
	for i := range records {
		records[i].Key = strings.TrimPrefix(records[i].Key, scope)
	}
	return records, err
}
