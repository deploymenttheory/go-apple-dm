// Package sqlite is the SQLite storage backend on modernc.org/sqlite (pure
// Go, no cgo): WAL journal, foreign keys, a busy timeout, and immediate
// write transactions so concurrent writers queue instead of failing
// (decision record 0012).
//
// Apple documentation on what an enrollment must retain and on NotNow:
// https://developer.apple.com/documentation/devicemanagement/check-in
// https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	msqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/deploymenttheory/go-apple-mdm/storage"
	"github.com/deploymenttheory/go-apple-mdm/storage/crypt"
	"github.com/deploymenttheory/go-apple-mdm/storage/sqlcommon"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrPathRequired is returned by Open for an empty path.
var ErrPathRequired = errors.New("sqlite: path is required")

// Dialect is the SQLite dialect.
var Dialect = sqlcommon.Dialect{
	Name:              "sqlite",
	Upsert:            sqlcommon.UpsertOnConflict,
	Migrations:        sqlcommon.MustSub(migrationFS, "migrations"),
	InsertIgnore:      sqlcommon.InsertIgnoreOnConflict,
	IsUniqueViolation: IsUniqueViolation,
}

// IsUniqueViolation reports whether err is a SQLite unique or primary key
// constraint failure.
func IsUniqueViolation(err error) bool {
	var e *msqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || e.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

// Store is the SQLite backend.
type Store struct{ *sqlcommon.Store }

var _ storage.Store = (*Store)(nil)

// Options tune Open.
type Options struct {
	// BusyTimeout waits for a locked database; default 10s.
	BusyTimeout time.Duration
	// Migrate applies pending migrations on open; default true unless
	// SkipMigrate is set.
	SkipMigrate bool
	// Keyring seals the secret columns (decision record 0013); nil keeps
	// them in plaintext.
	Keyring *crypt.Keyring
}

// DSN renders the driver connection string for a database file with the
// pragmas the backend relies on.
func DSN(path string, o Options) string {
	if o.BusyTimeout <= 0 {
		o.BusyTimeout = 10 * time.Second
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout("+strconv.FormatInt(o.BusyTimeout.Milliseconds(), 10)+")")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	q.Set("_time_format", "sqlite")
	return "file:" + path + "?" + q.Encode()
}

// Open opens (creating if needed) the database file and migrates it.
func Open(ctx context.Context, path string, o Options) (*Store, error) {
	if path == "" {
		return nil, ErrPathRequired
	}
	db, err := sql.Open("sqlite", DSN(path, o))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: connect %s: %w", path, err)
	}
	if !o.SkipMigrate {
		if _, err := sqlcommon.Migrate(ctx, db, Dialect); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Store{Store: sqlcommon.New(db, Dialect, sqlcommon.WithKeyring(o.Keyring))}, nil
}
