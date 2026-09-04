package inmem

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
)

// Push certificates (decision record 0015).

// StorePushCert implements storage.PushCertStore.
func (s *Store) StorePushCert(_ context.Context, topic string, certPEM, keyPEM []byte, at time.Time) (storage.PushCert, error) {
	rec, err := storage.ValidatePushCert(topic, certPEM, keyPEM, at)
	if err != nil {
		return storage.PushCert{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.Version = 1
	if prev, ok := s.pushCerts[rec.Topic]; ok {
		rec.Version = prev.Version + 1
	}
	rec.UpdatedAt = at.UTC()
	stored := rec
	s.pushCerts[rec.Topic] = &stored
	return withoutKey(rec), nil
}

func withoutKey(c storage.PushCert) storage.PushCert {
	c.KeyPEM = nil
	c.CertPEM = append([]byte(nil), c.CertPEM...)
	return c
}

// PushCert implements storage.PushCertStore.
func (s *Store) PushCert(_ context.Context, topic string) (*storage.PushCert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.pushCerts[topic]
	if !ok {
		return nil, fmt.Errorf("%w: push certificate for %q", storage.ErrNotFound, topic)
	}
	out := *c
	out.CertPEM = append([]byte(nil), c.CertPEM...)
	out.KeyPEM = append([]byte(nil), c.KeyPEM...)
	return &out, nil
}

// PushCerts implements storage.PushCertStore.
func (s *Store) PushCerts(_ context.Context) ([]storage.PushCert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]storage.PushCert, 0, len(s.pushCerts))
	for _, c := range s.pushCerts {
		out = append(out, withoutKey(*c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out, nil
}

// PushCertVersion implements storage.PushCertStore.
func (s *Store) PushCertVersion(_ context.Context, topic string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.pushCerts[topic]
	if !ok {
		return 0, fmt.Errorf("%w: push certificate for %q", storage.ErrNotFound, topic)
	}
	return c.Version, nil
}

// UserAuthenticate state (decision record 0016).

// userAuthTarget validates a user-channel id and checks its parent exists.
func (s *Store) userAuthTargetLocked(id mdm.EnrollmentID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %w", storage.ErrInvalid, err)
	}
	if !id.Channel.IsUser() {
		return fmt.Errorf("%w: %w: %s", storage.ErrInvalid, storage.ErrUserChannelRequired, id.ID)
	}
	if _, err := s.get(id.Device()); err != nil {
		return err
	}
	return nil
}

// StoreUserAuthChallenge implements storage.UserAuthStore.
func (s *Store) StoreUserAuthChallenge(_ context.Context, id mdm.EnrollmentID, challenge string, raw []byte, at time.Time) error {
	if challenge == "" {
		return fmt.Errorf("%w: empty challenge", storage.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.userAuthTargetLocked(id); err != nil {
		return err
	}
	s.userAuth[id.ID] = &storage.UserAuthState{ID: id, Challenge: challenge, ChallengeAt: at, AuthenticateRaw: append([]byte(nil), raw...)}
	return nil
}

// StoreUserAuthToken implements storage.UserAuthStore.
func (s *Store) StoreUserAuthToken(_ context.Context, id mdm.EnrollmentID, token string, raw []byte, at time.Time) error {
	if token == "" {
		return fmt.Errorf("%w: empty token", storage.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.userAuthTargetLocked(id); err != nil {
		return err
	}
	st, ok := s.userAuth[id.ID]
	if !ok {
		return fmt.Errorf("%w: no challenge issued for %s", storage.ErrNotFound, id.ID)
	}
	st.AuthToken, st.TokenAt = token, at
	st.Challenge, st.ChallengeAt = "", time.Time{}
	st.DigestRaw = append([]byte(nil), raw...)
	return nil
}

// UserAuth implements storage.UserAuthStore.
func (s *Store) UserAuth(_ context.Context, id mdm.EnrollmentID) (*storage.UserAuthState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.userAuthTargetLocked(id); err != nil {
		return nil, err
	}
	st, ok := s.userAuth[id.ID]
	if !ok {
		return nil, fmt.Errorf("%w: user auth state for %s", storage.ErrNotFound, id.ID)
	}
	out := *st
	out.AuthenticateRaw = append([]byte(nil), st.AuthenticateRaw...)
	out.DigestRaw = append([]byte(nil), st.DigestRaw...)
	return &out, nil
}

// ClearUserAuth implements storage.UserAuthStore.
func (s *Store) ClearUserAuth(_ context.Context, id mdm.EnrollmentID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.userAuthTargetLocked(id); err != nil {
		return err
	}
	delete(s.userAuth, id.ID)
	return nil
}

func (s *Store) clearUserAuthOfDeviceLocked(deviceID string) {
	for k, st := range s.userAuth {
		if st.ID.ParentID == deviceID {
			delete(s.userAuth, k)
		}
	}
}

// Export and import (decision record 0017).

func exportCursor(e storage.Enrollment) string { return e.ID.ParentID + "\x00" + e.ID.ID }

// Export implements storage.MigrationStore.
func (s *Store) Export(_ context.Context, p paging.Page) (paging.Result[storage.EnrollmentExport], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Cursor != "" && !strings.Contains(p.Cursor, "\x00") {
		return paging.Result[storage.EnrollmentExport]{}, fmt.Errorf("%w: bad cursor %q", storage.ErrInvalid, p.Cursor)
	}
	recs := make([]*record, 0, len(s.enrollments))
	for _, r := range s.enrollments {
		if p.Cursor == "" || exportCursor(r.Enrollment) > p.Cursor {
			recs = append(recs, r)
		}
	}
	sort.Slice(recs, func(i, j int) bool { return exportCursor(recs[i].Enrollment) < exportCursor(recs[j].Enrollment) })
	limit := p.Limit
	if limit <= 0 {
		limit = paging.DefaultPageSize
	}
	var out paging.Result[storage.EnrollmentExport]
	for i, r := range recs {
		if i == limit {
			out.NextCursor = exportCursor(recs[i-1].Enrollment)
			break
		}
		out.Items = append(out.Items, s.exportLocked(r))
	}
	return out, nil
}

func (s *Store) exportLocked(r *record) storage.EnrollmentExport {
	e := r.Enrollment
	e.Push.Token = append([]byte(nil), e.Push.Token...)
	e.UnlockToken = append([]byte(nil), e.UnlockToken...)
	e.AuthenticateRaw = append([]byte(nil), e.AuthenticateRaw...)
	e.TokenUpdateRaw = append([]byte(nil), e.TokenUpdateRaw...)
	x := storage.EnrollmentExport{Enrollment: e}
	if !e.ID.Channel.IsUser() {
		x.BootstrapToken = append([]byte(nil), s.bootstrap[e.ID.ID]...)
		x.CertHistory = s.historyLocked(func(a storage.CertAssociation) bool { return a.ID.ID == e.ID.ID })
	}
	return x
}

// Import implements storage.MigrationStore.
func (s *Store) Import(_ context.Context, rec storage.EnrollmentExport) error {
	id := rec.ID
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %w", storage.ErrInvalid, err)
	}
	for _, a := range rec.CertHistory {
		if a.ID.ID != id.ID || a.Hash == "" {
			return fmt.Errorf("%w: history row for %s in record %s", storage.ErrInvalid, a.ID.ID, id.ID)
		}
	}
	if id.Channel.IsUser() && (rec.CertHash != "" || len(rec.BootstrapToken) != 0 || len(rec.CertHistory) != 0) {
		return fmt.Errorf("%w: user channel %s carries device state", storage.ErrInvalid, id.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id.Channel.IsUser() {
		if _, ok := s.enrollments[id.ParentID]; !ok {
			return fmt.Errorf("%w: parent %s of %s is absent", storage.ErrInvalid, id.ParentID, id.ID)
		}
	}
	if rec.CertHash != "" {
		if owner, ok := s.certs[rec.CertHash]; ok && owner.ID != id.ID {
			return fmt.Errorf("%w: certificate already associated with %s", storage.ErrConflict, owner.ID)
		}
	}
	r, ok := s.enrollments[id.ID]
	if !ok {
		r = &record{}
		s.enrollments[id.ID] = r
	}
	e := rec.Enrollment
	e.Push.Token = append([]byte(nil), e.Push.Token...)
	e.UnlockToken = append([]byte(nil), e.UnlockToken...)
	e.AuthenticateRaw = append([]byte(nil), e.AuthenticateRaw...)
	e.TokenUpdateRaw = append([]byte(nil), e.TokenUpdateRaw...)
	r.Enrollment = e
	if id.Channel.IsUser() {
		return nil
	}
	s.dropCertLocked(id.ID)
	if e.CertHash != "" {
		s.certs[e.CertHash] = id
	}
	if len(rec.BootstrapToken) > 0 {
		s.bootstrap[id.ID] = append([]byte(nil), rec.BootstrapToken...)
	} else {
		delete(s.bootstrap, id.ID)
	}
	for _, a := range rec.CertHistory {
		s.recordAssociationLocked(id, a.Hash, a.At)
	}
	return nil
}
