package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/adminauthtest"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "adminauth.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func openStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	s, err := sqlstore.Open(context.Background(), openDB(t), sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestContract(t *testing.T) {
	adminauthtest.RunSuite(t, func(t *testing.T) adminauth.Store {
		t.Helper()
		return openStore(t)
	})
}

func TestOpen(t *testing.T) {
	ctx := context.Background()

	t.Run("NilDatabase", func(t *testing.T) {
		if _, err := sqlstore.Open(ctx, nil, sqlite.Dialect, sqlstore.Options{}); !errors.Is(err, adminauth.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("UnsupportedDialect", func(t *testing.T) {
		d := sqlcommon.Dialect{Name: "oracle"}
		if _, err := sqlstore.Open(ctx, openDB(t), d, sqlstore.Options{}); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
			t.Fatalf("err = %v, want ErrUnsupportedDialect", err)
		}
		if _, err := sqlstore.MigrationSet(d); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
			t.Fatalf("MigrationSet: %v", err)
		}
		if _, err := sqlstore.Version(ctx, openDB(t), d); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
			t.Fatalf("Version: %v", err)
		}
	})

	t.Run("MigratesAndRollsBack", func(t *testing.T) {
		db := openDB(t)
		s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		v, err := sqlstore.Version(ctx, db, sqlite.Dialect)
		if err != nil || v != 1 {
			t.Fatalf("version = %d, %v; want 1", v, err)
		}
		if s.DB() != db {
			t.Fatal("DB() returned a different pool")
		}
		if _, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
		// Every table is gone, so a query now fails rather than silently
		// returning nothing.
		if _, err := s.Principals(ctx, adminauth.Page{}); err == nil {
			t.Fatal("a query succeeded after the schema was rolled back")
		}
	})

	t.Run("SkipMigrateLeavesNoSchema", func(t *testing.T) {
		s, err := sqlstore.Open(ctx, openDB(t), sqlite.Dialect, sqlstore.Options{SkipMigrate: true})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := s.Principal(ctx, "alice"); err == nil {
			t.Fatal("a query succeeded with no schema")
		}
	})
}

func TestInvalidNames(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "not a name!"}, "d", time.Now()); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("principal: err = %v, want ErrInvalid", err)
	}
	if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "not a name!"}, time.Now()); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("policy: err = %v, want ErrInvalid", err)
	}
}

// A digest is unique across principals: two rows cannot share a live
// credential, so a race to set the same digest is a conflict rather than two
// principals answering to one token.
func TestDigestIsUnique(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	now := time.Now().UTC()
	if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "a"}, "shared", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "b"}, "shared", now); !errors.Is(err, adminauth.ErrConflict) {
		t.Fatalf("create: err = %v, want ErrConflict", err)
	}
	if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "b"}, "other", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetToken(ctx, "b", "shared", "id", time.Time{}, now); !errors.Is(err, adminauth.ErrConflict) {
		t.Fatalf("rotate onto a live digest: err = %v, want ErrConflict", err)
	}
}
