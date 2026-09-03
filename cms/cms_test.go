package cms_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-dm/cms"
)

type identity struct {
	cert *x509.Certificate
	key  crypto.Signer
}

func newCA(t *testing.T) identity {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return identity{cert: cert, key: key}
}

func newLeaf(t *testing.T, ca identity, key crypto.Signer, notBefore time.Time) identity {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "device"},
		NotBefore: notBefore, NotAfter: notBefore.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, key.Public(), ca.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return identity{cert: cert, key: key}
}

func rsaKey(t *testing.T) crypto.Signer {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func ecKey(t *testing.T) crypto.Signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func pool(ca identity) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

func TestSignVerifyRSAAndECDSA(t *testing.T) {
	t.Parallel()
	ca := newCA(t)
	body := []byte("<plist/>")
	for name, key := range map[string]crypto.Signer{"rsa": rsaKey(t), "ecdsa": ecKey(t)} {
		leaf := newLeaf(t, ca, key, time.Now().Add(-time.Minute))
		der, err := cms.Sign(body, leaf.cert, leaf.key)
		if err != nil {
			t.Fatalf("%s: Sign: %v", name, err)
		}
		header := cms.EncodeHeader(der)
		got, err := cms.VerifyHeader(header, body, cms.VerifyOptions{Roots: pool(ca)})
		if err != nil {
			t.Fatalf("%s: VerifyHeader: %v", name, err)
		}
		if !got.Equal(leaf.cert) {
			t.Fatalf("%s: signer mismatch", name)
		}
		if cms.Fingerprint(got) != cms.Fingerprint(leaf.cert) || len(cms.Fingerprint(got)) != 64 {
			t.Fatalf("%s: fingerprint", name)
		}
		// No trust store: signature only.
		if _, err := cms.Verify(der, body, cms.VerifyOptions{}); err != nil {
			t.Fatalf("%s: Verify without roots: %v", name, err)
		}
		// Tampered body.
		if _, err := cms.Verify(der, []byte("<plist>x</plist>"), cms.VerifyOptions{}); !errors.Is(err, cms.ErrSignature) {
			t.Fatalf("%s: tampered: %v", name, err)
		}
		// Wrong root.
		other := newCA(t)
		if _, err := cms.Verify(der, body, cms.VerifyOptions{Roots: pool(other)}); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("%s: wrong root: %v", name, err)
		}
	}
}

func TestVerifySigningTimeSkew(t *testing.T) {
	t.Parallel()
	ca := newCA(t)
	// The device's clock lags: its certificate was issued "in the future".
	for name, key := range map[string]crypto.Signer{"rsa": rsaKey(t), "ecdsa": ecKey(t)} {
		leaf := newLeaf(t, ca, key, time.Now().Add(2*time.Minute))
		body := []byte("body")
		der, err := cms.Sign(body, leaf.cert, leaf.key)
		if err != nil {
			t.Fatal(err)
		}
		now := func() time.Time { return time.Now().Add(3 * time.Minute) }
		if _, err := cms.Verify(der, body, cms.VerifyOptions{Roots: pool(ca), Now: now}); !errors.Is(err, cms.ErrSigningTime) {
			t.Fatalf("%s: without skew: %v", name, err)
		}
		if _, err := cms.Verify(der, body, cms.VerifyOptions{Roots: pool(ca), Now: now, ClockSkew: 5 * time.Minute}); err != nil {
			t.Fatalf("%s: with skew: %v", name, err)
		}
		if _, err := cms.Verify(der, body, cms.VerifyOptions{Now: now, ClockSkew: 5 * time.Minute}); err != nil {
			t.Fatalf("%s: with skew, no roots: %v", name, err)
		}
		// Skew too small still fails.
		if _, err := cms.Verify(der, body, cms.VerifyOptions{Roots: pool(ca), Now: now, ClockSkew: time.Second}); !errors.Is(err, cms.ErrSigningTime) {
			t.Fatalf("%s: small skew: %v", name, err)
		}
		// Tolerant path still rejects tampering and wrong roots.
		if _, err := cms.Verify(der, []byte("other"), cms.VerifyOptions{Now: now, ClockSkew: 5 * time.Minute}); !errors.Is(err, cms.ErrSignature) {
			t.Fatalf("%s: tolerant tampered: %v", name, err)
		}
		if _, err := cms.Verify(der, body, cms.VerifyOptions{Roots: pool(newCA(t)), Now: now, ClockSkew: 5 * time.Minute}); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("%s: tolerant wrong root: %v", name, err)
		}
		// Chain check at a time before the leaf is valid fails in the tolerant path too.
		early := func() time.Time { return time.Now().Add(-time.Hour) }
		if _, err := cms.Verify(der, body, cms.VerifyOptions{Roots: pool(ca), Now: early, ClockSkew: 5 * time.Minute}); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("%s: tolerant early: %v", name, err)
		}
	}
}

func TestVerifyErrors(t *testing.T) {
	t.Parallel()
	if _, err := cms.DecodeHeader(""); !errors.Is(err, cms.ErrHeader) {
		t.Error("empty header")
	}
	if _, err := cms.DecodeHeader("!!!"); !errors.Is(err, cms.ErrHeader) {
		t.Error("bad base64")
	}
	if _, err := cms.VerifyHeader("!!!", nil, cms.VerifyOptions{}); !errors.Is(err, cms.ErrHeader) {
		t.Error("VerifyHeader bad base64")
	}
	if _, err := cms.Verify([]byte("garbage"), nil, cms.VerifyOptions{}); !errors.Is(err, cms.ErrParse) {
		t.Error("garbage DER")
	}
	// Two signers.
	ca := newCA(t)
	a, b := newLeaf(t, ca, rsaKey(t), time.Now().Add(-time.Minute)), newLeaf(t, ca, rsaKey(t), time.Now().Add(-time.Minute))
	sd, _ := pkcs7.NewSignedData([]byte("x"))
	_ = sd.AddSigner(a.cert, a.key, pkcs7.SignerInfoConfig{})
	_ = sd.AddSigner(b.cert, b.key, pkcs7.SignerInfoConfig{})
	sd.Detach()
	der, _ := sd.Finish()
	if _, err := cms.Verify(der, []byte("x"), cms.VerifyOptions{}); !errors.Is(err, cms.ErrMultipleSigners) {
		t.Errorf("two signers: %v", err)
	}
	// Signed data without signers.
	sd2, _ := pkcs7.NewSignedData([]byte("x"))
	der2, _ := sd2.Finish()
	if _, err := cms.Verify(der2, []byte("x"), cms.VerifyOptions{}); !errors.Is(err, cms.ErrNoSigner) {
		t.Errorf("no signers: %v", err)
	}
	// Sign errors.
	if _, err := cms.Sign(nil, nil, nil); !errors.Is(err, cms.ErrSign) {
		t.Error("Sign nil")
	}
	// Expired certificate with a trust store fails chain verification.
	expired := newLeaf(t, ca, rsaKey(t), time.Now().Add(-48*time.Hour))
	derExp, err := cms.Sign([]byte("x"), expired.cert, expired.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cms.Verify(derExp, []byte("x"), cms.VerifyOptions{Roots: pool(ca)}); err == nil {
		t.Error("expired leaf should fail")
	}
}

func FuzzVerify(f *testing.F) {
	ca := newCA(&testing.T{})
	leaf := newLeaf(&testing.T{}, ca, rsaKey(&testing.T{}), time.Now().Add(-time.Minute))
	der, _ := cms.Sign([]byte("seed"), leaf.cert, leaf.key)
	f.Add(der, []byte("seed"))
	f.Fuzz(func(t *testing.T, sig, body []byte) {
		_, _ = cms.Verify(sig, body, cms.VerifyOptions{ClockSkew: time.Minute})
	})
}
