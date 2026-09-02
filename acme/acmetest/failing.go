package acmetest

import (
	"context"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/acme"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// Failing wraps an acme.Store and returns the error in Fail for any method
// named there, inside Update too, so the server's error paths can be
// exercised without a broken database.
type Failing struct {
	Store acme.Store
	// Fail maps a method name (PutOrder, TakeNonce, ...) to the error it
	// returns.
	Fail map[string]error
	// After lets a method fail only from the Nth call on (1 fails at
	// once); calls are counted per method.
	After map[string]int
	calls map[string]int
}

var _ acme.Store = (*Failing)(nil)

func (f *Failing) fail(method string) error {
	err, ok := f.Fail[method]
	if !ok {
		return nil
	}
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
	if after, ok := f.After[method]; ok && f.calls[method] < after {
		return nil
	}
	return fmt.Errorf("acmetest: %s: %w", method, err)
}

// txView is the transaction the callback receives.
type txView struct {
	f  *Failing
	tx acme.Tx
}

// Update implements acme.Store: fn runs inside the wrapped store's
// transaction against a wrapped Tx.
func (f *Failing) Update(ctx context.Context, fn func(acme.Tx) error) error {
	if err := f.fail("Update"); err != nil {
		return err
	}
	return f.Store.Update(ctx, func(tx acme.Tx) error { return fn(&txView{f: f, tx: tx}) })
}

// GetAccount implements acme.Reader.
func (f *Failing) GetAccount(ctx context.Context, id string) (*acme.Account, error) {
	if err := f.fail("GetAccount"); err != nil {
		return nil, err
	}
	return f.Store.GetAccount(ctx, id)
}

// AccountByThumbprint implements acme.Reader.
func (f *Failing) AccountByThumbprint(ctx context.Context, thumbprint string) (*acme.Account, error) {
	if err := f.fail("AccountByThumbprint"); err != nil {
		return nil, err
	}
	return f.Store.AccountByThumbprint(ctx, thumbprint)
}

// GetOrder implements acme.Reader.
func (f *Failing) GetOrder(ctx context.Context, id string) (*acme.Order, error) {
	if err := f.fail("GetOrder"); err != nil {
		return nil, err
	}
	return f.Store.GetOrder(ctx, id)
}

// GetAuthorization implements acme.Reader.
func (f *Failing) GetAuthorization(ctx context.Context, id string) (*acme.Authorization, error) {
	if err := f.fail("GetAuthorization"); err != nil {
		return nil, err
	}
	return f.Store.GetAuthorization(ctx, id)
}

// GetChallenge implements acme.Reader.
func (f *Failing) GetChallenge(ctx context.Context, id string) (*acme.Challenge, error) {
	if err := f.fail("GetChallenge"); err != nil {
		return nil, err
	}
	return f.Store.GetChallenge(ctx, id)
}

// GetCertificate implements acme.Reader.
func (f *Failing) GetCertificate(ctx context.Context, id string) (*acme.Certificate, error) {
	if err := f.fail("GetCertificate"); err != nil {
		return nil, err
	}
	return f.Store.GetCertificate(ctx, id)
}

// ListOrders implements acme.Reader.
func (f *Failing) ListOrders(
	ctx context.Context,
	accountID string,
	p storage.Page,
) (storage.Result[acme.Order], error) {
	if err := f.fail("ListOrders"); err != nil {
		return storage.Result[acme.Order]{}, err
	}
	return f.Store.ListOrders(ctx, accountID, p)
}

// ListCertificates implements acme.Reader.
func (f *Failing) ListCertificates(
	ctx context.Context,
	q acme.CertificateQuery,
	p storage.Page,
) (storage.Result[acme.Certificate], error) {
	if err := f.fail("ListCertificates"); err != nil {
		return storage.Result[acme.Certificate]{}, err
	}
	return f.Store.ListCertificates(ctx, q, p)
}

// PutNonce implements acme.Store.
func (f *Failing) PutNonce(ctx context.Context, n acme.Nonce) error {
	if err := f.fail("PutNonce"); err != nil {
		return err
	}
	return f.Store.PutNonce(ctx, n)
}

// TakeNonce implements acme.Store.
func (f *Failing) TakeNonce(ctx context.Context, value string) (*acme.Nonce, error) {
	if err := f.fail("TakeNonce"); err != nil {
		return nil, err
	}
	return f.Store.TakeNonce(ctx, value)
}

// Prune implements acme.Store.
func (f *Failing) Prune(ctx context.Context, before time.Time) (int, error) {
	if err := f.fail("Prune"); err != nil {
		return 0, err
	}
	return f.Store.Prune(ctx, before)
}

// The transaction view applies the same failures inside Update.

func (t *txView) GetAccount(ctx context.Context, id string) (*acme.Account, error) {
	if err := t.f.fail("GetAccount"); err != nil {
		return nil, err
	}
	return t.tx.GetAccount(ctx, id)
}

func (t *txView) AccountByThumbprint(ctx context.Context, thumbprint string) (*acme.Account, error) {
	if err := t.f.fail("AccountByThumbprint"); err != nil {
		return nil, err
	}
	return t.tx.AccountByThumbprint(ctx, thumbprint)
}

func (t *txView) GetOrder(ctx context.Context, id string) (*acme.Order, error) {
	if err := t.f.fail("GetOrder"); err != nil {
		return nil, err
	}
	return t.tx.GetOrder(ctx, id)
}

func (t *txView) GetAuthorization(ctx context.Context, id string) (*acme.Authorization, error) {
	if err := t.f.fail("GetAuthorization"); err != nil {
		return nil, err
	}
	return t.tx.GetAuthorization(ctx, id)
}

func (t *txView) GetChallenge(ctx context.Context, id string) (*acme.Challenge, error) {
	if err := t.f.fail("GetChallenge"); err != nil {
		return nil, err
	}
	return t.tx.GetChallenge(ctx, id)
}

func (t *txView) GetCertificate(ctx context.Context, id string) (*acme.Certificate, error) {
	if err := t.f.fail("GetCertificate"); err != nil {
		return nil, err
	}
	return t.tx.GetCertificate(ctx, id)
}

func (t *txView) ListOrders(
	ctx context.Context,
	accountID string,
	p storage.Page,
) (storage.Result[acme.Order], error) {
	if err := t.f.fail("ListOrders"); err != nil {
		return storage.Result[acme.Order]{}, err
	}
	return t.tx.ListOrders(ctx, accountID, p)
}

func (t *txView) ListCertificates(
	ctx context.Context,
	q acme.CertificateQuery,
	p storage.Page,
) (storage.Result[acme.Certificate], error) {
	if err := t.f.fail("ListCertificates"); err != nil {
		return storage.Result[acme.Certificate]{}, err
	}
	return t.tx.ListCertificates(ctx, q, p)
}

func (t *txView) PutAccount(ctx context.Context, a *acme.Account) error {
	if err := t.f.fail("PutAccount"); err != nil {
		return err
	}
	return t.tx.PutAccount(ctx, a)
}

func (t *txView) PutOrder(ctx context.Context, o *acme.Order) error {
	if err := t.f.fail("PutOrder"); err != nil {
		return err
	}
	return t.tx.PutOrder(ctx, o)
}

func (t *txView) PutAuthorization(ctx context.Context, a *acme.Authorization) error {
	if err := t.f.fail("PutAuthorization"); err != nil {
		return err
	}
	return t.tx.PutAuthorization(ctx, a)
}

func (t *txView) PutChallenge(ctx context.Context, c *acme.Challenge) error {
	if err := t.f.fail("PutChallenge"); err != nil {
		return err
	}
	return t.tx.PutChallenge(ctx, c)
}

func (t *txView) PutCertificate(ctx context.Context, c *acme.Certificate) error {
	if err := t.f.fail("PutCertificate"); err != nil {
		return err
	}
	return t.tx.PutCertificate(ctx, c)
}

func (t *txView) ClaimIdentifier(ctx context.Context, identifier, orderID string) error {
	if err := t.f.fail("ClaimIdentifier"); err != nil {
		return err
	}
	return t.tx.ClaimIdentifier(ctx, identifier, orderID)
}
