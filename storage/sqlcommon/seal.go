package sqlcommon

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-mdm/storage/crypt"
)

// Purposes name the sealed columns; each becomes the AAD prefix that binds
// a ciphertext to its column (decision record 0013).
const (
	purposeUnlockToken    = "enrollments.unlock_token"    // #nosec G101 -- a column name, not a credential
	purposeBootstrapToken = "enrollments.bootstrap_token" // #nosec G101 -- a column name, not a credential
	purposePushKey        = "push_certs.key_pem"          // #nosec G101 -- a column name, not a credential
	purposeUserAuthToken  = "user_auth.auth_token"        // #nosec G101 -- a column name, not a credential
)

// Option configures New.
type Option func(*Store)

// WithKeyring seals the secret columns (unlock tokens, bootstrap tokens,
// push private keys, user auth tokens) with the keyring. Without one the
// columns are stored in plaintext, which is the pre-0013 behaviour.
func WithKeyring(k *crypt.Keyring) Option { return func(s *Store) { s.keyring = k } }

// seal encrypts b for the column purpose and row. Empty input stays nil so
// COALESCE(?, col) semantics survive; without a keyring b is returned as is.
func (s *Store) seal(purpose, rowID string, b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if s.keyring == nil {
		return b, nil
	}
	out, err := s.keyring.Seal(b, crypt.AAD(purpose, rowID))
	if err != nil {
		return nil, wrap("seal "+purpose, err)
	}
	return out, nil
}

// open decrypts a stored column value. Plaintext rows are returned as is
// until the keyring is Strict; a sealed row without a keyring is an error.
func (s *Store) open(purpose, rowID string, b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if !crypt.IsSealed(b) {
		if s.keyring != nil && s.keyring.Strict() {
			return nil, fmt.Errorf("%w: %s for %s", crypt.ErrUnsealed, purpose, rowID)
		}
		return append([]byte(nil), b...), nil
	}
	if s.keyring == nil {
		return nil, fmt.Errorf("%w: %s for %s is sealed", crypt.ErrNoKeyring, purpose, rowID)
	}
	pt, _, err := s.keyring.Open(b, crypt.AAD(purpose, rowID))
	if err != nil {
		return nil, wrap("open "+purpose, err)
	}
	return pt, nil
}

// sealedColumn describes one column Rewrap maintains.
type sealedColumn struct {
	table, col, idCol, purpose string
}

var sealedColumns = []sealedColumn{
	{"enrollments", "unlock_token", "id", purposeUnlockToken},
	{"enrollments", "bootstrap_token", "id", purposeBootstrapToken},
	{"push_certs", "key_pem", "topic", purposePushKey},
	{"user_auth", "auth_token", "enrollment_id", purposeUserAuthToken},
}

// RewrapBatchSize bounds the rows read per SELECT during Rewrap.
const RewrapBatchSize = 500

// Rewrap re-seals every value that is unsealed or sealed under a retired
// key so it uses the active key, and returns how many rows it rewrote.
// Rows changed by another writer between read and write are skipped, so
// callers loop until it returns 0.
func (s *Store) Rewrap(ctx context.Context) (int, error) {
	if s.keyring == nil {
		return 0, crypt.ErrNoKeyring
	}
	total := 0
	for _, c := range sealedColumns {
		n, err := s.rewrapColumn(ctx, c)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

type rewrapRow struct {
	id    string
	value []byte
}

func (s *Store) rewrapColumn(ctx context.Context, c sealedColumn) (int, error) {
	active := s.keyring.Active()
	total := 0
	cursor := ""
	for {
		rows, err := s.rewrapPage(ctx, c, cursor)
		if err != nil {
			return total, err
		}
		if len(rows) == 0 {
			return total, nil
		}
		for _, r := range rows {
			cursor = r.id
			if name, ok := crypt.KeyName(r.value); ok && name == active {
				continue
			}
			pt, err := s.open(c.purpose, r.id, r.value)
			if err != nil {
				return total, err
			}
			sealed, err := s.seal(c.purpose, r.id, pt)
			if err != nil {
				return total, err
			}
			// The old bytes guard the write so a concurrent update is never
			// overwritten with stale plaintext.
			res, err := s.db.ExecContext(ctx, s.q("UPDATE "+c.table+" SET "+c.col+" = ? WHERE "+c.idCol+" = ? AND "+c.col+" = ?"), sealed, r.id, r.value)
			if err != nil {
				return total, wrap("rewrap "+c.purpose, err)
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				total++
			}
		}
		if len(rows) < RewrapBatchSize {
			return total, nil
		}
	}
}

func (s *Store) rewrapPage(ctx context.Context, c sealedColumn, cursor string) ([]rewrapRow, error) {
	rows, err := s.db.QueryContext(ctx, s.q("SELECT "+c.idCol+", "+c.col+" FROM "+c.table+" WHERE "+c.col+" IS NOT NULL AND "+c.idCol+" > ? ORDER BY "+c.idCol+" LIMIT ?"), cursor, RewrapBatchSize)
	if err != nil {
		return nil, wrap("rewrap scan "+c.purpose, err)
	}
	defer rows.Close()
	var out []rewrapRow
	for rows.Next() {
		var r rewrapRow
		if err := rows.Scan(&r.id, &r.value); err != nil {
			return nil, wrap("rewrap scan "+c.purpose, err)
		}
		if len(r.value) == 0 {
			continue
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("rewrap scan "+c.purpose, err)
	}
	return out, nil
}
