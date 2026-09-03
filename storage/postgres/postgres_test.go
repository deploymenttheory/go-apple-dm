//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/storage/postgres"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlcommon/sqltest"
	"github.com/deploymenttheory/go-apple-dm/storage/storagetest"
)

// openFresh connects to TEST_POSTGRES_DSN and resets the schema.
func openFresh(tb testing.TB) *postgres.Store {
	tb.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		tb.Skip("TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	s, err := postgres.Open(ctx, dsn, postgres.Options{Pool: sqlcommon.Pool{MaxOpenConns: 8, MaxIdleConns: 4}})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close() })
	resetSchema(tb, s.DB())
	if _, err := sqlcommon.Migrate(ctx, s.DB(), postgres.Dialect); err != nil {
		tb.Fatal(err)
	}
	return s
}

// resetSchema drops every table so each run starts from nothing, even
// when the database carries a schema from an older build.
func resetSchema(tb testing.TB, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) {
	tb.Helper()
	if _, err := db.ExecContext(context.Background(), "DROP TABLE IF EXISTS user_auth, push_certs, cert_associations, commands, enrollments, schema_migrations CASCADE"); err != nil {
		tb.Fatal(err)
	}
}

// seed100k prepares one enrollment with 100,000 pending commands.
func seed100k(tb testing.TB, s *postgres.Store) mdm.EnrollmentID {
	tb.Helper()
	ctx := context.Background()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "bench"}
	if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, time.Now()); err != nil {
		tb.Fatal(err)
	}
	sqltest.Seed(ctx, tb, s.DB(), postgres.Dialect, id.ID, 100_000)
	return id
}

// TestClear100kUnderOneSecond is the phase 4 exit criterion from the plan:
// Clear on 100k rows under 1s on PostgreSQL (NanoMDM #260 took minutes).
// The assertion is skipped under the race detector (make test-storage-perf
// runs it without) and STORAGE_TIMING=off downgrades it to a log line on
// slow machines.
func TestClear100kUnderOneSecond(t *testing.T) {
	s := openFresh(t)
	id := seed100k(t, s)
	ctx := context.Background()
	start := time.Now()
	n, err := s.Clear(ctx, id, storage.ClearFilter{})
	elapsed := time.Since(start)
	if err != nil || n != 100_000 {
		t.Fatalf("cleared %d: %v", n, err)
	}
	t.Logf("Clear of 100k commands took %s", elapsed)
	if elapsed > time.Second && !raceEnabled && os.Getenv("STORAGE_TIMING") != "off" {
		t.Fatalf("Clear of 100k commands took %s, want under 1s (set STORAGE_TIMING=off on slow machines)", elapsed)
	}
	if next, err := s.Next(ctx, id, false, time.Now()); err != nil || next != nil {
		t.Fatalf("queue not empty after Clear: %v %v", next, err)
	}
}

func BenchmarkClear100k(b *testing.B) {
	ctx := context.Background()
	for range b.N {
		b.StopTimer()
		s := openFresh(b)
		id := seed100k(b, s)
		b.StartTimer()
		if n, err := s.Clear(ctx, id, storage.ClearFilter{}); err != nil || n != 100_000 {
			b.Fatalf("cleared %d: %v", n, err)
		}
		b.StopTimer()
	}
}

func TestContract(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	if _, err := postgres.Open(ctx, "postgres://nobody:x@127.0.0.1:1/none", postgres.Options{}); err == nil {
		t.Fatal("unreachable server")
	}
	s, err := postgres.Open(ctx, dsn, postgres.Options{Pool: sqlcommon.Pool{MaxOpenConns: 8, MaxIdleConns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Start from an empty schema every run.
	resetSchema(t, s.DB())
	// A conflicting table makes Open fail at migration time.
	if _, err := s.DB().ExecContext(ctx, "CREATE TABLE enrollments (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.Open(ctx, dsn, postgres.Options{}); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("open over a conflicting schema: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE enrollments"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlcommon.Migrate(ctx, s.DB(), postgres.Dialect); err != nil {
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
	enc, err := postgres.Open(ctx, dsn, postgres.Options{SkipMigrate: true, Keyring: k})
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
	again, err := postgres.Open(ctx, dsn, postgres.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}
