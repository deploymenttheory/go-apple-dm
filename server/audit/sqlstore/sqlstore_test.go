package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/audit"
	"github.com/deploymenttheory/go-apple-dm/server/audit/audittest"
	"github.com/deploymenttheory/go-apple-dm/server/audit/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// openDB opens a temporary SQLite database and registers its cleanup.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "audit.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// openStore opens a migrated audit store backed by a temporary SQLite database.
func openStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	s, err := sqlstore.Open(context.Background(), openDB(t), sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestContract runs the shared audit-store suite against SQLite.
func TestContract(t *testing.T) {
	audittest.RunSuite(t, func(t *testing.T) audit.Store {
		t.Helper()
		return openStore(t)
	})
}

// TestOpen checks audit store configuration, database failures, skipped migrations, and pool
// access.
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

// TestMigrations checks audit schema versioning, rollback, and ownership of its migration table.
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

// TestInitialSchemaAllowsMissingEventIDAndDeduplicatesOccurrences checks that initial schema
// allows missing event ID and deduplicates occurrences.
func TestInitialSchemaAllowsMissingEventIDAndDeduplicatesOccurrences(t *testing.T) {
	ctx := t.Context()
	db := openDB(t)
	if _, err := sqlstore.Migrate(ctx, db, sqlite.Dialect); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO audit_records (at, type, actor, channel, enrollment_id, parent_id, fields) VALUES (?, 'enrolled', '', '', '', '', '')", audittest.T0); err != nil {
		t.Fatal(err)
	}
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Get(ctx, 1)
	if err != nil || old.Type != "enrolled" || old.EventID != "" {
		t.Fatal(old, err)
	}
	input := audit.Record{EventID: "occurrence", Type: "command-queued", At: audittest.T0}
	one, err := s.Append(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.Append(ctx, input)
	if err != nil || one.ID != two.ID || two.EventID != input.EventID {
		t.Fatal(one, two, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_records").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
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

// TestFailuresSurface checks that audit operations report errors from a closed database pool.
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
