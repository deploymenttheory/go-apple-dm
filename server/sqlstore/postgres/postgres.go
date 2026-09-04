package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/crypt"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrDSNRequired is returned by Open for an empty DSN.
var ErrDSNRequired = errors.New("postgres: DSN is required")

// uniqueViolation is the SQLSTATE for a unique constraint failure.
const uniqueViolation = "23505"

// Dialect is the PostgreSQL dialect.
var Dialect = sqlcommon.Dialect{
	Name:              "postgres",
	Dollar:            true,
	ForUpdate:         "FOR UPDATE",
	Upsert:            sqlcommon.UpsertOnConflict,
	Migrations:        sqlcommon.MustSub(migrationFS, "migrations"),
	InsertIgnore:      sqlcommon.InsertIgnoreOnConflict,
	IsUniqueViolation: IsUniqueViolation,
}

// IsUniqueViolation reports whether err is a PostgreSQL unique violation.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

// Store is the PostgreSQL backend.
type Store struct{ *sqlcommon.Store }

var _ storage.Store = (*Store)(nil)

// Options tune Open.
type Options struct {
	SkipMigrate bool
	Pool        sqlcommon.Pool
	// Keyring seals the secret columns (decision record 0013); nil keeps
	// them in plaintext.
	Keyring *crypt.Keyring
}

// Open connects with a pgx DSN or URL and migrates the schema. The DSN is
// parsed before any connection attempt, so a malformed one fails fast.
func Open(ctx context.Context, dsn string, o Options) (*Store, error) {
	if dsn == "" {
		return nil, ErrDSNRequired
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DSN: %w", err)
	}
	db := stdlib.OpenDB(*cfg)
	o.Pool.Apply(db)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if !o.SkipMigrate {
		if _, err := sqlcommon.Migrate(ctx, db, Dialect); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Store{Store: sqlcommon.New(db, Dialect, sqlcommon.WithKeyring(o.Keyring))}, nil
}
