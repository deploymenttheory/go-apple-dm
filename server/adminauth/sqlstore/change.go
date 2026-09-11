package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// ApplyPrincipal uses the existing singleton row to serialize lifecycle changes
// across connections, before any snapshot reads. It does not bump policy version.
func (s *Store) ApplyPrincipal(
	ctx context.Context,
	name string,
	change adminauth.PrincipalChange,
	now time.Time,
) (adminauth.Principal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return adminauth.Principal{}, wrap("principal transaction", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(
		ctx,
		"UPDATE admin_policy_version SET version = version WHERE id = 1",
	); err != nil {
		return adminauth.Principal{}, wrap("lock principals", err)
	}
	var version int64
	if err := tx.QueryRowContext(ctx, "SELECT version FROM admin_policy_version WHERE id = 1").
		Scan(&version); err != nil {
		return adminauth.Principal{}, wrap("principal lock missing", err)
	}
	row := tx.QueryRowContext(
		ctx,
		s.q("SELECT "+principalCols+" FROM admin_principals WHERE name = ?"),
		name,
	)
	previous, err := scanPrincipal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return adminauth.Principal{}, adminauth.ErrNotFound
	}
	if err != nil {
		return adminauth.Principal{}, wrap("read principal", err)
	}
	p, err := change.Apply(previous, now)
	if err != nil {
		return adminauth.Principal{}, err
	}
	if previous.Root && previous.Active(now) == nil && (!p.Root || p.Active(now) != nil) {
		var count int
		err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM admin_principals WHERE name <> ? AND root = ?
			AND token_digest IS NOT NULL AND token_id <> '' AND (expires_at IS NULL OR expires_at > ?)`), name, true, now.UTC()).
			Scan(&count)
		if err != nil {
			return adminauth.Principal{}, wrap("count active roots", err)
		}
		if count == 0 {
			return adminauth.Principal{}, adminauth.ErrLastRoot
		}
	}
	var query string
	var args []any
	switch change.Op {
	case "update":
		query, args = "UPDATE admin_principals SET roles = ?, root = ?, updated_at = ? WHERE name = ?", []any{
			strings.Join(p.Roles, ","),
			p.Root,
			now.UTC(),
			name,
		}
	case "rotate":
		query = "UPDATE admin_principals SET token_digest = ?, token_id = ?, token_at = ?, expires_at = ?, updated_at = ? WHERE name = ?"
		args = []any{change.Digest, p.TokenID, now.UTC(), nullTime(p.ExpiresAt), now.UTC(), name}
	case "revoke":
		query, args = "UPDATE admin_principals SET token_digest = NULL, token_id = '', token_at = NULL, expires_at = NULL, updated_at = ? WHERE name = ?", []any{
			now.UTC(),
			name,
		}
	case "delete":
		query, args = "DELETE FROM admin_principals WHERE name = ?", []any{name}
	}
	if _, err := tx.ExecContext(ctx, s.q(query), args...); err != nil {
		if s.d.IsUniqueViolation != nil && s.d.IsUniqueViolation(err) {
			return adminauth.Principal{}, adminauth.ErrConflict
		}
		return adminauth.Principal{}, wrap("change principal", err)
	}
	if err := tx.Commit(); err != nil {
		return adminauth.Principal{}, wrap("commit principal", err)
	}
	return p, nil
}
