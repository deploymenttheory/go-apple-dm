package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme"
)

var nonceCols = []string{"value", "issued_at"}

// PutNonce implements acme.Store.
func (s *Store) PutNonce(ctx context.Context, n acme.Nonce) error {
	if err := validID("nonce value", n.Value); err != nil {
		return err
	}
	return s.runInTx(ctx, func(t *txStore) error {
		return t.put(ctx, "put nonce", "acme_nonces", nonceCols, []any{n.Value, nullTime(n.IssuedAt)})
	})
}

// TakeNonce implements acme.Store. Removing the row is what takes the
// nonce, so the winner of a race is whichever caller's DELETE removed a
// row and the loser sees ErrNotFound, which is also how the server detects
// a replay: the first use removed it.
func (s *Store) TakeNonce(ctx context.Context, value string) (*acme.Nonce, error) {
	if err := validID("nonce value", value); err != nil {
		return nil, err
	}
	if s.mysql {
		return s.takeNonceDeleted(ctx, value)
	}
	// SQLite and PostgreSQL delete and return in one statement, so there is
	// no window between reading the nonce and consuming it.
	var n acme.Nonce
	var issued sql.NullTime
	found, err := s.view().row(ctx, "take nonce",
		"DELETE FROM acme_nonces WHERE value = ? RETURNING value, issued_at", []any{value}, &n.Value, &issued)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, notFound("nonce", value)
	}
	n.IssuedAt = fromNull(issued)
	return &n, nil
}

// takeNonceDeleted is TakeNonce where the engine has no DELETE ...
// RETURNING. The row is read and deleted in one transaction, and the
// DELETE is what decides the race: only the caller whose statement removed
// a row took the nonce, and a caller whose read saw a nonce another taker
// had already removed finds nothing to delete. The read is deliberately
// not a locking one, because InnoDB answers a locking read of a row that
// is not there with a gap lock and concurrent takers would deadlock over
// nonces neither of them can have.
func (s *Store) takeNonceDeleted(ctx context.Context, value string) (*acme.Nonce, error) {
	var n acme.Nonce
	err := s.runInTx(ctx, func(t *txStore) error {
		var issued sql.NullTime
		found, err := t.row(ctx, "take nonce",
			"SELECT value, issued_at FROM acme_nonces WHERE value = ?", []any{value}, &n.Value, &issued)
		if err != nil {
			return err
		}
		if !found {
			return notFound("nonce", value)
		}
		res, err := t.exec(ctx, "take nonce", "DELETE FROM acme_nonces WHERE value = ?", value)
		if err != nil {
			return err
		}
		removed, err := affected("take nonce", res)
		if err != nil {
			return err
		}
		if removed == 0 {
			return notFound("nonce", value)
		}
		n.IssuedAt = fromNull(issued)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// expiredOrders selects the orders past the cutoff. A zero expiry is a
// record that never expires and is stored as NULL, so it is kept: read as
// a timestamp it would be the distant past and would go on the first
// sweep.
const expiredOrders = "SELECT id FROM acme_orders WHERE expires IS NOT NULL AND expires < ?"

// expiredAuthorizations is the condition an authorization meets when it
// has expired itself or the order it belongs to has. Both directions of
// the link the two records keep on each other are followed, because an
// authorization outliving its order names something no request could ever
// reach again.
const expiredAuthorizations = "(expires IS NOT NULL AND expires < ?)" +
	" OR order_id IN (" + expiredOrders + ")" +
	" OR id IN (SELECT authz_id FROM acme_orders WHERE expires IS NOT NULL AND expires < ?)"

// pruneStatements run in this order so no statement reads rows an earlier
// one has already deleted: a challenge goes before the authorization that
// owns it, and an authorization before its order. Accounts and issued
// certificates are absent deliberately; they are the record of what was
// given out.
var pruneStatements = []struct {
	query string
	// cutoffs is how many times the statement names the cutoff.
	cutoffs int
}{
	{"DELETE FROM acme_challenges WHERE authz_id IN (SELECT id FROM acme_authorizations WHERE " + expiredAuthorizations + ")", 3},
	{"DELETE FROM acme_authorizations WHERE " + expiredAuthorizations, 3},
	{"DELETE FROM acme_orders WHERE expires IS NOT NULL AND expires < ?", 1},
	// A nonce with no issue time is treated as issued at the beginning of
	// time and prunes, which is how the in-memory store reads a zero
	// IssuedAt.
	{"DELETE FROM acme_nonces WHERE issued_at IS NULL OR issued_at < ?", 1},
}

// Prune implements acme.Store.
func (s *Store) Prune(ctx context.Context, before time.Time) (int, error) {
	removed := 0
	err := s.runInTx(ctx, func(t *txStore) error {
		cutoff := before.UTC()
		for _, stmt := range pruneStatements {
			args := make([]any, stmt.cutoffs)
			for i := range args {
				args[i] = cutoff
			}
			res, err := t.exec(ctx, "prune", stmt.query, args...)
			if err != nil {
				return err
			}
			n, err := affected("prune", res)
			if err != nil {
				return err
			}
			removed += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}
