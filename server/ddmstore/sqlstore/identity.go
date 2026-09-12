package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

func (t *txStore) validID(ctx context.Context, id mdm.EnrollmentID) error {
	if err := validID(id); err != nil {
		return err
	}
	if _, writing := t.q.(*sql.Tx); writing {
		cols := []string{"enrollment_id", "channel", "parent_id"}
		if _, err := t.exec(
			ctx,
			"register identity",
			t.s.d.InsertIgnore("ddm_identities", cols, cols[:1]),
			id.ID,
			int(id.Channel),
			id.ParentID,
		); err != nil {
			return err
		}
	}
	stored, err := t.EnrollmentIdentity(ctx, id.ID)
	if errors.Is(err, ddm.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if stored != id {
		return ddm.ErrNotFound
	}
	return nil
}

// EnrollmentIdentity implements ddm.AssignmentStore.
func (t *txStore) EnrollmentIdentity(ctx context.Context, rawID string) (mdm.EnrollmentID, error) {
	id := mdm.EnrollmentID{ID: rawID}
	var channel int
	found, err := t.row(
		ctx,
		"resolve identity",
		"SELECT channel, parent_id FROM ddm_identities WHERE enrollment_id = ?",
		[]any{rawID},
		&channel,
		&id.ParentID,
	)
	if err != nil {
		return mdm.EnrollmentID{}, err
	}
	if !found {
		return mdm.EnrollmentID{}, ddm.ErrNotFound
	}
	id.Channel = mdm.Channel(channel) // #nosec G115 -- stored channel is a uint8
	return id, nil
}

// EnrollmentIdentity implements ddm.AssignmentStore.
func (s *Store) EnrollmentIdentity(ctx context.Context, rawID string) (mdm.EnrollmentID, error) {
	return s.view().EnrollmentIdentity(ctx, rawID)
}
