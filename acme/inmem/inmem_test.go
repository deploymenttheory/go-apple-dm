package inmem_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/acme"
	"github.com/deploymenttheory/go-apple-mdm/acme/acmetest"
	"github.com/deploymenttheory/go-apple-mdm/acme/inmem"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

func TestContract(t *testing.T) {
	acmetest.RunAll(t, func(_ *testing.T) acme.Store { return inmem.New() })
}

// must fails the test on an error the store should not have returned.
func must(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// TestPruneCounts pins the count this backend reports, which the contract
// suite deliberately leaves loose.
func TestPruneCounts(t *testing.T) {
	ctx := context.Background()
	s := inmem.New()
	// An order with its authorization and challenge (three), an order
	// naming an authorization that never existed (one), an authorization
	// naming a challenge that never existed (one), and one nonce: six
	// records go, and a cascade that finds nothing counts nothing.
	order := acmetest.Order("order-1", "acct-1")
	order.Expires = acmetest.T0
	authz := acmetest.Authorization("authz-order-1", "order-1", "acct-1")
	authz.Expires = acmetest.T0
	orphanOrder := acmetest.Order("order-2", "acct-1")
	orphanOrder.AuthzID, orphanOrder.Expires = "authz-that-never-existed", acmetest.T0
	loneAuthz := acmetest.Authorization("authz-lone", "order-3", "acct-1")
	loneAuthz.ChallengeID, loneAuthz.Expires = "challenge-that-never-existed", acmetest.T0
	must(t, "seed", s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutOrder(ctx, order); err != nil {
			return err
		}
		if err := tx.PutOrder(ctx, orphanOrder); err != nil {
			return err
		}
		if err := tx.PutAuthorization(ctx, authz); err != nil {
			return err
		}
		if err := tx.PutAuthorization(ctx, loneAuthz); err != nil {
			return err
		}
		return tx.PutChallenge(ctx, acmetest.Challenge(authz.ChallengeID, authz.ID, "acct-1"))
	}))
	must(t, "nonce", s.PutNonce(ctx, acme.Nonce{Value: "old", IssuedAt: acmetest.T0}))

	n, err := s.Prune(ctx, acmetest.T0.Add(time.Hour))
	must(t, "prune", err)
	if n != 6 {
		t.Fatalf("pruned %d records, want 6", n)
	}
}

// TestPruneKeepsRecordsWithoutAnExpiry proves a zero expiry reads as "no
// expiry" rather than as the distant past, which would delete a record
// the moment it was written.
func TestPruneKeepsRecordsWithoutAnExpiry(t *testing.T) {
	ctx := context.Background()
	s := inmem.New()
	order := acmetest.Order("order-1", "acct-1")
	order.Expires = time.Time{}
	authz := acmetest.Authorization("authz-order-1", "order-1", "acct-1")
	authz.Expires = time.Time{}
	must(t, "seed", s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutOrder(ctx, order); err != nil {
			return err
		}
		return tx.PutAuthorization(ctx, authz)
	}))
	n, err := s.Prune(ctx, acmetest.T0.Add(100*365*24*time.Hour))
	must(t, "prune", err)
	if n != 0 {
		t.Fatalf("pruned %d records without an expiry", n)
	}
	must(t, "order after the prune", func() error { _, err := s.GetOrder(ctx, "order-1"); return err }())
}

// TestPruneKeepsClaims proves a client identifier is not released when the
// order that took it is pruned. Apple calls it a one-time code, so
// releasing it would make the replay it exists to stop possible again,
// just later.
func TestPruneKeepsClaims(t *testing.T) {
	ctx := context.Background()
	s := inmem.New()
	order := acmetest.Order("order-1", "acct-1")
	order.Expires = acmetest.T0
	must(t, "seed", s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutOrder(ctx, order); err != nil {
			return err
		}
		return tx.ClaimIdentifier(ctx, order.Identifier.Value, order.ID)
	}))
	if _, err := s.Prune(ctx, acmetest.T0.Add(time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	err := s.Update(ctx, func(tx acme.Tx) error {
		return tx.ClaimIdentifier(ctx, order.Identifier.Value, "order-2")
	})
	if !errors.Is(err, acme.ErrConflict) {
		t.Fatalf("claim after the prune: got %v, want ErrConflict", err)
	}
}

// TestMalformedCursor covers the one thing a caller can get wrong about an
// opaque cursor: making one up.
func TestMalformedCursor(t *testing.T) {
	ctx := context.Background()
	s := inmem.New()
	page := storage.Page{Cursor: "not base64url!"}
	if _, err := s.ListOrders(ctx, "acct-1", page); !errors.Is(err, acme.ErrInvalid) {
		t.Errorf("ListOrders: got %v, want ErrInvalid", err)
	}
	if _, err := s.ListCertificates(ctx, acme.CertificateQuery{}, page); !errors.Is(err, acme.ErrInvalid) {
		t.Errorf("ListCertificates: got %v, want ErrInvalid", err)
	}
}

// TestTimestampsAreUTC pins the normalisation a SQL backend would do for
// nothing, so a record written in a local zone reads the same on either.
func TestTimestampsAreUTC(t *testing.T) {
	ctx := context.Background()
	s := inmem.New()
	local := time.FixedZone("plus2", 2*3600)
	account := acmetest.Account("acct-1")
	account.CreatedAt = acmetest.T0.In(local)
	order := acmetest.Order("order-1", "acct-1")
	order.Expires, order.CreatedAt = acmetest.T0.In(local), acmetest.T0.In(local)
	order.Binding.NotAfter = acmetest.T0.In(local)
	authz := acmetest.Authorization("authz-order-1", "order-1", "acct-1")
	authz.Expires = acmetest.T0.In(local)
	challenge := acmetest.Challenge("challenge-1", "authz-order-1", "acct-1")
	challenge.ValidatedAt = acmetest.T0.In(local)
	cert := acmetest.Certificate("cert-1", "order-1", "acct-1")
	cert.NotAfter, cert.IssuedAt = acmetest.T0.In(local), acmetest.T0.In(local)
	must(t, "seed", s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutAccount(ctx, account); err != nil {
			return err
		}
		if err := tx.PutOrder(ctx, order); err != nil {
			return err
		}
		if err := tx.PutAuthorization(ctx, authz); err != nil {
			return err
		}
		if err := tx.PutChallenge(ctx, challenge); err != nil {
			return err
		}
		return tx.PutCertificate(ctx, cert)
	}))
	must(t, "nonce", s.PutNonce(ctx, acme.Nonce{Value: "n", IssuedAt: acmetest.T0.In(local)}))

	gotAccount, err := s.GetAccount(ctx, "acct-1")
	must(t, "account", err)
	gotOrder, err := s.GetOrder(ctx, "order-1")
	must(t, "order", err)
	gotAuthz, err := s.GetAuthorization(ctx, "authz-order-1")
	must(t, "authorization", err)
	gotChallenge, err := s.GetChallenge(ctx, "challenge-1")
	must(t, "challenge", err)
	gotCert, err := s.GetCertificate(ctx, "cert-1")
	must(t, "certificate", err)
	gotNonce, err := s.TakeNonce(ctx, "n")
	must(t, "nonce", err)
	times := map[string]time.Time{
		"account created":       gotAccount.CreatedAt,
		"order expires":         gotOrder.Expires,
		"order created":         gotOrder.CreatedAt,
		"binding not after":     gotOrder.Binding.NotAfter,
		"authorization expires": gotAuthz.Expires,
		"challenge validated":   gotChallenge.ValidatedAt,
		"certificate not after": gotCert.NotAfter,
		"certificate issued":    gotCert.IssuedAt,
		"nonce issued":          gotNonce.IssuedAt,
	}
	for name, ts := range times {
		if ts.Location() != time.UTC {
			t.Errorf("%s is in %v, want UTC", name, ts.Location())
		}
		if !ts.Equal(acmetest.T0) {
			t.Errorf("%s is %v, want %v", name, ts, acmetest.T0)
		}
	}
}

// TestOptionalFieldsStayAbsent covers the copies of the pointers a record
// may not have: an account whose key was not stored, a problem with no
// subproblem identifier, and a challenge with no attestation.
func TestOptionalFieldsStayAbsent(t *testing.T) {
	ctx := context.Background()
	s := inmem.New()
	account := acmetest.Account("acct-1")
	account.Key, account.Contact = nil, nil
	order := acmetest.Order("order-1", "acct-1")
	order.Binding.Organization = nil
	order.Error = acme.NewProblem(acme.ProblemServerInternal, "")
	order.Error.Subproblems = []*acme.Subproblem{nil, {Type: "urn:x", Detail: "no identifier"}}
	must(t, "seed", s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutAccount(ctx, account); err != nil {
			return err
		}
		return tx.PutOrder(ctx, order)
	}))
	gotAccount, err := s.GetAccount(ctx, "acct-1")
	must(t, "account", err)
	if gotAccount.Key != nil || gotAccount.Contact != nil {
		t.Errorf("absent account fields came back: %+v", gotAccount)
	}
	gotOrder, err := s.GetOrder(ctx, "order-1")
	must(t, "order", err)
	if gotOrder.Binding.Organization != nil {
		t.Errorf("absent organization came back: %v", gotOrder.Binding.Organization)
	}
	if len(gotOrder.Error.Subproblems) != 2 {
		t.Fatalf("subproblems: %+v", gotOrder.Error.Subproblems)
	}
	if gotOrder.Error.Subproblems[0] != nil {
		t.Errorf("nil subproblem came back as %+v", gotOrder.Error.Subproblems[0])
	}
	if gotOrder.Error.Subproblems[1].Identifier != nil {
		t.Errorf("absent subproblem identifier came back: %+v", gotOrder.Error.Subproblems[1].Identifier)
	}
	if gotOrder.Error.Algorithms != nil {
		t.Errorf("absent algorithms came back: %v", gotOrder.Error.Algorithms)
	}
}

// TestPageDefaultLimit proves a page with no limit is bounded by the
// storage default rather than by the size of the table.
func TestPageDefaultLimit(t *testing.T) {
	ctx := context.Background()
	s := inmem.New()
	total := storage.DefaultPageSize + 5
	must(t, "seed", s.Update(ctx, func(tx acme.Tx) error {
		for i := range total {
			if err := tx.PutOrder(ctx, acmetest.Order(orderID(i), "acct-1")); err != nil {
				return err
			}
		}
		return nil
	}))
	r, err := s.ListOrders(ctx, "acct-1", storage.Page{})
	must(t, "list", err)
	if len(r.Items) != storage.DefaultPageSize || r.NextCursor == "" {
		t.Fatalf("first page: %d items, cursor %q", len(r.Items), r.NextCursor)
	}
	r, err = s.ListOrders(ctx, "acct-1", storage.Page{Cursor: r.NextCursor})
	must(t, "second page", err)
	if len(r.Items) != 5 || r.NextCursor != "" {
		t.Fatalf("second page: %d items, cursor %q", len(r.Items), r.NextCursor)
	}
}

// orderID pads an index so the ids sort the way the pages do.
func orderID(i int) string {
	digits := "0123456789"
	return "order-" + string([]byte{digits[i/100%10], digits[i/10%10], digits[i%10]})
}
