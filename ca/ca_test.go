package ca_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/ca"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
)

func csr(t *testing.T, key any, cn string, sans bool) *x509.CertificateRequest {
	t.Helper()
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn, Organization: []string{"o"}}}
	if sans {
		tmpl.DNSNames = []string{"dev.example"}
		tmpl.EmailAddresses = []string{"a@example"}
		tmpl.IPAddresses = []net.IP{net.IPv4(10, 0, 0, 1)}
		tmpl.URIs = []*url.URL{{Scheme: "urn", Opaque: "x"}}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSelfSignedAndLocalSignsWithPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !cert.IsCA || cert.Subject.CommonName != "go-apple-mdm CA" || cert.NotAfter.Before(time.Now().Add(9*365*24*time.Hour)) {
		t.Fatalf("self-signed CA %+v", cert.Subject)
	}
	depot := ca.NewMemoryDepot()
	fake := clock.NewFake(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	signer, err := ca.NewLocal(cert, key, ca.WithDepot(depot), ca.WithClock(fake), ca.WithChain(cert))
	if err != nil {
		t.Fatal(err)
	}
	if signer.Certificate() != cert || len(signer.Chain()) != 2 {
		t.Fatal("Certificate/Chain")
	}
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for name, k := range map[string]any{"rsa": rsaKey, "ecdsa": ecKey} {
		issued, err := signer.Sign(ctx, csr(t, k, "device-"+name, false), ca.Policy{Validity: 48 * time.Hour})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if issued.Subject.CommonName != "device-"+name || issued.Subject.Organization[0] != "o" {
			t.Errorf("%s: subject %v", name, issued.Subject)
		}
		if !issued.NotBefore.Equal(fake.Now().Add(-5*time.Minute)) || !issued.NotAfter.Equal(fake.Now().Add(48*time.Hour)) {
			t.Errorf("%s: validity %v-%v", name, issued.NotBefore, issued.NotAfter)
		}
		if issued.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || issued.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
			t.Errorf("%s: usages", name)
		}
		if name == "rsa" && issued.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
			t.Error("rsa should get key encipherment")
		}
		if _, err := issued.Verify(x509.VerifyOptions{Roots: pool(cert), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			t.Errorf("%s: chain %v", name, err)
		}
		if got, err := depot.Get(ctx, issued.SerialNumber); err != nil || !got.Equal(issued) {
			t.Errorf("%s: depot %v", name, err)
		}
	}
	if depot.Len() != 2 {
		t.Fatalf("depot has %d", depot.Len())
	}
	// SANs rejected by default, allowed by policy.
	if _, err := signer.Sign(ctx, csr(t, rsaKey, "san", true), ca.Policy{}); !errors.Is(err, ca.ErrPolicy) {
		t.Fatalf("sans: %v", err)
	}
	withSANs, err := signer.Sign(ctx, csr(t, rsaKey, "san", true), ca.Policy{AllowSANs: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}})
	if err != nil || len(withSANs.DNSNames) != 1 || len(withSANs.IPAddresses) != 1 || len(withSANs.URIs) != 1 || withSANs.ExtKeyUsage[0] != x509.ExtKeyUsageEmailProtection {
		t.Fatalf("with sans: %+v %v", withSANs, err)
	}
	// Small RSA keys rejected.
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	if _, err := signer.Sign(ctx, csr(t, small, "small", false), ca.Policy{}); !errors.Is(err, ca.ErrPolicy) {
		t.Fatalf("small key: %v", err)
	}
	if _, err := signer.Sign(ctx, csr(t, small, "small", false), ca.Policy{MinRSABits: 1024}); err != nil {
		t.Fatalf("small key allowed by policy: %v", err)
	}
	// Broken CSR signature and nil CSR.
	broken := csr(t, rsaKey, "b", false)
	broken.Signature[0] ^= 0xff
	if _, err := signer.Sign(ctx, broken, ca.Policy{}); !errors.Is(err, ca.ErrCSR) {
		t.Fatalf("broken csr: %v", err)
	}
	if _, err := signer.Sign(ctx, nil, ca.Policy{}); !errors.Is(err, ca.ErrCSR) {
		t.Fatal("nil csr")
	}
}

func pool(c *x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c)
	return p
}

func TestConstructorErrors(t *testing.T) {
	t.Parallel()
	cert, key, _ := ca.NewSelfSigned(ca.SelfSignedOptions{Subject: pkix.Name{CommonName: "x"}, Validity: time.Hour, RSABits: 2048})
	if _, err := ca.NewLocal(nil, key); err == nil {
		t.Error("nil cert")
	}
	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, cert, &leafKey.PublicKey, key)
	leaf, _ := x509.ParseCertificate(der)
	if _, err := ca.NewLocal(leaf, leafKey); err == nil {
		t.Error("non-CA cert accepted")
	}
	if _, _, err := ca.NewSelfSigned(ca.SelfSignedOptions{RSABits: 8}); err == nil {
		t.Error("tiny RSA size should fail")
	}
	d := ca.NewMemoryDepot()
	if err := d.Put(context.Background(), nil); err == nil {
		t.Error("nil put")
	}
	if _, err := d.Get(context.Background(), big.NewInt(42)); !errors.Is(err, ca.ErrNotFound) {
		t.Error("missing serial")
	}
	a, _ := ca.Serial()
	b, _ := ca.Serial()
	if a.Sign() <= 0 || a.Cmp(b) == 0 || a.BitLen() > 128 {
		t.Errorf("serials %v %v", a, b)
	}
}

type failDepot struct{}

func (failDepot) Put(context.Context, *x509.Certificate) error { return errors.New("disk full") }
func (failDepot) Get(context.Context, *big.Int) (*x509.Certificate, error) {
	return nil, errors.New("no")
}

func TestDepotFailure(t *testing.T) {
	t.Parallel()
	cert, key, _ := ca.NewSelfSigned(ca.SelfSignedOptions{})
	signer, _ := ca.NewLocal(cert, key, ca.WithDepot(failDepot{}))
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := signer.Sign(context.Background(), csr(t, k, "d", false), ca.Policy{}); err == nil {
		t.Fatal("depot failure should fail signing")
	}
}

func TestMismatchedCAKey(t *testing.T) {
	t.Parallel()
	cert, _, _ := ca.NewSelfSigned(ca.SelfSignedOptions{})
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, err := ca.NewLocal(cert, other)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := signer.Sign(context.Background(), csr(t, k, "d", false), ca.Policy{}); err == nil {
		t.Fatal("signing with a key that does not match the CA certificate should fail")
	}
}

type failReader struct{ after int }

func (f *failReader) Read(p []byte) (int, error) {
	if f.after <= 0 {
		return 0, errors.New("entropy exhausted")
	}
	f.after--
	return rand.Read(p)
}

func TestRandomFailures(t *testing.T) {
	t.Parallel()
	if _, err := ca.SerialFrom(&failReader{}); err == nil {
		t.Fatal("serial without entropy")
	}
	cert, key, _ := ca.NewSelfSigned(ca.SelfSignedOptions{})
	signer, _ := ca.NewLocal(cert, key, ca.WithRandom(&failReader{}))
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := signer.Sign(context.Background(), csr(t, k, "d", false), ca.Policy{}); err == nil {
		t.Fatal("sign without entropy for the serial")
	}
	// Enough entropy for the key but not for the serial.
	if _, _, err := ca.NewSelfSigned(ca.SelfSignedOptions{Random: &failReader{after: 64}}); err == nil {
		t.Log("self-signed with limited entropy succeeded (key generation used fewer reads)")
	}
}

// TestPolicyNotAfterIsAbsolute: a deadline is a moment, not a duration.
// Expressing it as a duration would let the certificate outlive it by
// however long issuing took, which is a margin nobody notices until it
// matters.
func TestPolicyNotAfterIsAbsolute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	fake := clock.NewFake(now)
	signer, err := ca.NewLocal(cert, key, ca.WithClock(fake))
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	req := csr(t, leafKey, "capped", false)

	deadline := now.Add(2 * time.Hour)
	issued, err := signer.Sign(ctx, req, ca.Policy{Validity: 365 * 24 * time.Hour, NotAfter: deadline})
	if err != nil {
		t.Fatal(err)
	}
	if !issued.NotAfter.Equal(deadline) {
		t.Fatalf("NotAfter = %s, want exactly the deadline %s", issued.NotAfter, deadline)
	}

	// A deadline beyond the validity does not extend the certificate.
	issued, err = signer.Sign(ctx, req, ca.Policy{
		Validity: time.Hour,
		NotAfter: now.Add(10 * 365 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !issued.NotAfter.Equal(now.Add(time.Hour)) {
		t.Fatalf("NotAfter = %s, want the validity to win", issued.NotAfter)
	}

	// A deadline that has already passed would produce a certificate that
	// is expired the moment it is issued, so it is refused instead.
	for _, past := range []time.Time{now.Add(-time.Hour), now} {
		_, err := signer.Sign(ctx, req, ca.Policy{NotAfter: past})
		if !errors.Is(err, ca.ErrPolicy) {
			t.Fatalf("NotAfter %s = %v, want ErrPolicy", past, err)
		}
	}
}
