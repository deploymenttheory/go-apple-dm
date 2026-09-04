package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlcommon"
)

//go:embed migrations/*/*.sql
var migrationFS embed.FS

// MigrationsTable records the applied versions of the audit schema, separate
// from the storage, DDM, DEP, ACME, and admin tables so the version sequences
// never mix.
const MigrationsTable = "audit_schema_migrations"

// ErrUnsupportedDialect is returned for a dialect without embedded migrations.
var ErrUnsupportedDialect = errors.New("sqlstore: unsupported dialect")

// MigrationSet returns the audit migrations for the dialect.
func MigrationSet(d sqlcommon.Dialect) (sqlcommon.MigrationSet, error) {
	switch d.Name {
	case "sqlite", "postgres", "mysql":
		return sqlcommon.MigrationSet{Table: MigrationsTable, FS: sqlcommon.MustSub(migrationFS, "migrations/"+d.Name)}, nil
	default:
		return sqlcommon.MigrationSet{}, fmt.Errorf("%w: %q", ErrUnsupportedDialect, d.Name)
	}
}

// Migrate applies every pending audit migration and returns the versions
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

// Rollback reverts audit migrations newer than target (0 reverts all).
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

// Version returns the highest applied audit migration (0 when none).
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

// Store implements audit.Store over a *sql.DB it does not own: closing the
// pool is the caller's job.
type Store struct {
	db *sql.DB
	d  sqlcommon.Dialect
}

var _ audit.Store = (*Store)(nil)

// Open wraps an opened pool for the dialect and, unless o.SkipMigrate,
// applies pending migrations.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, o Options) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", audit.ErrInvalid)
	}
	if _, err := MigrationSet(d); err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, wrap("connect "+d.Name, err)
	}
	if !o.SkipMigrate {
		if _, err := Migrate(ctx, db, d); err != nil {
			return nil, err
		}
	}
	return &Store{db: db, d: d}, nil
}

// DB exposes the pool for health checks and tests.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) q(query string) string { return s.d.Rebind(query) }

func wrap(op string, err error) error { return fmt.Errorf("sqlstore: %s: %w", op, err) }

const recordCols = "id, at, type, actor, channel, enrollment_id, parent_id, fields"

// Append implements audit.Store.
//
// The id is read back rather than assumed, because the three dialects assign
// it differently and the value is the pagination cursor.
func (s *Store) Append(ctx context.Context, rec audit.Record) (audit.Record, error) {
	if rec.Type == "" {
		return audit.Record{}, audit.ErrInvalid
	}
	fields, err := encodeFields(rec.Fields)
	if err != nil {
		return audit.Record{}, err
	}
	at := rec.At.UTC()
	args := []any{at, rec.Type, rec.Actor, channelOf(rec.Enrollment), rec.Enrollment.ID, rec.Enrollment.ParentID, fields}
	const insert = `INSERT INTO audit_records (at, type, actor, channel, enrollment_id, parent_id, fields) VALUES (?, ?, ?, ?, ?, ?, ?)`

	var id int64
	if s.d.Dollar {
		// PostgreSQL has no LastInsertId; RETURNING is the portable-enough
		// alternative and keeps this to one round trip.
		if err := s.db.QueryRowContext(ctx, s.q(insert+" RETURNING id"), args...).Scan(&id); err != nil {
			return audit.Record{}, wrap("append", err)
		}
	} else {
		res, err := s.db.ExecContext(ctx, s.q(insert), args...)
		if err != nil {
			return audit.Record{}, wrap("append", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return audit.Record{}, wrap("append id", err)
		}
	}
	rec.ID, rec.At = id, at
	return rec, nil
}

// List implements audit.Store, newest first.
func (s *Store) List(ctx context.Context, q audit.Query, p audit.Page) (audit.Result[audit.Record], error) {
	var (
		where []string
		args  []any
	)
	if p.Cursor != "" {
		before, err := strconv.ParseInt(p.Cursor, 10, 64)
		if err != nil {
			return audit.Result[audit.Record]{}, audit.ErrInvalid
		}
		where, args = append(where, "id < ?"), append(args, before)
	}
	if q.Type != "" {
		where, args = append(where, "type = ?"), append(args, q.Type)
	}
	if q.Actor != "" {
		where, args = append(where, "actor = ?"), append(args, q.Actor)
	}
	if q.Enrollment != "" {
		where, args = append(where, "enrollment_id = ?"), append(args, q.Enrollment)
	}
	if !q.Since.IsZero() {
		where, args = append(where, "at >= ?"), append(args, q.Since.UTC())
	}
	if !q.Until.IsZero() {
		where, args = append(where, "at < ?"), append(args, q.Until.UTC())
	}

	limit := p.Size()
	query := "SELECT " + recordCols + " FROM audit_records"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	// One more than the page, so a next cursor is known without a count.
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return audit.Result[audit.Record]{}, wrap("list", err)
	}
	defer rows.Close()

	out := audit.Result[audit.Record]{}
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return audit.Result[audit.Record]{}, wrap("list scan", err)
		}
		out.Items = append(out.Items, rec)
	}
	if err := rows.Err(); err != nil {
		return audit.Result[audit.Record]{}, wrap("list rows", err)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = strconv.FormatInt(out.Items[len(out.Items)-1].ID, 10)
	}
	return out, nil
}

// Get implements audit.Store.
func (s *Store) Get(ctx context.Context, id int64) (audit.Record, error) {
	row := s.db.QueryRowContext(ctx, s.q("SELECT "+recordCols+" FROM audit_records WHERE id = ?"), id)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.Record{}, audit.ErrNotFound
	}
	if err != nil {
		return audit.Record{}, wrap("get", err)
	}
	return rec, nil
}

// Prune implements audit.Store. Retention is the only way a record leaves the
// trail, so this is one statement with no filter but age.
func (s *Store) Prune(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, s.q("DELETE FROM audit_records WHERE at < ?"), before.UTC())
	if err != nil {
		return 0, wrap("prune", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, wrap("prune count", err)
	}
	return int(n), nil
}

// scanner is what a *sql.Row and *sql.Rows have in common.
type scanner interface{ Scan(dest ...any) error }

func scanRecord(sc scanner) (audit.Record, error) {
	var (
		rec              audit.Record
		channel, fields  string
		id               int64
		at               time.Time
		enrollID, parent string
	)
	if err := sc.Scan(&id, &at, &rec.Type, &rec.Actor, &channel, &enrollID, &parent, &fields); err != nil {
		return audit.Record{}, err
	}
	rec.ID, rec.At = id, at.UTC()
	rec.Enrollment = mdm.EnrollmentID{Channel: channelFrom(channel), ID: enrollID, ParentID: parent}
	decoded, err := decodeFields(fields)
	if err != nil {
		return audit.Record{}, err
	}
	rec.Fields = decoded
	return rec, nil
}

func encodeFields(f map[string]any) (string, error) {
	if len(f) == 0 {
		return "", nil
	}
	b, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("%w: fields: %w", audit.ErrInvalid, err)
	}
	return string(b), nil
}

func decodeFields(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, wrap("decode fields", err)
	}
	return out, nil
}

// channelOf and channelFrom store the channel as its documented name rather
// than the numeric constant, so the column survives a renumbering and reads
// the same way the rest of the API spells it.
func channelOf(id mdm.EnrollmentID) string {
	if id.ID == "" && id.Channel == mdm.ChannelUnknown {
		return ""
	}
	return id.Channel.String()
}

func channelFrom(s string) mdm.Channel {
	for _, c := range []mdm.Channel{
		mdm.ChannelDevice, mdm.ChannelUser, mdm.ChannelSharedIPadUser,
		mdm.ChannelUserEnrollmentDevice, mdm.ChannelUserEnrollmentUser,
	} {
		if c.String() == s {
			return c
		}
	}
	return mdm.ChannelUnknown
}
