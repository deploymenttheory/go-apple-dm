package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

func TestParseSignerPEM(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecDER, _ := x509.MarshalECPrivateKey(ec)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(ec)
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	edDER, _ := x509.MarshalPKCS8PrivateKey(edKey)
	for name, tc := range map[string]struct {
		block *pem.Block
		ok    bool
	}{
		"ec":        {&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER}, true},
		"pkcs8":     {&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}, true},
		"ed25519":   {&pem.Block{Type: "PRIVATE KEY", Bytes: edDER}, true},
		"bad ec":    {&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte{1}}, false},
		"bad pkcs8": {&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}}, false},
		"bad rsa":   {&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte{1}}, false},
		"other":     {&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}, false},
	} {
		_, err := parseSignerPEM(pem.EncodeToMemory(tc.block))
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := parseSignerPEM([]byte("not pem")); !errors.Is(err, errPEM) {
		t.Fatal(err)
	}
}

func TestReadCertsPEM(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	_ = os.WriteFile(bad, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2}}), 0o600)
	if _, err := readCertsPEM(bad); err == nil {
		t.Fatal("malformed certificate accepted")
	}
	none := filepath.Join(dir, "none.pem")
	_ = os.WriteFile(none, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte{1}}), 0o600)
	if _, err := readCertsPEM(none); !errors.Is(err, errPEM) {
		t.Fatal(err)
	}
	if _, err := readCertsPEM(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestCompleteBranches(t *testing.T) {
	e := &enrollment{tokens: &accountdriven.Tokens{Store: accountdriven.NewMemStore()}}
	e.asweb = &accountdriven.AppleAsWeb{URL: "https://x/a", Tokens: e.tokens}
	e.oauth = &accountdriven.OAuth2{RedirectURL: "::bad", ClientID: "c", Tokens: e.tokens}
	claims := webauth.Claims{Subject: "s", Email: ""}
	for name, tc := range map[string]struct {
		flow string
		want int
	}{
		"unknown flow":     {"nope", http.StatusBadRequest},
		"asweb no email":   {"asweb", http.StatusInternalServerError},
		"oauth2 bad redir": {"oauth2", http.StatusInternalServerError},
	} {
		rec := httptest.NewRecorder()
		e.complete(context.Background(), webauth.Bound{Extra: map[string]string{"flow": tc.flow, "state": "s"}}, claims, webauth.Decision{}, rec, httptest.NewRequest(http.MethodGet, "/cb", nil))
		if rec.Code != tc.want {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestCompleteADEUnknownSerial(t *testing.T) {
	e := &enrollment{ade: ade.New(ade.Config{})}
	rec := httptest.NewRecorder()
	e.complete(context.Background(), webauth.Bound{Serial: "nope", Extra: map[string]string{"flow": "ade"}}, webauth.Claims{Email: "a@b"}, webauth.Decision{}, rec, httptest.NewRequest(http.MethodGet, "/cb", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown serial = %d", rec.Code)
	}
}

func TestParseDeviceInfoErrors(t *testing.T) {
	ca, err := testpki.NewCA("ca")
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := ca.Issue("device", time.Now().Add(-time.Minute))
	e := &enrollment{}
	parse := e.parseDeviceInfo([]*x509.Certificate{ca.Cert})
	signed, err := cms.SignAttached([]byte("not a plist"), leaf.Cert, leaf.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parse(httptest.NewRequest(http.MethodPost, "/e", bytes.NewReader(signed))); err == nil || !strings.Contains(err.Error(), "plist") {
		t.Fatalf("signed non-plist = %v", err)
	}
	if _, err := parse(httptest.NewRequest(http.MethodPost, "/e", strings.NewReader("garbage"))); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := parse(httptest.NewRequest(http.MethodPost, "/e", errReader{})); err == nil {
		t.Fatal("read error swallowed")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }
