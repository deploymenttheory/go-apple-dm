package sqlcommon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

var certAssociationCols = []string{"enrollment_id", "cert_hash", "associated_at"}

// AssociateCert implements storage.CertAuthStore. The unique index on
// cert_hash is the arbiter: a race between two enrollments for one hash
// ends with one winner and ErrConflict for the rest. The pin and its
// history row are written in one transaction (decision record 0014).
func (s *Store) AssociateCert(ctx context.Context, id mdm.EnrollmentID, hash string, at time.Time) error {
	if hash == "" {
		return fmt.Errorf("%w: empty certificate hash", storage.ErrInvalid)
	}
	if err := validID(id); err != nil {
		return err
	}
	dev := id.Device()
	at = at.UTC()
	return s.tx(ctx, func(q querier) error {
		res, err := q.ExecContext(ctx, s.q("UPDATE enrollments SET cert_hash = ?, cert_hash_at = ? WHERE id = ?"), hash, at, dev.ID)
		if s.d.uniqueViolation(err) {
			return fmt.Errorf("%w: certificate already associated with another enrollment", storage.ErrConflict)
		}
		if err != nil {
			return wrap("associate certificate", err)
		}
		if err := notFoundIfNoRows(res, dev.ID); err != nil {
			return err
		}
		return s.recordAssociation(ctx, q, dev.ID, hash, at)
	})
}

// recordAssociation appends to the history, keeping the first-seen time
// when the pair already exists.
func (s *Store) recordAssociation(ctx context.Context, q querier, deviceID, hash string, at time.Time) error {
	if _, err := q.ExecContext(ctx, s.q(s.d.InsertIgnore("cert_associations", certAssociationCols, certAssociationCols[:2])), deviceID, hash, at.UTC()); err != nil {
		return wrap("record certificate association", err)
	}
	return nil
}

const selectAssociation = "SELECT ca.enrollment_id, e.channel, e.parent_id, ca.cert_hash, ca.associated_at " +
	"FROM cert_associations ca JOIN enrollments e ON e.id = ca.enrollment_id"

func (s *Store) associations(ctx context.Context, where string, arg any) ([]storage.CertAssociation, error) {
	rows, err := s.db.QueryContext(ctx, s.q(selectAssociation+" WHERE "+where+" ORDER BY ca.associated_at, ca.cert_hash, ca.enrollment_id"), arg)
	if err != nil {
		return nil, wrap("certificate history", err)
	}
	defer rows.Close()
	out := []storage.CertAssociation{}
	for rows.Next() {
		var a storage.CertAssociation
		var channel int
		var at time.Time
		if err := rows.Scan(&a.ID.ID, &channel, &a.ID.ParentID, &a.Hash, &at); err != nil {
			return nil, wrap("scan certificate history", err)
		}
		a.ID.Channel = mdm.Channel(channel) // #nosec G115 -- stored from a uint8
		a.At = at.UTC()
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("certificate history", err)
	}
	return out, nil
}

// CertHistory implements storage.CertAuthStore.
func (s *Store) CertHistory(ctx context.Context, id mdm.EnrollmentID) ([]storage.CertAssociation, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	dev := id.Device()
	if err := s.exists(ctx, s.db, dev.ID); err != nil {
		return nil, err
	}
	return s.associations(ctx, "ca.enrollment_id = ?", dev.ID)
}

// CertHashHistory implements storage.CertAuthStore.
func (s *Store) CertHashHistory(ctx context.Context, hash string) ([]storage.CertAssociation, error) {
	if hash == "" {
		return nil, fmt.Errorf("%w: empty certificate hash", storage.ErrInvalid)
	}
	return s.associations(ctx, "ca.cert_hash = ?", hash)
}

// CertHash implements storage.CertAuthStore.
func (s *Store) CertHash(ctx context.Context, id mdm.EnrollmentID) (string, error) {
	if err := validID(id); err != nil {
		return "", err
	}
	var h sql.NullString
	err := s.db.QueryRowContext(ctx, s.q("SELECT cert_hash FROM enrollments WHERE id = ?"), id.Device().ID).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: enrollment %s", storage.ErrNotFound, id.Device().ID)
	}
	if err != nil {
		return "", wrap("cert hash", err)
	}
	return h.String, nil
}

// EnrollmentByCertHash implements storage.CertAuthStore.
func (s *Store) EnrollmentByCertHash(ctx context.Context, hash string) (mdm.EnrollmentID, error) {
	var id mdm.EnrollmentID
	var channel int
	err := s.db.QueryRowContext(ctx, s.q("SELECT id, channel, parent_id FROM enrollments WHERE cert_hash = ?"), hash).Scan(&id.ID, &channel, &id.ParentID)
	if errors.Is(err, sql.ErrNoRows) {
		return mdm.EnrollmentID{}, fmt.Errorf("%w: certificate hash", storage.ErrNotFound)
	}
	if err != nil {
		return mdm.EnrollmentID{}, wrap("enrollment by certificate", err)
	}
	id.Channel = mdm.Channel(channel) // #nosec G115 -- stored from a uint8
	return id, nil
}
