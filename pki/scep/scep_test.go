package scep_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	smallscep "github.com/smallstep/scep"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/scep"
)

type fixture struct {
	caCert *x509.Certificate
	caKey  *rsa.PrivateKey
	signer *ca.Local
	depot  *ca.MemoryDepot
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	depot := ca.NewMemoryDepot()
	signer, err := ca.NewLocal(cert, key, ca.WithDepot(depot))
	if err != nil {
		t.Fatal(err)
	}
	return fixture{caCert: cert, caKey: key, signer: signer, depot: depot}
}

func serve(t *testing.T, s *scep.Server) *scep.Client {
	t.Helper()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return scep.NewClient(srv.URL+"/scep", srv.Client())
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestClientEnrolls(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s, err := scep.NewServer(f.signer, f.caCert, f.caKey,
		scep.WithChallenge(scep.StaticChallenge("secret")),
		scep.WithPolicy(ca.Policy{Validity: time.Hour}),
		scep.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	c := serve(t, s)
	ctx := context.Background()

	caps, err := c.GetCACaps(ctx)
	if err != nil || !strings.Contains(caps, "POSTPKIOperation") || caps != s.CACaps() {
		t.Fatalf("caps %q %v", caps, err)
	}
	certs, err := c.GetCACert(ctx)
	if err != nil || len(certs) != 1 || !certs[0].Equal(f.caCert) {
		t.Fatalf("GetCACert: %v", err)
	}

	key := rsaKey(t)
	subject := pkix.Name{CommonName: "UDID-1", Organization: []string{"go-apple-dm"}}
	cert, err := c.Enroll(ctx, key, scep.EnrollOptions{Subject: subject, Challenge: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "UDID-1" || cert.Issuer.CommonName != f.caCert.Subject.CommonName {
		t.Fatalf("issued %v", cert.Subject)
	}
	if _, err := f.depot.Get(ctx, cert.SerialNumber); err != nil {
		t.Fatalf("depot: %v", err)
	}
	if cert.NotAfter.Sub(cert.NotBefore) > 2*time.Hour {
		t.Fatalf("policy validity ignored: %v", cert.NotAfter.Sub(cert.NotBefore))
	}

	// Wrong challenge is a signed FAILURE.
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: subject, Challenge: "nope"}); !errors.Is(err, scep.ErrRejected) {
		t.Fatalf("wrong challenge: %v", err)
	}
	// Renewal with the issued identity needs no challenge.
	key2 := rsaKey(t)
	renewed, err := c.Enroll(ctx, key2, scep.EnrollOptions{Subject: subject, Renew: &scep.Identity{Cert: cert, Key: key}})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewed.SerialNumber.Cmp(cert.SerialNumber) == 0 {
		t.Fatal("renewal reused the serial")
	}
	// A "renewal" signed by a certificate we did not issue must pass the challenge.
	stranger, strangerKey, _ := ca.NewSelfSigned(ca.SelfSignedOptions{Subject: pkix.Name{CommonName: "other"}})
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: subject, Renew: &scep.Identity{Cert: stranger, Key: strangerKey}}); !errors.Is(err, scep.ErrRejected) {
		t.Fatalf("foreign renewal: %v", err)
	}
	// Explicit recipients skip GetCACert.
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: subject, Challenge: "secret", Recipients: certs}); err != nil {
		t.Fatal(err)
	}
}

func TestCSRVerifierVeto(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s, _ := scep.NewServer(f.signer, f.caCert, f.caKey,
		scep.WithCSRVerifier(scep.CSRVerifierFunc(func(_ context.Context, csr *x509.CertificateRequest) error {
			if !strings.HasPrefix(csr.Subject.CommonName, "UDID-") {
				return errors.New("subject must be a UDID")
			}
			return nil
		})),
		scep.WithPolicy(ca.Policy{MinRSABits: 4096}),
		scep.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	c := serve(t, s)
	ctx := context.Background()
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: pkix.Name{CommonName: "bad"}}); !errors.Is(err, scep.ErrRejected) {
		t.Fatalf("verifier: %v", err)
	}
	// Passes the verifier, fails the CA policy (2048-bit key under a 4096 minimum).
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: pkix.Name{CommonName: "UDID-2"}}); !errors.Is(err, scep.ErrRejected) {
		t.Fatalf("policy: %v", err)
	}
}

func TestCACertBundleAndHandlerErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	inter, _, _ := ca.NewSelfSigned(ca.SelfSignedOptions{Subject: pkix.Name{CommonName: "intermediate"}})
	s, _ := scep.NewServer(f.signer, f.caCert, f.caKey, scep.WithExtraCerts(inter),
		scep.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	c := serve(t, s)
	ctx := context.Background()
	certs, err := c.GetCACert(ctx)
	if err != nil || len(certs) != 2 {
		t.Fatalf("bundle: %d %v", len(certs), err)
	}
	der, ct, _ := s.CACert()
	if ct != scep.ContentTypeCARACert || len(der) == 0 {
		t.Fatalf("content type %q", ct)
	}
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: pkix.Name{CommonName: "UDID-3"}}); err != nil {
		t.Fatal(err)
	}

	h := s.Handler()
	get := func(q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/scep?"+q, nil))
		return rec
	}
	if rec := get("operation=Bogus"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus op: %d", rec.Code)
	}
	if rec := get("operation=PKIOperation"); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty GET message: %d", rec.Code)
	}
	if rec := get("operation=PKIOperation&message=%25%25"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad base64: %d", rec.Code)
	}
	if rec := get("operation=PKIOperation&message=" + url.QueryEscape("AAAA")); rec.Code != http.StatusBadRequest {
		t.Fatalf("not a PKIMessage: %d", rec.Code)
	}
	// GET form of a real message (unpadded base64 with '+' as space) is accepted.
	key := rsaKey(t)
	self, _ := scep.SelfSigned(key, pkix.Name{CommonName: "UDID-4"})
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "UDID-4"}}, key)
	csr, _ := x509.ParseCertificateRequest(csrDER)
	msg, err := smallscep.NewCSRRequest(csr, &smallscep.PKIMessage{MessageType: smallscep.PKCSReq, Recipients: []*x509.Certificate{f.caCert}, SignerCert: self, SignerKey: key})
	if err != nil {
		t.Fatal(err)
	}
	enc := strings.TrimRight(b64(msg.Raw), "=")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/scep?operation=PKIOperation&message="+url.QueryEscape(enc), nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != scep.ContentTypePKIMessage {
		t.Fatalf("GET PKIOperation: %d %s", rec.Code, rec.Body.String())
	}
	// Oversized POST.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/scep?operation=PKIOperation", strings.NewReader(strings.Repeat("x", 1<<20+1))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized: %d", rec.Code)
	}
	// Envelope encrypted to a different RA cannot be decrypted: unparseable, 400.
	other, _, _ := ca.NewSelfSigned(ca.SelfSignedOptions{})
	msg2, _ := smallscep.NewCSRRequest(csr, &smallscep.PKIMessage{MessageType: smallscep.PKCSReq, Recipients: []*x509.Certificate{other}, SignerCert: self, SignerKey: key})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/scep?operation=PKIOperation", strings.NewReader(string(msg2.Raw))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign recipient: %d", rec.Code)
	}
	if _, err := s.PKIOperation(ctx, []byte("junk")); !errors.Is(err, scep.ErrCSR) {
		t.Fatalf("junk: %v", err)
	}
}

func b64(b []byte) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		rem := len(b) - i
		for j := range 3 {
			n <<= 8
			if j < rem {
				n |= uint32(b[i+j])
			}
		}
		for j := range 4 {
			if j <= rem {
				sb.WriteByte(tbl[(n>>(18-6*j))&63])
			} else {
				sb.WriteByte('=')
			}
		}
	}
	return sb.String()
}

func TestNewServerErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, err := scep.NewServer(nil, f.caCert, f.caKey); !errors.Is(err, scep.ErrRA) {
		t.Fatal("nil signer")
	}
	ecCert, ecKey := ecIdentity(t)
	if _, err := scep.NewServer(f.signer, ecCert, ecKey); !errors.Is(err, scep.ErrRA) {
		t.Fatal("EC RA accepted")
	}
}

func TestClientErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := scep.NewClient("http://127.0.0.1:1/scep", nil)
	if c.HTTP != http.DefaultClient {
		t.Fatal("default client")
	}
	if _, err := c.GetCACaps(ctx); !errors.Is(err, scep.ErrClient) {
		t.Fatalf("unreachable: %v", err)
	}
	if _, err := c.Enroll(ctx, nil, scep.EnrollOptions{}); !errors.Is(err, scep.ErrClient) {
		t.Fatal("nil key")
	}
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{}); !errors.Is(err, scep.ErrClient) {
		t.Fatal("GetCACert failure not propagated")
	}
	if _, err := scep.NewClient("://bad", nil).GetCACaps(ctx); !errors.Is(err, scep.ErrClient) {
		t.Fatal("bad URL")
	}

	// Server that answers with junk for every operation, then with a 500.
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
		case "badcert":
			w.Header().Set("Content-Type", scep.ContentTypeCACert)
			_, _ = w.Write([]byte("junk"))
		case "badbundle":
			w.Header().Set("Content-Type", scep.ContentTypeCARACert)
			_, _ = w.Write([]byte("junk"))
		case "emptybundle":
			w.Header().Set("Content-Type", scep.ContentTypeCARACert)
			der, _ := smallscep.DegenerateCertificates(nil)
			_, _ = w.Write(der)
		case "badrep":
			_, _ = w.Write([]byte("junk"))
		}
	}))
	defer srv.Close()
	c = scep.NewClient(srv.URL, srv.Client())
	for _, m := range []string{"500", "badcert", "badbundle", "emptybundle"} {
		mode = m
		if _, err := c.GetCACert(ctx); !errors.Is(err, scep.ErrClient) {
			t.Fatalf("%s: %v", m, err)
		}
	}
	mode = "badrep"
	f := newFixture(t)
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Recipients: []*x509.Certificate{f.caCert}}); !errors.Is(err, scep.ErrClient) {
		t.Fatalf("bad CertRep: %v", err)
	}
	// A CertRep we cannot decrypt (encrypted to someone else).
	ecCert, ecKey := ecIdentity(t)
	if _, err := scep.SelfSigned(ecKey, pkix.Name{CommonName: "x"}); err != nil {
		t.Fatal(err)
	}
	_ = ecCert
	mode = "500"
	if _, err := c.GetCACaps(ctx); !errors.Is(err, scep.ErrClient) {
		t.Fatal("500 caps")
	}
}

func TestClientUndecryptableCertRep(t *testing.T) {
	t.Parallel()
	// The real server issues to the request, but we forward a CertRep for a
	// different request so the client cannot decrypt it.
	f := newFixture(t)
	s, _ := scep.NewServer(f.signer, f.caCert, f.caKey)
	real := httptest.NewServer(s.Handler())
	defer real.Close()
	var stored []byte
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("operation") != "PKIOperation" {
			resp, err := http.Get(real.URL + "?" + r.URL.RawQuery) //nolint:noctx // test helper
			if err != nil {
				panic(err)
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			_, _ = io.Copy(w, resp.Body)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if stored == nil {
			resp, err := http.Post(real.URL+"?operation=PKIOperation", scep.ContentTypePKIMessage, strings.NewReader(string(body))) //nolint:noctx // test helper
			if err != nil {
				panic(err)
			}
			defer resp.Body.Close()
			stored, _ = io.ReadAll(resp.Body)
		}
		w.Header().Set("Content-Type", scep.ContentTypePKIMessage)
		_, _ = w.Write(stored)
	}))
	defer proxy.Close()
	c := scep.NewClient(proxy.URL, proxy.Client())
	ctx := context.Background()
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: pkix.Name{CommonName: "a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: pkix.Name{CommonName: "b"}}); !errors.Is(err, scep.ErrClient) {
		t.Fatalf("replayed CertRep: %v", err)
	}
}

func TestChallenges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	csr := &x509.CertificateRequest{Subject: pkix.Name{CommonName: "UDID-9"}}

	if err := (scep.NoChallenge{}).Verify(ctx, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := scep.StaticChallenge("s").Verify(ctx, "s", csr); err != nil {
		t.Fatal(err)
	}
	if err := scep.StaticChallenge("s").Verify(ctx, "x", csr); !errors.Is(err, scep.ErrChallenge) {
		t.Fatal("static mismatch")
	}
	if err := scep.StaticChallenge("").Verify(ctx, "", csr); !errors.Is(err, scep.ErrChallenge) {
		t.Fatal("empty static secret must never match")
	}

	fc := clock.NewFake(time.Unix(1_700_000_000, 0))
	ot := scep.NewOneTimeChallenges(time.Minute, fc)
	c1, err := ot.Issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := ot.Issue(ctx)
	if c1 == c2 || ot.Live() != 2 {
		t.Fatal("issue")
	}
	if err := ot.Verify(ctx, c1, csr); err != nil {
		t.Fatal(err)
	}
	if err := ot.Verify(ctx, c1, csr); !errors.Is(err, scep.ErrChallenge) {
		t.Fatal("reuse accepted")
	}
	fc.Advance(2 * time.Minute)
	if err := ot.Verify(ctx, c2, csr); !errors.Is(err, scep.ErrChallenge) {
		t.Fatal("expired accepted")
	}
	c3, _ := ot.Issue(ctx)
	fc.Advance(2 * time.Minute)
	c4, _ := ot.Issue(ctx) // sweeps the expired c3
	if ot.Live() != 1 || c3 == c4 {
		t.Fatalf("live %d", ot.Live())
	}
	def := scep.NewOneTimeChallenges(0, nil)
	if _, err := def.Issue(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := scep.NewHMACChallenge([]byte("short"), 0, nil); err == nil {
		t.Fatal("short key")
	}
	h, err := scep.NewHMACChallenge([]byte("0123456789abcdef0123456789abcdef"), time.Minute, fc)
	if err != nil {
		t.Fatal(err)
	}
	ch := h.Issue("UDID-9")
	if err := h.Verify(ctx, ch, csr); err != nil {
		t.Fatal(err)
	}
	if err := h.Verify(ctx, ch, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "UDID-other"}}); !errors.Is(err, scep.ErrChallenge) {
		t.Fatal("challenge bound to another subject accepted")
	}
	for _, bad := range []string{"", "nodot", "x.AAAA", "1.%%%", ch + "A"} {
		if err := h.Verify(ctx, bad, csr); !errors.Is(err, scep.ErrChallenge) {
			t.Fatalf("%q accepted", bad)
		}
	}
	if err := h.Verify(ctx, ch, nil); !errors.Is(err, scep.ErrChallenge) {
		t.Fatal("nil CSR")
	}
	fc.Advance(2 * time.Minute)
	if err := h.Verify(ctx, ch, csr); !errors.Is(err, scep.ErrChallenge) {
		t.Fatal("expired HMAC accepted")
	}
	hd, _ := scep.NewHMACChallenge([]byte("0123456789abcdef0123456789abcdef"), 0, nil)
	if err := hd.Verify(ctx, hd.Issue("z"), &x509.CertificateRequest{Subject: pkix.Name{CommonName: "z"}}); err != nil {
		t.Fatal(err)
	}
}

func TestPKIOperationSignerFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s, _ := scep.NewServer(&failSigner{Local: f.signer}, f.caCert, f.caKey)
	c := serve(t, s)
	if _, err := c.Enroll(context.Background(), rsaKey(t), scep.EnrollOptions{Subject: pkix.Name{CommonName: "u"}}); !errors.Is(err, scep.ErrRejected) {
		t.Fatalf("signer failure: %v", err)
	}
}

type failSigner struct{ *ca.Local }

func (failSigner) Sign(context.Context, *x509.CertificateRequest, ca.Policy) (*x509.Certificate, error) {
	return nil, errors.New("hsm offline")
}
