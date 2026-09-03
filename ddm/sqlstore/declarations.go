package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// declarationCols is the column list scanDeclaration reads, positionally.
const declarationCols = "identifier, type, kind, server_token, canonical, created_at, updated_at"

var versionCols = []string{"identifier", "server_token", "type", "canonical", "created_at"}

func scanDeclaration(row scanner) (ddm.Declaration, error) {
	var d ddm.Declaration
	if err := row.Scan(&d.Identifier, &d.Type, &d.Kind, &d.ServerToken, &d.Canonical, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return ddm.Declaration{}, wrap("scan declaration", err)
	}
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	return d, nil
}

// PutDeclaration implements ddm.DeclarationStore. A create stores every
// field as given; an update keeps the stored CreatedAt and takes
// d.UpdatedAt as the time the token changed. Each accepted change records
// the (identifier, token) revision unless it already exists.
func (t *txStore) PutDeclaration(ctx context.Context, d *ddm.Declaration) (bool, error) {
	if d == nil {
		return false, fmt.Errorf("%w: nil declaration", ddm.ErrInvalid)
	}
	if err := validName("identifier", d.Identifier); err != nil {
		return false, err
	}
	if err := validName("server token", d.ServerToken); err != nil {
		return false, err
	}
	var kind, token string
	found, err := t.row(ctx, "lookup declaration", "SELECT kind, server_token FROM ddm_declarations WHERE identifier = ?", []any{d.Identifier}, &kind, &token)
	if err != nil {
		return false, err
	}
	switch {
	case !found:
		if _, err := t.exec(ctx, "insert declaration", "INSERT INTO ddm_declarations ("+declarationCols+") VALUES (?, ?, ?, ?, ?, ?, ?)",
			d.Identifier, d.Type, string(d.Kind), d.ServerToken, nonNil(d.Canonical), utc(d.CreatedAt), utc(d.UpdatedAt)); err != nil {
			return false, err
		}
	case kind != string(d.Kind):
		return false, fmt.Errorf("%w: declaration %q is kind %q, not %q", ddm.ErrConflict, d.Identifier, kind, d.Kind)
	case token == d.ServerToken:
		return false, nil
	default:
		if _, err := t.exec(ctx, "update declaration", "UPDATE ddm_declarations SET type = ?, server_token = ?, canonical = ?, updated_at = ? WHERE identifier = ?",
			d.Type, d.ServerToken, nonNil(d.Canonical), utc(d.UpdatedAt), d.Identifier); err != nil {
			return false, err
		}
	}
	if _, err := t.exec(ctx, "insert declaration version", t.s.d.InsertIgnore("ddm_declaration_versions", versionCols, versionCols[:2]),
		d.Identifier, d.ServerToken, d.Type, nonNil(d.Canonical), utc(d.UpdatedAt)); err != nil {
		return false, err
	}
	return true, nil
}

// GetDeclaration implements ddm.DeclarationStore.
func (t *txStore) GetDeclaration(ctx context.Context, identifier string) (*ddm.Declaration, error) {
	if err := validName("identifier", identifier); err != nil {
		return nil, err
	}
	d, err := scanDeclaration(t.q.QueryRowContext(ctx, t.s.d.Rebind("SELECT "+declarationCols+" FROM ddm_declarations WHERE identifier = ?"), identifier))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("declaration", identifier)
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetDeclarationVersion implements ddm.DeclarationStore.
func (t *txStore) GetDeclarationVersion(ctx context.Context, identifier, serverToken string) (*ddm.DeclarationVersion, error) {
	if err := validName("identifier", identifier); err != nil {
		return nil, err
	}
	if err := validName("server token", serverToken); err != nil {
		return nil, err
	}
	v := ddm.DeclarationVersion{Identifier: identifier, ServerToken: serverToken}
	found, err := t.row(ctx, "get declaration version", "SELECT type, canonical, created_at FROM ddm_declaration_versions WHERE identifier = ? AND server_token = ?",
		[]any{identifier, serverToken}, &v.Type, &v.Canonical, &v.CreatedAt)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: declaration %q version %q", ddm.ErrNotFound, identifier, serverToken)
	}
	v.CreatedAt = v.CreatedAt.UTC()
	return &v, nil
}

// declarationTables lists the tables keyed by a declaration identifier,
// dependants first.
var declarationTables = []string{"ddm_declaration_versions", "ddm_set_declarations", "ddm_enrollment_declarations", "ddm_declarations"}

// DeleteDeclaration implements ddm.DeclarationStore.
func (t *txStore) DeleteDeclaration(ctx context.Context, identifier string) error {
	if err := validName("identifier", identifier); err != nil {
		return err
	}
	var n int64
	for _, table := range declarationTables {
		res, err := t.exec(ctx, "delete from "+table, "DELETE FROM "+table+" WHERE identifier = ?", identifier) // #nosec G202 -- table names are literals
		if err != nil {
			return err
		}
		if n, err = affected("delete declaration", res); err != nil {
			return err
		}
	}
	if n == 0 {
		return notFound("declaration", identifier)
	}
	return nil
}

// ListDeclarations implements ddm.DeclarationStore. The cursor is the last
// identifier of the previous page. An unknown InSet yields an empty page.
func (t *txStore) ListDeclarations(ctx context.Context, q ddm.DeclarationQuery, p storage.Page) (storage.Result[ddm.Declaration], error) {
	where, args := []string{"1 = 1"}, []any{}
	if q.Kind != "" {
		where, args = append(where, "kind = ?"), append(args, string(q.Kind))
	}
	if q.Type != "" {
		where, args = append(where, "type = ?"), append(args, q.Type)
	}
	if q.InSet != "" {
		where, args = append(where, "identifier IN (SELECT identifier FROM ddm_set_declarations WHERE set_name = ?)"), append(args, q.InSet)
	}
	where, args = after(where, args, "identifier", p)
	return keyset(ctx, t, "list declarations", "SELECT "+declarationCols+" FROM ddm_declarations WHERE "+strings.Join(where, " AND ")+" ORDER BY identifier", args, p,
		func(rows *sql.Rows) (ddm.Declaration, string, error) {
			d, err := scanDeclaration(rows)
			return d, d.Identifier, err
		})
}

// PruneVersions implements ddm.DeclarationStore.
func (t *txStore) PruneVersions(ctx context.Context) (int64, error) {
	res, err := t.exec(ctx, "prune versions", "DELETE FROM ddm_declaration_versions WHERE NOT EXISTS ("+
		"SELECT 1 FROM ddm_declarations d WHERE d.identifier = ddm_declaration_versions.identifier AND d.server_token = ddm_declaration_versions.server_token) AND NOT EXISTS ("+
		"SELECT 1 FROM ddm_snapshot_items i WHERE i.identifier = ddm_declaration_versions.identifier AND i.base_token = ddm_declaration_versions.server_token)")
	if err != nil {
		return 0, err
	}
	return affected("prune versions", res)
}

// PutDeclaration implements ddm.DeclarationStore.
func (s *Store) PutDeclaration(ctx context.Context, d *ddm.Declaration) (changed bool, err error) {
	err = s.write(ctx, func(t *txStore) error {
		changed, err = t.PutDeclaration(ctx, d)
		return err
	})
	return changed, err
}

// GetDeclaration implements ddm.DeclarationStore.
func (s *Store) GetDeclaration(ctx context.Context, identifier string) (*ddm.Declaration, error) {
	return s.view().GetDeclaration(ctx, identifier)
}

// GetDeclarationVersion implements ddm.DeclarationStore.
func (s *Store) GetDeclarationVersion(ctx context.Context, identifier, serverToken string) (*ddm.DeclarationVersion, error) {
	return s.view().GetDeclarationVersion(ctx, identifier, serverToken)
}

// DeleteDeclaration implements ddm.DeclarationStore.
func (s *Store) DeleteDeclaration(ctx context.Context, identifier string) error {
	return s.write(ctx, func(t *txStore) error { return t.DeleteDeclaration(ctx, identifier) })
}

// ListDeclarations implements ddm.DeclarationStore.
func (s *Store) ListDeclarations(ctx context.Context, q ddm.DeclarationQuery, p storage.Page) (storage.Result[ddm.Declaration], error) {
	return s.view().ListDeclarations(ctx, q, p)
}

// PruneVersions implements ddm.DeclarationStore.
func (s *Store) PruneVersions(ctx context.Context) (int64, error) {
	return s.view().PruneVersions(ctx)
}
