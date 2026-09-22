package inventorystore_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/inventorystore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestPersistenceFailures verifies plaintext policy, authenticated identity and transactional errors.
func TestPersistenceFailures(t *testing.T) {
	ctx := t.Context()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "failure.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keys, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "key", Strict: true}, Provider: secrets.Static{"key": bytes.Repeat([]byte{42}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := inventorystore.Open(ctx, db, sqlite.Dialect, nil)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := inventorystore.Open(ctx, db, sqlite.Dialect, keys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inventorystore.Open(ctx, nil, sqlite.Dialect, nil); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := inventorystore.Open(ctx, db, sqlcommon.Dialect{Name: "unknown"}, nil); err == nil {
		t.Fatal("unknown dialect")
	}
	for _, name := range []string{"sqlite", "postgres", "mysql"} {
		if _, err := inventorystore.MigrationSet(sqlcommon.Dialect{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := plain.Get(ctx, "missing"); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatal(err)
	}
	if err := plain.Update(ctx, func(tx inventory.Tx) error { return tx.Put(ctx, "plain", json.RawMessage(`{"value":1}`)) }); err != nil {
		t.Fatal(err)
	}
	if b, err := plain.Get(ctx, "plain"); err != nil || string(b) != `{"value":1}` {
		t.Fatal(string(b), err)
	}
	if _, err := sealed.Get(ctx, "plain"); !errors.Is(err, crypt.ErrUnsealed) {
		t.Fatal(err)
	}
	if _, err := sealed.Scan(ctx, "plain", "", 1); !errors.Is(err, crypt.ErrUnsealed) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 1001} {
		if _, err := plain.Scan(ctx, "", "", limit); !errors.Is(err, inventory.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := sealed.Update(ctx, func(tx inventory.Tx) error {
		if err := tx.Put(ctx, "secret", json.RawMessage(`"secret"`)); err != nil {
			return err
		}
		rows, err := tx.Scan(ctx, "secret", "", 10)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Get(ctx, "secret"); !errors.Is(err, crypt.ErrUnsealed) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE inventory_entries SET entry_key='moved' WHERE entry_key='secret'"); err != nil {
		t.Fatal(err)
	}
	if _, err := sealed.Get(ctx, "moved"); err == nil {
		t.Fatal("ciphertext move accepted")
	}
	if err := sealed.Update(ctx, func(tx inventory.Tx) error { return tx.Put(ctx, "bad", json.RawMessage(`{`)) }); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
	if err := plain.Update(ctx, func(tx inventory.Tx) error { return tx.Delete(ctx, "plain") }); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Get(ctx, "plain"); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := plain.Get(cancelled, "key"); err == nil {
		t.Fatal("cancelled read")
	}
	if _, err := plain.Scan(cancelled, "", "", 10); err == nil {
		t.Fatal("cancelled scan")
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE inventory_lock"); err != nil {
		t.Fatal(err)
	}
	if err := plain.Update(ctx, func(inventory.Tx) error { t.Fatal("unlocked transaction executed"); return nil }); err == nil {
		t.Fatal("missing lock accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := inventorystore.Open(ctx, db, sqlite.Dialect, nil); err == nil {
		t.Fatal("closed pool accepted")
	}
}
