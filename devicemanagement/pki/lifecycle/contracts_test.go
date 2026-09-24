package lifecycle

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var errRepository = errors.New("certificate repository unavailable")

// Faults are injected at transaction boundaries so tests can prove that a failed
// certificate transition never publishes half of its state or loses its key.
type faultRepository struct {
	state.Store
	get, list, txGet, put func(string) error
	beforeUpdate          func()
}

// Get runs the injected read-fault hook before delegating to the store.
func (s *faultRepository) Get(ctx context.Context, k string) (state.Record, error) {
	if s.get != nil {
		if err := s.get(k); err != nil {
			return state.Record{}, err
		}
	}
	return s.Store.Get(ctx, k)
}

// List runs the injected list-fault hook before delegating to the store.
func (s *faultRepository) List(ctx context.Context, p, a string, n int) ([]state.Record, error) {
	if s.list != nil {
		if err := s.list(p); err != nil {
			return nil, err
		}
	}
	return s.Store.List(ctx, p, a, n)
}

// Update runs the pre-update hook and wraps the transaction with fault injection.
func (s *faultRepository) Update(
	ctx context.Context,
	keys []string,
	fn func(state.Tx) error,
) error {
	if s.beforeUpdate != nil {
		s.beforeUpdate()
	}
	return s.Store.Update(
		ctx,
		keys,
		func(tx state.Tx) error { return fn(faultTransaction{Tx: tx, faults: s}) },
	)
}

type faultTransaction struct {
	state.Tx
	faults *faultRepository
}

// Get runs the injected transaction-read hook before delegating to the transaction.
func (tx faultTransaction) Get(ctx context.Context, k string) (state.Record, error) {
	if tx.faults.txGet != nil {
		if err := tx.faults.txGet(k); err != nil {
			return state.Record{}, err
		}
	}
	return tx.Tx.Get(ctx, k)
}

// List runs the injected list-fault hook before delegating to the transaction.
func (tx faultTransaction) List(ctx context.Context, p, a string, n int) ([]state.Record, error) {
	if tx.faults.list != nil {
		if err := tx.faults.list(p); err != nil {
			return nil, err
		}
	}
	return tx.Tx.List(ctx, p, a, n)
}

// Put runs the injected write-fault hook before delegating to the transaction.
func (tx faultTransaction) Put(ctx context.Context, v state.Record) error {
	if tx.faults.put != nil {
		if err := tx.faults.put(v.Key); err != nil {
			return err
		}
	}
	return tx.Tx.Put(ctx, v)
}

// requireError requires success or an error matching the supplied sentinel through errors.Is.
func requireError(t *testing.T, err, want error) {
	t.Helper()
	if want == nil && err != nil || want != nil && !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// testManager creates an in-memory lifecycle manager with fault injection and a mutable fixture
// clock.
func testManager(t *testing.T) (*Manager, *faultRepository, *time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	s := state.NewMemory()
	s.Now = func() time.Time { return now }
	f := &faultRepository{Store: s}
	return &Manager{Store: f}, f, &now
}

// requestFor builds an identity request, adding fixture DNS names for HTTPS identities.
func requestFor(id string, kind Kind) Request {
	r := Request{ID: id, Kind: kind, Subject: pkix.Name{CommonName: id}}
	if kind == ServerHTTPS {
		r.DNSNames = []string{"mdm.example", "127.0.0.1"}
	}
	return r
}

// pending begins a pending identity version, failing the test on error.
func pending(t *testing.T, m *Manager, id string, kind Kind) Identity {
	t.Helper()
	v, err := m.Begin(t.Context(), requestFor(id, kind))
	requireError(t, err, nil)
	return v
}

// rootIdentity creates and activates a root issuer and returns its certificate and key material.
func rootIdentity(t *testing.T, m *Manager, id string) Material {
	t.Helper()
	v := pending(t, m, id, EnrollmentCA)
	_, err := m.CreateIssuer(t.Context(), id, v.Pending, 0)
	requireError(t, err, nil)
	_, err = m.Activate(t.Context(), id, v.Pending)
	requireError(t, err, nil)
	material, err := m.LoadMaterial(t.Context(), id, "")
	requireError(t, err, nil)
	return material
}

// putRecord encodes and writes a state record in a transaction, failing the test on error.
func putRecord(t *testing.T, s state.Store, k string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	requireError(t, err, nil)
	requireError(
		t,
		s.Update(
			t.Context(),
			[]string{k},
			func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: k, Value: b}) },
		),
		nil,
	)
}

// changeRecord loads, mutates, and rewrites a stored lifecycle identity record.
func changeRecord(t *testing.T, m *Manager, id string, fn func(*record)) {
	t.Helper()
	r, err := read(t.Context(), m.Store, id)
	requireError(t, err, nil)
	fn(&r)
	putRecord(t, m.Store, prefix+id, r)
}

// privateSigner parses a PEM PKCS #8 private key and requires it to implement crypto.Signer.
func privateSigner(t *testing.T, data []byte) crypto.Signer {
	t.Helper()
	b, _ := pem.Decode(data)
	if b == nil {
		t.Fatal("missing key")
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	requireError(t, err, nil)
	return requireType[crypto.Signer](t, k)
}

// issueCertificate issues a PEM certificate using the supplied authority material and template.
func issueCertificate(
	t *testing.T,
	root Material,
	public crypto.PublicKey,
	template *x509.Certificate,
) []byte {
	t.Helper()
	chain, err := certificates(root.Certificate)
	requireError(t, err, nil)
	if template.SerialNumber == nil {
		template.SerialNumber, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		requireError(t, err, nil)
	}
	der, err := x509.CreateCertificate(
		rand.Reader,
		template,
		chain[0],
		public,
		privateSigner(t, root.Key),
	)
	requireError(t, err, nil)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestRequestValidationCancellationAndPublicHistory checks request validation cancellation and
// public history.
func TestRequestValidationCancellationAndPublicHistory(t *testing.T) {
	m, _, _ := testManager(t)
	ctx := WithAudit(t.Context(), "operator", "request")
	for _, req := range []Request{{ID: "../key", Kind: MDMPush}, {ID: "key", Kind: "unknown"}, {ID: "key", Kind: MDMPush}} {
		_, err := m.Begin(ctx, req)
		requireError(t, err, ErrInvalid)
	}
	req := requestFor("customer", MDMPush)
	v, err := m.Begin(ctx, req)
	requireError(t, err, nil)
	csr, err := m.Export(ctx, req.ID, "", "csr")
	requireError(t, err, nil)
	if !bytes.Contains(csr, []byte("CERTIFICATE REQUEST")) {
		t.Fatal("missing public CSR")
	}
	changed := req
	changed.Account = "another account"
	_, err = m.Begin(ctx, changed)
	requireError(t, err, ErrConflict)
	for _, artifact := range []string{"key", "certificate", "signed-request"} {
		_, err = m.Export(ctx, req.ID, "", artifact)
		requireError(t, err, ErrInvalid)
	}
	_, err = m.Export(ctx, req.ID, "missing", "csr")
	requireError(t, err, ErrNotFound)
	_, err = m.Activate(ctx, req.ID, v.Pending)
	requireError(t, err, ErrConflict)
	_, err = m.Cancel(ctx, req.ID, "missing")
	requireError(t, err, ErrNotFound)
	cancelled, err := m.Cancel(ctx, req.ID, v.Pending)
	requireError(t, err, nil)
	if cancelled.Pending != "" || cancelled.Revisions[0].Phase != "cancelled" {
		t.Fatal(cancelled)
	}
	_, err = m.Cancel(ctx, req.ID, v.Pending)
	requireError(t, err, nil)
	_, err = m.Import(ctx, req.ID, v.Pending, []byte("invalid"))
	requireError(t, err, ErrConflict)
	_, err = m.Begin(ctx, req)
	requireError(t, err, nil)
	history, cursor, err := m.History(ctx, req.ID, "", 1)
	requireError(t, err, nil)
	if len(history) != 1 || cursor == "" || history[0].Actor != "operator" ||
		history[0].Action != "request" {
		t.Fatal(history, cursor)
	}
	more, next, err := m.History(ctx, req.ID, cursor, 100)
	requireError(t, err, nil)
	if len(more) != 3 || next != "" {
		t.Fatal(more, next)
	}
	for _, entry := range append(history, more...) {
		data, err := json.Marshal(entry)
		requireError(t, err, nil)
		if bytes.Contains(data, []byte("PRIVATE KEY")) {
			t.Fatal("private key in history")
		}
	}
	for _, n := range []int{0, 1001} {
		_, _, err = m.History(ctx, req.ID, "", n)
		requireError(t, err, ErrInvalid)
	}
}

// TestReadAndWriteFailuresPreservePendingMaterial checks read and write failures preserve pending
// material.
func TestReadAndWriteFailuresPreservePendingMaterial(t *testing.T) {
	m, s, _ := testManager(t)
	ctx := t.Context()
	v := pending(t, m, "enrollment-ca", EnrollmentCA)
	before, err := m.LoadMaterial(ctx, v.ID, v.Pending)
	requireError(t, err, nil)
	for _, target := range []string{prefix, "pki/lifecycle/history/"} {
		s.put = func(k string) error {
			if strings.HasPrefix(k, target) {
				return errRepository
			}
			return nil
		}
		_, err = m.Cancel(ctx, v.ID, v.Pending)
		requireError(t, err, errRepository)
		s.put = nil
		current, err := m.Get(ctx, v.ID)
		requireError(t, err, nil)
		if current.Pending != v.Pending || current.Generation != v.Generation {
			t.Fatal("failed transaction committed", current)
		}
	}
	after, err := m.LoadMaterial(ctx, v.ID, v.Pending)
	requireError(t, err, nil)
	if !bytes.Equal(before.Key, after.Key) || !bytes.Equal(before.CSR, after.CSR) {
		t.Fatal("failed mutation replaced private material")
	}
	s.get = func(string) error { return errRepository }
	_, err = m.Begin(ctx, v.Request)
	requireError(t, err, errRepository)
	_, err = m.LoadMaterial(ctx, v.ID, "")
	requireError(t, err, errRepository)
	_, err = m.Export(ctx, v.ID, "", "csr")
	requireError(t, err, errRepository)
	s.get = nil
	s.txGet = func(string) error { return errRepository }
	_, err = m.Get(ctx, v.ID)
	requireError(t, err, errRepository)
	_, err = m.Cancel(ctx, v.ID, v.Pending)
	requireError(t, err, errRepository)
	_, err = m.Begin(ctx, requestFor("other", EnrollmentCA))
	requireError(t, err, errRepository)
	s.txGet = nil
	s.list = func(string) error { return errRepository }
	_, err = m.List(ctx)
	requireError(t, err, errRepository)
	_, _, err = m.History(ctx, v.ID, "", 1)
	requireError(t, err, errRepository)
	s.list = nil
	_, err = m.LoadMaterial(ctx, v.ID, "missing")
	requireError(t, err, ErrNotFound)
	for _, call := range []func() error{
		func() error { _, e := m.Get(ctx, "../issuer"); return e },
		func() error { _, e := m.Cancel(ctx, "../issuer", "1"); return e },
		func() error { _, e := m.LoadMaterial(ctx, "../issuer", ""); return e },
		func() error { _, _, e := m.History(ctx, "../issuer", "", 1); return e },
	} {
		requireError(t, call(), ErrInvalid)
	}
	// A concurrent change between the optimistic lookup and lock acquisition
	// must reject a different request rather than overwrite its key.
	s.beforeUpdate = func() {
		s.beforeUpdate = nil
		putRecord(t, s.Store, prefix+"racing", record{Request: requestFor("racing", VendorSigning)})
	}
	_, err = m.Begin(ctx, requestFor("racing", EnrollmentCA))
	requireError(t, err, ErrConflict)
}

// TestListPaginationAndCorruptStoredJSON checks list pagination and corrupt stored JSON.
func TestListPaginationAndCorruptStoredJSON(t *testing.T) {
	m, s, _ := testManager(t)
	for i := range 101 {
		id := fmt.Sprintf("identity-%03d", i)
		putRecord(t, s.Store, prefix+id, record{Request: requestFor(id, EnrollmentCA)})
	}
	items, err := m.List(t.Context())
	requireError(t, err, nil)
	if len(items) != 101 || items[100].ID != "identity-100" {
		t.Fatal(len(items))
	}
	s.txGet = func(string) error { return errRepository }
	_, err = m.List(t.Context())
	requireError(t, err, errRepository)
	s.txGet = nil
	for _, k := range []string{prefix + "identity-000", "pki/lifecycle/history/identity-000/0001"} {
		requireError(
			t,
			s.Store.Update(
				t.Context(),
				[]string{k},
				func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: k, Value: []byte("{")}) },
			),
			nil,
		)
	}
	if _, err = m.List(t.Context()); err == nil {
		t.Fatal("corrupt list entry accepted")
	}
	if _, err = m.Get(t.Context(), "identity-000"); err == nil {
		t.Fatal("corrupt identity accepted")
	}
	if _, _, err = m.History(t.Context(), "identity-000", "", 10); err == nil {
		t.Fatal("corrupt history accepted")
	}
}

// TestNoticesOncePerThresholdAndAtomicFailures checks notices once per threshold and atomic
// failures.
func TestNoticesOncePerThresholdAndAtomicFailures(t *testing.T) {
	m, s, now := testManager(t)
	ctx := t.Context()
	rootIdentity(t, m, "enrollment-ca")
	_, created, err := m.RecordNotice(ctx, "enrollment-ca")
	requireError(t, err, nil)
	if created {
		t.Fatal("premature notice")
	}
	identity, err := m.Get(ctx, "enrollment-ca")
	requireError(t, err, nil)
	end := identity.Revisions[0].NotAfter
	for _, days := range []int{180, 30, 14, 7, 1, 0} {
		*now = end.Add(-time.Duration(days) * 24 * time.Hour)
		notice, created, err := m.RecordNotice(ctx, "enrollment-ca")
		requireError(t, err, nil)
		if !created || notice.Revision != "1" || notice.Severity == "" {
			t.Fatal(notice, created)
		}
		_, created, err = m.RecordNotice(ctx, "enrollment-ca")
		requireError(t, err, nil)
		if created {
			t.Fatal("duplicate notice")
		}
	}
	*now = end.Add(-2 * 24 * time.Hour)
	s.txGet = func(k string) error {
		if strings.Contains(k, "/notice/") {
			return errRepository
		}
		return nil
	}
	_, created, err = m.RecordNotice(ctx, "enrollment-ca")
	requireError(t, err, errRepository)
	if created {
		t.Fatal("failed notice reported created")
	}
	s.txGet = nil
	// A different identity makes the write failure independent of prior notices.
	putRecord(
		t,
		s.Store,
		prefix+"other",
		record{
			Request: requestFor("other", MDMPush),
			Active:  "1",
			Revisions: []storedRevision{
				{
					Revision: Revision{
						ID:        "1",
						NotBefore: end.Add(-365 * 24 * time.Hour),
						NotAfter:  end,
					},
				},
			},
		},
	)
	s.put = func(string) error { return errRepository }
	_, created, err = m.RecordNotice(ctx, "other")
	requireError(t, err, errRepository)
	if created {
		t.Fatal("notice survived rollback")
	}
	s.put = nil
	_, _, err = m.RecordNotice(ctx, "missing")
	requireError(t, err, ErrNotFound)
	_, _, err = m.RecordNotice(ctx, "../other")
	requireError(t, err, ErrInvalid)
}

// Check the key type used by the fixture as well as the public/private binding.
func TestGeneratedRequestRejectsUnencodableSubject(t *testing.T) {
	m, _, _ := testManager(t)
	req := requestFor("invalid-subject", ServerHTTPS)
	req.Subject.ExtraNames = []pkix.AttributeTypeAndValue{
		{Type: []int{2, 5, 4, 3}, Value: make(chan int)},
	}
	if _, err := m.Begin(t.Context(), req); err == nil {
		t.Fatal("unencodable CSR subject accepted")
	}
	v := pending(t, m, "key-check", MDMPush)
	mat, err := m.LoadMaterial(t.Context(), v.ID, v.Pending)
	requireError(t, err, nil)
	if _, ok := privateSigner(t, mat.Key).(*rsa.PrivateKey); !ok {
		t.Fatal("portal CSR requires RSA")
	}
}
