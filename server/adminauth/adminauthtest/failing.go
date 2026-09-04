package adminauthtest

import (
	"context"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
)

// ErrFailing is what a Failing store returns from the selected method.
var ErrFailing = errors.New("adminauthtest: injected failure")

// Failing wraps a Store and makes one named method fail, so a caller's error
// path is reachable without a broken database. Fail is the method name, for
// example "Principal" or "Policies"; an empty Fail passes everything through.
type Failing struct {
	adminauth.Store
	Fail string
}

func (f *Failing) fails(name string) bool { return f.Fail == name }

// CreatePrincipal implements adminauth.Store.
func (f *Failing) CreatePrincipal(ctx context.Context, p adminauth.Principal, digest string, now time.Time) (adminauth.Principal, error) {
	if f.fails("CreatePrincipal") {
		return adminauth.Principal{}, ErrFailing
	}
	return f.Store.CreatePrincipal(ctx, p, digest, now)
}

// Principal implements adminauth.Store.
func (f *Failing) Principal(ctx context.Context, name string) (adminauth.Principal, error) {
	if f.fails("Principal") {
		return adminauth.Principal{}, ErrFailing
	}
	return f.Store.Principal(ctx, name)
}

// PrincipalByDigest implements adminauth.Store.
func (f *Failing) PrincipalByDigest(ctx context.Context, digest string) (adminauth.Principal, error) {
	if f.fails("PrincipalByDigest") {
		return adminauth.Principal{}, ErrFailing
	}
	return f.Store.PrincipalByDigest(ctx, digest)
}

// Principals implements adminauth.Store.
func (f *Failing) Principals(ctx context.Context, p adminauth.Page) (adminauth.Result[adminauth.Principal], error) {
	if f.fails("Principals") {
		return adminauth.Result[adminauth.Principal]{}, ErrFailing
	}
	return f.Store.Principals(ctx, p)
}

// UpdatePrincipal implements adminauth.Store.
func (f *Failing) UpdatePrincipal(ctx context.Context, name string, roles []string, root bool, now time.Time) (adminauth.Principal, error) {
	if f.fails("UpdatePrincipal") {
		return adminauth.Principal{}, ErrFailing
	}
	return f.Store.UpdatePrincipal(ctx, name, roles, root, now)
}

// SetToken implements adminauth.Store.
func (f *Failing) SetToken(ctx context.Context, name, digest, tokenID string, expires, now time.Time) (adminauth.Principal, error) {
	if f.fails("SetToken") {
		return adminauth.Principal{}, ErrFailing
	}
	return f.Store.SetToken(ctx, name, digest, tokenID, expires, now)
}

// RevokeToken implements adminauth.Store.
func (f *Failing) RevokeToken(ctx context.Context, name string, now time.Time) error {
	if f.fails("RevokeToken") {
		return ErrFailing
	}
	return f.Store.RevokeToken(ctx, name, now)
}

// DeletePrincipal implements adminauth.Store.
func (f *Failing) DeletePrincipal(ctx context.Context, name string) error {
	if f.fails("DeletePrincipal") {
		return ErrFailing
	}
	return f.Store.DeletePrincipal(ctx, name)
}

// CountRoot implements adminauth.Store.
func (f *Failing) CountRoot(ctx context.Context) (int, error) {
	if f.fails("CountRoot") {
		return 0, ErrFailing
	}
	return f.Store.CountRoot(ctx)
}

// PutPolicy implements adminauth.Store.
func (f *Failing) PutPolicy(ctx context.Context, p adminauth.Policy, now time.Time) (adminauth.Policy, error) {
	if f.fails("PutPolicy") {
		return adminauth.Policy{}, ErrFailing
	}
	return f.Store.PutPolicy(ctx, p, now)
}

// GetPolicy implements adminauth.Store.
func (f *Failing) GetPolicy(ctx context.Context, name string) (adminauth.Policy, error) {
	if f.fails("GetPolicy") {
		return adminauth.Policy{}, ErrFailing
	}
	return f.Store.GetPolicy(ctx, name)
}

// Policies implements adminauth.Store.
func (f *Failing) Policies(ctx context.Context) ([]adminauth.Policy, error) {
	if f.fails("Policies") {
		return nil, ErrFailing
	}
	return f.Store.Policies(ctx)
}

// DeletePolicy implements adminauth.Store.
func (f *Failing) DeletePolicy(ctx context.Context, name string) error {
	if f.fails("DeletePolicy") {
		return ErrFailing
	}
	return f.Store.DeletePolicy(ctx, name)
}

// PolicyVersion implements adminauth.Store.
func (f *Failing) PolicyVersion(ctx context.Context) (int64, error) {
	if f.fails("PolicyVersion") {
		return 0, ErrFailing
	}
	return f.Store.PolicyVersion(ctx)
}
