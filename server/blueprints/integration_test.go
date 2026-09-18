//go:build integration

package blueprints_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

func TestPersistentBlueprints(t *testing.T) {
	for _, dialect := range []sqlcommon.Dialect{postgres.Dialect, mysql.Dialect} {
		t.Run(dialect.Name, func(t *testing.T) {
			var db *sql.DB
			var err error
			if dialect.Name == "postgres" {
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN not set")
				}
				cfg, e := pgx.ParseConfig(dsn)
				if e != nil {
					t.Fatal(e)
				}
				db = stdlib.OpenDB(*cfg)
			} else {
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN not set")
				}
				dsn, err = mysql.NormalizeDSN(dsn)
				if err != nil {
					t.Fatal(err)
				}
				db, err = sql.Open("mysql", dsn)
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := sqlstore.Rollback(t.Context(), db, dialect, 0); err != nil {
				t.Fatal(err)
			}
			cfg := sqlConfig(t, db, dialect)
			if _, err := db.ExecContext(t.Context(), "DELETE FROM protocol_state WHERE record_key LIKE 'blueprint/%' OR record_key LIKE 'configuration-profile/%'"); err != nil {
				t.Fatal(err)
			}
			exercise(t, cfg)
		})
	}
}
