package sqlstore_test

import (
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	depsql "github.com/deploymenttheory/go-apple-dm/server/depstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func TestSyncStateMigrationPreservesInventory(t *testing.T) {
	checkSyncStateUpgrade(t, openDB(t), sqlite.Dialect)
}

func checkSyncStateUpgrade(t *testing.T, db *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	ctx := t.Context()
	set, err := depsql.MigrationSet(dialect)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := fs.ReadFile(set.FS, "0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	set.FS = fstest.MapFS{"0001_init.sql": &fstest.MapFile{Data: initial}}
	if _, err := sqlcommon.MigrateSet(ctx, db, dialect, set); err != nil {
		t.Fatal(err)
	}
	st, err := depsql.Open(ctx, db, dialect, depsql.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := st.PutAccount(ctx, &dep.Account{Name: "upgrade", ProfileUUID: "profile", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDevices(ctx, "upgrade", []dep.Device{{SerialNumber: "KEPT", ProfileUUID: "profile"}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, dialect.Rebind("INSERT INTO dep_cursors (account, value, phase, updated_at) VALUES (?, ?, ?, ?)"), "upgrade", "old-cursor", "fetch", now); err != nil {
		t.Fatal(err)
	}
	applied, err := depsql.Migrate(ctx, db, dialect)
	if err != nil || len(applied) != 1 || applied[0] != 2 {
		t.Fatalf("upgrade: %v %v", applied, err)
	}
	account, err := st.GetAccount(ctx, "upgrade")
	if err != nil || account.ProfileUUID != "profile" {
		t.Fatalf("account changed: %+v %v", account, err)
	}
	device, err := st.GetDevice(ctx, "upgrade", "KEPT")
	if err != nil || device.Deleted || device.ProfileUUID != "profile" {
		t.Fatalf("inventory changed: %+v %v", device, err)
	}
	cursor, err := st.Cursor(ctx, "upgrade")
	if err != nil || cursor.Value != "old-cursor" || cursor.Phase != dep.PhaseFetch || cursor.Revision != 0 || cursor.Generation != "" {
		t.Fatalf("cursor changed: %+v %v", cursor, err)
	}
	state, err := st.AssignmentState(ctx, "upgrade")
	if err != nil || state != (dep.AssignmentState{}) {
		t.Fatalf("new scheduling state: %+v %v", state, err)
	}
}
