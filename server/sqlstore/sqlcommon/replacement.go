package sqlcommon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

const purposeReplacement = "enrollment_replacements.state_blob"

var _ storage.ReplacementStore = (*Store)(nil)

func (s *Store) TransitionReplacement(ctx context.Context, id mdm.EnrollmentID, change storage.ReplacementChange) (*storage.Replacement, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	if id.Channel.IsUser() {
		return nil, fmt.Errorf("%w: replacement requires a device channel", storage.ErrInvalid)
	}
	var r *storage.Replacement
	err := s.tx(ctx, func(q querier) error {
		// This row exists even before the first attempt. Its write lock serializes
		// replacement with ordinary enrollment changes on all three dialects.
		res, err := q.ExecContext(ctx, s.q("UPDATE enrollments SET id = id WHERE id = ?"), id.ID)
		if err != nil {
			return wrap("lock replacement enrollment", err)
		}
		if err = notFoundIfNoRows(res, id.ID); err != nil {
			return err
		}
		e, err := scanEnrollment(q.QueryRowContext(ctx, s.q(selectEnrollment+" WHERE id = ?"), id.ID))
		if err != nil {
			return wrap("read replacement enrollment", err)
		}
		var blob []byte
		err = q.QueryRowContext(ctx, s.q("SELECT state_blob FROM enrollment_replacements WHERE enrollment_id = ?"), id.ID).Scan(&blob)
		if err == nil {
			blob, err = s.open(purposeReplacement, id.ID, blob)
			if err != nil {
				return err
			}
			if err = json.Unmarshal(blob, &r); err != nil {
				return wrap("decode replacement", err)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return wrap("read replacement", err)
		}
		commit, err := storage.AdvanceReplacement(&r, e, change)
		if err != nil {
			return err
		}
		if r == nil {
			return nil
		}
		if commit {
			if err := s.commitReplacement(ctx, q, id, r, change.At); err != nil {
				return err
			}
		}
		blob, err = json.Marshal(storage.CloneReplacement(r))
		if err != nil {
			return wrap("encode replacement", err)
		}
		blob, err = s.seal(purposeReplacement, id.ID, blob)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, s.q(s.d.Upsert("enrollment_replacements", []string{"enrollment_id", "state_blob"}, []string{"enrollment_id"})), id.ID, blob)
		if err != nil {
			return wrap("write replacement", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return storage.CloneReplacement(r), nil
}

func (s *Store) commitReplacement(ctx context.Context, q querier, id mdm.EnrollmentID, r *storage.Replacement, at time.Time) error {
	var n int
	if err := q.QueryRowContext(ctx, s.q("SELECT COUNT(*) FROM cert_associations WHERE cert_hash = ? AND enrollment_id <> ?"), r.CandidateHash, id.ID).Scan(&n); err != nil {
		return wrap("candidate history", err)
	}
	if n != 0 {
		return fmt.Errorf("%w: candidate certificate reused", storage.ErrConflict)
	}
	_, err := q.ExecContext(ctx, s.q("UPDATE enrollments SET cert_hash = ?, cert_hash_at = ?, authenticate_raw = ? WHERE id = ?"), r.CandidateHash, at.UTC(), r.AuthenticateRaw, id.ID)
	if s.d.uniqueViolation(err) {
		return fmt.Errorf("%w: candidate certificate reused", storage.ErrConflict)
	}
	if err != nil {
		return wrap("commit replacement identity", err)
	}
	if err := s.recordAssociation(ctx, q, id.ID, r.CandidateHash, at); err != nil {
		return err
	}
	for _, t := range r.Tokens {
		if err := s.replacementToken(ctx, q, t, at); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) replacementToken(ctx context.Context, q querier, t storage.ReplacementToken, at time.Time) error {
	e, err := scanEnrollment(q.QueryRowContext(ctx, s.q(selectEnrollment+" WHERE id = ?"), t.ID.ID))
	if errors.Is(err, sql.ErrNoRows) {
		e = &storage.Enrollment{ID: t.ID, EnrolledAt: at}
		// Insert the user channel with defaults; never upsert/reset an existing row.
		_, err = q.ExecContext(ctx, s.q(s.d.InsertIgnore("enrollments", enrollmentCols, []string{"id"})),
			t.ID.ID, int(t.ID.Channel), t.ID.ParentID, false, "", "", nil,
			"", "", "", "", "", "", "", "", "", "",
			"", "", false, "", nil, nil, nil, nil, nil, nil, nil, at.UTC(), nil, at.UTC(), nil)
	}
	if err != nil {
		return wrap("read replacement token channel", err)
	}
	if err = s.openEnrollment(e); err != nil {
		return err
	}
	storage.ApplyReplacementToken(e, t, at)
	unlock, err := s.seal(purposeUnlockToken, e.ID.ID, e.UnlockToken)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, s.q("UPDATE enrollments SET topic = ?, push_magic = ?, push_token = ?, enabled = ?, token_updated_at = ?, last_seen_at = ?, disabled_at = NULL, token_update_raw = ?, unlock_token = ?, user_short_name = ?, user_long_name = ?, not_on_console = ?, enrollment_user_id = ? WHERE id = ?"),
		e.Push.Topic, e.Push.Magic, e.Push.Token, true, at.UTC(), at.UTC(), e.TokenUpdateRaw, unlock, e.UserShortName, e.UserLongName, e.NotOnConsole, e.EnrollmentUserID, e.ID.ID)
	if err != nil {
		return wrap("commit replacement token", err)
	}
	return nil
}
