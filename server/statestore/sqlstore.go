package statestore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/state"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

//go:embed migrations/*/*.sql
var migrations embed.FS

// Store implements state.Store with SQL transactions and authoritative database time.
type Store struct {
	keyring *crypt.Keyring
	db      *sql.DB
	d       sqlcommon.Dialect
}

var _ state.Store = (*Store)(nil)

// Open applies the separate state schema migrations and wraps the caller's pool.
func Open(
	ctx context.Context,
	db *sql.DB,
	d sqlcommon.Dialect,
	keys ...*crypt.Keyring,
) (*Store, error) {
	if db == nil || d.Upsert == nil {
		return nil, state.ErrInvalid
	}
	switch d.Name {
	case "sqlite", "postgres", "mysql":
	default:
		return nil, fmt.Errorf("statestore: unsupported dialect %q", d.Name)
	}
	set := sqlcommon.MigrationSet{
		Table: "state_schema_migrations",
		FS:    sqlcommon.MustSub(migrations, "migrations/"+d.Name),
	}
	if _, err := sqlcommon.MigrateSet(ctx, db, d, set); err != nil {
		return nil, err
	}
	s := &Store{db: db, d: d}
	if len(keys) > 0 {
		s.keyring = keys[0]
	}
	return s, nil
}

type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type transaction struct {
	keyring *crypt.Keyring
	q       queryer
	d       sqlcommon.Dialect
	now     time.Time
}

func (t *transaction) Now() time.Time { return t.now }
func (t *transaction) Get(ctx context.Context, k string) (state.Record, error) {
	if !state.ValidKey(k) {
		return state.Record{}, state.ErrInvalid
	}
	var r state.Record
	var expires int64
	err := t.q.QueryRowContext(ctx, t.d.Rebind("SELECT record_key, value, expires_at FROM protocol_state WHERE record_key = ?"), k).
		Scan(&r.Key, &r.Value, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return r, state.ErrNotFound
	}
	if expires != 0 {
		r.ExpiresAt = time.UnixMicro(expires).UTC()
	}
	if err == nil && t.keyring != nil {
		r.Value, err = sqlcommon.OpenBlob(t.keyring, "protocol_state.value", r.Value, r.Key)
	}
	return r, err
}

func (t *transaction) List(
	ctx context.Context,
	prefix, after string,
	limit int,
) ([]state.Record, error) {
	if limit <= 0 || limit > 10000 {
		return nil, state.ErrInvalid
	}
	// Key range, not LIKE: '%' and '_' in a namespace are literal characters.
	rows, err := t.q.QueryContext(
		ctx,
		t.d.Rebind(
			"SELECT record_key, value, expires_at FROM protocol_state WHERE record_key >= ? AND record_key < ? AND record_key > ? ORDER BY record_key LIMIT ?",
		),
		prefix,
		prefix+"\x7f",
		after,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []state.Record
	for rows.Next() {
		var r state.Record
		var expires int64
		if err := rows.Scan(&r.Key, &r.Value, &expires); err != nil {
			return nil, err
		}
		if expires != 0 {
			r.ExpiresAt = time.UnixMicro(expires).UTC()
		}
		if t.keyring != nil {
			r.Value, err = sqlcommon.OpenBlob(t.keyring, "protocol_state.value", r.Value, r.Key)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *transaction) Put(ctx context.Context, r state.Record) error {
	if !state.ValidKey(r.Key) {
		return state.ErrInvalid
	}
	var expires int64
	if !r.ExpiresAt.IsZero() {
		expires = r.ExpiresAt.UnixMicro()
	}
	value := r.Value
	if value == nil {
		value = []byte{}
	}
	value, err := sqlcommon.SealBlob(t.keyring, "protocol_state.value", value, r.Key)
	if err != nil {
		return err
	}
	_, err = t.q.ExecContext(
		ctx,
		t.d.Rebind(
			t.d.Upsert(
				"protocol_state",
				[]string{"record_key", "value", "expires_at"},
				[]string{"record_key"},
			),
		),
		r.Key,
		value,
		expires,
	)
	return err
}

func (t *transaction) Delete(ctx context.Context, k string) error {
	if !state.ValidKey(k) {
		return state.ErrInvalid
	}
	_, err := t.q.ExecContext(ctx, t.d.Rebind("DELETE FROM protocol_state WHERE record_key = ?"), k)
	return err
}

// Get implements state.Reader.
func (s *Store) Get(ctx context.Context, k string) (state.Record, error) {
	return (&transaction{q: s.db, d: s.d, keyring: s.keyring}).Get(ctx, k)
}

// List implements state.Reader.
func (s *Store) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	return (&transaction{q: s.db, d: s.d, keyring: s.keyring}).List(ctx, prefix, after, limit)
}

func databaseTime(ctx context.Context, q queryer, d sqlcommon.Dialect) (time.Time, error) {
	query := "SELECT CAST((julianday('now') - 2440587.5) * 86400000000 AS INTEGER)"
	switch d.Name {
	case "postgres":
		query = "SELECT CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000000 AS BIGINT)"
	case "mysql":
		query = "SELECT CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(6)) * 1000000 AS SIGNED)"
	}
	var us int64
	err := q.QueryRowContext(ctx, query).Scan(&us)
	return time.UnixMicro(us).UTC(), err
}

// Update locks fixed shards in ascending order. SQLite's initial UPDATE obtains
// its writer lock before any read, avoiding read-to-write upgrade races. PostgreSQL
// and MySQL use READ COMMITTED so reads after lock acquisition see the last commit.
func (s *Store) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	if len(keys) == 0 || fn == nil {
		return state.ErrInvalid
	}
	var shards []int
	for _, k := range keys {
		if !state.ValidKey(k) {
			return state.ErrInvalid
		}
		h := sha256.Sum256([]byte(k))
		shards = append(shards, int(h[0]))
	}
	slices.Sort(shards)
	shards = slices.Compact(shards)
	opts := &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	if s.d.Name == "sqlite" {
		opts = nil
	}
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, shard := range shards {
		if _, err := tx.ExecContext(
			ctx,
			s.d.Rebind("UPDATE protocol_state_locks SET shard = shard WHERE shard = ?"),
			shard,
		); err != nil {
			return err
		}
	}
	now, err := databaseTime(ctx, tx, s.d)
	if err != nil {
		return err
	}
	if err := fn(&transaction{q: tx, d: s.d, now: now, keyring: s.keyring}); err != nil {
		return err
	}
	return tx.Commit()
}

// Prune uses the same shard locks as writers and rechecks expiry after locking.
func (s *Store) Prune(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 10000 {
		return 0, state.ErrInvalid
	}
	now, err := databaseTime(ctx, s.db, s.d)
	if err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, s.d.Rebind("SELECT record_key FROM protocol_state WHERE expires_at > 0 AND expires_at <= ? ORDER BY record_key LIMIT ?"), now.UnixMicro(), limit)
	if err != nil {
		return 0, err
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			_ = rows.Close()
			return 0, err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil || len(keys) == 0 {
		return 0, err
	}
	n := 0
	err = s.Update(ctx, keys, func(tx state.Tx) error {
		for _, k := range keys {
			r, err := tx.Get(ctx, k)
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if !r.ExpiresAt.IsZero() && !tx.Now().Before(r.ExpiresAt) {
				if err := tx.Delete(ctx, k); err != nil {
					return err
				}
				n++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// Rewrap rotates protocol state under the active key. Call until it returns zero.
func (s *Store) Rewrap(ctx context.Context) (int, error) {
	return sqlcommon.RewrapBlobs(
		ctx,
		s.db,
		s.d,
		s.keyring,
		[]sqlcommon.BlobColumn{
			{Table: "protocol_state", Column: "value", Keys: []string{"record_key"}},
		},
	)
}
