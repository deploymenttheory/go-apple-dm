package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/server/audit"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit/audittest"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/sqlite"
)

// A row whose fields column is not JSON is damage the store cannot fix, but
// it must surface it rather than hand back a half-decoded record. Reaching
// that path needs the column written behind the store's back.
func TestCorruptFieldsAreReported(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Append(ctx, audit.Record{At: audittest.T0, Type: "enrolled", Fields: map[string]any{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE audit_records SET fields = 'not json' WHERE id = ?", rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, rec.ID); err == nil {
		t.Error("Get returned a record with an undecodable payload")
	}
	if _, err := s.List(ctx, audit.Query{}, audit.Page{}); err == nil {
		t.Error("List returned a record with an undecodable payload")
	}
}

// A schema that no longer matches is the other half of the same problem: the
// scan must fail rather than silently produce a zero record.
func TestScanFailureIsReported(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, audit.Record{At: audittest.T0, Type: "enrolled"}); err != nil {
		t.Fatal(err)
	}
	// Replace the timestamp with something that cannot scan into time.Time.
	if _, err := db.ExecContext(ctx, "UPDATE audit_records SET at = 'never'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx, audit.Query{}, audit.Page{}); err == nil {
		t.Error("List accepted an unscannable row")
	}
}

// The migration helpers report a pool that cannot answer, rather than
// reporting version 0 and carrying on.
func TestMigrationHelpersOnAClosedPool(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Migrate(ctx, db, sqlite.Dialect); err == nil {
		t.Error("Migrate succeeded on a closed pool")
	}
	if _, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0); err == nil {
		t.Error("Rollback succeeded on a closed pool")
	}
	if _, err := sqlstore.Version(ctx, db, sqlite.Dialect); err == nil {
		t.Error("Version succeeded on a closed pool")
	}
}

// A cursor is a row id, and a row id is an integer. Anything else is the
// caller's mistake, reported as such rather than scanned as SQL.
func TestListRejectsANonNumericCursor(t *testing.T) {
	s := openStore(t)
	for _, cursor := range []string{"abc", "1; DROP TABLE audit_records", ""} {
		if cursor == "" {
			continue
		}
		if _, err := s.List(context.Background(), audit.Query{}, audit.Page{Cursor: cursor}); !errors.Is(err, audit.ErrInvalid) {
			t.Errorf("cursor %q: err = %v, want ErrInvalid", cursor, err)
		}
	}
	// The table is still there.
	var n int
	if err := s.DB().QueryRow("SELECT COUNT(*) FROM audit_records").Scan(&n); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the table did not survive: %v", err)
	}
}
