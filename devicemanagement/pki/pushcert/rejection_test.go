package pushcert_test

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func TestUntrustedProviderIdentity(t *testing.T) {
	t.Parallel()
	ca, err := testpki.NewCA("issuer")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tc := range []struct {
		name   string
		change func(*x509.Certificate)
	}{
		{"CA", func(c *x509.Certificate) { c.IsCA = true; c.BasicConstraintsValid = true }},
		{"no signing usage", func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageKeyEncipherment }},
		{"server only", func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := ca.IssuePush("com.apple.mgmt.External.test", now.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			leaf := *id.Cert
			leaf.Subject.ExtraNames = leaf.Subject.Names
			tc.change(&leaf)
			der, err := x509.CreateCertificate(rand.Reader, &leaf, ca.Cert, id.Key.Public(), ca.Key)
			if err != nil {
				t.Fatal(err)
			}
			pair := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: id.Key, Leaf: id.Cert}
			if err := pushcert.Validate(
				pair,
				"com.apple.mgmt.External.test",
				true,
				now,
			); !errors.Is(
				err,
				pushcert.ErrInvalid,
			) {
				t.Fatalf("invalid TLS identity accepted: %v", err)
			}
			_, key, err := id.PEM()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := storage.ValidatePushCert(
				"com.apple.mgmt.External.test",
				der,
				key,
				now,
			); !errors.Is(
				err,
				storage.ErrInvalid,
			) {
				t.Fatalf("store accepted invalid TLS usage: %v", err)
			}
		})
	}
	id := appIdentity(t, appTopics(t))
	_, key, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{[]byte("broken"), pemBlock("CERTIFICATE", []byte("broken"))} {
		if _, err := pushcert.Inspect(raw); !errors.Is(err, pushcert.ErrInvalid) {
			t.Fatalf("inspect: %v", err)
		}
		if _, err := pushcert.PEM(raw); !errors.Is(err, pushcert.ErrInvalid) {
			t.Fatalf("PEM: %v", err)
		}
		if err := pushcert.Validate(
			tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: id.Key},
			"com.example.app",
			false,
			now,
		); !errors.Is(
			err,
			pushcert.ErrInvalid,
		) {
			t.Fatalf("validate: %v", err)
		}
	}
	if err := pushcert.Validate(
		tls.Certificate{Certificate: [][]byte{id.Cert.Raw}, PrivateKey: ca.Key},
		"com.example.app",
		false,
		now,
	); !errors.Is(
		err,
		pushcert.ErrKeyMismatch,
	) {
		t.Fatalf("mismatched signing key: %v", err)
	}
	ext, err := asn1.Marshal(struct {
		Topic    string
		Services []string
	}{"", []string{"topic"}})
	if err != nil {
		t.Fatal(err)
	}
	bad := appIdentity(t, ext)
	if err := pushcert.Validate(
		tls.Certificate{Certificate: [][]byte{bad.Cert.Raw}, PrivateKey: bad.Key},
		"com.example.app",
		false,
		now,
	); !errors.Is(
		err,
		pushcert.ErrInvalid,
	) {
		t.Fatalf("invalid topic extension: %v", err)
	}
	for _, csr := range [][]byte{[]byte("broken"), pemBlock("CERTIFICATE", id.Cert.Raw)} {
		if _, err := pushcert.SignCSR(
			csr,
			id.Cert.Raw,
			key,
			ca.Pool(),
			now,
		); !errors.Is(
			err,
			pushcert.ErrInvalid,
		) {
			t.Fatalf("invalid CSR: %v", err)
		}
	}
	_, csr, err := pushcert.GenerateCSR(pkix.Name{CommonName: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pushcert.SignCSR(
		csr,
		id.Cert.Raw,
		key,
		ca.Pool(),
		now,
	); !errors.Is(
		err,
		pushcert.ErrInvalid,
	) {
		t.Fatalf("non-RSA vendor: %v", err)
	}
	if _, _, err := pushcert.GenerateCSR(
		pkix.Name{
			ExtraNames: []pkix.AttributeTypeAndValue{
				{Type: asn1.ObjectIdentifier{2, 5, 4, 3}, Value: make(chan int)},
			},
		},
	); err == nil {
		t.Fatal("invalid CSR subject accepted")
	}
}
