package cms

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"
)

func selfSigned(t *testing.T, key crypto.Signer, notBefore time.Time) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "self"}, NotBefore: notBefore, NotAfter: notBefore.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

// signWith produces a detached signature with the given digest OID so every
// hash branch of the tolerant verifier is exercised.
func signWith(t *testing.T, key crypto.Signer, cert *x509.Certificate, digest, enc asn1.ObjectIdentifier, content []byte) []byte {
	t.Helper()
	sd, err := pkcs7.NewSignedData(content)
	if err != nil {
		t.Fatal(err)
	}
	sd.SetDigestAlgorithm(digest)
	sd.SetEncryptionAlgorithm(enc)
	if err := sd.AddSigner(cert, key, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	sd.Detach()
	der, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestTolerantAllHashes(t *testing.T) {
	t.Parallel()
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	future := time.Now().Add(2 * time.Minute)
	rsaCert, ecCert := selfSigned(t, rsaKey, future), selfSigned(t, ecKey, future)
	now := func() time.Time { return time.Now().Add(3 * time.Minute) }
	opts := VerifyOptions{Now: now, ClockSkew: 5 * time.Minute}
	content := []byte("c")
	cases := []struct {
		name   string
		key    crypto.Signer
		cert   *x509.Certificate
		digest asn1.ObjectIdentifier
		enc    asn1.ObjectIdentifier
	}{
		{"rsa-sha1", rsaKey, rsaCert, pkcs7.OIDDigestAlgorithmSHA1, pkcs7.OIDEncryptionAlgorithmRSA},
		{"rsa-sha384", rsaKey, rsaCert, pkcs7.OIDDigestAlgorithmSHA384, pkcs7.OIDEncryptionAlgorithmRSA},
		{"rsa-sha512", rsaKey, rsaCert, pkcs7.OIDDigestAlgorithmSHA512, pkcs7.OIDEncryptionAlgorithmRSA},
		{"rsa-sha256-explicit", rsaKey, rsaCert, pkcs7.OIDDigestAlgorithmSHA256, pkcs7.OIDEncryptionAlgorithmRSASHA256},
		{"ecdsa-sha256", ecKey, ecCert, pkcs7.OIDDigestAlgorithmSHA256, pkcs7.OIDEncryptionAlgorithmECDSAP256},
		{"ecdsa-sha384", ecKey, ecCert, pkcs7.OIDDigestAlgorithmSHA384, pkcs7.OIDEncryptionAlgorithmECDSAP256},
	}
	for _, c := range cases {
		der := signWith(t, c.key, c.cert, c.digest, c.enc, content)
		if _, err := Verify(der, content, opts); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestTolerantErrorBranches(t *testing.T) {
	t.Parallel()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	cert := selfSigned(t, key, time.Now().Add(-time.Minute))
	content := []byte("c")
	der := signWith(t, key, cert, pkcs7.OIDDigestAlgorithmSHA256, pkcs7.OIDEncryptionAlgorithmRSA, content)
	parse := func() *pkcs7.PKCS7 {
		p7, err := pkcs7.Parse(der)
		if err != nil {
			t.Fatal(err)
		}
		p7.Content = content
		return p7
	}
	// Baseline: tolerant path succeeds on a valid structure.
	if err := verifyTolerant(parse(), cert, content); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// No authenticated attributes.
	p7 := parse()
	p7.Signers[0].AuthenticatedAttributes = nil
	if err := verifyTolerant(p7, cert, content); !errors.Is(err, ErrSignature) {
		t.Errorf("no attributes: %v", err)
	}
	// Corrupt message digest attribute value.
	p7 = parse()
	for i := range p7.Signers[0].AuthenticatedAttributes {
		if p7.Signers[0].AuthenticatedAttributes[i].Type.Equal(pkcs7.OIDAttributeMessageDigest) {
			p7.Signers[0].AuthenticatedAttributes[i].Value.Bytes = []byte{0xff}
		}
	}
	if err := verifyTolerant(p7, cert, content); !errors.Is(err, ErrSignature) {
		t.Errorf("bad digest attribute: %v", err)
	}
	// Unsupported digest algorithm.
	p7 = parse()
	p7.Signers[0].DigestAlgorithm.Algorithm = asn1.ObjectIdentifier{1, 2, 3}
	if err := verifyTolerant(p7, cert, content); !errors.Is(err, ErrAlgorithm) {
		t.Errorf("bad digest oid: %v", err)
	}
	// Unsupported signature algorithm.
	p7 = parse()
	p7.Signers[0].DigestEncryptionAlgorithm.Algorithm = asn1.ObjectIdentifier{1, 2, 3}
	if err := verifyTolerant(p7, cert, content); !errors.Is(err, ErrAlgorithm) {
		t.Errorf("bad encryption oid: %v", err)
	}
	// Corrupt signature bytes.
	p7 = parse()
	p7.Signers[0].EncryptedDigest[0] ^= 0xff
	if err := verifyTolerant(p7, cert, content); !errors.Is(err, ErrSignature) {
		t.Errorf("bad signature: %v", err)
	}
	// Digest mismatch.
	if err := verifyTolerant(parse(), cert, []byte("other")); !errors.Is(err, ErrSignature) {
		t.Errorf("digest mismatch: %v", err)
	}
}

func TestAlgorithmTables(t *testing.T) {
	t.Parallel()
	for _, oid := range []asn1.ObjectIdentifier{pkcs7.OIDDigestAlgorithmSHA1, pkcs7.OIDDigestAlgorithmSHA256, pkcs7.OIDDigestAlgorithmSHA384, pkcs7.OIDDigestAlgorithmSHA512} {
		if _, err := hashFor(oid); err != nil {
			t.Errorf("hashFor(%v): %v", oid, err)
		}
	}
	if _, err := hashFor(asn1.ObjectIdentifier{9, 9}); !errors.Is(err, ErrAlgorithm) {
		t.Error("hashFor unknown")
	}
	rsaOIDs := []asn1.ObjectIdentifier{pkcs7.OIDEncryptionAlgorithmRSA, pkcs7.OIDEncryptionAlgorithmRSASHA1, pkcs7.OIDEncryptionAlgorithmRSASHA256, pkcs7.OIDEncryptionAlgorithmRSASHA384, pkcs7.OIDEncryptionAlgorithmRSASHA512}
	for _, oid := range rsaOIDs {
		if alg, err := signatureAlgorithm(oid, crypto.SHA256); err != nil || alg != x509.SHA256WithRSA {
			t.Errorf("rsa %v: %v %v", oid, alg, err)
		}
	}
	ecOIDs := []asn1.ObjectIdentifier{pkcs7.OIDEncryptionAlgorithmECDSAP256, pkcs7.OIDEncryptionAlgorithmECDSAP384, pkcs7.OIDEncryptionAlgorithmECDSAP521, pkcs7.OIDDigestAlgorithmECDSASHA1, pkcs7.OIDDigestAlgorithmECDSASHA256, pkcs7.OIDDigestAlgorithmECDSASHA384, pkcs7.OIDDigestAlgorithmECDSASHA512}
	for _, oid := range ecOIDs {
		if alg, err := signatureAlgorithm(oid, crypto.SHA512); err != nil || alg != x509.ECDSAWithSHA512 {
			t.Errorf("ecdsa %v: %v %v", oid, alg, err)
		}
	}
	if _, err := signatureAlgorithm(asn1.ObjectIdentifier{9, 9}, crypto.SHA256); !errors.Is(err, ErrAlgorithm) {
		t.Error("signatureAlgorithm unknown")
	}
	if !withinSkew(&pkcs7.SigningTimeNotValidError{SigningTime: time.Unix(90, 0), NotBefore: time.Unix(100, 0), NotAfter: time.Unix(200, 0)}, 20*time.Second) {
		t.Error("withinSkew before")
	}
	if withinSkew(&pkcs7.SigningTimeNotValidError{SigningTime: time.Unix(300, 0), NotBefore: time.Unix(100, 0), NotAfter: time.Unix(200, 0)}, 20*time.Second) {
		t.Error("withinSkew after")
	}
}

// failingSigner reports an RSA public key but cannot sign.
type failingSigner struct{ pub crypto.PublicKey }

func (f failingSigner) Public() crypto.PublicKey { return f.pub }
func (failingSigner) Sign(_ io.Reader, _ []byte, _ crypto.SignerOpts) ([]byte, error) {
	return nil, errors.New("hsm unavailable")
}

func TestSignerFailure(t *testing.T) {
	t.Parallel()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	cert := selfSigned(t, key, time.Now().Add(-time.Minute))
	if _, err := Sign([]byte("x"), cert, failingSigner{pub: &key.PublicKey}); !errors.Is(err, ErrSign) {
		t.Errorf("failing signer: %v", err)
	}
}

func TestSignUnsupportedKey(t *testing.T) {
	t.Parallel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	if _, err := Sign([]byte("x"), cert, priv); !errors.Is(err, ErrAlgorithm) {
		t.Errorf("ed25519: %v", err)
	}
	if (VerifyOptions{}).now().IsZero() {
		t.Error("default now")
	}
}
