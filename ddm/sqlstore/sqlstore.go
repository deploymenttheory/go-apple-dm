package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
	"github.com/deploymenttheory/go-apple-mdm/storage/sqlcommon"
)

//go:embed migrations/*/*.sql
var migrationFS embed.FS

// MigrationsTable records the applied versions of the DDM schema, separate
// from the storage schema's table so the two version sequences never mix.
const MigrationsTable = "ddm_schema_migrations"

// ErrUnsupportedDialect is returned for a dialect without embedded
// migrations (anything but sqlite, postgres, and mysql).
var ErrUnsupportedDialect = errors.New("sqlstore: unsupported dialect")

// MigrationSet returns the DDM migrations for the dialect.
func MigrationSet(d sqlcommon.Dialect) (sqlcommon.MigrationSet, error) {
	switch d.Name {
	case "sqlite", "postgres", "mysql":
		return sqlcommon.MigrationSet{Table: MigrationsTable, FS: sqlcommon.MustSub(migrationFS, "migrations/"+d.Name)}, nil
	default:
		return sqlcommon.MigrationSet{}, fmt.Errorf("%w: %q", ErrUnsupportedDialect, d.Name)
	}
}

// Migrate applies every pending DDM migration and returns the versions
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

// Rollback reverts DDM migrations newer than target (0 reverts all).
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

// Version returns the highest applied DDM migration (0 when none).
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
type Options struct {
	// SkipMigrate leaves the schema alone; the caller has run Migrate.
	SkipMigrate bool
}

// Store implements ddm.Store over a *sql.DB it does not own: closing the
// pool is the caller's job.
type Store struct {
	db *sql.DB
	d  sqlcommon.Dialect
	// mysql selects the syntax MySQL lacks in common with the others: the
	// upsert clause and RETURNING.
	mysql bool
}

var _ ddm.Store = (*Store)(nil)

// Open wraps an opened pool for the dialect and, unless o.SkipMigrate,
// applies pending migrations.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, o Options) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ddm.ErrInvalid)
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

// validID maps an ill-formed enrollment id to ddm.ErrInvalid.
func validID(id mdm.EnrollmentID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ddm.ErrInvalid, err)
	}
	return nil
}

// validName rejects an empty name, identifier, token, or path.
func validName(what, name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty %s", ddm.ErrInvalid, what)
	}
	return nil
}

// notFound builds an ErrNotFound for one named record.
func notFound(what, name string) error {
	return fmt.Errorf("%w: %s %q", ddm.ErrNotFound, what, name)
}

// utc normalises a timestamp before it is written.
func utc(t time.Time) time.Time { return t.UTC() }

// nullBytes stores an empty slice as NULL so it reads back as nil.
func nullBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// nonNil stores a nil slice as an empty value for NOT NULL columns.
func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
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

// after appends the keyset condition for a string cursor.
func after(where []string, args []any, key string, p storage.Page) ([]string, []any) {
	if p.Cursor == "" {
		return where, args
	}
	return append(where, key+" > ?"), append(args, p.Cursor)
}

// scanner is *sql.Row or *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// scanEnrollmentID reads the (enrollment_id, channel, parent_id) triple
// every enrollment-keyed table stores, so results carry the full identity.
func scanEnrollmentID(row scanner, id *mdm.EnrollmentID) error {
	var channel int
	if err := row.Scan(&id.ID, &channel, &id.ParentID); err != nil {
		return wrap("scan enrollment id", err)
	}
	id.Channel = mdm.Channel(channel) // #nosec G115 -- stored from a uint8
	return nil
}
