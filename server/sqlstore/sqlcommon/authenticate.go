package sqlcommon

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// EnrollmentByID implements storage.EnrollmentStore.
func (s *Store) EnrollmentByID(ctx context.Context, id string) (*storage.Enrollment, error) {
	e, err := scanEnrollment(s.db.QueryRowContext(ctx, s.q(selectEnrollment+" WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, wrap("resolve enrollment", err)
	}
	if err := s.openEnrollment(e); err != nil {
		return nil, err
	}
	return e, nil
}

// identity reads the complete immutable identity, optionally locking lifecycle
// changes. Callers must lock the parent before a child to keep lock order stable.
func (s *Store) identity(
	ctx context.Context,
	q querier,
	id mdm.EnrollmentID,
	lock bool,
) (*storage.Enrollment, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	if lock {
		if _, err := q.ExecContext(
			ctx,
			s.q("UPDATE enrollments SET id = id WHERE id = ?"),
			id.ID,
		); err != nil {
			return nil, wrap("lock enrollment", err)
		}
	}
	e, err := scanEnrollment(q.QueryRowContext(ctx, s.q(selectEnrollment+" WHERE id = ?"), id.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, wrap("read enrollment identity", err)
	}
	if e.ID != id {
		return nil, storage.ErrNotFound
	}
	return e, nil
}

// ensureEnrollment serializes both first creation and existing-row transitions.
// Inserting an unpinned pending row and all subsequent changes share one commit.
func (s *Store) ensureEnrollment(
	ctx context.Context,
	q querier,
	id mdm.EnrollmentID,
	at time.Time,
) (*storage.Enrollment, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	if id.Channel.IsUser() {
		p, err := s.identity(ctx, q, id.Device(), true)
		if err != nil {
			return nil, err
		}
		if !p.DisabledAt.IsZero() {
			return nil, storage.ErrDisabled
		}
	}
	cols := []string{"id", "channel", "parent_id", "device_name", "enrolled_at", "last_seen_at"}
	if _, err := q.ExecContext(
		ctx,
		s.q(s.d.InsertIgnore("enrollments", cols, cols[:1])),
		id.ID,
		int(id.Channel),
		id.ParentID,
		"",
		at.UTC(),
		at.UTC(),
	); err != nil {
		return nil, wrap("initialize enrollment", err)
	}
	e, err := s.identity(ctx, q, id, true)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, storage.ErrConflict
	}
	return e, err
}

// deviceIdentity permits a user handshake before its enrollment row exists,
// while rejecting a raw ID already belonging to another channel or parent.
func (s *Store) deviceIdentity(ctx context.Context, q querier, id mdm.EnrollmentID) error {
	if err := validID(id); err != nil {
		return err
	}
	if id.Channel.IsUser() {
		var channel int
		var parent string
		err := q.QueryRowContext(ctx, s.q("SELECT channel, parent_id FROM enrollments WHERE id = ?"), id.ID).
			Scan(&channel, &parent)
		if err == nil && (channel != int(id.Channel) || parent != id.ParentID) {
			return storage.ErrNotFound
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return wrap("user identity", err)
		}
	}
	return s.exists(ctx, q, id.Device())
}

func (s *Store) usable(ctx context.Context, q querier, id mdm.EnrollmentID, active bool) error {
	if id.Channel.IsUser() {
		p, err := s.identity(ctx, q, id.Device(), true)
		if err != nil {
			return err
		}
		if !p.DisabledAt.IsZero() || active && !p.Enabled {
			return storage.ErrDisabled
		}
	}
	e, err := s.identity(ctx, q, id, true)
	if err != nil {
		return err
	}
	if !e.DisabledAt.IsZero() || active && !e.Enabled {
		return storage.ErrDisabled
	}
	return nil
}

// AuthenticateEnrollment implements storage.EnrollmentStore.
func (s *Store) AuthenticateEnrollment(
	ctx context.Context,
	id mdm.EnrollmentID,
	c storage.AuthenticateChange,
) error {
	if _, err := storage.CheckAuthenticate(
		id,
		nil,
		storage.AuthenticateChange{At: c.At},
	); err != nil {
		return err
	}
	return s.tx(ctx, func(q querier) error {
		e, err := s.ensureEnrollment(ctx, q, id, c.At)
		if err != nil {
			return err
		}
		retry, err := storage.CheckAuthenticate(id, e, c)
		if err != nil || retry {
			return err
		}
		if c.Hash != "" && !c.AllowReuse {
			var n int
			if err := q.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM cert_associations WHERE cert_hash = ? AND enrollment_id <> ?"), c.Hash, id.ID).
				Scan(&n); err != nil {
				return wrap("certificate history", err)
			}
			if n != 0 {
				return storage.ErrConflict
			}
		}
		if err := s.resetAuthenticate(ctx, q, id, c.Message, c.Raw, c.At); err != nil {
			return err
		}
		if c.Hash != "" {
			_, err := q.ExecContext(
				ctx,
				s.q("UPDATE enrollments SET cert_hash = ?, cert_hash_at = ? WHERE id = ?"),
				c.Hash,
				c.At.UTC(),
				id.ID,
			)
			if s.d.uniqueViolation(err) {
				return storage.ErrConflict
			}
			if err != nil {
				return wrap("pin identity", err)
			}
			return s.recordAssociation(ctx, q, id.ID, c.Hash, c.At)
		}
		return nil
	})
}
