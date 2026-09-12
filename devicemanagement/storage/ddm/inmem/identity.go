package inmem

import (
	"context"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

func (t *tx) validID(id mdm.EnrollmentID) error {
	if err := validID(id); err != nil {
		return err
	}
	if stored, ok := t.st.ids[id.ID]; ok && stored != id {
		return ddm.ErrNotFound
	}
	return nil
}

// EnrollmentIdentity implements ddm.AssignmentStore.
func (t *tx) EnrollmentIdentity(_ context.Context, rawID string) (mdm.EnrollmentID, error) {
	id, ok := t.st.ids[rawID]
	if !ok {
		return mdm.EnrollmentID{}, ddm.ErrNotFound
	}
	return id, nil
}

// EnrollmentIdentity implements ddm.AssignmentStore.
func (s *Store) EnrollmentIdentity(ctx context.Context, rawID string) (mdm.EnrollmentID, error) {
	v, done := s.view()
	defer done()
	return v.EnrollmentIdentity(ctx, rawID)
}
