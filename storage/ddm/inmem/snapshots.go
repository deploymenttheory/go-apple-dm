package inmem

import (
	"bytes"
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
)

func copySnapshot(s ddm.Snapshot) ddm.Snapshot {
	items := make([]ddm.SnapshotItem, len(s.Items))
	for i, it := range s.Items {
		it.Expanded = bytes.Clone(it.Expanded)
		items[i] = it
	}
	s.Items = items
	return s
}

// PutSnapshot implements ddm.SnapshotStore.
func (t *tx) PutSnapshot(_ context.Context, s *ddm.Snapshot) error {
	if s == nil {
		return fmt.Errorf("%w: nil snapshot", ddm.ErrInvalid)
	}
	if err := validID(s.ID); err != nil {
		return err
	}
	for _, it := range s.Items {
		if err := validName("snapshot item identifier", it.Identifier); err != nil {
			return err
		}
	}
	t.st.ids[s.ID.ID] = s.ID
	t.st.snapshots[s.ID.ID] = copySnapshot(*s)
	return nil
}

// Snapshot implements ddm.SnapshotStore.
func (t *tx) Snapshot(_ context.Context, id mdm.EnrollmentID) (*ddm.Snapshot, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	s, ok := t.st.snapshots[id.ID]
	if !ok {
		return nil, notFound("snapshot for enrollment", id.ID)
	}
	out := copySnapshot(s)
	return &out, nil
}

// PutSnapshot implements ddm.SnapshotStore.
func (s *Store) PutSnapshot(ctx context.Context, snap *ddm.Snapshot) error {
	v, done := s.view()
	defer done()
	return v.PutSnapshot(ctx, snap)
}

// Snapshot implements ddm.SnapshotStore.
func (s *Store) Snapshot(ctx context.Context, id mdm.EnrollmentID) (*ddm.Snapshot, error) {
	v, done := s.view()
	defer done()
	return v.Snapshot(ctx, id)
}
