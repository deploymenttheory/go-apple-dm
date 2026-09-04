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

	"github.com/deploymenttheory/go-apple-dm/server/audit"
	"github.com/deploymenttheory/go-apple-dm/server/audit/audittest"
	"github.com/deploymenttheory/go-apple-dm/server/audit/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// auditTables is every table this migration set owns.
var auditTables = []string{"audit_records"}

func runShared(t *testing.T, db *sql.DB, d sqlcommon.Dialect, cascade string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+strings.Join(append(auditTables, sqlstore.MigrationsTable), ", ")+cascade); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Open(ctx, db, d, sqlstore.Options{}); err != nil {
		t.Fatal(err)
	}
	if v, err := sqlstore.Version(ctx, db, d); err != nil || v != 1 {
		t.Fatalf("version %d %v", v, err)
	}
	if applied, err := sqlstore.Migrate(ctx, db, d); err != nil || len(applied) != 0 {
		t.Fatalf("second migrate: %v %v", applied, err)
	}
	audittest.RunSuite(t, func(t *testing.T) audit.Store {
		t.Helper()
		// Each case starts from an empty trail. The identity sequence is
		// deliberately not reset: IDsAreNotReused holds across cases too.
		for _, table := range auditTables {
			if _, err := db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				t.Fatal(err)
			}
		}
		s, err := sqlstore.Open(ctx, db, d, sqlstore.Options{SkipMigrate: true})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
	if reverted, err := sqlstore.Rollback(ctx, db, d, 0); err != nil || len(reverted) != 1 {
		t.Fatalf("rollback: %v %v", reverted, err)
	}
}

func TestStorePostgres(t *testing.T) {
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

func TestStoreMySQL(t *testing.T) {
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
