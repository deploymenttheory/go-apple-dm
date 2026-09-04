package inmem

import (
	"context"
	"maps"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
)

// ClearEnrollment implements ddm.Tx. Declarations and sets themselves are
// left alone; only this enrollment's rows go.
func (t *tx) ClearEnrollment(_ context.Context, id mdm.EnrollmentID) error {
	if err := validID(id); err != nil {
		return err
	}
	key := id.ID
	delete(t.st.enrollSets, key)
	delete(t.st.enrollDecls, key)
	delete(t.st.snapshots, key)
	delete(t.st.statusDecls, key)
	delete(t.st.statusValues, key)
	delete(t.st.statusErrors, key)
	delete(t.st.statusReports, key)
	maps.DeleteFunc(t.st.changes, func(_ int64, c ddm.Change) bool { return c.ID.ID == key })
	delete(t.st.ids, key)
	return nil
}

// ClearEnrollment implements ddm.Tx.
func (s *Store) ClearEnrollment(ctx context.Context, id mdm.EnrollmentID) error {
	v, done := s.view()
	defer done()
	return v.ClearEnrollment(ctx, id)
}
