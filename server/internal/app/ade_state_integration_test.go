//go:build integration

package app

import (
	"bytes"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// TestADEEvidenceSQLReplicas exercises sealed evidence across independent PostgreSQL and MySQL pools.
func TestADEEvidenceSQLReplicas(t *testing.T) {
	for _, tc := range []struct {
		name, driver, env string
		dialect           sqlcommon.Dialect
	}{
		{"postgres", "pgx", "TEST_POSTGRES_DSN", postgres.Dialect}, {"mysql", "mysql", "TEST_MYSQL_DSN", mysql.Dialect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if dsn == "" {
				t.Skip(tc.env + " not set")
			}
			var err error
			if tc.name == "mysql" {
				dsn, err = mysql.NormalizeDSN(dsn)
				if err != nil {
					t.Fatal(err)
				}
			}
			db1, err := sql.Open(tc.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db1.Close() })
			db2, err := sql.Open(tc.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db2.Close() })
			ring, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "ade-test"}, Provider: secrets.Static{"ade-test": bytes.Repeat([]byte{5}, 32)}})
			if err != nil {
				t.Fatal(err)
			}
			first, err := statestore.Open(t.Context(), db1, tc.dialect, ring)
			if err != nil {
				t.Fatal(err)
			}
			second, err := statestore.Open(t.Context(), db2, tc.dialect, ring)
			if err != nil {
				t.Fatal(err)
			}
			exerciseADEEvidenceState(t, first, second, time.Now().UTC())
		})
	}
}
