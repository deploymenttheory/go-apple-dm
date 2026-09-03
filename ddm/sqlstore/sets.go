package sqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// PutSet implements ddm.SetStore. An existing set is left untouched.
func (t *txStore) PutSet(ctx context.Context, name string, at time.Time) (bool, error) {
	if err := validName("set name", name); err != nil {
		return false, err
	}
	found, err := t.exists(ctx, "ddm_sets", "name", name)
	if err != nil || found {
		return false, err
	}
	if _, err := t.exec(ctx, "insert set", "INSERT INTO ddm_sets (name, created_at, updated_at) VALUES (?, ?, ?)", name, utc(at), utc(at)); err != nil {
		return false, err
	}
	return true, nil
}

// setTables lists the tables keyed by a set name, dependants first.
var setTables = []struct{ table, column string }{
	{"ddm_set_declarations", "set_name"}, {"ddm_enrollment_sets", "set_name"}, {"ddm_sets", "name"},
}

// DeleteSet implements ddm.SetStore.
func (t *txStore) DeleteSet(ctx context.Context, name string) error {
	if err := validName("set name", name); err != nil {
		return err
	}
	var n int64
	for _, st := range setTables {
		res, err := t.exec(ctx, "delete from "+st.table, "DELETE FROM "+st.table+" WHERE "+st.column+" = ?", name) // #nosec G202 -- table and column names are literals
		if err != nil {
			return err
		}
		if n, err = affected("delete set", res); err != nil {
			return err
		}
	}
	if n == 0 {
		return notFound("set", name)
	}
	return nil
}

// GetSet implements ddm.SetStore.
func (t *txStore) GetSet(ctx context.Context, name string) (*ddm.Set, error) {
	if err := validName("set name", name); err != nil {
		return nil, err
	}
	set := ddm.Set{Name: name}
	found, err := t.row(ctx, "get set", "SELECT created_at, updated_at FROM ddm_sets WHERE name = ?", []any{name}, &set.CreatedAt, &set.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, notFound("set", name)
	}
	set.CreatedAt, set.UpdatedAt = set.CreatedAt.UTC(), set.UpdatedAt.UTC()
	return &set, nil
}

// ListSets implements ddm.SetStore. The cursor is the last name of the
// previous page.
func (t *txStore) ListSets(ctx context.Context, p storage.Page) (storage.Result[ddm.Set], error) {
	where, args := after([]string{"1 = 1"}, []any{}, "name", p)
	return keyset(ctx, t, "list sets", "SELECT name, created_at, updated_at FROM ddm_sets WHERE "+strings.Join(where, " AND ")+" ORDER BY name", args, p,
		func(rows *sql.Rows) (ddm.Set, string, error) {
			var set ddm.Set
			if err := rows.Scan(&set.Name, &set.CreatedAt, &set.UpdatedAt); err != nil {
				return ddm.Set{}, "", wrap("scan set", err)
			}
			set.CreatedAt, set.UpdatedAt = set.CreatedAt.UTC(), set.UpdatedAt.UTC()
			return set, set.Name, nil
		})
}

// requireSet is ErrNotFound for an unknown set.
func (t *txStore) requireSet(ctx context.Context, name string) error {
	found, err := t.exists(ctx, "ddm_sets", "name", name)
	if err != nil {
		return err
	}
	if !found {
		return notFound("set", name)
	}
	return nil
}

// requireDeclaration is ErrNotFound for an unknown declaration.
func (t *txStore) requireDeclaration(ctx context.Context, identifier string) error {
	found, err := t.exists(ctx, "ddm_declarations", "identifier", identifier)
	if err != nil {
		return err
	}
	if !found {
		return notFound("declaration", identifier)
	}
	return nil
}

// AddSetDeclaration implements ddm.SetStore. A membership change bumps the
// set's UpdatedAt to at.
func (t *txStore) AddSetDeclaration(ctx context.Context, set, identifier string, at time.Time) (bool, error) {
	if err := validName("set name", set); err != nil {
		return false, err
	}
	if err := validName("identifier", identifier); err != nil {
		return false, err
	}
	if err := t.requireSet(ctx, set); err != nil {
		return false, err
	}
	if err := t.requireDeclaration(ctx, identifier); err != nil {
		return false, err
	}
	var one int
	found, err := t.row(ctx, "lookup set membership", "SELECT 1 FROM ddm_set_declarations WHERE set_name = ? AND identifier = ?", []any{set, identifier}, &one)
	if err != nil || found {
		return false, err
	}
	if _, err := t.exec(ctx, "insert set membership", "INSERT INTO ddm_set_declarations (set_name, identifier, added_at) VALUES (?, ?, ?)", set, identifier, utc(at)); err != nil {
		return false, err
	}
	if _, err := t.exec(ctx, "touch set", "UPDATE ddm_sets SET updated_at = ? WHERE name = ?", utc(at), set); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveSetDeclaration implements ddm.SetStore.
func (t *txStore) RemoveSetDeclaration(ctx context.Context, set, identifier string) (bool, error) {
	if err := validName("set name", set); err != nil {
		return false, err
	}
	if err := validName("identifier", identifier); err != nil {
		return false, err
	}
	if err := t.requireSet(ctx, set); err != nil {
		return false, err
	}
	res, err := t.exec(ctx, "remove set membership", "DELETE FROM ddm_set_declarations WHERE set_name = ? AND identifier = ?", set, identifier)
	if err != nil {
		return false, err
	}
	n, err := affected("remove set membership", res)
	return n > 0, err
}

// SetDeclarations implements ddm.SetStore.
func (t *txStore) SetDeclarations(ctx context.Context, set string) ([]string, error) {
	if err := validName("set name", set); err != nil {
		return nil, err
	}
	if err := t.requireSet(ctx, set); err != nil {
		return nil, err
	}
	return t.column(ctx, "set declarations", "SELECT identifier FROM ddm_set_declarations WHERE set_name = ? ORDER BY identifier", set)
}

// DeclarationSets implements ddm.SetStore. An unknown identifier is in no
// set.
func (t *txStore) DeclarationSets(ctx context.Context, identifier string) ([]string, error) {
	if err := validName("identifier", identifier); err != nil {
		return nil, err
	}
	return t.column(ctx, "declaration sets", "SELECT set_name FROM ddm_set_declarations WHERE identifier = ? ORDER BY set_name", identifier)
}

// PutSet implements ddm.SetStore.
func (s *Store) PutSet(ctx context.Context, name string, at time.Time) (created bool, err error) {
	err = s.write(ctx, func(t *txStore) error {
		created, err = t.PutSet(ctx, name, at)
		return err
	})
	return created, err
}

// DeleteSet implements ddm.SetStore.
func (s *Store) DeleteSet(ctx context.Context, name string) error {
	return s.write(ctx, func(t *txStore) error { return t.DeleteSet(ctx, name) })
}

// GetSet implements ddm.SetStore.
func (s *Store) GetSet(ctx context.Context, name string) (*ddm.Set, error) {
	return s.view().GetSet(ctx, name)
}

// ListSets implements ddm.SetStore.
func (s *Store) ListSets(ctx context.Context, p storage.Page) (storage.Result[ddm.Set], error) {
	return s.view().ListSets(ctx, p)
}

// AddSetDeclaration implements ddm.SetStore.
func (s *Store) AddSetDeclaration(ctx context.Context, set, identifier string, at time.Time) (changed bool, err error) {
	err = s.write(ctx, func(t *txStore) error {
		changed, err = t.AddSetDeclaration(ctx, set, identifier, at)
		return err
	})
	return changed, err
}

// RemoveSetDeclaration implements ddm.SetStore.
func (s *Store) RemoveSetDeclaration(ctx context.Context, set, identifier string) (changed bool, err error) {
	err = s.write(ctx, func(t *txStore) error {
		changed, err = t.RemoveSetDeclaration(ctx, set, identifier)
		return err
	})
	return changed, err
}

// SetDeclarations implements ddm.SetStore.
func (s *Store) SetDeclarations(ctx context.Context, set string) ([]string, error) {
	return s.view().SetDeclarations(ctx, set)
}

// DeclarationSets implements ddm.SetStore.
func (s *Store) DeclarationSets(ctx context.Context, identifier string) ([]string, error) {
	return s.view().DeclarationSets(ctx, identifier)
}
