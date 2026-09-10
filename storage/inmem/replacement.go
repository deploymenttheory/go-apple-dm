package inmem

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

var _ storage.ReplacementStore = (*Store)(nil)

func (s *Store) TransitionReplacement(ctx context.Context, id mdm.EnrollmentID, change storage.ReplacementChange) (*storage.Replacement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id.Channel.IsUser() {
		return nil, fmt.Errorf("%w: replacement requires a device channel", storage.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.get(id)
	if err != nil {
		return nil, err
	}
	r := storage.CloneReplacement(s.replacements[id.ID])
	commit, err := storage.AdvanceReplacement(&r, &e.Enrollment, change)
	if err != nil {
		return nil, err
	}
	if commit {
		for _, token := range r.Tokens {
			if row := s.enrollments[token.ID.ID]; row != nil && row.ID != token.ID {
				return nil, storage.ErrConflict
			}
		}
		for _, h := range s.history {
			if h.Hash == r.CandidateHash && h.ID != id {
				return nil, fmt.Errorf("%w: candidate certificate reused", storage.ErrConflict)
			}
		}
		s.dropCertLocked(id.ID)
		s.certs[r.CandidateHash] = id
		e.CertHash, e.CertHashAt = r.CandidateHash, change.At
		e.AuthenticateRaw = append([]byte(nil), r.AuthenticateRaw...)
		s.history = append(s.history, storage.CertAssociation{ID: id, Hash: r.CandidateHash, At: change.At})
		for _, t := range r.Tokens {
			row := s.enrollments[t.ID.ID]
			if row == nil {
				row = &record{Enrollment: storage.Enrollment{ID: t.ID, EnrolledAt: change.At}}
				s.enrollments[t.ID.ID] = row
			}
			storage.ApplyReplacementToken(&row.Enrollment, t, change.At)
		}
	}
	s.replacements[id.ID] = storage.CloneReplacement(r)
	return storage.CloneReplacement(r), nil
}
