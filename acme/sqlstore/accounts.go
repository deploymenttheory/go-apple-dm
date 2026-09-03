package sqlstore

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/acme"
)

// accountCols are the indexed copies beside the record: the thumbprint is
// the account's identity and carries the unique index, the status and the
// creation time are what an operator lists accounts by.
var accountCols = []string{"id", "thumbprint", "status", "created_at", "record"}

// PutAccount implements acme.Writer.
func (t *txStore) PutAccount(ctx context.Context, a *acme.Account) error {
	if a == nil {
		return fmt.Errorf("%w: nil account", acme.ErrInvalid)
	}
	if err := validID("account id", a.ID); err != nil {
		return err
	}
	if err := validID("account thumbprint", a.Thumbprint); err != nil {
		return err
	}
	raw, err := encode("account", a)
	if err != nil {
		return err
	}
	// The unique index on thumbprint is what makes a second registration of
	// one key a conflict. Reading the thumbprint first and writing after
	// would let two registrations of the same key both find it free.
	return t.put(ctx, "put account", "acme_accounts", accountCols, []any{a.ID, a.Thumbprint, a.Status, nullTime(a.CreatedAt), raw})
}

// GetAccount implements acme.Reader.
func (t *txStore) GetAccount(ctx context.Context, id string) (*acme.Account, error) {
	var a acme.Account
	if err := t.get(ctx, "get account", "acme_accounts", "account", id, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// AccountByThumbprint implements acme.Reader. RFC 8555 makes a key the
// identity of an account, so the index this reads is the same one that
// refuses a duplicate registration.
func (t *txStore) AccountByThumbprint(ctx context.Context, thumbprint string) (*acme.Account, error) {
	if err := validID("account thumbprint", thumbprint); err != nil {
		return nil, err
	}
	var id string
	var raw []byte
	found, err := t.row(ctx, "get account by thumbprint", "SELECT id, record FROM acme_accounts WHERE thumbprint = ?", []any{thumbprint}, &id, &raw)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, notFound("account for thumbprint", thumbprint)
	}
	var a acme.Account
	if err := decode("acme_accounts", id, raw, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Store methods outside Update run against the pool.

// GetAccount implements acme.Reader.
func (s *Store) GetAccount(ctx context.Context, id string) (*acme.Account, error) {
	return s.view().GetAccount(ctx, id)
}

// AccountByThumbprint implements acme.Reader.
func (s *Store) AccountByThumbprint(ctx context.Context, thumbprint string) (*acme.Account, error) {
	return s.view().AccountByThumbprint(ctx, thumbprint)
}
