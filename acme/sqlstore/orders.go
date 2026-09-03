package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// orderCols carry the account the order belongs to, the authorization it
// owns, and the expiry Prune reads. authz_id is a copy of the record's
// link and is what lets Prune take an order's authorization and challenge
// with it without decoding every row.
var orderCols = []string{"id", "account_id", "authz_id", "status", "expires", "created_at", "record"}

var authorizationCols = []string{"id", "order_id", "expires", "record"}

var challengeCols = []string{"id", "authz_id", "record"}

// PutOrder implements acme.Writer.
func (t *txStore) PutOrder(ctx context.Context, o *acme.Order) error {
	if o == nil {
		return fmt.Errorf("%w: nil order", acme.ErrInvalid)
	}
	if err := validID("order id", o.ID); err != nil {
		return err
	}
	if err := validID("order account id", o.AccountID); err != nil {
		return err
	}
	raw, err := encode("order", o)
	if err != nil {
		return err
	}
	return t.put(ctx, "put order", "acme_orders", orderCols,
		[]any{o.ID, o.AccountID, o.AuthzID, o.Status, nullTime(o.Expires), nullTime(o.CreatedAt), raw})
}

// GetOrder implements acme.Reader.
func (t *txStore) GetOrder(ctx context.Context, id string) (*acme.Order, error) {
	var o acme.Order
	if err := t.get(ctx, "get order", "acme_orders", "order", id, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// ListOrders implements acme.Reader.
func (t *txStore) ListOrders(
	ctx context.Context,
	accountID string,
	p storage.Page,
) (storage.Result[acme.Order], error) {
	if err := validID("account id", accountID); err != nil {
		return storage.Result[acme.Order]{}, err
	}
	where, args := after([]string{"account_id = ?"}, []any{accountID}, "id", p)
	query := "SELECT id, record FROM acme_orders WHERE " + strings.Join(where, " AND ") + " ORDER BY id"
	return keyset(ctx, t, "list orders", query, args, p, func(rows *sql.Rows) (acme.Order, string, error) {
		var o acme.Order
		id, err := scanRecord(rows, "acme_orders", &o)
		return o, id, err
	})
}

// PutAuthorization implements acme.Writer.
func (t *txStore) PutAuthorization(ctx context.Context, a *acme.Authorization) error {
	if a == nil {
		return fmt.Errorf("%w: nil authorization", acme.ErrInvalid)
	}
	if err := validID("authorization id", a.ID); err != nil {
		return err
	}
	raw, err := encode("authorization", a)
	if err != nil {
		return err
	}
	return t.put(ctx, "put authorization", "acme_authorizations", authorizationCols,
		[]any{a.ID, a.OrderID, nullTime(a.Expires), raw})
}

// GetAuthorization implements acme.Reader.
func (t *txStore) GetAuthorization(ctx context.Context, id string) (*acme.Authorization, error) {
	var a acme.Authorization
	if err := t.get(ctx, "get authorization", "acme_authorizations", "authorization", id, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// PutChallenge implements acme.Writer. The attestation object travels
// inside the record, where JSON encodes it as base64 and returns the same
// bytes: finalize verifies the stored attestation again against a
// certificate request the challenge never saw, so a re-encoded copy would
// not serve.
func (t *txStore) PutChallenge(ctx context.Context, c *acme.Challenge) error {
	if c == nil {
		return fmt.Errorf("%w: nil challenge", acme.ErrInvalid)
	}
	if err := validID("challenge id", c.ID); err != nil {
		return err
	}
	raw, err := encode("challenge", c)
	if err != nil {
		return err
	}
	return t.put(ctx, "put challenge", "acme_challenges", challengeCols, []any{c.ID, c.AuthzID, raw})
}

// GetChallenge implements acme.Reader.
func (t *txStore) GetChallenge(ctx context.Context, id string) (*acme.Challenge, error) {
	var c acme.Challenge
	if err := t.get(ctx, "get challenge", "acme_challenges", "challenge", id, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ClaimIdentifier implements acme.Writer. The primary key decides the
// race: a second insert for one identifier violates it, which is the
// conflict Apple's one-time client identifier calls for, and a claim
// naming the order that already holds it loses too because a retry that
// got this far has already had its order created.
func (t *txStore) ClaimIdentifier(ctx context.Context, identifier, orderID string) error {
	if err := validID("client identifier", identifier); err != nil {
		return err
	}
	if err := validID("order id", orderID); err != nil {
		return err
	}
	_, err := t.exec(ctx, "claim identifier",
		"INSERT INTO acme_claims (identifier, order_id, claimed_at) VALUES (?, ?, ?)",
		identifier, orderID, time.Now().UTC())
	return err
}

// scanRecord reads an id and its record column from a listing row and
// decodes the record, returning the id as the page cursor.
func scanRecord(rows *sql.Rows, table string, v any) (string, error) {
	var id string
	var raw []byte
	if err := rows.Scan(&id, &raw); err != nil {
		return "", wrap("scan "+table, err)
	}
	return id, decode(table, id, raw, v)
}

// Store methods outside Update run against the pool.

// GetOrder implements acme.Reader.
func (s *Store) GetOrder(ctx context.Context, id string) (*acme.Order, error) {
	return s.view().GetOrder(ctx, id)
}

// ListOrders implements acme.Reader.
func (s *Store) ListOrders(
	ctx context.Context,
	accountID string,
	p storage.Page,
) (storage.Result[acme.Order], error) {
	return s.view().ListOrders(ctx, accountID, p)
}

// GetAuthorization implements acme.Reader.
func (s *Store) GetAuthorization(ctx context.Context, id string) (*acme.Authorization, error) {
	return s.view().GetAuthorization(ctx, id)
}

// GetChallenge implements acme.Reader.
func (s *Store) GetChallenge(ctx context.Context, id string) (*acme.Challenge, error) {
	return s.view().GetChallenge(ctx, id)
}
