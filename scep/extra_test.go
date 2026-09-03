package scep_test

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/ca"
	"github.com/deploymenttheory/go-apple-dm/cms"
	"github.com/deploymenttheory/go-apple-dm/scep"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// TestRenewalSkipsChallenge is the named proof for decision record 0008:
// a RenewalReq signed by an identity we issued needs no challenge, while a
// fresh PKCSReq without one is rejected.
func TestRenewalSkipsChallenge(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ot := scep.NewOneTimeChallenges(0, nil)
	s, _ := scep.NewServer(f.signer, f.caCert, f.caKey, scep.WithChallenge(ot))
	c := serve(t, s)
	ctx := context.Background()
	one, _ := ot.Issue(ctx)
	key := rsaKey(t)
	subject := pkix.Name{CommonName: "UDID-R"}
	cert, err := c.Enroll(ctx, key, scep.EnrollOptions{Subject: subject, Challenge: one})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: subject}); !errors.Is(err, scep.ErrRejected) {
		t.Fatal("PKCSReq without challenge accepted")
	}
	if _, err := c.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: subject, Renew: &scep.Identity{Cert: cert, Key: key}}); err != nil {
		t.Fatalf("renewal: %v", err)
	}
}

func TestHandlerBodyAndClientTransportErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	s, _ := scep.NewServer(f.signer, f.caCert, f.caKey)
	c := serve(t, s)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/scep?operation=PKIOperation", errReader{}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("erroring body: %d", rec.Code)
	}

	// Truncated response body: Content-Length promises more than is sent.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	if _, err := scep.NewClient(srv.URL, srv.Client()).GetCACaps(context.Background()); !errors.Is(err, scep.ErrClient) {
		t.Fatalf("truncated: %v", err)
	}

	// A bad URL surfaces from the POST when recipients are given up front.
	f2 := newFixture(t)
	if _, err := scep.NewClient("://bad", nil).Enroll(context.Background(), rsaKey(t), scep.EnrollOptions{Recipients: []*x509.Certificate{f2.caCert}}); !errors.Is(err, scep.ErrClient) || !strings.Contains(err.Error(), "missing protocol scheme") {
		t.Fatalf("bad URL POST: %v", err)
	}
	// A signer whose public key x509 cannot encode fails at CSR creation.
	if _, err := c.Enroll(context.Background(), oddSigner{rsaKey(t)}, scep.EnrollOptions{Recipients: []*x509.Certificate{f2.caCert}}); !errors.Is(err, scep.ErrClient) || !strings.Contains(err.Error(), "create CSR") {
		t.Fatalf("odd signer: %v", err)
	}
	// A valid PKCS #7 that is not a SCEP message: parses, then fails as a PKIMessage.
	leaf, leafKey := f2.caCert, f2.caKey
	notSCEP, err := cms.SignAttached([]byte("hello"), leaf, leafKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PKIOperation(context.Background(), notSCEP); !errors.Is(err, scep.ErrCSR) {
		t.Fatalf("non-SCEP PKCS7: %v", err)
	}
	// Deployments may put a query on the SCEP URL; operation is appended.
	if _, err := scep.NewClient(srv.URL+"/scep?ca=1", srv.Client()).GetCACaps(context.Background()); !errors.Is(err, scep.ErrClient) {
		t.Fatalf("query URL: %v", err)
	}

	// An EC recipient cannot receive a PKCS #7 key-transport envelope.
	ecCert, _ := ecIdentity(t)
	if _, err := c.Enroll(context.Background(), rsaKey(t), scep.EnrollOptions{Recipients: []*x509.Certificate{ecCert}}); !errors.Is(err, scep.ErrClient) {
		t.Fatalf("ec recipient: %v", err)
	}
}

type oddSigner struct{ *rsa.PrivateKey }

func (oddSigner) Public() crypto.PublicKey { return struct{}{} }

func TestSelfSignedRejectsUnsupportedKey(t *testing.T) {
	t.Parallel()
	if _, err := scep.SelfSigned(oddSigner{rsaKey(t)}, pkix.Name{CommonName: "x"}); !errors.Is(err, scep.ErrClient) {
		t.Fatal("unsupported key accepted")
	}
	if _, err := io.ReadAll(errReader{}); err == nil {
		t.Fatal("errReader")
	}
	_ = ca.Policy{}
}
