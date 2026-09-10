package httpapi_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/testpki"
)

func TestCertificateTrustBoundaries(t *testing.T) {
	ca, err := testpki.NewCA("root")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue("device", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httpapi.CertFromContext(r.Context()) == nil {
			t.Fatal("identity missing")
		}
		w.WriteHeader(204)
	})
	r := httptest.NewRequest("GET", "https://mdm.example/mdm", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf.Cert}}
	w := httptest.NewRecorder()
	httpapi.CertFromTLS(next).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("unverified TLS accepted", w.Code)
	}
	r.TLS.VerifiedChains = [][]*x509.Certificate{{leaf.Cert, ca.Cert}}
	w = httptest.NewRecorder()
	httpapi.CertFromTLS(next).ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	header := httpapi.CertFromHeader(
		"Client-Cert",
		httpapi.WithHeaderRoots(ca.Pool()),
		httpapi.WithHeaderPeers(netip.MustParsePrefix("192.0.2.0/24")),
	)(
		next,
	)
	r = httptest.NewRequest("GET", "https://mdm.example/mdm", nil)
	r.Header.Set("Client-Cert", ":"+base64.StdEncoding.EncodeToString(leaf.Cert.Raw)+":")
	r.RemoteAddr = "198.51.100.1:1234"
	r.Header.Set("X-Forwarded-For", "192.0.2.1")
	w = httptest.NewRecorder()
	header.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("forged peer accepted", w.Code)
	}
	r.RemoteAddr = "192.0.2.1:1234"
	w = httptest.NewRecorder()
	header.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("trusted proxy rejected", w.Code)
	}
	r.Header.Add("Client-Cert", r.Header.Get("Client-Cert"))
	w = httptest.NewRecorder()
	header.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("duplicate assertion accepted", w.Code)
	}
	other, err := ca.Issue("another-device", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Del("Client-Cert")
	r.Header.Set("Client-Cert", ":"+base64.StdEncoding.EncodeToString(other.Cert.Raw)+":")
	r.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf.Cert},
		VerifiedChains:   [][]*x509.Certificate{{leaf.Cert, ca.Cert}},
	}
	w = httptest.NewRecorder()
	httpapi.CertFromTLS(header).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("conflicting credential sources accepted", w.Code)
	}
}

func TestDuplicateEmptyCertificateEvidenceRejected(t *testing.T) {
	next := http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { t.Fatal("ambiguous credentials reached handler") },
	)
	for name, handler := range map[string]http.Handler{
		"Client-Cert":  httpapi.CertFromHeader("Client-Cert")(next),
		cms.HeaderName: httpapi.CertFromMdmSignature(cms.VerifyOptions{}, 0)(next),
	} {
		r := httptest.NewRequest("PUT", "https://mdm.example/mdm", nil)
		r.Header.Add(name, "")
		r.Header.Add(name, "second credential")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatal(name, w.Code)
		}
	}
}
