//go:build integration

package sqlstore_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	sqlstore "github.com/deploymenttheory/go-apple-dm/v3/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/ddm/ddmtest"
)

// ddmTables lists every table of the schema, dependants first, so the
// list serves both DROP and DELETE.
var ddmTables = []string{
	"ddm_changes", "ddm_status_reports", "ddm_status_errors", "ddm_status_values", "ddm_status_declarations",
	"ddm_snapshot_items", "ddm_snapshots", "ddm_enrollment_declarations", "ddm_enrollment_sets",
	"ddm_set_declarations", "ddm_sets", "ddm_declaration_versions", "ddm_declarations",
}

// runShared runs the contract suite on one server-backed store, emptying
// every table before each subtest, and checks the migration bookkeeping.
func runShared(t *testing.T, db *sql.DB, d sqlcommon.Dialect, cascade string) {
	t.Helper()
	ctx := context.Background()
	// Start from nothing, even when the database carries a schema from an
	// older build.
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+strings.Join(append(ddmTables, sqlstore.MigrationsTable), ", ")+cascade); err != nil {
		t.Fatal(err)
	}
	s, err := sqlstore.Open(ctx, db, d, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := sqlstore.Version(ctx, db, d); err != nil || v != 1 {
		t.Fatalf("version %d %v", v, err)
	}
	// Opening again over the migrated schema applies nothing.
	if applied, err := sqlstore.Migrate(ctx, db, d); err != nil || len(applied) != 0 {
		t.Fatalf("second migrate: %v %v", applied, err)
	}
	if _, err := sqlstore.Open(ctx, db, d, sqlstore.Options{SkipMigrate: true}); err != nil {
		t.Fatal(err)
	}
	ddmtest.RunAll(t, func(t *testing.T) ddm.Store {
		t.Helper()
		for _, table := range ddmTables {
			if _, err := db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				t.Fatal(err)
			}
		}
		return s
	})
	if reverted, err := sqlstore.Rollback(ctx, db, d, 0); err != nil || len(reverted) != 1 {
		t.Fatalf("rollback: %v %v", reverted, err)
	}
	if v, err := sqlstore.Version(ctx, db, d); err != nil || v != 0 {
		t.Fatalf("version after rollback %d %v", v, err)
	}
}

func TestContractPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	db.SetMaxOpenConns(8)
	runShared(t, db, postgres.Dialect, " CASCADE")
}

func TestContractMySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	normalized, err := mysql.NormalizeDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	runShared(t, db, mysql.Dialect, "")
}
