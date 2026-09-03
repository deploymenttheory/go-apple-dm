package sqlstore

import (
	"context"

	"github.com/deploymenttheory/go-apple-dm/mdm"
)

// enrollmentTables lists every table keyed by enrollment_id, dependants
// first. Declarations and sets themselves are never touched.
var enrollmentTables = []string{
	"ddm_enrollment_sets", "ddm_enrollment_declarations", "ddm_snapshot_items", "ddm_snapshots",
	"ddm_status_declarations", "ddm_status_values", "ddm_status_errors", "ddm_status_reports", "ddm_changes",
}

// ClearEnrollment implements ddm.Tx. Only this enrollment's rows go; a
// device's user channels are separate enrollments and keep theirs.
func (t *txStore) ClearEnrollment(ctx context.Context, id mdm.EnrollmentID) error {
	if err := validID(id); err != nil {
		return err
	}
	for _, table := range enrollmentTables {
		if _, err := t.exec(ctx, "clear "+table, "DELETE FROM "+table+" WHERE enrollment_id = ?", id.ID); err != nil { // #nosec G202 -- table names are literals
			return err
		}
	}
	return nil
}

// ClearEnrollment implements ddm.Tx.
func (s *Store) ClearEnrollment(ctx context.Context, id mdm.EnrollmentID) error {
	return s.write(ctx, func(t *txStore) error { return t.ClearEnrollment(ctx, id) })
}
