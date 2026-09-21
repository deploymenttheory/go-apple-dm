//go:build integration

package inventorystore_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
)

// Tests only create and remove their own random namespace, never existing fleet tables.
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	control := stdlib.OpenDB(*cfg)
	defer func() { _ = control.Close() }()
	name := "inventory_test_" + strings.ToLower(rand.Text())
	if _, e := control.ExecContext(t.Context(), "CREATE SCHEMA "+name); e != nil {
		t.Fatal(e)
	}
	defer func() { _, _ = control.ExecContext(context.Background(), "DROP SCHEMA "+name+" CASCADE") }()
	cfg.RuntimeParams["search_path"] = name
	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()
	checkStore(t, db, postgres.Dialect)
}

// TestMySQL runs the contract in a newly created, isolated MySQL test database.
func TestMySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	cfg, e := mysqldriver.ParseDSN(dsn)
	if e != nil {
		t.Fatal(e)
	}
	control, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = control.Close() }()
	name := "inventory_test_" + strings.ToLower(rand.Text())
	if _, e := control.ExecContext(t.Context(), "CREATE DATABASE "+name); e != nil {
		t.Fatal(fmt.Errorf("test user needs permission to create isolated database: %w", e))
	}
	defer func() { _, _ = control.ExecContext(context.Background(), "DROP DATABASE "+name) }()
	cfg.DBName = name
	normalized, e := mysql.NormalizeDSN(cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("mysql", normalized)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	checkStore(t, db, mysql.Dialect)
}
