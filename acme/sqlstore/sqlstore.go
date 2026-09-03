package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlcommon"
)

//go:embed migrations/*/*.sql
var migrationFS embed.FS

// MigrationsTable records the applied versions of the ACME schema,
// separate from the storage, DDM, and DEP tables so the version sequences
// never mix.
const MigrationsTable = "acme_schema_migrations"

// ErrUnsupportedDialect is returned for a dialect without embedded
// migrations (anything but sqlite, postgres, and mysql).
var ErrUnsupportedDialect = errors.New("sqlstore: unsupported dialect")

// MigrationSet returns the ACME migrations for the dialect.
func MigrationSet(d sqlcommon.Dialect) (sqlcommon.MigrationSet, error) {
	switch d.Name {
	case "sqlite", "postgres", "mysql":
		return sqlcommon.MigrationSet{Table: MigrationsTable, FS: sqlcommon.MustSub(migrationFS, "migrations/"+d.Name)}, nil
	default:
		return sqlcommon.MigrationSet{}, fmt.Errorf("%w: %q", ErrUnsupportedDialect, d.Name)
	}
}

// Migrate applies every pending ACME migration and returns the versions
// applied.
func Migrate(ctx context.Context, db *sql.DB, d sqlcommon.Dialect) ([]int, error) {
	set, err := MigrationSet(d)
	if err != nil {
		return nil, err
	}
	applied, err := sqlcommon.MigrateSet(ctx, db, d, set)
	if err != nil {
		return applied, wrap("migrate", err)
	}
	return applied, nil
}

// Rollback reverts ACME migrations newer than target (0 reverts all).
func Rollback(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, target int) ([]int, error) {
	set, err := MigrationSet(d)
	if err != nil {
		return nil, err
	}
	reverted, err := sqlcommon.RollbackSet(ctx, db, d, set, target)
	if err != nil {
		return reverted, wrap("rollback", err)
	}
	return reverted, nil
}

// Version returns the highest applied ACME migration (0 when none).
func Version(ctx context.Context, db *sql.DB, d sqlcommon.Dialect) (int, error) {
	if _, err := MigrationSet(d); err != nil {
		return 0, err
	}
	v, err := sqlcommon.VersionOf(ctx, db, d, MigrationsTable)
	if err != nil {
		return 0, wrap("version", err)
	}
	return v, nil
}

// Options tune Open.
//
// There is no Keyring here, unlike the DEP and MDM stores. Nothing this
// package holds is a secret at rest: an account key is a public JWK, the
// attestation object is a signed statement the device sent in clear, an
// issued certificate is published, and a nonce is worthless the moment it
// is taken. A keyring would add a strict-mode failure path guarding
// nothing.
type Options struct {
	// SkipMigrate leaves the schema alone; the caller has run Migrate.
	SkipMigrate bool
}

// Store implements acme.Store over a *sql.DB it does not own: closing the
// pool is the caller's job.
type Store struct {
	db *sql.DB
	d  sqlcommon.Dialect
	// mysql selects the syntax MySQL lacks in common with the others: it
	// has no DELETE ... RETURNING for taking a nonce in one statement.
	mysql bool
}

var _ acme.Store = (*Store)(nil)

// Open wraps an opened pool for the dialect and, unless o.SkipMigrate,
// applies pending migrations.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, o Options) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", acme.ErrInvalid)
	}
	set, err := MigrationSet(d)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, wrap("connect "+d.Name, err)
	}
	if !o.SkipMigrate {
		if _, err := sqlcommon.MigrateSet(ctx, db, d, set); err != nil {
			return nil, wrap("migrate", err)
		}
	}
	return &Store{db: db, d: d, mysql: d.Name == "mysql"}, nil
}

// DB exposes the pool for health checks and tests.
func (s *Store) DB() *sql.DB { return s.db }

func wrap(op string, err error) error { return fmt.Errorf("sqlstore: %s: %w", op, err) }

// validID rejects an empty identifier, thumbprint, or nonce value.
func validID(what, id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty %s", acme.ErrInvalid, what)
	}
	return nil
}

// notFound builds an ErrNotFound for one named record.
func notFound(what, id string) error {
	return fmt.Errorf("%w: %s %q", acme.ErrNotFound, what, id)
}

// encode renders a whole record as the JSON of a record column.
// Deterministic output costs nothing and makes the same value the same
// bytes, so a row can be compared between engines and between backups.
func encode(what string, v any) ([]byte, error) {
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("%w: encode %s: %w", acme.ErrInvalid, what, err)
	}
	return raw, nil
}

// decode reads a record column. A row that will not decode names its
// table and its id, because a zero record returned in its place would
// look to the server like a record that was never written: an order with
// no status, or a challenge whose attestation vanished.
func decode(table, id string, raw []byte, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("sqlstore: %s %q: decode record: %w", table, id, err)
	}
	return nil
}

// nullTime writes a zero time as NULL. MySQL refuses a zero DATETIME, and
// the record column keeps the value the caller gave, so the indexed copy
// only has to say "no time here" for the queries that filter on it.
func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

// fromNull reads a nullable timestamp as a value.
func fromNull(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

// placeholders returns "?, ?, ?" for n values.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// pageLimit applies the default page size.
func pageLimit(p storage.Page) int {
	if p.Limit <= 0 {
		return storage.DefaultPageSize
	}
	return p.Limit
}
