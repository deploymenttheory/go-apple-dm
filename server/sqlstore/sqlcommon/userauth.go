package sqlcommon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
)

var userAuthCols = []string{"enrollment_id", "parent_id", "challenge", "challenge_at", "auth_token", "token_at", "authenticate_raw", "digest_raw", "updated_at"}

// userAuthTarget validates a user-channel id and checks that its parent
// device enrollment exists (decision record 0016).
func (s *Store) userAuthTarget(ctx context.Context, q querier, id mdm.EnrollmentID) error {
	if err := validID(id); err != nil {
		return err
	}
	if !id.Channel.IsUser() {
		return fmt.Errorf("%w: %w: %s", storage.ErrInvalid, storage.ErrUserChannelRequired, id.ID)
	}
	return s.exists(ctx, q, id.Device().ID)
}

// StoreUserAuthChallenge implements storage.UserAuthStore.
func (s *Store) StoreUserAuthChallenge(ctx context.Context, id mdm.EnrollmentID, challenge string, raw []byte, at time.Time) error {
	if challenge == "" {
		return fmt.Errorf("%w: empty challenge", storage.ErrInvalid)
	}
	at = at.UTC()
	return s.tx(ctx, func(q querier) error {
		if err := s.userAuthTarget(ctx, q, id); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, s.q(s.d.Upsert("user_auth", userAuthCols, []string{"enrollment_id"})),
			id.ID, id.Device().ID, challenge, at, nil, nil, nullBytes(raw), nil, at); err != nil {
			return wrap("store user auth challenge", err)
		}
		return nil
	})
}

// StoreUserAuthToken implements storage.UserAuthStore.
func (s *Store) StoreUserAuthToken(ctx context.Context, id mdm.EnrollmentID, token string, raw []byte, at time.Time) error {
	if token == "" {
		return fmt.Errorf("%w: empty token", storage.ErrInvalid)
	}
	at = at.UTC()
	return s.tx(ctx, func(q querier) error {
		if err := s.userAuthTarget(ctx, q, id); err != nil {
			return err
		}
		sealed, err := s.seal(purposeUserAuthToken, id.ID, []byte(token))
		if err != nil {
			return err
		}
		res, err := q.ExecContext(ctx, s.q("UPDATE user_auth SET auth_token = ?, token_at = ?, digest_raw = ?, challenge = NULL, challenge_at = NULL, updated_at = ? WHERE enrollment_id = ?"),
			sealed, at, nullBytes(raw), at, id.ID)
		if err != nil {
			return wrap("store user auth token", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return wrap("rows affected", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: no challenge issued for %s", storage.ErrNotFound, id.ID)
		}
		return nil
	})
}

// UserAuth implements storage.UserAuthStore.
func (s *Store) UserAuth(ctx context.Context, id mdm.EnrollmentID) (*storage.UserAuthState, error) {
	if err := s.userAuthTarget(ctx, s.db, id); err != nil {
		return nil, err
	}
	var (
		st                     storage.UserAuthState
		challenge              sql.NullString
		challengeAt, tokenAt   sql.NullTime
		token, authRaw, digest []byte
	)
	err := s.db.QueryRowContext(ctx, s.q("SELECT challenge, challenge_at, auth_token, token_at, authenticate_raw, digest_raw FROM user_auth WHERE enrollment_id = ?"), id.ID).
		Scan(&challenge, &challengeAt, &token, &tokenAt, &authRaw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: user auth state for %s", storage.ErrNotFound, id.ID)
	}
	if err != nil {
		return nil, wrap("user auth state", err)
	}
	opened, err := s.open(purposeUserAuthToken, id.ID, token)
	if err != nil {
		return nil, err
	}
	st.ID = id
	st.Challenge, st.ChallengeAt = challenge.String, fromNull(challengeAt)
	st.AuthToken, st.TokenAt = string(opened), fromNull(tokenAt)
	st.AuthenticateRaw = append([]byte(nil), authRaw...)
	st.DigestRaw = append([]byte(nil), digest...)
	return &st, nil
}

// ClearUserAuth implements storage.UserAuthStore.
func (s *Store) ClearUserAuth(ctx context.Context, id mdm.EnrollmentID) error {
	if err := s.userAuthTarget(ctx, s.db, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, s.q("DELETE FROM user_auth WHERE enrollment_id = ?"), id.ID); err != nil {
		return wrap("clear user auth", err)
	}
	return nil
}
