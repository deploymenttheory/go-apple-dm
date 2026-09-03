//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver

	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/storage/postgres"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlite"
)

// newStore opens the storage backend named by E2E_STORE: sqlite (the
// default, one file per test), postgres (one schema per test, needs
// TEST_POSTGRES_DSN), or inmem.
func newStore(t *testing.T) storage.Store {
	t.Helper()
	ctx := context.Background()
	switch backend := os.Getenv("E2E_STORE"); backend {
	case "", "sqlite":
		s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e2e.db"), sqlite.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		return s
	case "inmem":
		return inmem.New()
	case "postgres":
		return newPostgresStore(t)
	default:
		t.Fatalf("E2E_STORE=%q: want sqlite, postgres, or inmem", backend)
		return nil
	}
}

// newPostgresStore creates a throwaway schema on the TEST_POSTGRES_DSN
// server and opens the store with search_path pointed at it, so parallel
// tests never share tables. The schema is dropped after the store closes.
func newPostgresStore(t *testing.T) storage.Store {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("E2E_STORE=postgres needs TEST_POSTGRES_DSN (make testdb-up prints it)")
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	name := "e2e_" + hex.EncodeToString(b[:])

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G202 -- name is generated hex, not user input.
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// #nosec G202 -- name is generated hex, not user input.
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})

	// pgx forwards unknown URL parameters as runtime parameters.
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	s, err := postgres.Open(ctx, u.String(), postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Registered after the drop cleanup so it runs first (cleanups are LIFO).
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
