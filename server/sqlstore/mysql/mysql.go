package mysql

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/crypt"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrDSNRequired is returned by Open for an empty DSN.
var ErrDSNRequired = errors.New("mysql: DSN is required")

// errDupEntry is MySQL's duplicate key error number.
const errDupEntry = 1062

// Dialect is the MySQL dialect.
var Dialect = sqlcommon.Dialect{
	Name:              "mysql",
	ForUpdate:         "FOR UPDATE",
	Upsert:            sqlcommon.UpsertOnDuplicateKey,
	Migrations:        sqlcommon.MustSub(migrationFS, "migrations"),
	InsertIgnore:      sqlcommon.InsertIgnoreDuplicateKey,
	IsUniqueViolation: IsUniqueViolation,
}

// IsUniqueViolation reports whether err is a MySQL duplicate key error.
func IsUniqueViolation(err error) bool {
	var me *gomysql.MySQLError
	return errors.As(err, &me) && me.Number == errDupEntry
}

// Store is the MySQL backend.
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

// NormalizeDSN forces the settings the backend needs: parseTime, UTC,
// utf8mb4, and clientFoundRows so RowsAffected reports matched rows (as
// PostgreSQL and SQLite do) and idempotent writes are not mistaken for
// missing rows.
func NormalizeDSN(dsn string) (string, error) {
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("mysql: parse DSN: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.ClientFoundRows = true
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["charset"] = "utf8mb4"
	cfg.Params["time_zone"] = "'+00:00'"
	return cfg.FormatDSN(), nil
}

// Open connects with a go-sql-driver DSN and migrates the schema.
func Open(ctx context.Context, dsn string, o Options) (*Store, error) {
	if dsn == "" {
		return nil, ErrDSNRequired
	}
	normalized, err := NormalizeDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		return nil, fmt.Errorf("mysql: open: %w", err)
	}
	o.Pool.Apply(db)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("mysql: connect: %w", err)
	}
	if !o.SkipMigrate {
		if _, err := sqlcommon.Migrate(ctx, db, Dialect); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Store{Store: sqlcommon.New(db, Dialect, sqlcommon.WithKeyring(o.Keyring))}, nil
}
