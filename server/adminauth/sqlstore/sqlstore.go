package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/sqlcommon"
)

//go:embed migrations/*/*.sql
var migrationFS embed.FS

// MigrationsTable records the applied versions of the admin schema, separate
// from the storage, DDM, DEP, and ACME tables so the version sequences never
// mix.
const MigrationsTable = "adminauth_schema_migrations"

// ErrUnsupportedDialect is returned for a dialect without embedded migrations.
var ErrUnsupportedDialect = errors.New("sqlstore: unsupported dialect")

// MigrationSet returns the admin migrations for the dialect.
func MigrationSet(d sqlcommon.Dialect) (sqlcommon.MigrationSet, error) {
	switch d.Name {
	case "sqlite", "postgres", "mysql":
		return sqlcommon.MigrationSet{Table: MigrationsTable, FS: sqlcommon.MustSub(migrationFS, "migrations/"+d.Name)}, nil
	default:
		return sqlcommon.MigrationSet{}, fmt.Errorf("%w: %q", ErrUnsupportedDialect, d.Name)
	}
}

// Migrate applies every pending admin migration and returns the versions
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

// Rollback reverts admin migrations newer than target (0 reverts all).
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

// Version returns the highest applied admin migration (0 when none).
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

// Store implements adminauth.Store over a *sql.DB it does not own: closing
// the pool is the caller's job.
type Store struct {
	db *sql.DB
	d  sqlcommon.Dialect
}

var _ adminauth.Store = (*Store)(nil)

// Open wraps an opened pool for the dialect and, unless o.SkipMigrate,
// applies pending migrations.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, o Options) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", adminauth.ErrInvalid)
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

const principalCols = "name, roles, root, token_id, token_at, expires_at, created_at, updated_at"

// CreatePrincipal implements adminauth.Store.
func (s *Store) CreatePrincipal(ctx context.Context, p adminauth.Principal, digest string, now time.Time) (adminauth.Principal, error) {
	if !adminauth.ValidName(p.Name) {
		return adminauth.Principal{}, fmt.Errorf("%w: principal name %q", adminauth.ErrInvalid, p.Name)
	}
	p.Roles = sortedRoles(p.Roles)
	p.CreatedAt, p.UpdatedAt, p.TokenAt = now.UTC(), now.UTC(), now.UTC()
	_, err := s.db.ExecContext(ctx, s.q(
		`INSERT INTO admin_principals (name, roles, root, token_digest, token_id, token_at, expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		p.Name, strings.Join(p.Roles, ","), p.Root, nullString(digest), p.TokenID,
		nullTime(p.TokenAt), nullTime(p.ExpiresAt), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		if s.d.IsUniqueViolation != nil && s.d.IsUniqueViolation(err) {
			return adminauth.Principal{}, fmt.Errorf("%w: principal %q", adminauth.ErrConflict, p.Name)
		}
		return adminauth.Principal{}, wrap("create principal", err)
	}
	return p, nil
}

// Principal implements adminauth.Store.
func (s *Store) Principal(ctx context.Context, name string) (adminauth.Principal, error) {
	row := s.db.QueryRowContext(ctx, s.q("SELECT "+principalCols+" FROM admin_principals WHERE name = ?"), name)
	p, err := scanPrincipal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return adminauth.Principal{}, fmt.Errorf("%w: principal %q", adminauth.ErrNotFound, name)
	}
	if err != nil {
		return adminauth.Principal{}, wrap("get principal", err)
	}
	return p, nil
}

// PrincipalByDigest implements adminauth.Store. This is the authentication
// path: one indexed lookup on the unique digest index.
func (s *Store) PrincipalByDigest(ctx context.Context, digest string) (adminauth.Principal, error) {
	// A revoked principal stores NULL, so an empty digest must never match.
	if digest == "" {
		return adminauth.Principal{}, fmt.Errorf("%w: token", adminauth.ErrNotFound)
	}
	row := s.db.QueryRowContext(ctx, s.q("SELECT "+principalCols+" FROM admin_principals WHERE token_digest = ?"), digest)
	p, err := scanPrincipal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return adminauth.Principal{}, fmt.Errorf("%w: token", adminauth.ErrNotFound)
	}
	if err != nil {
		return adminauth.Principal{}, wrap("get principal by digest", err)
	}
	return p, nil
}

// Principals implements adminauth.Store with a keyset cursor on name.
func (s *Store) Principals(ctx context.Context, p adminauth.Page) (adminauth.Result[adminauth.Principal], error) {
	var out adminauth.Result[adminauth.Principal]
	limit := p.Limit
	if limit <= 0 {
		limit = adminauth.DefaultPageSize
	}
	query := "SELECT " + principalCols + " FROM admin_principals"
	args := []any{}
	if p.Cursor != "" {
		query += " WHERE name > ?"
		args = append(args, p.Cursor)
	}
	query += " ORDER BY name LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return out, wrap("list principals", err)
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanPrincipal(rows)
		if err != nil {
			return out, wrap("scan principal", err)
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return out, wrap("list principals", err)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].Name
	}
	return out, nil
}

// UpdatePrincipal implements adminauth.Store.
func (s *Store) UpdatePrincipal(ctx context.Context, name string, roles []string, root bool, now time.Time) (adminauth.Principal, error) {
	res, err := s.db.ExecContext(ctx, s.q(
		"UPDATE admin_principals SET roles = ?, root = ?, updated_at = ? WHERE name = ?"),
		strings.Join(sortedRoles(roles), ","), root, now.UTC(), name)
	if err != nil {
		return adminauth.Principal{}, wrap("update principal", err)
	}
	if err := affected(res, "principal", name); err != nil {
		return adminauth.Principal{}, err
	}
	return s.Principal(ctx, name)
}

// SetToken implements adminauth.Store, replacing the current digest so the
// previous token stops working at once.
func (s *Store) SetToken(ctx context.Context, name, digest, tokenID string, expires, now time.Time) (adminauth.Principal, error) {
	res, err := s.db.ExecContext(ctx, s.q(
		`UPDATE admin_principals SET token_digest = ?, token_id = ?, token_at = ?, expires_at = ?, updated_at = ?
		 WHERE name = ?`),
		nullString(digest), tokenID, nullTime(now.UTC()), nullTime(expires), now.UTC(), name)
	if err != nil {
		if s.d.IsUniqueViolation != nil && s.d.IsUniqueViolation(err) {
			return adminauth.Principal{}, fmt.Errorf("%w: token digest", adminauth.ErrConflict)
		}
		return adminauth.Principal{}, wrap("set token", err)
	}
	if err := affected(res, "principal", name); err != nil {
		return adminauth.Principal{}, err
	}
	return s.Principal(ctx, name)
}

// RevokeToken implements adminauth.Store. The digest becomes NULL rather than
// an empty string, so the unique index still admits many revoked rows.
func (s *Store) RevokeToken(ctx context.Context, name string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, s.q(
		`UPDATE admin_principals SET token_digest = NULL, token_id = '', token_at = NULL, expires_at = NULL, updated_at = ?
		 WHERE name = ?`), now.UTC(), name)
	if err != nil {
		return wrap("revoke token", err)
	}
	return affected(res, "principal", name)
}

// DeletePrincipal implements adminauth.Store.
func (s *Store) DeletePrincipal(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, s.q("DELETE FROM admin_principals WHERE name = ?"), name)
	if err != nil {
		return wrap("delete principal", err)
	}
	return affected(res, "principal", name)
}

// CountRoot implements adminauth.Store.
func (s *Store) CountRoot(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM admin_principals WHERE root = ?"), true).Scan(&n); err != nil {
		return 0, wrap("count root", err)
	}
	return n, nil
}

// PutPolicy implements adminauth.Store. The write and the version bump share
// one transaction, so a compiled set never sees a version that does not match
// the policies it would read.
func (s *Store) PutPolicy(ctx context.Context, p adminauth.Policy, now time.Time) (adminauth.Policy, error) {
	if !adminauth.ValidName(p.Name) {
		return adminauth.Policy{}, fmt.Errorf("%w: policy name %q", adminauth.ErrInvalid, p.Name)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return adminauth.Policy{}, wrap("put policy", err)
	}
	defer func() { _ = tx.Rollback() }()

	var created time.Time
	err = tx.QueryRowContext(ctx, s.q("SELECT created_at FROM admin_policies WHERE name = ?"), p.Name).Scan(&created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		created = now.UTC()
		if _, err := tx.ExecContext(ctx, s.q(
			"INSERT INTO admin_policies (name, source, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)"),
			p.Name, p.Source, p.Description, created, now.UTC()); err != nil {
			return adminauth.Policy{}, wrap("put policy", err)
		}
	case err != nil:
		return adminauth.Policy{}, wrap("put policy", err)
	default:
		if _, err := tx.ExecContext(ctx, s.q(
			"UPDATE admin_policies SET source = ?, description = ?, updated_at = ? WHERE name = ?"),
			p.Source, p.Description, now.UTC(), p.Name); err != nil {
			return adminauth.Policy{}, wrap("put policy", err)
		}
	}
	if err := bumpVersion(ctx, tx, s.d); err != nil {
		return adminauth.Policy{}, err
	}
	if err := tx.Commit(); err != nil {
		return adminauth.Policy{}, wrap("put policy", err)
	}
	p.CreatedAt, p.UpdatedAt = created.UTC(), now.UTC()
	return p, nil
}

// GetPolicy implements adminauth.Store.
func (s *Store) GetPolicy(ctx context.Context, name string) (adminauth.Policy, error) {
	var p adminauth.Policy
	err := s.db.QueryRowContext(ctx, s.q(
		"SELECT name, source, description, created_at, updated_at FROM admin_policies WHERE name = ?"), name).
		Scan(&p.Name, &p.Source, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return adminauth.Policy{}, fmt.Errorf("%w: policy %q", adminauth.ErrNotFound, name)
	}
	if err != nil {
		return adminauth.Policy{}, wrap("get policy", err)
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, nil
}

// Policies implements adminauth.Store, ordered by name.
func (s *Store) Policies(ctx context.Context) ([]adminauth.Policy, error) {
	rows, err := s.db.QueryContext(ctx, s.q(
		"SELECT name, source, description, created_at, updated_at FROM admin_policies ORDER BY name"))
	if err != nil {
		return nil, wrap("list policies", err)
	}
	defer rows.Close()
	var out []adminauth.Policy
	for rows.Next() {
		var p adminauth.Policy
		if err := rows.Scan(&p.Name, &p.Source, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, wrap("scan policy", err)
		}
		p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list policies", err)
	}
	return out, nil
}

// DeletePolicy implements adminauth.Store.
func (s *Store) DeletePolicy(ctx context.Context, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrap("delete policy", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, s.q("DELETE FROM admin_policies WHERE name = ?"), name)
	if err != nil {
		return wrap("delete policy", err)
	}
	if err := affected(res, "policy", name); err != nil {
		return err
	}
	if err := bumpVersion(ctx, tx, s.d); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return wrap("delete policy", err)
	}
	return nil
}

// PolicyVersion implements adminauth.Store.
func (s *Store) PolicyVersion(ctx context.Context) (int64, error) {
	var v int64
	if err := s.db.QueryRowContext(ctx, s.q("SELECT version FROM admin_policy_version WHERE id = 1")).Scan(&v); err != nil {
		return 0, wrap("policy version", err)
	}
	return v, nil
}

// bumpVersion increments the single version row inside the caller's
// transaction.
func bumpVersion(ctx context.Context, tx *sql.Tx, d sqlcommon.Dialect) error {
	if _, err := tx.ExecContext(ctx, d.Rebind("UPDATE admin_policy_version SET version = version + 1 WHERE id = 1")); err != nil {
		return wrap("bump policy version", err)
	}
	return nil
}

// scanner is the shared shape of *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanPrincipal(sc scanner) (adminauth.Principal, error) {
	var (
		p        adminauth.Principal
		roles    string
		tokenAt  sql.NullTime
		expires  sql.NullTime
		created  time.Time
		updated  time.Time
		rootFlag bool
	)
	if err := sc.Scan(&p.Name, &roles, &rootFlag, &p.TokenID, &tokenAt, &expires, &created, &updated); err != nil {
		return adminauth.Principal{}, err
	}
	p.Root = rootFlag
	p.Roles = splitRoles(roles)
	p.CreatedAt, p.UpdatedAt = created.UTC(), updated.UTC()
	if tokenAt.Valid {
		p.TokenAt = tokenAt.Time.UTC()
	}
	if expires.Valid {
		p.ExpiresAt = expires.Time.UTC()
	}
	return p, nil
}

func affected(res sql.Result, what, name string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return wrap("rows affected", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s %q", adminauth.ErrNotFound, what, name)
	}
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func sortedRoles(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return slices.Compact(out)
}

func splitRoles(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
