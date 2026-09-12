package scep_test

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	smallscep "github.com/smallstep/scep"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/internal/scepwire"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func TestClientRejectsUntrustedReply(t *testing.T) {
	trusted, err := testpki.NewCA("trusted-enrollment-ca")
	if err != nil {
		t.Fatal(err)
	}
	rogue, err := testpki.NewCA("attacker-ca")
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := rogue.Issue("wrong-subject", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		req, err := smallscep.ParsePKIMessage(raw)
		if err != nil {
			t.Error(err)
			return
		}
		// Only the public request envelope is read; the trusted CA key is never used.
		req.CSRReqMessage = &smallscep.CSRReqMessage{CSR: &x509.CertificateRequest{}}
		req.TransactionID = "unrelated-transaction"
		req.SenderNonce = []byte("unrelated-nonce!")
		rep, err := req.Success(rogue.Cert, rogue.Key, wrong.Cert)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", scep.ContentTypePKIMessage)
		_, _ = w.Write(rep.Raw)
	}))
	defer srv.Close()
	got, err := scep.NewClient(srv.URL, srv.Client()).
		Enroll(t.Context(), key, scep.EnrollOptions{Subject: pkix.Name{CommonName: "expected-device"}, Challenge: "synthetic-secret", Recipients: []*x509.Certificate{trusted.Cert}})
	if err == nil {
		t.Fatalf(
			"accepted rogue certificate: issuer=%q key=%T",
			got.Issuer.CommonName,
			got.PublicKey,
		)
	}
}

func TestClientChecksIssuedIdentityAndTransaction(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t)
	key := rsaKey(t)
	wrongKey := rsaKey(t)
	for _, mode := range []string{"valid", "RA", "transaction", "nonce", "key", "issuer", "expired", "request reflection"} {
		t.Run(mode, func(t *testing.T) {
			raCert, raKey := f.caCert, f.caKey
			if mode == "RA" {
				raKey = rsaKey(t)
				raCert = clientTestCertificate(
					t,
					f.caCert,
					f.caKey,
					raKey.Public(),
					time.Now().Add(time.Hour),
				)
			}
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					request, err := smallscep.ParsePKIMessage(body)
					if err != nil {
						t.Error(err)
						return
					}
					if mode == "request reflection" {
						_, _ = w.Write(body)
						return
					}
					if err = request.DecryptPKIEnvelope(raCert, raKey); err != nil {
						t.Error(err)
						return
					}
					pub := request.CSR.PublicKey
					issuer, signer := f.caCert, f.caKey
					until := time.Now().Add(time.Hour)
					switch mode {
					case "transaction":
						request.TransactionID = "another-transaction"
					case "nonce":
						request.SenderNonce = bytes.Repeat([]byte{7}, 16)
					case "key":
						pub = wrongKey.Public()
					case "issuer":
						issuer, signer = other.caCert, other.caKey
					case "expired":
						until = time.Now().Add(-time.Hour)
					}
					issued := clientTestCertificate(t, issuer, signer, pub, until)
					parsed, err := pkcs7.Parse(body)
					if err != nil {
						t.Error(err)
						return
					}
					response, err := scepwire.Reply(request, raCert, raKey, parsed.GetOnlySigner(), issued, "")
					if err != nil {
						t.Error(err)
						return
					}
					_, _ = w.Write(response)
				}),
			)
			defer server.Close()
			recipients := []*x509.Certificate{f.caCert}
			if mode == "RA" {
				recipients = []*x509.Certificate{raCert, f.caCert}
			}
			got, err := scep.NewClient(server.URL, server.Client()).
				Enroll(t.Context(), key, scep.EnrollOptions{
					Subject: pkix.Name{CommonName: "requested-subject"}, Recipients: recipients,
				})
			if mode == "valid" || mode == "RA" {
				if err != nil {
					t.Fatal(err)
				}
				if got.Subject.CommonName != "CA-selected subject" {
					t.Fatal("CA subject override lost")
				}
			} else if !errors.Is(err, scep.ErrClient) {
				t.Fatalf("unsafe %s response accepted: %v", mode, err)
			}
		})
	}
}

func clientTestCertificate(
	t *testing.T,
	issuer *x509.Certificate,
	signer crypto.Signer,
	pub crypto.PublicKey,
	until time.Time,
) *x509.Certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(
			time.Now().UnixNano(),
		),
		Subject:   pkix.Name{CommonName: "CA-selected subject"},
		NotBefore: time.Now().Add(-2 * time.Hour),
		NotAfter:  until,
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestClientDiscoveryAndRedirectTrust(t *testing.T) {
	f := newFixture(t)
	endpoint, err := newTestServer(f.signer, f.caCert, f.caKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(endpoint.Handler())
	defer server.Close()
	key := rsaKey(t)
	// An HTTPS URL alone is insufficient if the caller disabled verification.
	unverified := server.Client().Transport.(*http.Transport).Clone()
	unverified.TLSClientConfig = unverified.TLSClientConfig.Clone()
	unverified.TLSClientConfig.InsecureSkipVerify = true
	defer unverified.CloseIdleConnections()
	if _, err := scep.NewClient(server.URL, &http.Client{Transport: unverified}).
		Enroll(t.Context(), key, scep.EnrollOptions{}); !errors.Is(
		err,
		scep.ErrClient,
	) {
		t.Fatalf("unverified discovery accepted: %v", err)
	}
	plain := httptest.NewServer(endpoint.Handler())
	defer plain.Close()
	if _, err := scep.NewClient(plain.URL, plain.Client()).
		Enroll(t.Context(), key, scep.EnrollOptions{}); !errors.Is(
		err,
		scep.ErrClient,
	) {
		t.Fatalf("HTTP discovery accepted: %v", err)
	}
	var reached bool
	destination := httptest.NewServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }),
	)
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := scep.NewClient(redirect.URL, redirect.Client())
	if _, err := client.Enroll(
		t.Context(),
		key,
		scep.EnrollOptions{Recipients: []*x509.Certificate{f.caCert}},
	); !errors.Is(err, scep.ErrClient) ||
		reached {
		t.Fatalf("redirect followed: reached=%v err=%v", reached, err)
	}
}
