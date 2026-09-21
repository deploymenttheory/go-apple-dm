package inventorystore_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/inventorystore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestSQLite runs the encrypted inventory contract in a temporary SQLite database.
func TestSQLite(t *testing.T) {
	db, e := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "inventory.db"), sqlite.Options{}))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	checkStore(t, db, sqlite.Dialect)
}

// checkStore checks migrations, encrypted persistence, indexed queries and shared-transaction rollback.
func checkStore(t *testing.T, db *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	ctx := t.Context()
	keys, e := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "test", Strict: true}, Provider: secrets.Static{"test": bytes.Repeat([]byte{42}, 32)}})
	if e != nil {
		t.Fatal(e)
	}
	store, e := inventorystore.Open(ctx, db, dialect, keys)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := inventory.New(store)
	if e != nil {
		t.Fatal(e)
	}
	raw := json.RawMessage(`{"id":"opaque","attributes":{"serialNumber":"SERIAL-SECRET","imei":["one","two"],"future":null}}`)
	o, e := inventory.ResourceObservation(inventory.SourceReference{Kind: "axm.device", AccountID: "account", ResourceID: "opaque"}, raw, time.Now().UTC(), time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	device, e := repo.Observe(ctx, "SERIAL-SECRET", o)
	if e != nil {
		t.Fatal(e)
	}
	var payload []byte
	if e := db.QueryRowContext(ctx, dialect.Rebind("SELECT payload FROM inventory_entries WHERE entry_key = ?"), "device/"+device.ID).Scan(&payload); e != nil {
		t.Fatal(e)
	}
	if !crypt.IsSealed(payload) || bytes.Contains(payload, []byte("SERIAL-SECRET")) {
		t.Fatal("inventory stored without encryption")
	}
	reopened, e := inventorystore.Open(ctx, db, dialect, keys)
	if e != nil {
		t.Fatal(e)
	}
	r, e := inventory.New(reopened)
	if e != nil {
		t.Fatal(e)
	}
	page, e := r.Devices(ctx, inventory.DeviceQuery{Conditions: []inventory.Condition{{Field: "imei", Operator: "contains", Value: json.RawMessage(`"two"`)}}})
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != device.ID {
		t.Fatalf("restart/query %+v %v", page, e)
	}
	boom := errors.New("outer protocol rollback")
	unit := sqlcommon.UnitOfWork{DB: db, Dialect: dialect}
	e = unit.Run(ctx, func(ctx context.Context) error {
		if _, e := r.Observe(ctx, "OTHER", inventory.Observation{Source: inventory.SourceReference{Kind: "mdm.DeviceInformation", ResourceID: "new"}, Raw: json.RawMessage(`{"attributes":{"serialNumber":"OTHER"}}`), ObservedAt: time.Now().UTC()}); e != nil {
			return e
		}
		return boom
	})
	if !errors.Is(e, boom) {
		t.Fatal(e)
	}
	page, e = r.Devices(ctx, inventory.DeviceQuery{})
	if e != nil || len(page.Items) != 1 {
		t.Fatal("projection survived protocol rollback", e, len(page.Items))
	}
	set, e := inventorystore.MigrationSet(dialect)
	if e != nil {
		t.Fatal(e)
	}
	version, e := sqlcommon.VersionOf(ctx, db, dialect, set.Table)
	if e != nil || version != 1 {
		t.Fatal(version, e)
	}
	if _, e := sqlcommon.RollbackSet(ctx, db, dialect, set, 0); e != nil {
		t.Fatal(e)
	}
	if _, e := inventorystore.Open(ctx, db, dialect, keys); e != nil {
		t.Fatal(e)
	}
}
