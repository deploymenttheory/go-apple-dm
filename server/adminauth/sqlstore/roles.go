package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

func (s *Store) lock(ctx context.Context, tx *sql.Tx) error {
	res, err := tx.ExecContext(ctx, "UPDATE admin_policy_version SET version = version WHERE id = 1")
	if err != nil {
		return wrap("lock authority", err)
	}
	// MySQL reports changed rows; verify the singleton with a read instead.
	_ = res
	var version int64
	return tx.QueryRowContext(ctx, "SELECT version FROM admin_policy_version WHERE id = 1").Scan(&version)
}

func (s *Store) validateRoles(ctx context.Context, roles []string) error {
	for _, name := range roles {
		if _, err := s.Role(ctx, name); err != nil {
			return fmt.Errorf("%w: unknown role %q: %w", adminauth.ErrInvalid, name, err)
		}
	}
	return nil
}

func (s *Store) replaceRoles(ctx context.Context, name string, roles []string) error {
	q := sqlcommon.Query(ctx, s.db)
	if _, err := q.ExecContext(ctx, s.q("DELETE FROM admin_principal_roles WHERE principal_name = ?"), name); err != nil {
		return err
	}
	for _, role := range sortedRoles(roles) {
		if _, err := q.ExecContext(ctx, s.q("INSERT INTO admin_principal_roles (principal_name,role_name) VALUES (?,?)"), name, role); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) principalRoles(ctx context.Context, name string) ([]string, error) {
	rows, err := sqlcommon.Query(ctx, s.db).QueryContext(ctx, s.q("SELECT role_name FROM admin_principal_roles WHERE principal_name = ? ORDER BY role_name"), name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var roles []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		roles = append(roles, name)
	}
	return roles, rows.Err()
}

func (s *Store) Role(ctx context.Context, name string) (adminauth.Role, error) {
	var role adminauth.Role
	err := sqlcommon.Query(ctx, s.db).QueryRowContext(ctx, s.q("SELECT name,description,created_at,updated_at FROM admin_roles WHERE name = ?"), name).Scan(&role.Name, &role.Description, &role.CreatedAt, &role.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return role, adminauth.ErrNotFound
	}
	return role, err
}

func (s *Store) PutRole(ctx context.Context, role adminauth.Role, now time.Time) (adminauth.Role, error) {
	if !adminauth.ValidName(role.Name) {
		return adminauth.Role{}, adminauth.ErrInvalid
	}
	err := s.runInTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		old, err := s.Role(ctx, role.Name)
		role.CreatedAt, role.UpdatedAt = now.UTC(), now.UTC()
		if errors.Is(err, adminauth.ErrNotFound) {
			_, err = tx.ExecContext(ctx, s.q("INSERT INTO admin_roles (name,description,created_at,updated_at) VALUES (?,?,?,?)"), role.Name, role.Description, role.CreatedAt, role.UpdatedAt)
		} else if err == nil {
			role.CreatedAt = old.CreatedAt
			_, err = tx.ExecContext(ctx, s.q("UPDATE admin_roles SET description = ?, updated_at = ? WHERE name = ?"), role.Description, role.UpdatedAt, role.Name)
		}
		if err != nil {
			return err
		}
		return bumpVersion(ctx, tx, s.d)
	})
	return role, err
}

func (s *Store) Roles(ctx context.Context, p adminauth.Page) (adminauth.Result[adminauth.Role], error) {
	out := adminauth.Result[adminauth.Role]{Items: []adminauth.Role{}}
	if p.Limit <= 0 {
		p.Limit = adminauth.DefaultPageSize
	}
	rows, err := sqlcommon.Query(ctx, s.db).QueryContext(ctx, s.q("SELECT name,description,created_at,updated_at FROM admin_roles WHERE name > ? ORDER BY name LIMIT ?"), p.Cursor, p.Limit+1)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var role adminauth.Role
		if err := rows.Scan(&role.Name, &role.Description, &role.CreatedAt, &role.UpdatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, role)
	}
	if len(out.Items) > p.Limit {
		out.Items = out.Items[:p.Limit]
		out.NextCursor = out.Items[len(out.Items)-1].Name
	}
	return out, rows.Err()
}

func (s *Store) DeleteRole(ctx context.Context, name string) error {
	return s.runInTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		if _, err := s.Role(ctx, name); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM admin_principal_roles WHERE role_name = ?"), name).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return adminauth.ErrConflict
		}
		policies, err := s.Policies(ctx)
		if err != nil {
			return err
		}
		for _, p := range policies {
			refs, err := adminauth.References(p.Source, string(adminauth.EntityRole))
			if err != nil {
				return err
			}
			if slices.Contains(refs, name) {
				return adminauth.ErrConflict
			}
		}
		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM admin_roles WHERE name = ?"), name); err != nil {
			return err
		}
		return bumpVersion(ctx, tx, s.d)
	})
}

func (s *Store) Initialized(ctx context.Context) (bool, error) {
	var initialized bool
	err := sqlcommon.Query(ctx, s.db).QueryRowContext(ctx, "SELECT initialized FROM admin_policy_version WHERE id = 1").Scan(&initialized)
	return initialized, err
}

func (s *Store) BootstrapPrincipal(ctx context.Context, p adminauth.Principal, digest string, now time.Time) (adminauth.Principal, error) {
	var out adminauth.Principal
	err := s.runInTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		initialized, err := s.Initialized(ctx)
		if err != nil {
			return err
		}
		if initialized {
			return adminauth.ErrConflict
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_principals").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return adminauth.ErrConflict
		}
		if !p.Root || p.TokenID == "" || digest == "" || p.Active(now) != nil {
			return adminauth.ErrInvalid
		}
		out, err = s.createPrincipal(ctx, p, digest, now)
		return err
	})
	return out, err
}

// migrateRoles imports the legacy CSV memberships once. The legacy column is
// retained for schema rollback, but normalized memberships are authoritative.
func (s *Store) migrateRoles(ctx context.Context) error {
	return s.runInTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		var done bool
		if err := tx.QueryRowContext(ctx, "SELECT roles_migrated FROM admin_policy_version WHERE id = 1").Scan(&done); err != nil {
			return err
		}
		if done {
			return nil
		}
		rows, err := tx.QueryContext(ctx, "SELECT name,roles FROM admin_principals")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		members := map[string][]string{}
		roles := map[string]bool{}
		for rows.Next() {
			var name, list string
			if err := rows.Scan(&name, &list); err != nil {
				return err
			}
			members[name] = splitRoles(list)
			for _, role := range members[name] {
				roles[role] = true
			}
		}
		err = rows.Err()
		if err != nil {
			return err
		}
		policies, err := s.Policies(ctx)
		if err != nil {
			return err
		}
		for _, p := range policies {
			refs, err := adminauth.References(p.Source, string(adminauth.EntityRole))
			if err != nil {
				continue
			}
			for _, role := range refs {
				roles[role] = true
			}
		}
		for role := range roles {
			if !adminauth.ValidName(role) {
				return fmt.Errorf("%w: legacy role %q", adminauth.ErrInvalid, role)
			}
			if _, err := s.PutRole(ctx, adminauth.Role{Name: role}, time.Now().UTC()); err != nil {
				return err
			}
		}
		for name, roles := range members {
			if err := s.replaceRoles(ctx, name, roles); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, s.q("UPDATE admin_policy_version SET roles_migrated = ? WHERE id = 1"), true)
		return err
	})
}
