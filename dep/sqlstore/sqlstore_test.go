package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/dep/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "dep.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestContract(t *testing.T) {
	deptest.RunStoreSuite(t, func(t *testing.T, k *crypt.Keyring) dep.Store {
		s, err := sqlstore.Open(context.Background(), openDB(t), sqlite.Dialect, sqlstore.Options{Keyring: k})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestOpenAndMigrations(t *testing.T) {
	ctx := context.Background()
	if _, err := sqlstore.Open(ctx, nil, sqlite.Dialect, sqlstore.Options{}); !errors.Is(err, dep.ErrInvalid) {
		t.Fatalf("nil db = %v", err)
	}
	if _, err := sqlstore.MigrationSet(sqlcommon.Dialect{Name: "oracle"}); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("unknown dialect = %v", err)
	}
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.DB() != db {
		t.Fatal("DB accessor")
	}
	v, err := sqlstore.Version(ctx, db, sqlite.Dialect)
	if err != nil || v != 1 {
		t.Fatalf("version = %d %v", v, err)
	}
	// Idempotent: a second Open applies nothing; SkipMigrate leaves the schema alone.
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true}); err != nil {
		t.Fatal(err)
	}
	reverted, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0)
	if err != nil || len(reverted) != 1 {
		t.Fatalf("rollback = %v %v", reverted, err)
	}
	if v, err := sqlstore.Version(ctx, db, sqlite.Dialect); err != nil || v != 0 {
		t.Fatalf("version after rollback = %d %v", v, err)
	}
	// Without the schema the store fails on use.
	bare, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bare.GetAccount(ctx, "x"); err == nil {
		t.Fatal("query without schema succeeded")
	}
}

// TestMigrationsAgreeAcrossDialects pins the three embedded migration
// directories to the same version and name sequence with a down section
// each, so a schema change cannot land in one engine only.
func TestMigrationsAgreeAcrossDialects(t *testing.T) {
	load := func(name string) []sqlcommon.Migration {
		t.Helper()
		set, err := sqlstore.MigrationSet(sqlcommon.Dialect{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		ms, err := sqlcommon.LoadMigrations(set.FS)
		if err != nil || len(ms) == 0 {
			t.Fatalf("%s migrations: %v %v", name, ms, err)
		}
		return ms
	}
	ref := load("sqlite")
	for _, name := range []string{"postgres", "mysql"} {
		ms := load(name)
		if len(ms) != len(ref) {
			t.Fatalf("%s has %d migrations, sqlite %d", name, len(ms), len(ref))
		}
		for i := range ref {
			if ms[i].Version != ref[i].Version || ms[i].Name != ref[i].Name || len(ms[i].Down) == 0 || len(ref[i].Down) == 0 {
				t.Fatalf("%s migration %d = %s (%d down) vs sqlite %s (%d down)", name, ms[i].Version, ms[i].Name, len(ms[i].Down), ref[i].Name, len(ref[i].Down))
			}
		}
	}
}
