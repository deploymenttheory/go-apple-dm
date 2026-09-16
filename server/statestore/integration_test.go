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
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

func TestPostgresSharedState(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	defer func(cleanup func() error) { _ = cleanup() }(db.Close)
	db.SetMaxOpenConns(8)
	exercise(t, db, postgres.Dialect)
	if _, err := sqlcommon.Migrate(t.Context(), db, postgres.Dialect); err != nil {
		t.Fatal(err)
	}
	exerciseCertificateActivation(t, db, postgres.Dialect)
}

func TestMySQLSharedState(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	dsn, err := mysql.NormalizeDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(db.Close)
	db.SetMaxOpenConns(8)
	exercise(t, db, mysql.Dialect)
	if _, err := sqlcommon.Migrate(t.Context(), db, mysql.Dialect); err != nil {
		t.Fatal(err)
	}
	exerciseCertificateActivation(t, db, mysql.Dialect)
}
