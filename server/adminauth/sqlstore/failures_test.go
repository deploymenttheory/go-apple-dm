package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlite"
)

// Every method surfaces a database failure rather than reporting empty state.
// An authorization store that answered "no principals" when the database was
// unreachable would fail open at the worst moment.
func TestWriteFailuresSurface(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "f.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Seed one of each so the read paths have something to find before the
	// pool goes away.
	if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "alice"}, "d", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "p", Source: "permit (principal, action, resource);"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	for name, call := range map[string]func() error{
		"CreatePrincipal": func() error {
			_, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "bob"}, "d2", now)
			return err
		},
		"Principal":         func() error { _, err := s.Principal(ctx, "alice"); return err },
		"PrincipalByDigest": func() error { _, err := s.PrincipalByDigest(ctx, "d"); return err },
		"Principals":        func() error { _, err := s.Principals(ctx, adminauth.Page{}); return err },
		"UpdatePrincipal": func() error {
			_, err := s.UpdatePrincipal(ctx, "alice", []string{"r"}, false, now)
			return err
		},
		"SetToken": func() error {
			_, err := s.SetToken(ctx, "alice", "d3", "id", time.Time{}, now)
			return err
		},
		"RevokeToken":     func() error { return s.RevokeToken(ctx, "alice", now) },
		"DeletePrincipal": func() error { return s.DeletePrincipal(ctx, "alice") },
		"CountRoot":       func() error { _, err := s.CountRoot(ctx); return err },
		"PutPolicy": func() error {
			_, err := s.PutPolicy(ctx, adminauth.Policy{Name: "p2", Source: "permit (principal, action, resource);"}, now)
			return err
		},
		"GetPolicy":     func() error { _, err := s.GetPolicy(ctx, "p"); return err },
		"Policies":      func() error { _, err := s.Policies(ctx); return err },
		"DeletePolicy":  func() error { return s.DeletePolicy(ctx, "p") },
		"PolicyVersion": func() error { _, err := s.PolicyVersion(ctx); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatalf("%s returned no error with the pool closed", name)
			}
		})
	}
}

// A closed pool must not be mistaken for a missing row: ErrNotFound would tell
// a caller the principal does not exist when the truth is that storage is
// unreachable.
func TestClosedPoolIsNotNotFound(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "n.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = s.Principal(ctx, "alice")
	if err == nil || errors.Is(err, adminauth.ErrNotFound) {
		t.Fatalf("err = %v, want a storage failure rather than ErrNotFound", err)
	}
}

// Migrate, Rollback, and Version all surface a failure from the pool too.
func TestMigrationFailuresSurface(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "m.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Migrate(ctx, db, sqlite.Dialect); err == nil {
		t.Fatal("Migrate on a closed pool returned no error")
	}
	if _, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0); err == nil {
		t.Fatal("Rollback on a closed pool returned no error")
	}
	if _, err := sqlstore.Version(ctx, db, sqlite.Dialect); err == nil {
		t.Fatal("Version on a closed pool returned no error")
	}
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err == nil {
		t.Fatal("Open on a closed pool returned no error")
	}
	// Rollback and Migrate also refuse an unsupported dialect.
	bad := sqlite.Dialect
	bad.Name = "oracle"
	if _, err := sqlstore.Migrate(ctx, db, bad); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := sqlstore.Rollback(ctx, db, bad, 0); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("Rollback: %v", err)
	}
}
