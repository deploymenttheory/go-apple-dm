//go:build integration

package mysql_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/v3/secrets"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/storagetest"
)

func TestContract(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	ctx := context.Background()
	if _, err := mysql.Open(ctx, "nobody:x@tcp(127.0.0.1:1)/none", mysql.Options{}); err == nil {
		t.Fatal("unreachable server")
	}
	s, err := mysql.Open(ctx, dsn, mysql.Options{Pool: sqlcommon.Pool{MaxOpenConns: 8, MaxIdleConns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Start from an empty schema every run, even when the database carries
	// a schema from an older build.
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE IF EXISTS user_auth, push_certs, cert_associations, commands, enrollments, schema_migrations"); err != nil {
		t.Fatal(err)
	}
	// A conflicting table makes Open fail at migration time.
	if _, err := s.DB().ExecContext(ctx, "CREATE TABLE enrollments (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := mysql.Open(ctx, dsn, mysql.Options{}); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("open over a conflicting schema: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE enrollments"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlcommon.Migrate(ctx, s.DB(), mysql.Dialect); err != nil {
		t.Fatal(err)
	}
	reset := func(t *testing.T, st storage.Store) storage.Store {
		t.Helper()
		for _, table := range []string{"user_auth", "push_certs", "cert_associations", "commands", "enrollments"} {
			if _, err := s.DB().ExecContext(ctx, "DELETE FROM "+table); err != nil {
				t.Fatal(err)
			}
		}
		return st
	}
	t.Run("Plaintext", func(t *testing.T) {
		storagetest.RunAll(t, func(t *testing.T) storage.Store { return reset(t, s) })
	})
	// The sealed-column suites also run with a keyring (decision record 0013).
	k, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "k1"}, Provider: secrets.Static{"k1": []byte("integration-key-material-32-bytes")}})
	if err != nil {
		t.Fatal(err)
	}
	enc, err := mysql.Open(ctx, dsn, mysql.Options{SkipMigrate: true, Keyring: k})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	t.Run("Encrypted", func(t *testing.T) {
		f := func(t *testing.T) storage.Store { return reset(t, enc) }
		t.Run("Enrollment", func(t *testing.T) { storagetest.RunEnrollmentSuite(t, f) })
		t.Run("BootstrapToken", func(t *testing.T) { storagetest.RunBootstrapTokenSuite(t, f) })
		t.Run("PushCert", func(t *testing.T) { storagetest.RunPushCertSuite(t, f) })
		t.Run("UserAuth", func(t *testing.T) { storagetest.RunUserAuthSuite(t, f) })
		t.Run("Migration", func(t *testing.T) { storagetest.RunMigrationSuite(t, f) })
		if n, err := enc.Rewrap(ctx); err != nil || n != 0 {
			t.Fatalf("Rewrap after the encrypted run = %d %v (every row must already be sealed)", n, err)
		}
	})
	again, err := mysql.Open(ctx, dsn, mysql.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}
