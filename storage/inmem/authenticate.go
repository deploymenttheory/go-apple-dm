package inmem

import (
	"context"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

func (s *Store) deviceRecordLocked(id mdm.EnrollmentID) (*record, error) {
	if err := id.Validate(); err != nil {
		return nil, storage.ErrInvalid
	}
	if r := s.enrollments[id.ID]; r != nil && r.ID != id {
		return nil, storage.ErrNotFound
	}
	return s.get(id.Device())
}

func clearPendingLocked(r *record, at time.Time) {
	for _, q := range r.queue {
		if !q.State.Terminal() {
			q.State, q.CompletedAt = storage.StateCleared, at
		}
	}
}

// EnrollmentByID implements storage.EnrollmentStore.
func (s *Store) EnrollmentByID(ctx context.Context, id string) (*storage.Enrollment, error) {
	s.mu.Lock()
	r := s.enrollments[id]
	if r == nil {
		s.mu.Unlock()
		return nil, storage.ErrNotFound
	}
	canonical := r.ID
	s.mu.Unlock()
	return s.Get(ctx, canonical)
}

// AuthenticateEnrollment implements storage.EnrollmentStore.
func (s *Store) AuthenticateEnrollment(
	ctx context.Context,
	id mdm.EnrollmentID,
	c storage.AuthenticateChange,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.enrollments[id.ID]
	var e *storage.Enrollment
	if r != nil {
		e = &r.Enrollment
	}
	retry, err := storage.CheckAuthenticate(id, e, c)
	if err != nil || retry {
		return err
	}
	if c.Hash != "" {
		if owner, ok := s.certs[c.Hash]; ok && owner != id {
			return storage.ErrConflict
		}
		if !c.AllowReuse {
			for _, h := range s.history {
				if h.Hash == c.Hash && h.ID != id {
					return storage.ErrConflict
				}
			}
		}
	}
	if r == nil {
		r = &record{}
		s.enrollments[id.ID] = r
	}
	if err := s.resetAuthenticateLocked(r, id, c.Message, c.Raw, c.At); err != nil {
		return err
	}
	if c.Hash != "" {
		s.certs[c.Hash] = id
		r.CertHash, r.CertHashAt = c.Hash, c.At
		s.recordAssociationLocked(id, c.Hash, c.At)
	}
	return nil
}
