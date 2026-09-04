package acmetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/acme/jose"
	"github.com/deploymenttheory/go-apple-dm/paging"
)

// Factory returns a fresh, empty store for one test.
type Factory func(t *testing.T) acme.Store

// T0 is the instant every sample record is stamped with, so a test that
// expires or prunes something has a fixed point to reason from.
var T0 = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// errBoom is what a transaction returns when a test wants it to fail. It
// is a sentinel so the suite can prove Update returns the callback's own
// error rather than one of its own.
var errBoom = errors.New("acmetest: the transaction failed")

// errAssert is the failure a check made inside a transaction reports; the
// callback cannot call t.Fatalf without leaving the transaction open.
var errAssert = errors.New("acmetest: assertion failed inside the transaction")

// ptr is the address of a value, for the optional properties an
// attestation may or may not carry.
func ptr[T any](v T) *T { return &v }

// Account returns a fully populated account.
func Account(id string) *acme.Account {
	return &acme.Account{
		ID:         id,
		Thumbprint: "thumbprint-" + id,
		Key:        &jose.JWK{Kty: "EC", Crv: "P-256", X: "x-" + id, Y: "y-" + id},
		Status:     acme.StatusValid,
		Contact:    []string{"mailto:admin@example.com"},
		CreatedAt:  T0,
	}
}

// Order returns a pending order for one permanent identifier, bound to a
// named device. Its authorization is "authz-<id>".
func Order(id, accountID string) *acme.Order {
	return &acme.Order{
		ID:         id,
		AccountID:  accountID,
		Identifier: acme.Identifier{Type: acme.IdentifierPermanent, Value: "client-" + id},
		Binding: acme.Binding{
			Serial:       "SERIAL-" + id,
			UDID:         "UDID-" + id,
			EnrollmentID: "enrollment-" + id,
			CommonName:   "device " + id,
			Organization: []string{"Example Ltd"},
			NotAfter:     T0.Add(365 * 24 * time.Hour),
		},
		Status:    acme.StatusPending,
		AuthzID:   "authz-" + id,
		Expires:   T0.Add(time.Hour),
		CreatedAt: T0,
	}
}

// Authorization returns the pending authorization of an order. Its
// challenge is "challenge-<id>".
func Authorization(id, orderID, accountID string) *acme.Authorization {
	return &acme.Authorization{
		ID:          id,
		OrderID:     orderID,
		AccountID:   accountID,
		Identifier:  acme.Identifier{Type: acme.IdentifierPermanent, Value: "client-" + orderID},
		Status:      acme.StatusPending,
		ChallengeID: "challenge-" + id,
		Expires:     T0.Add(time.Hour),
	}
}

// Challenge returns the pending device-attest-01 challenge of an
// authorization, not yet answered.
func Challenge(id, authzID, accountID string) *acme.Challenge {
	return &acme.Challenge{
		ID:        id,
		AuthzID:   authzID,
		AccountID: accountID,
		Type:      acme.ChallengeDeviceAttest,
		Token:     "token-" + id,
		Status:    acme.StatusPending,
	}
}

// Certificate returns an issued certificate with the device properties an
// attestation carried. KextsAllowed is deliberately absent: a property
// the attestation did not report is not the same as one reported false.
func Certificate(id, orderID, accountID string) *acme.Certificate {
	return &acme.Certificate{
		ID:        id,
		OrderID:   orderID,
		AccountID: accountID,
		Serial:    "serial-" + id,
		ChainPEM:  []byte("-----BEGIN CERTIFICATE-----\n" + id + "\n-----END CERTIFICATE-----\n"),
		Device: attest.Properties{
			SerialNumber:           "SERIAL-" + orderID,
			UDID:                   "UDID-" + orderID,
			SoftwareUpdateDeviceID: "J314sAP",
			OSVersion:              "15.1",
			SEPOSVersion:           "15.1",
			LLBVersion:             "11881.41.5",
			SecureBoot:             "Full Security",
			SIPEnabled:             ptr(true),
			Freshness:              []byte{0x01, 0x02, 0x03, 0x04},
		},
		Binding: acme.Binding{
			Serial:       "SERIAL-" + orderID,
			UDID:         "UDID-" + orderID,
			CommonName:   "device " + orderID,
			Organization: []string{"Example Ltd"},
		},
		NotAfter: T0.Add(365 * 24 * time.Hour),
		IssuedAt: T0,
	}
}

// RunAll runs every subtest against stores from the factory.
func RunAll(t *testing.T, newStore Factory) {
	t.Helper()
	t.Run("Accounts", func(t *testing.T) { runAccounts(t, newStore) })
	t.Run("Orders", func(t *testing.T) { runOrders(t, newStore) })
	t.Run("OrderList", func(t *testing.T) { runOrderList(t, newStore) })
	t.Run("Authorizations", func(t *testing.T) { runAuthorizations(t, newStore) })
	t.Run("ChallengeAttestation", func(t *testing.T) { runChallengeAttestation(t, newStore) })
	t.Run("Certificates", func(t *testing.T) { runCertificates(t, newStore) })
	t.Run("CertificateList", func(t *testing.T) { runCertificateList(t, newStore) })
	t.Run("IdentifierClaims", func(t *testing.T) { runClaims(t, newStore) })
	t.Run("NonceSingleUse", func(t *testing.T) { runNonces(t, newStore) })
	t.Run("Update", func(t *testing.T) { runUpdate(t, newStore) })
	t.Run("Prune", func(t *testing.T) { runPrune(t, newStore) })
	t.Run("InvalidArguments", func(t *testing.T) { runInvalid(t, newStore) })
	t.Run("Concurrency", func(t *testing.T) { runConcurrency(t, newStore) })
}

func must(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

// inTx applies one change in its own transaction, which is what a caller
// with a single record to write does.
func inTx(t *testing.T, what string, s acme.Store, fn func(acme.Tx) error) {
	t.Helper()
	must(t, what, s.Update(context.Background(), fn))
}

func runAccounts(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	_, err := s.GetAccount(ctx, "missing")
	wantErr(t, "get missing", err, acme.ErrNotFound)
	_, err = s.AccountByThumbprint(ctx, "missing")
	wantErr(t, "thumbprint missing", err, acme.ErrNotFound)

	a := Account("acct-1")
	inTx(t, "put", s, func(tx acme.Tx) error { return tx.PutAccount(ctx, a) })
	got, err := s.GetAccount(ctx, "acct-1")
	must(t, "get", err)
	if !reflect.DeepEqual(got, a) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, a)
	}
	// The record is a copy: a caller that mutates what it was given
	// cannot reach the stored account.
	got.Status, got.Contact[0], got.Key.X = acme.StatusDeactivated, "mailto:someone@else", "tampered"
	again, err := s.GetAccount(ctx, "acct-1")
	must(t, "get again", err)
	if !reflect.DeepEqual(again, a) {
		t.Fatalf("stored account mutated through the result: %+v", again)
	}
	// RFC 8555 makes the key the identity of the account, so a returning
	// client is recognised by its thumbprint.
	byKey, err := s.AccountByThumbprint(ctx, a.Thumbprint)
	must(t, "by thumbprint", err)
	if byKey.ID != a.ID {
		t.Fatalf("thumbprint resolved to %q, want %q", byKey.ID, a.ID)
	}
	// A second account registering one key is a conflict, not a second
	// account: the two would be indistinguishable afterwards.
	dup := Account("acct-2")
	dup.Thumbprint = a.Thumbprint
	err = s.Update(ctx, func(tx acme.Tx) error { return tx.PutAccount(ctx, dup) })
	wantErr(t, "duplicate thumbprint", err, acme.ErrConflict)
	_, err = s.GetAccount(ctx, "acct-2")
	wantErr(t, "conflicting account stored anyway", err, acme.ErrNotFound)

	// Writing an existing account keeps its id and takes the new fields.
	b := Account("acct-1")
	b.Status, b.Contact = acme.StatusDeactivated, []string{"mailto:new@example.com"}
	inTx(t, "update", s, func(tx acme.Tx) error { return tx.PutAccount(ctx, b) })
	got, err = s.GetAccount(ctx, "acct-1")
	must(t, "get updated", err)
	if got.ID != "acct-1" || got.Status != acme.StatusDeactivated || !slices.Equal(got.Contact, b.Contact) {
		t.Fatalf("update: %+v", got)
	}
	// A re-keyed account takes its new thumbprint and releases the old
	// one, which another account may then register.
	c := Account("acct-1")
	c.Thumbprint = "thumbprint-rekeyed"
	inTx(t, "rekey", s, func(tx acme.Tx) error { return tx.PutAccount(ctx, c) })
	_, err = s.AccountByThumbprint(ctx, a.Thumbprint)
	wantErr(t, "old thumbprint", err, acme.ErrNotFound)
	if got, err := s.AccountByThumbprint(ctx, c.Thumbprint); err != nil || got.ID != "acct-1" {
		t.Fatalf("new thumbprint: %+v %v", got, err)
	}
	inTx(t, "reuse released thumbprint", s, func(tx acme.Tx) error { return tx.PutAccount(ctx, dup) })
}

func runOrders(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	_, err := s.GetOrder(ctx, "missing")
	wantErr(t, "get missing", err, acme.ErrNotFound)

	o := Order("order-1", "acct-1")
	inTx(t, "put", s, func(tx acme.Tx) error { return tx.PutOrder(ctx, o) })
	got, err := s.GetOrder(ctx, "order-1")
	must(t, "get", err)
	if !reflect.DeepEqual(got, o) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, o)
	}
	got.Binding.Organization[0] = "Tampered"
	if again, _ := s.GetOrder(ctx, "order-1"); again.Binding.Organization[0] != "Example Ltd" {
		t.Fatal("stored order mutated through the result")
	}
	// A status transition and the certificate an order finishes with
	// persist, because the next request reads them back to decide what to
	// answer.
	o.Status, o.CertificateID = acme.StatusValid, "cert-1"
	inTx(t, "finish", s, func(tx acme.Tx) error { return tx.PutOrder(ctx, o) })
	got, err = s.GetOrder(ctx, "order-1")
	must(t, "get finished", err)
	if got.Status != acme.StatusValid || got.CertificateID != "cert-1" {
		t.Fatalf("transition: %+v", got)
	}
	// The problem document a failed order carries round trips whole: a
	// client polling the order is told what it was refused with, so a
	// backend that stores only the type answers the wrong thing.
	o.Status, o.CertificateID = acme.StatusInvalid, ""
	o.Error = acme.NewProblem(acme.ProblemBadAttestationStatement, "the attestation chain did not verify")
	o.Error.Algorithms = []string{"ES256"}
	o.Error = o.Error.WithSubproblem(acme.ProblemRejectedIdentifier, "already used", o.Identifier)
	inTx(t, "fail", s, func(tx acme.Tx) error { return tx.PutOrder(ctx, o) })
	got, err = s.GetOrder(ctx, "order-1")
	must(t, "get failed", err)
	if got.Error == nil {
		t.Fatal("the order error was dropped")
	}
	if got.Error.Type != acme.ProblemBadAttestationStatement {
		t.Errorf("problem type: %q", got.Error.Type)
	}
	if got.Error.Detail != o.Error.Detail {
		t.Errorf("problem detail: %q", got.Error.Detail)
	}
	if got.Error.Status != http.StatusBadRequest {
		t.Errorf("problem status: %d", got.Error.Status)
	}
	if !errors.Is(got.Error, acme.ErrBadAttestation) {
		t.Errorf("problem does not match its sentinel: %v", got.Error)
	}
	if !slices.Equal(got.Error.Algorithms, []string{"ES256"}) {
		t.Errorf("problem algorithms: %v", got.Error.Algorithms)
	}
	if len(got.Error.Subproblems) != 1 || got.Error.Subproblems[0].Identifier == nil {
		t.Fatalf("subproblems: %+v", got.Error.Subproblems)
	}
	if got.Error.Subproblems[0].Identifier.Value != o.Identifier.Value {
		t.Errorf("subproblem identifier: %+v", got.Error.Subproblems[0].Identifier)
	}
	// The problem is a copy too, pointers and all.
	got.Error.Detail, got.Error.Subproblems[0].Identifier.Value = "tampered", "tampered"
	again, err := s.GetOrder(ctx, "order-1")
	must(t, "get again", err)
	if again.Error.Detail != o.Error.Detail || again.Error.Subproblems[0].Identifier.Value != o.Identifier.Value {
		t.Fatalf("stored problem mutated through the result: %+v", again.Error)
	}
}

func runOrderList(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	const n = 5
	inTx(t, "seed", s, func(tx acme.Tx) error {
		for i := range n {
			if err := tx.PutOrder(ctx, Order(fmt.Sprintf("order-%02d", i), "acct-1")); err != nil {
				return err
			}
		}
		return tx.PutOrder(ctx, Order("order-other", "acct-2"))
	})
	// A listing is one account's own orders and nobody else's.
	r, err := s.ListOrders(ctx, "acct-1", paging.Page{})
	must(t, "list", err)
	if len(r.Items) != n {
		t.Fatalf("listed %d orders, want %d", len(r.Items), n)
	}
	for _, o := range r.Items {
		if o.AccountID != "acct-1" {
			t.Fatalf("listing leaked order %q of account %q", o.ID, o.AccountID)
		}
	}
	if !slices.IsSortedFunc(r.Items, func(a, b acme.Order) int { return cmpString(a.ID, b.ID) }) {
		t.Fatalf("listing is not ordered by id: %+v", r.Items)
	}
	// The cursor reaches every record exactly once and then stops.
	seen := map[string]int{}
	pages, p := 0, paging.Page{Limit: 2}
	for {
		page, err := s.ListOrders(ctx, "acct-1", p)
		must(t, "page", err)
		pages++
		if len(page.Items) > p.Limit {
			t.Fatalf("page of %d items over a limit of %d", len(page.Items), p.Limit)
		}
		for _, o := range page.Items {
			seen[o.ID]++
		}
		if page.NextCursor == "" {
			break
		}
		p.Cursor = page.NextCursor
		if pages > n {
			t.Fatal("paging did not end")
		}
	}
	if len(seen) != n {
		t.Fatalf("paging saw %d orders over %d pages, want %d", len(seen), pages, n)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("order %q seen %d times", id, count)
		}
	}
	if r, _ := s.ListOrders(ctx, "acct-2", paging.Page{}); len(r.Items) != 1 || r.Items[0].ID != "order-other" {
		t.Fatalf("other account: %+v", r.Items)
	}
	// An account with no orders lists nothing, which is not an error.
	r, err = s.ListOrders(ctx, "acct-nobody", paging.Page{})
	must(t, "list unknown account", err)
	if len(r.Items) != 0 || r.NextCursor != "" {
		t.Fatalf("unknown account: %+v", r)
	}
}

// cmpString orders two ids, for the sortedness check.
func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func runAuthorizations(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	_, err := s.GetAuthorization(ctx, "missing")
	wantErr(t, "get missing authorization", err, acme.ErrNotFound)
	_, err = s.GetChallenge(ctx, "missing")
	wantErr(t, "get missing challenge", err, acme.ErrNotFound)

	a := Authorization("authz-1", "order-1", "acct-1")
	c := Challenge("challenge-authz-1", "authz-1", "acct-1")
	inTx(t, "put", s, func(tx acme.Tx) error {
		if err := tx.PutAuthorization(ctx, a); err != nil {
			return err
		}
		return tx.PutChallenge(ctx, c)
	})
	gotAuthz, err := s.GetAuthorization(ctx, a.ID)
	must(t, "get authorization", err)
	if !reflect.DeepEqual(gotAuthz, a) {
		t.Fatalf("authorization round trip\n got %+v\nwant %+v", gotAuthz, a)
	}
	gotChal, err := s.GetChallenge(ctx, c.ID)
	must(t, "get challenge", err)
	if !reflect.DeepEqual(gotChal, c) {
		t.Fatalf("challenge round trip\n got %+v\nwant %+v", gotChal, c)
	}
	// A challenge that verified settles both records.
	a.Status, c.Status, c.ValidatedAt = acme.StatusValid, acme.StatusValid, T0.Add(time.Minute)
	c.Attestation = []byte("attestation")
	inTx(t, "validate", s, func(tx acme.Tx) error {
		if err := tx.PutAuthorization(ctx, a); err != nil {
			return err
		}
		return tx.PutChallenge(ctx, c)
	})
	gotAuthz, _ = s.GetAuthorization(ctx, a.ID)
	gotChal, _ = s.GetChallenge(ctx, c.ID)
	if gotAuthz.Status != acme.StatusValid || gotChal.Status != acme.StatusValid {
		t.Fatalf("validated: %+v %+v", gotAuthz, gotChal)
	}
	if !gotChal.ValidatedAt.Equal(c.ValidatedAt) {
		t.Errorf("validated at %v, want %v", gotChal.ValidatedAt, c.ValidatedAt)
	}
	// A challenge that failed keeps the problem it failed with.
	c.Status, c.Attestation = acme.StatusInvalid, nil
	c.Error = acme.NewProblem(acme.ProblemBadAttestationStatement, "no freshness code in the leaf")
	inTx(t, "fail", s, func(tx acme.Tx) error { return tx.PutChallenge(ctx, c) })
	gotChal, _ = s.GetChallenge(ctx, c.ID)
	if gotChal.Error == nil || gotChal.Error.Detail != c.Error.Detail {
		t.Fatalf("challenge error: %+v", gotChal.Error)
	}
}

func runChallengeAttestation(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	// The stored attestation is verified again at finalize, against the
	// key in a certificate request the challenge never saw, so it has to
	// come back byte for byte. A backend that trims, pads, or re-encodes
	// it breaks issuance, and nothing before finalize would notice.
	att := make([]byte, 512)
	for i := range att {
		att[i] = byte(i % 256)
	}
	att[0], att[1] = 0x00, 0xff
	c := Challenge("challenge-1", "authz-1", "acct-1")
	c.Attestation = att
	inTx(t, "put", s, func(tx acme.Tx) error { return tx.PutChallenge(ctx, c) })
	got, err := s.GetChallenge(ctx, c.ID)
	must(t, "get", err)
	if !bytes.Equal(got.Attestation, att) {
		t.Fatalf("attestation of %d bytes came back as %d and did not match", len(att), len(got.Attestation))
	}
	// The store keeps what it was given, not the caller's slice, in
	// either direction.
	got.Attestation[0], att[1] = 0x01, 0x01
	again, err := s.GetChallenge(ctx, c.ID)
	must(t, "get again", err)
	if again.Attestation[0] != 0x00 || again.Attestation[1] != 0xff {
		t.Fatal("stored attestation mutated through a caller's slice")
	}
	// A challenge answered with a zero-length attestation and one never
	// answered at all are both readable.
	empty := Challenge("challenge-empty", "authz-1", "acct-1")
	empty.Attestation = []byte{}
	none := Challenge("challenge-none", "authz-1", "acct-1")
	none.Attestation = nil
	inTx(t, "put edge cases", s, func(tx acme.Tx) error {
		if err := tx.PutChallenge(ctx, empty); err != nil {
			return err
		}
		return tx.PutChallenge(ctx, none)
	})
	for _, id := range []string{empty.ID, none.ID} {
		got, err := s.GetChallenge(ctx, id)
		must(t, "get "+id, err)
		if len(got.Attestation) != 0 {
			t.Errorf("%s: %d attestation bytes, want none", id, len(got.Attestation))
		}
	}
}

func runCertificates(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	_, err := s.GetCertificate(ctx, "missing")
	wantErr(t, "get missing", err, acme.ErrNotFound)

	c := Certificate("cert-1", "order-1", "acct-1")
	inTx(t, "put", s, func(tx acme.Tx) error { return tx.PutCertificate(ctx, c) })
	got, err := s.GetCertificate(ctx, c.ID)
	must(t, "get", err)
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, c)
	}
	if !bytes.Equal(got.ChainPEM, c.ChainPEM) {
		t.Errorf("chain: %q", got.ChainPEM)
	}
	// A property the attestation did not report comes back absent, not
	// false: an unreported SIP flag read as "off" would be a claim about
	// the device that nothing made.
	if got.Device.KextsAllowed != nil {
		t.Errorf("absent property came back as %v", *got.Device.KextsAllowed)
	}
	if got.Device.SIPEnabled == nil || !*got.Device.SIPEnabled {
		t.Errorf("SIPEnabled: %v", got.Device.SIPEnabled)
	}
	// Everything reachable through the record is a copy.
	got.ChainPEM[0], got.Device.Freshness[0] = 'X', 0xff
	*got.Device.SIPEnabled = false
	got.Binding.Organization[0] = "Tampered"
	again, err := s.GetCertificate(ctx, c.ID)
	must(t, "get again", err)
	if !reflect.DeepEqual(again, c) {
		t.Fatalf("stored certificate mutated through the result: %+v", again)
	}
}

func runCertificateList(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	// Four certificates over two accounts, with one UDID held twice: a
	// device that enrolled again under another account.
	inTx(t, "seed", s, func(tx acme.Tx) error {
		for _, c := range []*acme.Certificate{
			certFor("cert-01", "acct-1", "S1", "U1"),
			certFor("cert-02", "acct-1", "S2", "U2"),
			certFor("cert-03", "acct-2", "S3", "U3"),
			certFor("cert-04", "acct-2", "S4", "U1"),
		} {
			if err := tx.PutCertificate(ctx, c); err != nil {
				return err
			}
		}
		return nil
	})
	for name, tc := range map[string]struct {
		query acme.CertificateQuery
		want  []string
	}{
		"everything": {acme.CertificateQuery{}, []string{"cert-01", "cert-02", "cert-03", "cert-04"}},
		"by serial":  {acme.CertificateQuery{DeviceSerial: "S2"}, []string{"cert-02"}},
		"by udid":    {acme.CertificateQuery{UDID: "U1"}, []string{"cert-01", "cert-04"}},
		"by account": {acme.CertificateQuery{AccountID: "acct-1"}, []string{"cert-01", "cert-02"}},
		"serial in account": {
			acme.CertificateQuery{DeviceSerial: "S1", AccountID: "acct-1"},
			[]string{"cert-01"},
		},
		"serial in the wrong account": {acme.CertificateQuery{DeviceSerial: "S1", AccountID: "acct-2"}, nil},
		"unknown udid":                {acme.CertificateQuery{UDID: "nobody"}, nil},
	} {
		r, err := s.ListCertificates(ctx, tc.query, paging.Page{})
		must(t, name, err)
		var ids []string
		for _, c := range r.Items {
			ids = append(ids, c.ID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Errorf("%s: got %v, want %v", name, ids, tc.want)
		}
	}
	// Paging a filtered listing reaches every match exactly once.
	seen := map[string]int{}
	p := paging.Page{Limit: 1}
	for pages := 0; ; pages++ {
		page, err := s.ListCertificates(ctx, acme.CertificateQuery{}, p)
		must(t, "page", err)
		for _, c := range page.Items {
			seen[c.ID]++
		}
		if page.NextCursor == "" {
			break
		}
		p.Cursor = page.NextCursor
		if pages > 4 {
			t.Fatal("paging did not end")
		}
	}
	if len(seen) != 4 {
		t.Fatalf("paging saw %d certificates, want 4", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("certificate %q seen %d times", id, count)
		}
	}
}

// certFor is a certificate with the serial and UDID a filter test needs.
func certFor(id, accountID, serial, udid string) *acme.Certificate {
	c := Certificate(id, "order-"+id, accountID)
	c.Serial, c.Device.UDID, c.Device.SerialNumber = serial, udid, serial
	return c
}

func runClaims(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	// Apple calls the ClientIdentifier an anti-replay, one-time code, so
	// the first claim wins and the second is refused rather than
	// overwriting it.
	inTx(t, "claim", s, func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "client-1", "order-1") })
	err := s.Update(ctx, func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "client-1", "order-2") })
	wantErr(t, "second claim", err, acme.ErrConflict)
	inTx(t, "another identifier", s, func(tx acme.Tx) error {
		return tx.ClaimIdentifier(ctx, "client-2", "order-2")
	})
	// A claim taken in a transaction that later fails is not taken: the
	// order it was for does not exist either, so the identifier is free.
	err = s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutOrder(ctx, Order("order-3", "acct-1")); err != nil {
			return err
		}
		if err := tx.ClaimIdentifier(ctx, "client-3", "order-3"); err != nil {
			return err
		}
		return errBoom
	})
	wantErr(t, "rolled back", err, errBoom)
	_, err = s.GetOrder(ctx, "order-3")
	wantErr(t, "order survived the rollback", err, acme.ErrNotFound)
	inTx(t, "claim after a rollback", s, func(tx acme.Tx) error {
		return tx.ClaimIdentifier(ctx, "client-3", "order-4")
	})
}

func runNonces(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	must(t, "put", s.PutNonce(ctx, acme.Nonce{Value: "nonce-1", IssuedAt: T0}))
	must(t, "put second", s.PutNonce(ctx, acme.Nonce{Value: "nonce-2", IssuedAt: T0}))
	got, err := s.TakeNonce(ctx, "nonce-1")
	must(t, "take", err)
	if got.Value != "nonce-1" || !got.IssuedAt.Equal(T0) {
		t.Fatalf("nonce: %+v", got)
	}
	// Taking the same nonce twice is how a replay is caught: the first
	// use removed it, so the repeat finds nothing and the server answers
	// badNonce.
	_, err = s.TakeNonce(ctx, "nonce-1")
	wantErr(t, "replayed nonce", err, acme.ErrNotFound)
	// A nonce the server never issued is the same miss.
	_, err = s.TakeNonce(ctx, "never-issued")
	wantErr(t, "unknown nonce", err, acme.ErrNotFound)
	// Taking one leaves the others alone.
	if got, err := s.TakeNonce(ctx, "nonce-2"); err != nil || got.Value != "nonce-2" {
		t.Fatalf("second nonce: %+v %v", got, err)
	}
}

func runUpdate(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	// Everything one request changes lands together, and the transaction
	// reads its own writes on the way.
	a := Account("acct-1")
	inTx(t, "commit", s, func(tx acme.Tx) error {
		if err := tx.PutAccount(ctx, a); err != nil {
			return err
		}
		if err := tx.PutOrder(ctx, Order("order-1", "acct-1")); err != nil {
			return err
		}
		if err := tx.PutAuthorization(ctx, Authorization("authz-order-1", "order-1", "acct-1")); err != nil {
			return err
		}
		if err := tx.PutChallenge(ctx, Challenge("challenge-authz-order-1", "authz-order-1", "acct-1")); err != nil {
			return err
		}
		if err := tx.PutCertificate(ctx, Certificate("cert-1", "order-1", "acct-1")); err != nil {
			return err
		}
		if err := tx.ClaimIdentifier(ctx, "client-order-1", "order-1"); err != nil {
			return err
		}
		return readBack(ctx, tx)
	})
	for name, get := range map[string]func() error{
		"account":       func() error { _, err := s.GetAccount(ctx, "acct-1"); return err },
		"order":         func() error { _, err := s.GetOrder(ctx, "order-1"); return err },
		"authorization": func() error { _, err := s.GetAuthorization(ctx, "authz-order-1"); return err },
		"challenge":     func() error { _, err := s.GetChallenge(ctx, "challenge-authz-order-1"); return err },
		"certificate":   func() error { _, err := s.GetCertificate(ctx, "cert-1"); return err },
	} {
		must(t, name+" after the commit", get())
	}
	// A callback that fails takes every write it made with it, and Update
	// returns the callback's own error rather than one of its own.
	err := s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutOrder(ctx, Order("order-2", "acct-1")); err != nil {
			return err
		}
		if err := tx.PutAuthorization(ctx, Authorization("authz-order-2", "order-2", "acct-1")); err != nil {
			return err
		}
		if err := tx.PutChallenge(ctx, Challenge("challenge-authz-order-2", "authz-order-2", "acct-1")); err != nil {
			return err
		}
		if err := tx.PutCertificate(ctx, Certificate("cert-2", "order-2", "acct-1")); err != nil {
			return err
		}
		if err := tx.PutAccount(ctx, Account("acct-2")); err != nil {
			return err
		}
		if _, err := tx.GetOrder(ctx, "order-2"); err != nil {
			return fmt.Errorf("%w: the order is invisible inside its own transaction", errAssert)
		}
		return errBoom
	})
	wantErr(t, "rollback", err, errBoom)
	for name, get := range map[string]func() error{
		"account":       func() error { _, err := s.GetAccount(ctx, "acct-2"); return err },
		"order":         func() error { _, err := s.GetOrder(ctx, "order-2"); return err },
		"authorization": func() error { _, err := s.GetAuthorization(ctx, "authz-order-2"); return err },
		"challenge":     func() error { _, err := s.GetChallenge(ctx, "challenge-authz-order-2"); return err },
		"certificate":   func() error { _, err := s.GetCertificate(ctx, "cert-2"); return err },
	} {
		wantErr(t, name+" survived the rollback", get(), acme.ErrNotFound)
	}
	// The account written before the rollback is untouched by it.
	if got, err := s.GetAccount(ctx, "acct-1"); err != nil || got.Status != a.Status {
		t.Fatalf("committed account after a rollback: %+v %v", got, err)
	}
}

// readBack reads every record a committing transaction has just written,
// so a backend that hides its own writes until commit is caught.
func readBack(ctx context.Context, tx acme.Tx) error {
	if _, err := tx.GetAccount(ctx, "acct-1"); err != nil {
		return err
	}
	if _, err := tx.AccountByThumbprint(ctx, "thumbprint-acct-1"); err != nil {
		return err
	}
	if _, err := tx.GetOrder(ctx, "order-1"); err != nil {
		return err
	}
	if _, err := tx.GetAuthorization(ctx, "authz-order-1"); err != nil {
		return err
	}
	if _, err := tx.GetChallenge(ctx, "challenge-authz-order-1"); err != nil {
		return err
	}
	if _, err := tx.GetCertificate(ctx, "cert-1"); err != nil {
		return err
	}
	orders, err := tx.ListOrders(ctx, "acct-1", paging.Page{})
	if err != nil {
		return err
	}
	certs, err := tx.ListCertificates(ctx, acme.CertificateQuery{AccountID: "acct-1"}, paging.Page{})
	if err != nil {
		return err
	}
	if len(orders.Items) != 1 || len(certs.Items) != 1 {
		return fmt.Errorf("%w: %d orders and %d certificates inside the transaction",
			errAssert, len(orders.Items), len(certs.Items))
	}
	return nil
}

func runPrune(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	cutoff := T0.Add(time.Hour)

	stale := Order("order-stale", "acct-1")
	stale.AuthzID, stale.Expires = "authz-stale", T0
	staleAuthz := Authorization("authz-stale", "order-stale", "acct-1")
	staleAuthz.ChallengeID, staleAuthz.Expires = "challenge-stale", T0
	live := Order("order-live", "acct-1")
	live.AuthzID, live.Expires = "authz-live", T0.Add(24*time.Hour)
	liveAuthz := Authorization("authz-live", "order-live", "acct-1")
	liveAuthz.ChallengeID, liveAuthz.Expires = "challenge-live", T0.Add(24*time.Hour)

	inTx(t, "seed", s, func(tx acme.Tx) error {
		if err := tx.PutAccount(ctx, Account("acct-1")); err != nil {
			return err
		}
		if err := tx.PutCertificate(ctx, Certificate("cert-1", "order-stale", "acct-1")); err != nil {
			return err
		}
		for _, o := range []*acme.Order{stale, live} {
			if err := tx.PutOrder(ctx, o); err != nil {
				return err
			}
		}
		for _, a := range []*acme.Authorization{staleAuthz, liveAuthz} {
			if err := tx.PutAuthorization(ctx, a); err != nil {
				return err
			}
		}
		for _, c := range []*acme.Challenge{
			Challenge("challenge-stale", "authz-stale", "acct-1"),
			Challenge("challenge-live", "authz-live", "acct-1"),
		} {
			if err := tx.PutChallenge(ctx, c); err != nil {
				return err
			}
		}
		return nil
	})
	must(t, "old nonce", s.PutNonce(ctx, acme.Nonce{Value: "old", IssuedAt: T0}))
	must(t, "fresh nonce", s.PutNonce(ctx, acme.Nonce{Value: "fresh", IssuedAt: cutoff.Add(time.Minute)}))

	n, err := s.Prune(ctx, cutoff)
	must(t, "prune", err)
	// The stale order, its authorization, its challenge, and the nonce
	// issued before the cutoff: four records, and a backend that counts a
	// cascade differently is not wrong.
	if n < 4 {
		t.Fatalf("pruned %d records, want at least 4", n)
	}
	for name, get := range map[string]func() error{
		"order":         func() error { _, err := s.GetOrder(ctx, "order-stale"); return err },
		"authorization": func() error { _, err := s.GetAuthorization(ctx, "authz-stale"); return err },
		"challenge":     func() error { _, err := s.GetChallenge(ctx, "challenge-stale"); return err },
		"nonce":         func() error { _, err := s.TakeNonce(ctx, "old"); return err },
	} {
		wantErr(t, "expired "+name+" survived the prune", get(), acme.ErrNotFound)
	}
	// What has not expired is left where it was, and an account and an
	// issued certificate are never pruned at all: they are the record of
	// what was given out.
	for name, get := range map[string]func() error{
		"order":         func() error { _, err := s.GetOrder(ctx, "order-live"); return err },
		"authorization": func() error { _, err := s.GetAuthorization(ctx, "authz-live"); return err },
		"challenge":     func() error { _, err := s.GetChallenge(ctx, "challenge-live"); return err },
		"account":       func() error { _, err := s.GetAccount(ctx, "acct-1"); return err },
		"certificate":   func() error { _, err := s.GetCertificate(ctx, "cert-1"); return err },
	} {
		must(t, "live "+name+" after the prune", get())
	}
	if got, err := s.TakeNonce(ctx, "fresh"); err != nil || got.Value != "fresh" {
		t.Fatalf("fresh nonce after the prune: %+v %v", got, err)
	}
	// A second prune at the same cutoff has nothing left to remove.
	if n, err := s.Prune(ctx, cutoff); err != nil || n != 0 {
		t.Fatalf("second prune removed %d records: %v", n, err)
	}
}

func runInvalid(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	write := func(fn func(acme.Tx) error) error { return s.Update(ctx, fn) }
	checks := map[string]func() error{
		"Update nil callback":   func() error { return s.Update(ctx, nil) },
		"GetAccount":            func() error { _, err := s.GetAccount(ctx, ""); return err },
		"AccountByThumbprint":   func() error { _, err := s.AccountByThumbprint(ctx, ""); return err },
		"GetOrder":              func() error { _, err := s.GetOrder(ctx, ""); return err },
		"GetAuthorization":      func() error { _, err := s.GetAuthorization(ctx, ""); return err },
		"GetChallenge":          func() error { _, err := s.GetChallenge(ctx, ""); return err },
		"GetCertificate":        func() error { _, err := s.GetCertificate(ctx, ""); return err },
		"ListOrders":            func() error { _, err := s.ListOrders(ctx, "", paging.Page{}); return err },
		"PutAccount nil":        func() error { return write(func(tx acme.Tx) error { return tx.PutAccount(ctx, nil) }) },
		"PutOrder nil":          func() error { return write(func(tx acme.Tx) error { return tx.PutOrder(ctx, nil) }) },
		"PutAuthorization nil":  func() error { return write(func(tx acme.Tx) error { return tx.PutAuthorization(ctx, nil) }) },
		"PutChallenge nil":      func() error { return write(func(tx acme.Tx) error { return tx.PutChallenge(ctx, nil) }) },
		"PutCertificate nil":    func() error { return write(func(tx acme.Tx) error { return tx.PutCertificate(ctx, nil) }) },
		"PutAccount without id": func() error { return write(putAccount(ctx, &acme.Account{Thumbprint: "t"})) },
		"PutAccount without a thumbprint": func() error {
			return write(putAccount(ctx, &acme.Account{ID: "a"}))
		},
		"PutOrder without id": func() error { return write(putOrder(ctx, &acme.Order{AccountID: "a"})) },
		"PutOrder without account": func() error {
			return write(putOrder(ctx, &acme.Order{ID: "o"}))
		},
		"PutAuthorization without id": func() error {
			return write(func(tx acme.Tx) error { return tx.PutAuthorization(ctx, &acme.Authorization{}) })
		},
		"PutChallenge without id": func() error {
			return write(func(tx acme.Tx) error { return tx.PutChallenge(ctx, &acme.Challenge{}) })
		},
		"PutCertificate without id": func() error {
			return write(func(tx acme.Tx) error { return tx.PutCertificate(ctx, &acme.Certificate{}) })
		},
		"ClaimIdentifier without identifier": func() error {
			return write(func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "", "order-1") })
		},
		"ClaimIdentifier without order": func() error {
			return write(func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "client-1", "") })
		},
		"PutNonce":  func() error { return s.PutNonce(ctx, acme.Nonce{IssuedAt: T0}) },
		"TakeNonce": func() error { _, err := s.TakeNonce(ctx, ""); return err },
	}
	for name, fn := range checks {
		if err := fn(); !errors.Is(err, acme.ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func putAccount(ctx context.Context, a *acme.Account) func(acme.Tx) error {
	return func(tx acme.Tx) error { return tx.PutAccount(ctx, a) }
}

func putOrder(ctx context.Context, o *acme.Order) func(acme.Tx) error {
	return func(tx acme.Tx) error { return tx.PutOrder(ctx, o) }
}

func runConcurrency(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t)
	inTx(t, "account", s, func(tx acme.Tx) error { return tx.PutAccount(ctx, Account("acct-1")) })
	const orders = 16
	var wg sync.WaitGroup
	for i := range orders {
		wg.Go(func() {
			id := fmt.Sprintf("order-%02d", i)
			err := s.Update(ctx, func(tx acme.Tx) error {
				o := Order(id, "acct-1")
				if err := tx.PutOrder(ctx, o); err != nil {
					return err
				}
				if err := tx.PutAuthorization(ctx, Authorization(o.AuthzID, id, "acct-1")); err != nil {
					return err
				}
				return tx.ClaimIdentifier(ctx, o.Identifier.Value, id)
			})
			if err != nil {
				t.Errorf("update %s: %v", id, err)
			}
			if _, err := s.GetAccount(ctx, "acct-1"); err != nil {
				t.Errorf("read during %s: %v", id, err)
			}
		})
	}
	wg.Wait()
	r, err := s.ListOrders(ctx, "acct-1", paging.Page{})
	if err != nil || len(r.Items) != orders {
		t.Fatalf("orders after concurrent updates: %d %v", len(r.Items), err)
	}

	// Exactly one taker wins each nonce. A store that let two win would
	// let one signed request be replayed, which is the whole reason
	// taking a nonce is a single operation rather than a read and a
	// delete.
	const nonces, takers = 32, 4
	for i := range nonces {
		must(t, "put nonce", s.PutNonce(ctx, acme.Nonce{Value: fmt.Sprintf("nonce-%02d", i), IssuedAt: T0}))
	}
	var won atomic.Int64
	var takes sync.WaitGroup
	for range takers {
		takes.Go(func() {
			for i := range nonces {
				value := fmt.Sprintf("nonce-%02d", i)
				got, err := s.TakeNonce(ctx, value)
				switch {
				case err == nil:
					if got.Value != value {
						t.Errorf("took %q for %q", got.Value, value)
					}
					won.Add(1)
				case errors.Is(err, acme.ErrNotFound):
				default:
					t.Errorf("take %s: %v", value, err)
				}
			}
		})
	}
	takes.Wait()
	if got := won.Load(); got != nonces {
		t.Fatalf("%d takers won %d nonces, want %d", takers, got, nonces)
	}
}
