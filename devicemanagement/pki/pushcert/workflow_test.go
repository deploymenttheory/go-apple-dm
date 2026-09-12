package pushcert_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"howett.net/plist"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func appIdentity(t *testing.T, extension []byte) *testpki.Identity {
	t.Helper()
	ca, err := testpki.NewCA("app issuer")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.IssuePush("com.example.app", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	leaf := *id.Cert
	leaf.ExtraExtensions = []pkix.Extension{
		{Id: asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 3, 6}, Value: extension},
		{Id: asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 6, 3, 1}, Value: []byte{5, 0}},
	}
	// Names parsed from the wire must be explicitly preserved when reissuing.
	leaf.Subject.ExtraNames = leaf.Subject.Names
	der, err := x509.CreateCertificate(rand.Reader, &leaf, ca.Cert, id.Key.Public(), ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	id.Cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func appTopics(t *testing.T) []byte {
	t.Helper()
	der, err := asn1.Marshal(struct {
		Topic    string `asn1:"utf8"`
		Services []string
	}{"com.example.app", []string{"topic"}})
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestAppCertificateWorkflow(t *testing.T) {
	t.Parallel()
	id := appIdentity(t, appTopics(t))
	cert, key, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{cert, id.Cert.Raw} {
		info, err := pushcert.Inspect(data)
		if err != nil || info.MDM || info.Topic != "com.example.app" ||
			len(info.Capabilities[info.Topic]) != 1 {
			t.Fatalf("inspect: %+v %v", info, err)
		}
		p, err := pushcert.ParseApp(data, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := pushcert.Validate(p.TLS, p.Topic, false, p.NotBefore); err != nil {
			t.Fatal(err)
		}
		for _, at := range []time.Time{p.NotBefore.Add(-time.Second), p.NotAfter, p.NotAfter.Add(time.Second)} {
			if err := pushcert.Validate(p.TLS, p.Topic, false, at); err == nil {
				t.Fatal("invalid validity accepted")
			}
		}
		if err := pushcert.Validate(
			p.TLS,
			"com.example.other",
			false,
			time.Now(),
		); !errors.Is(
			err,
			pushcert.ErrNoTopic,
		) {
			t.Fatalf("wrong topic: %v", err)
		}
		if err := pushcert.Validate(
			p.TLS,
			p.Topic,
			true,
			time.Now(),
		); !errors.Is(
			err,
			pushcert.ErrNoTopic,
		) {
			t.Fatalf("app accepted as MDM: %v", err)
		}
		p.TLS.PrivateKey = nil
		if err := pushcert.Validate(p.TLS, p.Topic, false, time.Now()); err == nil {
			t.Fatal("missing key accepted")
		}
		if _, err := pushcert.Parse(data, key); !errors.Is(err, pushcert.ErrNoTopic) {
			t.Fatalf("MDM parse: %v", err)
		}
		canonical, err := pushcert.PEM(data)
		if err != nil || string(canonical) != string(cert) {
			t.Fatal("normalization")
		}
	}
	for _, ext := range [][]byte{{1, 2, 3}, {0x30, 0}, append(appTopics(t), 0), {0x30, 3, 0x0c, 1, 'x'}} {
		bad := appIdentity(t, ext)
		_, badKey, err := bad.PEM()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pushcert.ParseApp(bad.Cert.Raw, badKey); err == nil {
			t.Fatal("malformed/unauthorized accepted")
		}
		if len(ext) > 2 {
			if _, err := pushcert.Inspect(bad.Cert.Raw); err == nil {
				t.Fatal("malformed extension accepted")
			}
		}
	}
	if err := pushcert.Validate(tls.Certificate{}, "x", false, time.Now()); err == nil {
		t.Fatal("empty accepted")
	}
}

func TestVendorEnvelope(t *testing.T) {
	t.Parallel()
	key, csr, err := pushcert.GenerateCSR(pkix.Name{CommonName: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	csrBlock, _ := pem.Decode(csr)
	request, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil || request.CheckSignature() != nil ||
		request.SignatureAlgorithm != x509.SHA256WithRSA {
		t.Fatal("CSR signature")
	}
	keyBlock, _ := pem.Decode(key)
	private, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil || !request.PublicKey.(*rsa.PublicKey).Equal(private.(*rsa.PrivateKey).Public()) {
		t.Fatal("CSR key pair")
	}
	root, err := testpki.NewCA("trusted vendor root")
	if err != nil {
		t.Fatal(err)
	}
	vendorKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	intermediateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	intermediate := &x509.Certificate{
		SerialNumber:          big.NewInt(5),
		Subject:               pkix.Name{CommonName: "intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(
		rand.Reader,
		intermediate,
		root.Cert,
		intermediateKey.Public(),
		root.Key,
	)
	if err != nil {
		t.Fatal(err)
	}
	intermediate, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(6),
		Subject:      pkix.Name{CommonName: "vendor"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err = x509.CreateCertificate(
		rand.Reader,
		leaf,
		intermediate,
		vendorKey.Public(),
		intermediateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	chain := append(pemBlock("CERTIFICATE", der), pemBlock("CERTIFICATE", intermediate.Raw)...)
	chain = append(chain, pemBlock("CERTIFICATE", root.Cert.Raw)...)
	vendorPEM := pemBlock("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(vendorKey))
	for _, input := range [][]byte{csr, csrBlock.Bytes} {
		signed, err := pushcert.SignCSR(input, chain, vendorPEM, root.Pool(), now)
		if err != nil {
			t.Fatal(err)
		}
		xml, err := base64.StdEncoding.DecodeString(string(signed))
		if err != nil {
			t.Fatal(err)
		}
		var envelope map[string]string
		if _, err := plist.Unmarshal(xml, &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope) != 3 || envelope["PushCertCertificateChain"] != string(chain) {
			t.Fatal("envelope keys/chain")
		}
		raw, err := base64.StdEncoding.DecodeString(envelope["PushCertRequestCSR"])
		if err != nil || string(raw) != string(request.Raw) {
			t.Fatal("CSR changed")
		}
		sig, err := base64.StdEncoding.DecodeString(envelope["PushCertSignature"])
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		if err := rsa.VerifyPKCS1v15(
			&vendorKey.PublicKey,
			crypto.SHA256,
			digest[:],
			sig,
		); err != nil {
			t.Fatal(err)
		}
	}
	tampered := append([]byte(nil), csrBlock.Bytes...)
	tampered[len(tampered)-1] ^= 1
	for _, tc := range []struct {
		csr, chain, key []byte
		roots           *x509.CertPool
		at              time.Time
	}{
		{tampered, chain, vendorPEM, root.Pool(), now},
		{csr, append(append(pemBlock("CERTIFICATE", der), pemBlock("CERTIFICATE", root.Cert.Raw)...), pemBlock("CERTIFICATE", intermediate.Raw)...), vendorPEM, root.Pool(), now},
		{csr, pemBlock("CERTIFICATE", der), vendorPEM, root.Pool(), now},
		{csr, chain, key, root.Pool(), now},
		{csr, chain, vendorPEM, x509.NewCertPool(), now},
		{csr, chain, vendorPEM, nil, now},
		{csr, chain, vendorPEM, root.Pool(), now.Add(time.Hour)},
	} {
		if _, err := pushcert.SignCSR(tc.csr, tc.chain, tc.key, tc.roots, tc.at); err == nil {
			t.Fatal("invalid signing input accepted")
		}
	}
}
