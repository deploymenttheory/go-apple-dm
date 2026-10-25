//go:build integration

package statestore_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
)

// TestPostgresAppsBooks exercises sealed location workflows across independent
// caller-owned PostgreSQL pools, including configuration and token-recovery races.
func TestPostgresAppsBooks(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	runAppsBooksSQL(t, func(t *testing.T) *sql.DB {
		t.Helper()
		return ownAppsBooksPool(t, stdlib.OpenDB(*cfg))
	}, postgres.Dialect)
}

// TestMySQLAppsBooks runs the same sealed workflow contract across independent
// caller-owned MySQL pools without resetting shared databases or tables.
func TestMySQLAppsBooks(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	dsn, err := mysql.NormalizeDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	runAppsBooksSQL(t, func(t *testing.T) *sql.DB {
		t.Helper()
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			t.Fatal(err)
		}
		return ownAppsBooksPool(t, db)
	}, mysql.Dialect)
}
