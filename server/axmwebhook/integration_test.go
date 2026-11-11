//go:build integration

package axmwebhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
)

// integrationKeys supplies isolated test storage encryption material.
func integrationKeys(t *testing.T) *crypt.Keyring {
	t.Helper()
	keys, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "test", Strict: true}, Provider: secrets.Static{"test": bytes.Repeat([]byte{42}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// TestPostgresReceipt uses a random schema without changing existing fleet state.
func TestPostgresReceipt(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	control := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = control.Close() })
	name := "inventory_test_axm_webhook_" + strings.ToLower(rand.Text())
	if _, err := control.ExecContext(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = control.ExecContext(context.Background(), "DROP SCHEMA "+name+" CASCADE") })
	cfg.RuntimeParams["search_path"] = name
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	checkSQLReceipt(t, db, postgres.Dialect, integrationKeys(t))
}

// TestMySQLReceipt uses a random database and the repository's migration policy.
func TestMySQLReceipt(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	control, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	name := "inventory_test_axm_webhook_" + strings.ToLower(rand.Text())
	if _, err := control.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = control.ExecContext(context.Background(), "DROP DATABASE "+name) })
	cfg.DBName = name
	normalized, err := mysql.NormalizeDSN(cfg.FormatDSN())
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	checkSQLReceipt(t, db, mysql.Dialect, integrationKeys(t))
}
