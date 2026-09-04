package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/crypt"
)

//go:embed migrations/*/*.sql
var migrationFS embed.FS

// MigrationsTable records the applied versions of the DEP schema, separate
// from the storage and DDM tables so the version sequences never mix.
const MigrationsTable = "dep_schema_migrations"

// Purposes name the sealed columns; each is the AAD prefix binding a
// ciphertext to its column (decision record 0013).
const (
	PurposeConsumerSecret = "dep_accounts.consumer_secret" // #nosec G101 -- a column name, not a credential
	PurposeAccessToken    = "dep_accounts.access_token"    // #nosec G101 -- a column name, not a credential
	PurposeAccessSecret   = "dep_accounts.access_secret"   // #nosec G101 -- a column name, not a credential
	PurposeSession        = "dep_sessions.token"           // #nosec G101 -- a column name, not a credential
	PurposeKeyPEM         = "dep_keypairs.key_pem"         // #nosec G101 -- a column name, not a credential
)

// ErrUnsupportedDialect is returned for a dialect without embedded
// migrations (anything but sqlite, postgres, and mysql).
var ErrUnsupportedDialect = errors.New("sqlstore: unsupported dialect")

// MigrationSet returns the DEP migrations for the dialect.
func MigrationSet(d sqlcommon.Dialect) (sqlcommon.MigrationSet, error) {
	switch d.Name {
	case "sqlite", "postgres", "mysql":
		return sqlcommon.MigrationSet{Table: MigrationsTable, FS: sqlcommon.MustSub(migrationFS, "migrations/"+d.Name)}, nil
	default:
		return sqlcommon.MigrationSet{}, fmt.Errorf("%w: %q", ErrUnsupportedDialect, d.Name)
	}
}

// Migrate applies every pending DEP migration and returns the versions
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

// Rollback reverts DEP migrations newer than target (0 reverts all).
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

// Version returns the highest applied DEP migration (0 when none).
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
	// Keyring seals the OAuth secrets, session tokens, and private keys
	// (decision record 0013); nil keeps them in plaintext.
	Keyring *crypt.Keyring
}

// Store implements dep.Store over a *sql.DB it does not own: closing the
// pool is the caller's job.
type Store struct {
	db      *sql.DB
	d       sqlcommon.Dialect
	keyring *crypt.Keyring
	// mysql selects the syntax MySQL lacks in common with the others.
	mysql bool
}

var _ dep.Store = (*Store)(nil)

// Open wraps an opened pool for the dialect and, unless o.SkipMigrate,
// applies pending migrations.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, o Options) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", dep.ErrInvalid)
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
	return &Store{db: db, d: d, keyring: o.Keyring, mysql: d.Name == "mysql"}, nil
}

// DB exposes the pool for health checks and tests.
func (s *Store) DB() *sql.DB { return s.db }

func wrap(op string, err error) error { return fmt.Errorf("sqlstore: %s: %w", op, err) }

// seal encrypts b for the column purpose and row; empty input stays nil.
func (s *Store) seal(purpose, rowID string, b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if s.keyring == nil {
		return b, nil
	}
	out, err := s.keyring.Seal(b, crypt.AAD(purpose, rowID))
	if err != nil {
		return nil, wrap("seal "+purpose, err)
	}
	return out, nil
}

// open decrypts a stored column value. Plaintext rows are returned as is
// until the keyring is Strict; a sealed row without a keyring is an error.
func (s *Store) open(purpose, rowID string, b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if !crypt.IsSealed(b) {
		if s.keyring != nil && s.keyring.Strict() {
			return nil, fmt.Errorf("%w: %s for %s", crypt.ErrUnsealed, purpose, rowID)
		}
		return append([]byte(nil), b...), nil
	}
	if s.keyring == nil {
		return nil, fmt.Errorf("%w: %s for %s is sealed", crypt.ErrNoKeyring, purpose, rowID)
	}
	pt, _, err := s.keyring.Open(b, crypt.AAD(purpose, rowID))
	if err != nil {
		return nil, wrap("open "+purpose, err)
	}
	return pt, nil
}

// RawSecrets returns the stored bytes of every secret column of the
// account as they rest in the database, keyed consumer_secret,
// access_token, access_secret, session, and key_pem:<stage>. It lets the
// contract suite prove sealing.
func (s *Store) RawSecrets(ctx context.Context, name string) (map[string][]byte, error) {
	if err := validName("account name", name); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	v := s.view()
	var cs, at, as []byte
	found, err := v.row(ctx, "raw account", "SELECT consumer_secret, access_token, access_secret FROM dep_accounts WHERE name = ?", []any{name}, &cs, &at, &as)
	if err != nil {
		return nil, err
	}
	if found {
		out["consumer_secret"], out["access_token"], out["access_secret"] = cs, at, as
	}
	var tok []byte
	if found, err = v.row(ctx, "raw session", "SELECT token FROM dep_sessions WHERE account = ?", []any{name}, &tok); err != nil {
		return nil, err
	}
	if found {
		out["session"] = tok
	}
	err = v.each(ctx, "raw keypairs", "SELECT stage, key_pem FROM dep_keypairs WHERE account = ?", []any{name}, func(rows *sql.Rows) error {
		var stage string
		var key []byte
		if err := rows.Scan(&stage, &key); err != nil {
			return wrap("scan raw keypair", err)
		}
		out["key_pem:"+stage] = key
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// validName rejects an empty name, serial, UUID, or stage.
func validName(what, name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty %s", dep.ErrInvalid, what)
	}
	return nil
}

// notFound builds an ErrNotFound for one named record.
func notFound(what, name string) error {
	return fmt.Errorf("%w: %s %q", dep.ErrNotFound, what, name)
}

// utc normalises a timestamp before it is written.
func utc(t time.Time) time.Time { return t.UTC() }

// nullTime writes a zero or nil time as NULL.
func nullTime(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

// timePtr reads a nullable timestamp.
func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
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
func pageLimit(p paging.Page) int {
	return p.Size()
}
