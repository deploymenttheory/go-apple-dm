//go:build integration

package recovery

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
)

func TestPostgresDatabaseRecovery(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	control := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = control.Close() })
	newDB := func() *sql.DB {
		name := fmt.Sprintf("recovery_%d", time.Now().UnixNano())
		if _, err := control.ExecContext(t.Context(), "CREATE SCHEMA "+name); err != nil {
			t.Fatal(err)
		}
		c := config.Copy()
		c.RuntimeParams["search_path"] = name
		db := stdlib.OpenDB(*c)
		t.Cleanup(
			func() { _ = db.Close(); _, _ = control.Exec("DROP SCHEMA " + name + " CASCADE") },
		)
		return db
	}
	source := sqlFixture(t, newDB(), postgres.Dialect)
	exerciseSQLRecovery(t, source, newDB)
}

// TEST_MYSQL_DSN names the disposable test database, as in the existing storage
// suites. Packages using it must run serially. MySQL's test account cannot create
// another database, so restore runs after removing the checkpointed fixture's
// tables from that same disposable database.
func TestMySQLDatabaseRecovery(t *testing.T) {
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
	t.Cleanup(func() { _ = db.Close() })
	clear := func() *sql.DB {
		names, err := tableNames(t.Context(), db, "mysql")
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(t.Context(), "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(t.Context(), "SET FOREIGN_KEY_CHECKS = 1")
		for _, name := range names {
			if strings.Contains(name, "`") {
				t.Fatal("invalid table")
			}
			if _, err := conn.ExecContext(t.Context(), "DROP TABLE `"+name+"`"); err != nil {
				t.Fatal(err)
			}
		}
		return db
	}
	source := sqlFixture(t, clear(), mysql.Dialect)
	exerciseSQLRecovery(t, source, clear)
}
