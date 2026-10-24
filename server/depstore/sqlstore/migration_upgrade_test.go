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

// TestSyncStateMigrationPreservesInventory checks that sync state migration preserves inventory.
func TestSyncStateMigrationPreservesInventory(t *testing.T) {
	checkSyncStateUpgrade(t, openDB(t), sqlite.Dialect)
}

// checkSyncStateUpgrade checks that the sync-state migration preserves accounts, devices, and
// cursor data while adding empty scheduling state.
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
	// Seed the old schema directly: current account writers require name locks.
	if _, err := db.ExecContext(ctx, dialect.Rebind("INSERT INTO dep_accounts (name, profile_uuid, org_name, server_name, admin_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)"), "upgrade", "profile", "", "", "", now, now); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDevices(ctx, "upgrade", []dep.Device{{SerialNumber: "KEPT", ProfileUUID: "profile"}}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, dialect.Rebind("INSERT INTO dep_cursors (account, value, phase, updated_at) VALUES (?, ?, ?, ?)"), "upgrade", "old-cursor", "fetch", now); err != nil {
		t.Fatal(err)
	}
	applied, err := depsql.Migrate(ctx, db, dialect)
	if err != nil || len(applied) != 3 || applied[0] != 2 || applied[1] != 3 || applied[2] != 4 {
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

// TestProfileChoicesMigrationReplay models MySQL retaining DDL when migration
// bookkeeping fails. Replaying installation must retain choices and state.
func TestProfileChoicesMigrationReplay(t *testing.T) {
	db := openDB(t)
	if _, err := depsql.Open(t.Context(), db, sqlite.Dialect, depsql.Options{}); err != nil {
		t.Fatal(err)
	}
	checkProfileChoicesReplay(t, db, sqlite.Dialect)
}

// checkProfileChoicesReplay verifies replay preserves desired choices and operational state.
func checkProfileChoicesReplay(t *testing.T, db *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	ctx := t.Context()
	store, err := depsql.Open(ctx, db, dialect, depsql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a := &dep.Account{Name: "replay", ProfileUUID: "default", ConsumerKey: "consumer", ConsumerSecret: "preserved", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.PutAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := store.PutProfile(ctx, a.Name, &dep.Profile{ProfileUUID: "explicit", ProfileName: "explicit"}); err != nil {
		t.Fatal(err)
	}
	a.ProfileTargets = map[string]string{"SERIAL": "explicit"}
	if err := store.PutAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetAccount(ctx, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutAssignment(ctx, &dep.Assignment{Account: a.Name, SerialNumber: "SERIAL", Status: dep.AssignmentPending, ProfileUUID: "explicit"}); err != nil {
		t.Fatal(err)
	}
	state := dep.AssignmentState{NotBefore: time.Now().UTC().Truncate(time.Second), Failures: 3}
	if err := store.PutAssignmentState(ctx, a.Name, state); err != nil {
		t.Fatal(err)
	}
	if err := store.PutKeypair(ctx, a.Name, dep.StageStaged, &dep.Keypair{CertPEM: []byte("certificate"), KeyPEM: []byte("private"), CreatedAt: a.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCursor(ctx, a.Name, dep.Cursor{Value: "cursor", Phase: dep.PhaseSync, Revision: 5, UpdatedAt: a.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM dep_schema_migrations WHERE version = 4"); err != nil {
		t.Fatal(err)
	}
	applied, err := depsql.Migrate(ctx, db, dialect)
	if err != nil || len(applied) != 1 || applied[0] != 4 {
		t.Fatalf("replay: %v %v", applied, err)
	}
	after, err := store.GetAccount(ctx, a.Name)
	if err != nil || after.ProfileRevision != before.ProfileRevision || after.DesiredProfile("SERIAL") != "explicit" || after.ProfileUUID != "default" || after.ConsumerSecret != "preserved" {
		t.Fatalf("replay changed account: %v", err)
	}
	assignment, err := store.GetAssignment(ctx, a.Name, "SERIAL")
	if err != nil || assignment.Status != dep.AssignmentPending {
		t.Fatal("replay lost uncertainty", err)
	}
	current, err := store.AssignmentState(ctx, a.Name)
	if err != nil || current != state {
		t.Fatal("replay lost worker state", err)
	}
	key, err := store.Keypair(ctx, a.Name, dep.StageStaged)
	if err != nil || string(key.KeyPEM) != "private" {
		t.Fatal("replay lost staged key", err)
	}
	cursor, err := store.Cursor(ctx, a.Name)
	if err != nil || cursor.Value != "cursor" || cursor.Revision != 5 {
		t.Fatal("replay lost checkpoint", err)
	}
	if err := store.DeleteAccount(ctx, a.Name); err != nil {
		t.Fatal(err)
	}
}
