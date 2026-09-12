package revocation_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"golang.org/x/crypto/ocsp"
)

type fixture struct {
	reg    *revocation.Registry
	st     *state.Memory
	clock  *clock.Fake
	issuer revocation.Issuer
	id     string
	leaf   *x509.Certificate
	csr    *x509.CertificateRequest
}

func setup(t *testing.T) *fixture {
	t.Helper()
	root, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Now().UTC().Truncate(time.Second))
	st := state.NewMemory()
	st.Now = clk.Now
	i := revocation.Issuer{Certificate: root, Signer: key, CRLTTL: time.Hour, CRLRefresh: 30 * time.Minute, OCSPTTL: 5 * time.Minute}
	reg, err := revocation.New(st, i)
	if err != nil {
		t.Fatal(err)
	}
	reg.Now = clk.Now
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "device"}}, leafKey)
	csr, _ := x509.ParseCertificateRequest(raw)
	signer, _ := ca.NewLocal(root, key, ca.WithClock(clk))
	leaf, err := signer.Sign(t.Context(), csr, ca.Policy{Validity: 24 * time.Hour, CRLDistributionPoints: []string{"https://mdm.example/pki/crl/issuer"}, OCSPServer: []string{"https://mdm.example/pki/ocsp/issuer"}})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{reg: reg, st: st, clock: clk, issuer: i, id: cms.Fingerprint(root), leaf: leaf, csr: csr}
}

func TestRevocationAndSignedPublication(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	if err := f.reg.Check(ctx, f.leaf); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	provenance := revocation.Provenance{Source: "acme", AccountID: "account", Identifiers: []string{"permanent-identifier:device"}}
	if err := f.reg.Register(ctx, f.id, f.leaf, provenance); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.Check(ctx, f.leaf); err != nil {
		t.Fatal(err)
	}
	if len(f.leaf.CRLDistributionPoints) != 1 || len(f.leaf.OCSPServer) != 1 {
		t.Fatal("missing status extensions")
	}
	first, err := f.reg.CRL(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	crl, err := x509.ParseRevocationList(first)
	if err != nil || crl.CheckSignatureFrom(f.issuer.Certificate) != nil || crl.Number.Int64() != 1 || len(crl.RevokedCertificateEntries) != 0 {
		t.Fatal(crl, err)
	}
	var wg sync.WaitGroup
	for range 15 {
		wg.Go(func() {
			der, err := f.reg.CRL(ctx, f.id)
			if err != nil || !bytes.Equal(der, first) {
				t.Error("concurrent publication changed", err)
			}
		})
	}
	wg.Wait()
	request, _ := ocsp.CreateRequest(f.leaf, f.issuer.Certificate, &ocsp.RequestOptions{Hash: crypto.SHA256})
	der, err := f.reg.OCSP(ctx, f.id, request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := ocsp.ParseResponseForCert(der, f.leaf, f.issuer.Certificate)
	if err != nil || response.Status != ocsp.Good || response.NextUpdate.Sub(response.ThisUpdate) != 5*time.Minute {
		t.Fatal(response, err)
	}
	if err := f.reg.Revoke(ctx, f.id, f.leaf.SerialNumber, ocsp.KeyCompromise); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.Check(ctx, f.leaf); !errors.Is(err, revocation.ErrRevoked) {
		t.Fatal(err)
	}
	if err := f.reg.Revoke(ctx, f.id, f.leaf.SerialNumber, 0); !errors.Is(err, revocation.ErrRevoked) {
		t.Fatal(err)
	}
	if err := f.reg.Register(ctx, f.id, f.leaf, revocation.Provenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}
	c, err := f.reg.ByCertificate(ctx, f.leaf)
	if err != nil || c.Status != revocation.Revoked || c.Reason != 1 || c.Provenance.AccountID != "account" {
		t.Fatal(c, err)
	}
	second, err := f.reg.CRL(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	crl, err = x509.ParseRevocationList(second)
	if err != nil || crl.CheckSignatureFrom(f.issuer.Certificate) != nil || crl.Number.Int64() != 2 || len(crl.RevokedCertificateEntries) != 1 || crl.RevokedCertificateEntries[0].SerialNumber.Cmp(f.leaf.SerialNumber) != 0 || crl.RevokedCertificateEntries[0].ReasonCode != 1 {
		t.Fatal(crl, err)
	}
	der, err = f.reg.OCSP(ctx, f.id, request)
	if err != nil {
		t.Fatal(err)
	}
	response, err = ocsp.ParseResponseForCert(der, f.leaf, f.issuer.Certificate)
	if err != nil || response.Status != ocsp.Revoked || response.RevocationReason != 1 || !response.RevokedAt.Equal(f.clock.Now()) {
		t.Fatal(response, err)
	}
	unknownReq, _ := ocsp.CreateRequest(&x509.Certificate{SerialNumber: big.NewInt(9000)}, f.issuer.Certificate, nil)
	der, err = f.reg.OCSP(ctx, f.id, unknownReq)
	if err != nil {
		t.Fatal(err)
	}
	response, err = ocsp.ParseResponse(der, f.issuer.Certificate)
	if err != nil || response.Status != ocsp.Unknown {
		t.Fatal("unknown serial claimed good", response, err)
	}
	f.clock.Advance(31 * time.Minute)
	der, err = f.reg.CRL(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	crl, _ = x509.ParseRevocationList(der)
	if crl.Number.Int64() != 3 {
		t.Fatal(crl.Number)
	}
	p := revocation.ProvenanceFromContext(revocation.WithProvenance(ctx, provenance))
	if p.AccountID != "account" {
		t.Fatal(p)
	}
	if revocation.ProvenanceFromContext(ctx).Source != "" {
		t.Fatal("unexpected context")
	}
}

func TestUnknownExpiryAndInvalidInputs(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	for _, reason := range []int{-1, 6, 7, 8, 11, 100} {
		if revocation.ValidReason(reason) {
			t.Fatal(reason)
		}
		if err := f.reg.Revoke(ctx, f.id, f.leaf.SerialNumber, reason); !errors.Is(err, revocation.ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, reason := range []int{0, 1, 2, 3, 4, 5, 9, 10} {
		if !revocation.ValidReason(reason) {
			t.Fatal(reason)
		}
	}
	if err := f.reg.Register(ctx, "unknown", f.leaf, revocation.Provenance{}); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	for _, cert := range []*x509.Certificate{nil, f.issuer.Certificate, {SerialNumber: big.NewInt(-1)}, {SerialNumber: new(big.Int).Lsh(big.NewInt(1), 170)}} {
		if err := f.reg.Register(ctx, f.id, cert, revocation.Provenance{}); !errors.Is(err, revocation.ErrInvalid) {
			t.Fatal(err)
		}
	}
	other := setup(t)
	if err := f.reg.Register(ctx, f.id, other.leaf, revocation.Provenance{}); !errors.Is(err, revocation.ErrInvalid) {
		t.Fatal(err)
	}
	for _, serial := range []*big.Int{nil, big.NewInt(-1), big.NewInt(999)} {
		if _, err := f.reg.Lookup(ctx, f.id, serial); !errors.Is(err, revocation.ErrUnknown) {
			t.Fatal(err)
		}
	}
	if _, err := f.reg.Lookup(ctx, "unknown", big.NewInt(1)); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := f.reg.ByCertificate(ctx, nil); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	if err := f.reg.Revoke(ctx, f.id, big.NewInt(999), 0); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := f.reg.CRL(ctx, "unknown"); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := f.reg.OCSP(ctx, "unknown", nil); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := f.reg.OCSP(ctx, f.id, []byte("broken")); !errors.Is(err, revocation.ErrInvalid) {
		t.Fatal(err)
	}
	wrongReq, _ := ocsp.CreateRequest(other.leaf, other.issuer.Certificate, nil)
	if _, err := f.reg.OCSP(ctx, f.id, wrongReq); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	if err := f.reg.Register(ctx, f.id, f.leaf, revocation.Provenance{}); err != nil {
		t.Fatal(err)
	}
	f.reg.Now = func() time.Time { return f.leaf.NotBefore.Add(-time.Second) }
	if err := f.reg.Check(ctx, f.leaf); !errors.Is(err, revocation.ErrExpired) {
		t.Fatal(err)
	}
	f.reg.Now = func() time.Time { return f.leaf.NotAfter }
	if err := f.reg.Check(ctx, f.leaf); !errors.Is(err, revocation.ErrExpired) {
		t.Fatal(err)
	}
	f.reg.Now = nil // Real-time path remains valid for this newly issued certificate.
	if err := f.reg.Check(ctx, f.leaf); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(20 * 365 * 24 * time.Hour)
	if _, err := f.reg.CRL(ctx, f.id); !errors.Is(err, revocation.ErrExpired) {
		t.Fatal(err)
	}
	f.reg.Now = f.clock.Now
	request, _ := ocsp.CreateRequest(f.leaf, f.issuer.Certificate, nil)
	if _, err := f.reg.OCSP(ctx, f.id, request); !errors.Is(err, revocation.ErrExpired) {
		t.Fatal(err)
	}
}

func TestConfigAndHTTP(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	for _, bad := range []revocation.Issuer{{}, {Certificate: f.issuer.Certificate}, {Certificate: f.issuer.Certificate, Signer: f.issuer.Signer}, {Certificate: f.leaf, Signer: f.issuer.Signer, CRLTTL: time.Hour, CRLRefresh: time.Minute, OCSPTTL: time.Minute}} {
		if _, err := revocation.New(f.st, bad); err == nil {
			t.Fatal("invalid issuer")
		}
	}
	if _, err := revocation.New(nil, f.issuer); err == nil {
		t.Fatal("nil store")
	}
	if _, err := revocation.New(f.st); err == nil {
		t.Fatal("no issuer")
	}
	if _, err := revocation.New(f.st, f.issuer, f.issuer); err == nil {
		t.Fatal("duplicate issuer")
	}
	wrong := f.issuer
	wrong.Signer, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := revocation.New(f.st, wrong); err == nil {
		t.Fatal("key mismatch")
	}
	h := f.reg.Handler("/pki/")
	request, _ := ocsp.CreateRequest(f.leaf, f.issuer.Certificate, nil)
	for _, tc := range []struct {
		method, path, ct string
		body             []byte
		status           int
	}{
		{"GET", "/pki/crl/" + f.id, "", nil, 200}, {"GET", "/pki/crl/unknown", "", nil, 404},
		{"POST", "/pki/ocsp/" + f.id, "application/ocsp-request", request, 200},
		{"GET", "/pki/ocsp/" + f.id + "/" + base64.StdEncoding.EncodeToString(request), "", nil, 200},
		{"GET", "/pki/ocsp/" + f.id + "/invalid", "", nil, 400},
		{"POST", "/pki/ocsp/" + f.id, "text/plain", nil, 415},
		{"POST", "/pki/ocsp/" + f.id, "application/ocsp-request", make([]byte, 5000), 400},
		{"POST", "/pki/ocsp/" + f.id, "application/ocsp-request", []byte("bad"), 400},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.ct)
		h.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatal(tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	req := httptest.NewRequest("GET", "/pki/crl/"+f.id, nil).WithContext(cancelled)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
