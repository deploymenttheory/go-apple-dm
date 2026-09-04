package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/postgres"
)

func TestOpenArgumentErrors(t *testing.T) {
	t.Parallel()
	if _, err := postgres.Open(context.Background(), "", postgres.Options{}); !errors.Is(err, postgres.ErrDSNRequired) {
		t.Fatalf("empty DSN: %v", err)
	}
	// pgx parses the DSN when the connector is opened, so a malformed URL
	// fails before any connection attempt.
	if _, err := postgres.Open(context.Background(), "postgres://%zz", postgres.Options{}); err == nil || errors.Is(err, postgres.ErrDSNRequired) {
		t.Fatalf("malformed DSN: %v", err)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()
	dup := &pgconn.PgError{Code: "23505"}
	if !postgres.IsUniqueViolation(dup) || !postgres.IsUniqueViolation(errors.Join(errors.New("wrapped"), dup)) {
		t.Fatal("unique violation not detected")
	}
	for name, err := range map[string]error{
		"nil":        nil,
		"other code": &pgconn.PgError{Code: "42P01"},
		"plain":      errors.New("x"),
	} {
		if postgres.IsUniqueViolation(err) {
			t.Errorf("%s reported as unique violation", name)
		}
	}
}
