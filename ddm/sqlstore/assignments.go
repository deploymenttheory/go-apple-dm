package sqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// enrollmentIDCols is the identity triple every enrollment-keyed table
// stores.
const enrollmentIDCols = "enrollment_id, channel, parent_id"

// assign inserts the (enrollment, target) row of an assignment table
// unless it exists, reporting whether it was new.
func (t *txStore) assign(ctx context.Context, table, column string, id mdm.EnrollmentID, target string, at time.Time) (bool, error) {
	var one int
	found, err := t.row(ctx, "lookup assignment", "SELECT 1 FROM "+table+" WHERE enrollment_id = ? AND "+column+" = ?", []any{id.ID, target}, &one) // #nosec G202 -- table and column names are literals
	if err != nil || found {
		return false, err
	}
	if _, err := t.exec(ctx, "insert assignment", "INSERT INTO "+table+" ("+enrollmentIDCols+", "+column+", assigned_at) VALUES (?, ?, ?, ?, ?)", // #nosec G202 -- table and column names are literals
		id.ID, int(id.Channel), id.ParentID, target, utc(at)); err != nil {
		return false, err
	}
	return true, nil
}

// unassign deletes one assignment row, reporting whether it existed.
func (t *txStore) unassign(ctx context.Context, table, column string, id mdm.EnrollmentID, target string) (bool, error) {
	res, err := t.exec(ctx, "delete assignment", "DELETE FROM "+table+" WHERE enrollment_id = ? AND "+column+" = ?", id.ID, target) // #nosec G202 -- table and column names are literals
	if err != nil {
		return false, err
	}
	n, err := affected("delete assignment", res)
	return n > 0, err
}

// AssignSet implements ddm.AssignmentStore.
func (t *txStore) AssignSet(ctx context.Context, id mdm.EnrollmentID, set string, at time.Time) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}
	if err := validName("set name", set); err != nil {
		return false, err
	}
	if err := t.requireSet(ctx, set); err != nil {
		return false, err
	}
	return t.assign(ctx, "ddm_enrollment_sets", "set_name", id, set, at)
}

// UnassignSet implements ddm.AssignmentStore. An unknown set is simply not
// assigned.
func (t *txStore) UnassignSet(ctx context.Context, id mdm.EnrollmentID, set string) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}
	if err := validName("set name", set); err != nil {
		return false, err
	}
	return t.unassign(ctx, "ddm_enrollment_sets", "set_name", id, set)
}

// EnrollmentSets implements ddm.AssignmentStore.
func (t *txStore) EnrollmentSets(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return t.column(ctx, "enrollment sets", "SELECT set_name FROM ddm_enrollment_sets WHERE enrollment_id = ? ORDER BY set_name", id.ID)
}

// SetEnrollments implements ddm.AssignmentStore. The cursor is the last
// enrollment id of the previous page.
func (t *txStore) SetEnrollments(ctx context.Context, set string, p storage.Page) (storage.Result[mdm.EnrollmentID], error) {
	if err := validName("set name", set); err != nil {
		return storage.Result[mdm.EnrollmentID]{}, err
	}
	if err := t.requireSet(ctx, set); err != nil {
		return storage.Result[mdm.EnrollmentID]{}, err
	}
	where, args := after([]string{"set_name = ?"}, []any{set}, "enrollment_id", p)
	return keyset(ctx, t, "set enrollments", "SELECT "+enrollmentIDCols+" FROM ddm_enrollment_sets WHERE "+strings.Join(where, " AND ")+" ORDER BY enrollment_id", args, p,
		func(rows *sql.Rows) (mdm.EnrollmentID, string, error) {
			var id mdm.EnrollmentID
			err := scanEnrollmentID(rows, &id)
			return id, id.ID, err
		})
}

// AssignDeclaration implements ddm.AssignmentStore.
func (t *txStore) AssignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string, at time.Time) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}
	if err := validName("identifier", identifier); err != nil {
		return false, err
	}
	if err := t.requireDeclaration(ctx, identifier); err != nil {
		return false, err
	}
	return t.assign(ctx, "ddm_enrollment_declarations", "identifier", id, identifier, at)
}

// UnassignDeclaration implements ddm.AssignmentStore.
func (t *txStore) UnassignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}
	if err := validName("identifier", identifier); err != nil {
		return false, err
	}
	return t.unassign(ctx, "ddm_enrollment_declarations", "identifier", id, identifier)
}

// EnrollmentDeclarations implements ddm.AssignmentStore.
func (t *txStore) EnrollmentDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return t.column(ctx, "enrollment declarations", "SELECT identifier FROM ddm_enrollment_declarations WHERE enrollment_id = ? ORDER BY identifier", id.ID)
}

// StaticDeclarations implements ddm.AssignmentStore: the direct
// assignments and the members of the assigned sets, once each, by
// identifier.
func (t *txStore) StaticDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]ddm.Declaration, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	var out []ddm.Declaration
	err := t.each(ctx, "static declarations", "SELECT "+declarationCols+" FROM ddm_declarations WHERE identifier IN ("+
		"SELECT identifier FROM ddm_enrollment_declarations WHERE enrollment_id = ? UNION "+
		"SELECT sd.identifier FROM ddm_set_declarations sd JOIN ddm_enrollment_sets es ON es.set_name = sd.set_name WHERE es.enrollment_id = ?) ORDER BY identifier",
		[]any{id.ID, id.ID}, func(rows *sql.Rows) error {
			d, err := scanDeclaration(rows)
			if err != nil {
				return err
			}
			out = append(out, d)
			return nil
		})
	return out, err
}

// AffectedEnrollments implements ddm.AssignmentStore: every enrollment
// directly assigned one of the identifiers, assigned one of the sets, or
// assigned a set holding one of the identifiers, ordered by (parent id,
// id) so device channels come before user channels.
func (t *txStore) AffectedEnrollments(ctx context.Context, identifiers, sets []string) ([]mdm.EnrollmentID, error) {
	for _, identifier := range identifiers {
		if err := validName("identifier", identifier); err != nil {
			return nil, err
		}
	}
	for _, set := range sets {
		if err := validName("set name", set); err != nil {
			return nil, err
		}
	}
	var parts []string
	var args []any
	if len(identifiers) > 0 {
		in := "(" + placeholders(len(identifiers)) + ")"
		parts = append(parts,
			"SELECT "+enrollmentIDCols+" FROM ddm_enrollment_declarations WHERE identifier IN "+in,
			"SELECT es.enrollment_id, es.channel, es.parent_id FROM ddm_enrollment_sets es JOIN ddm_set_declarations sd ON sd.set_name = es.set_name WHERE sd.identifier IN "+in)
		for range 2 {
			for _, identifier := range identifiers {
				args = append(args, identifier)
			}
		}
	}
	if len(sets) > 0 {
		parts = append(parts, "SELECT "+enrollmentIDCols+" FROM ddm_enrollment_sets WHERE set_name IN ("+placeholders(len(sets))+")")
		for _, set := range sets {
			args = append(args, set)
		}
	}
	out := make([]mdm.EnrollmentID, 0)
	if len(parts) == 0 {
		return out, nil
	}
	err := t.each(ctx, "affected enrollments", strings.Join(parts, " UNION ")+" ORDER BY parent_id, enrollment_id", args, func(rows *sql.Rows) error {
		var id mdm.EnrollmentID
		if err := scanEnrollmentID(rows, &id); err != nil {
			return err
		}
		out = append(out, id)
		return nil
	})
	return out, err
}

// AssignSet implements ddm.AssignmentStore.
func (s *Store) AssignSet(ctx context.Context, id mdm.EnrollmentID, set string, at time.Time) (changed bool, err error) {
	err = s.write(ctx, func(t *txStore) error {
		changed, err = t.AssignSet(ctx, id, set, at)
		return err
	})
	return changed, err
}

// UnassignSet implements ddm.AssignmentStore.
func (s *Store) UnassignSet(ctx context.Context, id mdm.EnrollmentID, set string) (bool, error) {
	return s.view().UnassignSet(ctx, id, set)
}

// EnrollmentSets implements ddm.AssignmentStore.
func (s *Store) EnrollmentSets(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	return s.view().EnrollmentSets(ctx, id)
}

// SetEnrollments implements ddm.AssignmentStore.
func (s *Store) SetEnrollments(ctx context.Context, set string, p storage.Page) (storage.Result[mdm.EnrollmentID], error) {
	return s.view().SetEnrollments(ctx, set, p)
}

// AssignDeclaration implements ddm.AssignmentStore.
func (s *Store) AssignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string, at time.Time) (changed bool, err error) {
	err = s.write(ctx, func(t *txStore) error {
		changed, err = t.AssignDeclaration(ctx, id, identifier, at)
		return err
	})
	return changed, err
}

// UnassignDeclaration implements ddm.AssignmentStore.
func (s *Store) UnassignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string) (bool, error) {
	return s.view().UnassignDeclaration(ctx, id, identifier)
}

// EnrollmentDeclarations implements ddm.AssignmentStore.
func (s *Store) EnrollmentDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	return s.view().EnrollmentDeclarations(ctx, id)
}

// StaticDeclarations implements ddm.AssignmentStore.
func (s *Store) StaticDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]ddm.Declaration, error) {
	return s.view().StaticDeclarations(ctx, id)
}

// AffectedEnrollments implements ddm.AssignmentStore.
func (s *Store) AffectedEnrollments(ctx context.Context, identifiers, sets []string) ([]mdm.EnrollmentID, error) {
	return s.view().AffectedEnrollments(ctx, identifiers, sets)
}
