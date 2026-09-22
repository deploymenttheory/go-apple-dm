package inventorystore

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

//go:embed migrations/*/*.sql
var migrations embed.FS

// MigrationSet registers inventory with migrations and disaster recovery.
func MigrationSet(d sqlcommon.Dialect) (sqlcommon.MigrationSet, error) {
	switch d.Name {
	case "sqlite", "postgres", "mysql":
		return sqlcommon.MigrationSet{Table: "inventory_schema_migrations", FS: sqlcommon.MustSub(migrations, "migrations/"+d.Name)}, nil
	default:
		return sqlcommon.MigrationSet{}, fmt.Errorf("inventory: unsupported dialect %q", d.Name)
	}
}

// Store serializes reconciliation and seals document payloads with the server keyring.
type Store struct {
	db   *sql.DB
	d    sqlcommon.Dialect
	keys *crypt.Keyring
}

// Open applies migrations and wraps a pool owned by the caller.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, keys *crypt.Keyring) (*Store, error) {
	if db == nil {
		return nil, inventory.ErrInvalid
	}
	set, err := MigrationSet(d)
	if err != nil {
		return nil, err
	}
	if _, err = sqlcommon.MigrateSet(ctx, db, d, set); err != nil {
		return nil, err
	}
	return &Store{db: db, d: d, keys: keys}, nil
}

// decode authenticates the payload against its document key and column purpose.
func (s *Store) decode(key string, b []byte) (json.RawMessage, error) {
	if crypt.IsSealed(b) {
		if s.keys == nil {
			return nil, crypt.ErrUnsealed
		}
		plain, _, err := s.keys.Open(b, crypt.AAD("inventory_entries.payload", key))
		return plain, err
	}
	if s.keys != nil && s.keys.Strict() {
		return nil, crypt.ErrUnsealed
	}
	return b, nil
}

// Get returns a decrypted document copy.
func (s *Store) Get(ctx context.Context, key string) (json.RawMessage, error) {
	var b []byte
	err := sqlcommon.Query(ctx, s.db).QueryRowContext(ctx, s.d.Rebind("SELECT payload FROM inventory_entries WHERE entry_key = ?"), key).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, inventory.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.decode(key, b)
}

// Scan uses the primary-key range; values never participate in SQL construction.
func (s *Store) Scan(ctx context.Context, prefix, after string, limit int) ([]inventory.Entry, error) {
	if limit < 1 || limit > 1000 {
		return nil, inventory.ErrInvalid
	}
	rows, err := sqlcommon.Query(ctx, s.db).QueryContext(ctx, s.d.Rebind("SELECT entry_key, payload FROM inventory_entries WHERE entry_key >= ? AND entry_key < ? AND entry_key > ? ORDER BY entry_key LIMIT ?"), prefix, prefix+"\x7f", after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []inventory.Entry{}
	for rows.Next() {
		var e inventory.Entry
		var b []byte
		if err := rows.Scan(&e.Key, &b); err != nil {
			return nil, err
		}
		e.Value, err = s.decode(e.Key, b)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Update joins the protocol/audit transaction when one is already active.
func (s *Store) Update(ctx context.Context, fn func(inventory.Tx) error) error {
	u := sqlcommon.UnitOfWork{DB: s.db, Dialect: s.d}
	return u.Run(ctx, func(ctx context.Context) error {
		if _, err := sqlcommon.Query(ctx, s.db).ExecContext(ctx, "UPDATE inventory_lock SET version = version + 1 WHERE id = 1"); err != nil {
			return err
		}
		return fn(&transaction{Store: s, ctx: ctx})
	})
}

type transaction struct {
	*Store
	ctx context.Context
}

// Get reads using the transaction context captured by Update.
func (t *transaction) Get(_ context.Context, k string) (json.RawMessage, error) {
	return t.Store.Get(t.ctx, k)
}

// Scan reads an ordered key range within the captured transaction.
func (t *transaction) Scan(_ context.Context, p, a string, n int) ([]inventory.Entry, error) {
	return t.Store.Scan(t.ctx, p, a, n)
}

// Put validates and seals one document before upserting it in the transaction.
func (t *transaction) Put(_ context.Context, key string, b json.RawMessage) error {
	if !inventory.ValidEntry(key, b) {
		return inventory.ErrInvalid
	}
	var err error
	if t.keys != nil {
		b, err = t.keys.Seal(b, crypt.AAD("inventory_entries.payload", key))
		if err != nil {
			return err
		}
	}
	q := t.d.Upsert("inventory_entries", []string{"entry_key", "payload"}, []string{"entry_key"})
	_, err = sqlcommon.Query(t.ctx, t.db).ExecContext(t.ctx, t.d.Rebind(q), key, []byte(b))
	return err
}

// Delete removes one document within the captured transaction.
func (t *transaction) Delete(_ context.Context, key string) error {
	_, err := sqlcommon.Query(t.ctx, t.db).ExecContext(t.ctx, t.d.Rebind("DELETE FROM inventory_entries WHERE entry_key = ?"), key)
	return err
}
