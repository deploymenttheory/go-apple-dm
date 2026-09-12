package scep

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func TestSharedGrantIssuanceOverAppleSCEPWire(t *testing.T) {
	g, password, _ := grantFixture(t)
	root, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	local, err := ca.NewLocal(root, key)
	if err != nil {
		t.Fatal(err)
	}
	var registrations atomic.Int64
	issuer := CertificateIssuer{
		Store:     g.Store,
		Signer:    local,
		Authorize: g.Verify,
		Register: func(context.Context, *x509.Certificate) error {
			if registrations.Add(1) == 1 {
				return state.ErrInvalid
			}
			return nil
		},
	}
	srv, err := NewServer(
		local,
		root,
		key,
		WithChallenge(g),
		WithCSRVerifier(
			CSRVerifierFunc(func(context.Context, *x509.CertificateRequest) error { return nil }),
		),
		WithIssuance(
			func(ctx context.Context, csr *x509.CertificateRequest, p ca.Policy, password string, renewal bool) (*x509.Certificate, error) {
				if renewal {
					if RenewalCertificate(ctx) == nil {
						return nil, ErrChallenge
					}
					cert, err := local.Sign(ctx, csr, p)
					if err != nil {
						return nil, err
					}
					if err := issuer.Register(ctx, cert); err != nil {
						return nil, err
					}
					return cert, nil
				}
				return issuer.Issue(ctx, password, csr, p)
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewTLSServer(srv.Handler())
	defer httpServer.Close()
	client := NewClient(httpServer.URL+"/scep", httpServer.Client())
	deviceKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	options := EnrollOptions{Subject: pkix.Name{CommonName: "device"}, Challenge: password}
	if cert, err := client.Enroll(t.Context(), deviceKey, options); err == nil || cert != nil {
		t.Fatal("registration failure returned a certificate")
	}
	first, err := client.Enroll(t.Context(), deviceKey, options)
	if err != nil {
		t.Fatal("same-CSR recovery", err)
	}
	again, err := client.Enroll(t.Context(), deviceKey, options)
	if err != nil || !bytes.Equal(first.Raw, again.Raw) {
		t.Fatal("wire retry changed certificate", err)
	}
	options.Challenge = ""
	options.Renew = &Identity{Cert: first, Key: deviceKey}
	if _, err := client.Enroll(t.Context(), deviceKey, options); err != nil {
		t.Fatal("authorized renewal required a challenge", err)
	}
}

func grantCSR(t *testing.T) *x509.CertificateRequest {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(
		rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "device"}},
		k,
	)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	return csr
}

func grantFixture(t *testing.T) (Grants, string, *x509.CertificateRequest) {
	t.Helper()
	g := Grants{
		Store: state.NewMemory(),
		Authorize: func(_ context.Context, g Grant, csr *x509.CertificateRequest) error {
			if string(g.Binding) != `"device"` || csr.Subject.CommonName != "device" {
				return ErrChallenge
			}
			return nil
		},
	}
	p, err := g.Issue(
		t.Context(),
		Grant{Binding: json.RawMessage(`"device"`), ExpiresAt: time.Now().Add(time.Hour)},
	)
	if err != nil {
		t.Fatal(err)
	}
	return g, p, grantCSR(t)
}

func TestGrantReplicasReserveOneCSRAndRecheckAdmission(t *testing.T) {
	g, password, csr := grantFixture(t)
	bad := *csr
	bad.Signature = []byte("invalid")
	if err := g.Verify(t.Context(), password, &bad); err == nil {
		t.Fatal("invalid signature reserved a grant")
	}
	other := grantCSR(t)
	requests := []*x509.CertificateRequest{csr, other}
	var accepted [2]atomic.Int64
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			replica := g
			which := i % 2
			if err := replica.Verify(t.Context(), password, requests[which]); err == nil {
				accepted[which].Add(1)
			}
		})
	}
	wg.Wait()
	if (accepted[0].Load() == 0) == (accepted[1].Load() == 0) ||
		accepted[0].Load()+accepted[1].Load() != 16 {
		t.Fatal("reservation split", accepted[0].Load(), accepted[1].Load())
	}
	winner := csr
	if accepted[0].Load() == 0 {
		winner = other
	}
	replica := g
	replica.Authorize = func(context.Context, Grant, *x509.CertificateRequest) error { return ErrChallenge }
	if err := replica.Verify(t.Context(), password, winner); !errors.Is(err, ErrChallenge) {
		t.Fatal("admission not rechecked", err)
	}
	if err := g.Verify(t.Context(), password, winner); err != nil {
		t.Fatal(err)
	}
}

func TestGrantExpiryAndInvalidConfiguration(t *testing.T) {
	g, password, csr := grantFixture(t)
	for _, bad := range []Grants{{}, {Store: g.Store}, {Authorize: g.Authorize}} {
		if _, err := bad.Issue(t.Context(), Grant{}); err == nil {
			t.Fatal("missing configuration")
		}
		if err := bad.Verify(t.Context(), password, csr); err == nil {
			t.Fatal("missing configuration")
		}
	}
	for _, grant := range []Grant{{}, {CSRHash: "claimed", ExpiresAt: time.Now().Add(time.Hour)}, {ExpiresAt: time.Now().Add(-time.Hour)}, {Binding: json.RawMessage("not-json"), ExpiresAt: time.Now().Add(time.Hour)}} {
		if _, err := g.Issue(t.Context(), grant); err == nil {
			t.Fatal("invalid new grant accepted")
		}
	}
	for _, p := range []string{"", "missing"} {
		if err := g.Verify(t.Context(), p, csr); !errors.Is(err, ErrChallenge) {
			t.Fatal(err)
		}
	}
	if err := g.Verify(t.Context(), password, nil); err == nil {
		t.Fatal("nil CSR accepted")
	}
	now := time.Now()
	g.Store.(*state.Memory).Now = func() time.Time { return now }
	p, err := g.Issue(
		t.Context(),
		Grant{Binding: json.RawMessage(`"device"`), ExpiresAt: now.Add(24 * time.Hour)},
	)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := g.Store.Get(t.Context(), grantKey(p))
	if !r.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatal("deadline not capped")
	}
	now = now.Add(time.Hour)
	if err := g.Verify(t.Context(), p, csr); !errors.Is(err, ErrChallenge) {
		t.Fatal("expired grant", err)
	}
}

type grantFaultStore struct {
	state.Store
	read, update, txRead, put error
	txValue                   []byte
}

func (s grantFaultStore) Get(ctx context.Context, k string) (state.Record, error) {
	if s.read != nil {
		return state.Record{}, s.read
	}
	return s.Store.Get(ctx, k)
}

func (s grantFaultStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	if s.update != nil {
		return s.update
	}
	return s.Store.Update(
		ctx,
		keys,
		func(tx state.Tx) error { return fn(grantFaultTx{Tx: tx, read: s.txRead, put: s.put, value: s.txValue}) },
	)
}

type grantFaultTx struct {
	state.Tx
	read, put error
	value     []byte
}

func (s grantFaultTx) Get(ctx context.Context, k string) (state.Record, error) {
	if s.read != nil {
		return state.Record{}, s.read
	}
	r, err := s.Tx.Get(ctx, k)
	if s.value != nil {
		r.Value = s.value
	}
	return r, err
}

func (s grantFaultTx) Put(ctx context.Context, r state.Record) error {
	if s.put != nil {
		return s.put
	}
	return s.Tx.Put(ctx, r)
}

func TestGrantStorageFailuresAndChangedAuthorization(t *testing.T) {
	g, password, csr := grantFixture(t)
	backend := g.Store
	boom := errors.New("backend unavailable")
	for _, fault := range []grantFaultStore{{read: boom}, {update: boom}, {txRead: boom}, {txRead: state.ErrNotFound}, {put: boom}, {txValue: []byte("corrupt")}, {txValue: []byte(`{"binding":"changed"}`)}} {
		fault.Store = backend
		g.Store = fault
		if err := g.Verify(t.Context(), password, csr); err == nil {
			t.Fatal("storage failure accepted")
		}
	}
	g.Store = grantFaultStore{Store: backend, update: boom}
	if _, err := g.Issue(
		t.Context(),
		Grant{ExpiresAt: time.Now().Add(time.Hour)},
	); !errors.Is(
		err,
		boom,
	) {
		t.Fatal(err)
	}
	g.Store = backend
	if err := backend.Update(t.Context(), []string{grantKey(password)}, func(tx state.Tx) error {
		return tx.Put(t.Context(), state.Record{Key: grantKey(password), Value: []byte("corrupt")})
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.Verify(t.Context(), password, csr); err == nil {
		t.Fatal("corrupt grant accepted")
	}
}

type receiptSigner struct {
	ca.Signer
	fn func(context.Context, *x509.CertificateRequest, ca.Policy) (*x509.Certificate, error)
}

func (s receiptSigner) Sign(
	ctx context.Context,
	csr *x509.CertificateRequest,
	p ca.Policy,
) (*x509.Certificate, error) {
	return s.fn(ctx, csr, p)
}

func TestCertificateReceiptSurvivesRegistrationFailureAndReplicas(t *testing.T) {
	g, password, csr := grantFixture(t)
	root, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	local, err := ca.NewLocal(root, key)
	if err != nil {
		t.Fatal(err)
	}
	var signed, registered atomic.Int64
	issuer := CertificateIssuer{
		Store:     g.Store,
		Authorize: g.Verify,
		Signer: receiptSigner{
			fn: func(ctx context.Context, csr *x509.CertificateRequest, p ca.Policy) (*x509.Certificate, error) {
				signed.Add(1)
				return local.Sign(ctx, csr, p)
			},
		},
		Register: func(context.Context, *x509.Certificate) error { registered.Add(1); return nil },
	}
	failed := issuer
	failed.Register = func(context.Context, *x509.Certificate) error { return state.ErrInvalid }
	if c, err := failed.Issue(t.Context(), password, csr, ca.Policy{}); err == nil || c != nil {
		t.Fatal("returned an unregistered certificate")
	}
	var wg sync.WaitGroup
	certs := make(chan *x509.Certificate, 16)
	for range 16 {
		wg.Go(func() {
			replica := issuer
			c, err := replica.Issue(t.Context(), password, csr, ca.Policy{})
			if err != nil {
				t.Error(err)
			}
			certs <- c
		})
	}
	wg.Wait()
	close(certs)
	var first *x509.Certificate
	for c := range certs {
		if c == nil {
			t.Fatal("missing certificate")
		}
		if first == nil {
			first = c
		}
		if !bytes.Equal(c.Raw, first.Raw) {
			t.Fatal("retry issued a different certificate")
		}
	}
	if signed.Load() != 1 || registered.Load() != 16 {
		t.Fatal("receipt/registration", signed.Load(), registered.Load())
	}
	issuer.Authorize = func(context.Context, string, *x509.CertificateRequest) error { return ErrChallenge }
	if c, err := issuer.Issue(
		t.Context(),
		password,
		csr,
		ca.Policy{},
	); c != nil ||
		!errors.Is(err, ErrChallenge) {
		t.Fatal("cached certificate bypassed authorization")
	}
}

func TestCertificateIssuerFailurePaths(t *testing.T) {
	g, password, csr := grantFixture(t)
	root, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	local, err := ca.NewLocal(root, key)
	if err != nil {
		t.Fatal(err)
	}
	issuer := CertificateIssuer{
		Store:     g.Store,
		Signer:    local,
		Authorize: g.Verify,
		Register:  func(context.Context, *x509.Certificate) error { return nil },
	}
	for _, mode := range []string{"store", "signer", "authorize", "register", "password", "csr", "read", "put", "update", "sign-error", "nil-certificate", "ca-certificate", "wrong-key", "expired", "not-yet-valid", "corrupt-receipt"} {
		t.Run(mode, func(t *testing.T) {
			i := issuer
			p := password
			request := csr
			switch mode {
			case "store":
				i.Store = nil
			case "signer":
				i.Signer = nil
			case "authorize":
				i.Authorize = nil
			case "register":
				i.Register = nil
			case "password":
				p = ""
			case "csr":
				request = nil
			case "read":
				i.Store = grantFaultStore{Store: g.Store, txRead: state.ErrInvalid}
			case "put":
				i.Store = grantFaultStore{Store: g.Store, put: state.ErrInvalid}
			case "update":
				i.Store = grantFaultStore{Store: g.Store, update: state.ErrInvalid}
			case "corrupt-receipt":
				k := "scep/certificate/" + grantHash([]byte(password)) + "/" + grantHash(csr.Raw)
				if err := g.Store.Update(
					t.Context(),
					[]string{k},
					func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: k, Value: []byte("corrupt")}) },
				); err != nil {
					t.Fatal(err)
				}
			default:
				i.Signer = receiptSigner{
					fn: func(ctx context.Context, _ *x509.CertificateRequest, _ ca.Policy) (*x509.Certificate, error) {
						if mode == "sign-error" {
							return nil, state.ErrInvalid
						}
						if mode == "nil-certificate" {
							return nil, nil
						}
						if mode == "ca-certificate" {
							return root, nil
						}
						c, err := local.Sign(ctx, csr, ca.Policy{})
						if err != nil {
							return nil, err
						}
						switch mode {
						case "wrong-key":
							c.PublicKey = root.PublicKey
						case "expired":
							c.NotAfter = time.Now().Add(-time.Hour)
						case "not-yet-valid":
							c.NotBefore = time.Now().Add(time.Hour)
						}
						return c, nil
					},
				}
			}
			if c, err := i.Issue(t.Context(), p, request, ca.Policy{}); err == nil || c != nil {
				t.Fatal("issuance failure returned certificate")
			}
		})
	}
}
