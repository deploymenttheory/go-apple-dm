package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/server/audit"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit/audittest"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "audit.db"), sqlite.Options{}))
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
	audittest.RunSuite(t, func(t *testing.T) audit.Store {
		t.Helper()
		return openStore(t)
	})
}

func TestOpen(t *testing.T) {
	ctx := context.Background()

	t.Run("NilDatabase", func(t *testing.T) {
		if _, err := sqlstore.Open(ctx, nil, sqlite.Dialect, sqlstore.Options{}); !errors.Is(err, audit.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("UnknownDialect", func(t *testing.T) {
		_, err := sqlstore.Open(ctx, openDB(t), sqlcommon.Dialect{Name: "oracle"}, sqlstore.Options{})
		if !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
			t.Fatalf("err = %v, want ErrUnsupportedDialect", err)
		}
	})

	t.Run("ClosedDatabase", func(t *testing.T) {
		db := openDB(t)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err == nil {
			t.Fatal("Open accepted a closed database")
		}
	})

	t.Run("SkipMigrateNeedsTheSchema", func(t *testing.T) {
		db := openDB(t)
		s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Append(ctx, audit.Record{At: audittest.T0, Type: "enrolled"}); err == nil {
			t.Fatal("Append succeeded against a schema that was never created")
		}
	})

	t.Run("ExposesThePool", func(t *testing.T) {
		if openStore(t).DB() == nil {
			t.Fatal("DB returned nil")
		}
	})
}

func TestMigrations(t *testing.T) {
	ctx := context.Background()

	t.Run("VersionAndRollback", func(t *testing.T) {
		db := openDB(t)
		if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err != nil {
			t.Fatal(err)
		}
		v, err := sqlstore.Version(ctx, db, sqlite.Dialect)
		if err != nil || v != 1 {
			t.Fatalf("version = %d, err = %v", v, err)
		}
		// A second migrate applies nothing.
		applied, err := sqlstore.Migrate(ctx, db, sqlite.Dialect)
		if err != nil || len(applied) != 0 {
			t.Fatalf("applied = %v, err = %v", applied, err)
		}
		reverted, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0)
		if err != nil || len(reverted) != 1 {
			t.Fatalf("reverted = %v, err = %v", reverted, err)
		}
	})

	t.Run("UnknownDialect", func(t *testing.T) {
		d := sqlcommon.Dialect{Name: "oracle"}
		if _, err := sqlstore.MigrationSet(d); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
			t.Fatalf("err = %v", err)
		}
		if _, err := sqlstore.Migrate(ctx, openDB(t), d); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
			t.Fatalf("err = %v", err)
		}
		if _, err := sqlstore.Rollback(ctx, openDB(t), d, 0); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
			t.Fatalf("err = %v", err)
		}
		if _, err := sqlstore.Version(ctx, openDB(t), d); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
			t.Fatalf("err = %v", err)
		}
	})

	// The audit schema keeps its own version sequence, so it can move
	// independently of storage, DDM, DEP, ACME, and admin.
	t.Run("OwnsItsMigrationTable", func(t *testing.T) {
		if sqlstore.MigrationsTable != "audit_schema_migrations" {
			t.Fatalf("table = %q", sqlstore.MigrationsTable)
		}
	})
}

// Fields are stored as JSON, so a payload that cannot be encoded is a caller
// error rather than a corrupt row.
func TestAppendRejectsUnencodableFields(t *testing.T) {
	s := openStore(t)
	_, err := s.Append(context.Background(), audit.Record{
		At: audittest.T0, Type: "enrolled", Fields: map[string]any{"ch": make(chan int)},
	})
	if !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestFailuresSurface(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, audit.Record{At: audittest.T0, Type: "enrolled"}); err == nil {
		t.Error("Append succeeded on a closed pool")
	}
	if _, err := s.List(ctx, audit.Query{}, audit.Page{}); err == nil {
		t.Error("List succeeded on a closed pool")
	}
	if _, err := s.Get(ctx, 1); err == nil {
		t.Error("Get succeeded on a closed pool")
	}
	if _, err := s.Prune(ctx, time.Now()); err == nil {
		t.Error("Prune succeeded on a closed pool")
	}
}
