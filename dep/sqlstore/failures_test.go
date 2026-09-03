package sqlstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/dep/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlite"
)

// TestClosedDatabaseSurfaces proves every method reports a driver
// failure instead of hiding it: the database is closed under the store.
func TestClosedDatabaseSurfaces(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	acct := &dep.Account{Name: "a", ConsumerKey: "ck", ConsumerSecret: "cs", AccessToken: "at", AccessSecret: "as", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.PutAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"PutAccount":      func() error { return s.PutAccount(ctx, acct) },
		"GetAccount":      func() error { _, err := s.GetAccount(ctx, "a"); return err },
		"DeleteAccount":   func() error { return s.DeleteAccount(ctx, "a") },
		"ListAccounts":    func() error { _, err := s.ListAccounts(ctx, storage.Page{}); return err },
		"SetAccountState": func() error { return s.SetAccountState(ctx, "a", dep.AccountState{TokenInvalid: true}) },
		"PutKeypair": func() error {
			return s.PutKeypair(ctx, "a", dep.StageStaged, &dep.Keypair{CertPEM: []byte("c"), KeyPEM: []byte("k")})
		},
		"Keypair":        func() error { _, err := s.Keypair(ctx, "a", dep.StageCurrent); return err },
		"UpstageKeypair": func() error { return s.UpstageKeypair(ctx, "a") },
		"Session":        func() error { _, err := s.Session(ctx, "a"); return err },
		"SetSession":     func() error { return s.SetSession(ctx, "a", "tok") },
		"Cursor":         func() error { _, err := s.Cursor(ctx, "a"); return err },
		"SetCursor": func() error {
			return s.SetCursor(ctx, "a", dep.Cursor{Value: "c", Phase: dep.PhaseSync, UpdatedAt: time.Now()})
		},
		"PutDevices":  func() error { return s.PutDevices(ctx, "a", []dep.Device{{SerialNumber: "S"}}, time.Now()) },
		"GetDevice":   func() error { _, err := s.GetDevice(ctx, "a", "S"); return err },
		"ListDevices": func() error { _, err := s.ListDevices(ctx, "a", dep.DeviceQuery{}, storage.Page{}); return err },
		"PutProfile": func() error {
			return s.PutProfile(ctx, "a", &dep.Profile{ProfileUUID: "p", ProfileName: "n", URL: "https://x"})
		},
		"GetProfile":    func() error { _, err := s.GetProfile(ctx, "a", "p"); return err },
		"DeleteProfile": func() error { return s.DeleteProfile(ctx, "a", "p") },
		"ListProfiles":  func() error { _, err := s.ListProfiles(ctx, "a", storage.Page{}); return err },
		"PutAssignment": func() error {
			return s.PutAssignment(ctx, &dep.Assignment{Account: "a", SerialNumber: "S", ProfileUUID: "p", Status: dep.StatusSuccess})
		},
		"GetAssignment":   func() error { _, err := s.GetAssignment(ctx, "a", "S"); return err },
		"ListAssignments": func() error { _, err := s.ListAssignments(ctx, "a", dep.AssignmentQuery{}, storage.Page{}); return err },
		"Update":          func() error { return s.Update(ctx, func(dep.Tx) error { return nil }) },
	}
	for name, call := range calls {
		if err := call(); err == nil || errors.Is(err, dep.ErrNotFound) {
			t.Errorf("%s: err = %v, want a driver error", name, err)
		}
	}
	if _, err := sqlstore.Version(ctx, db, sqlite.Dialect); err == nil {
		t.Error("Version on a closed database succeeded")
	}
	if _, err := sqlstore.Migrate(ctx, db, sqlite.Dialect); err == nil {
		t.Error("Migrate on a closed database succeeded")
	}
	if _, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0); err == nil {
		t.Error("Rollback on a closed database succeeded")
	}
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err == nil {
		t.Error("Open on a closed database succeeded")
	}
}

func TestMigrateDirect(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	applied, err := sqlstore.Migrate(ctx, db, sqlite.Dialect)
	if err != nil || len(applied) != 1 {
		t.Fatalf("migrate = %v %v", applied, err)
	}
	if v, err := sqlstore.Version(ctx, db, sqlite.Dialect); err != nil || v != 1 {
		t.Fatalf("version = %d %v", v, err)
	}
}
