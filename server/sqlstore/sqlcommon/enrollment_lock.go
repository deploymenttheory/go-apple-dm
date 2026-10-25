package sqlcommon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// GetForUpdate reads and locks the native enrollment until the existing shared
// transaction completes. Callers must use UnitOfWork on this exact pool; a lock
// acquired in an independent transaction would not protect later queue writes.
func (s *Store) GetForUpdate(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	tx, ok := CurrentTransaction(ctx, s.db)
	if !ok {
		return nil, ErrTransaction
	}
	enrollment, err := s.identity(ctx, tx, id, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: enrollment %s", storage.ErrNotFound, id.ID)
	}
	if err != nil {
		return nil, wrap("lock enrollment", err)
	}
	if err := s.openEnrollment(enrollment); err != nil {
		return nil, err
	}
	return enrollment, nil
}
