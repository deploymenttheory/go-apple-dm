package pushcert_test

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func TestConfiguredSHA1RootDoesNotRequireSelfSignatureValidation(t *testing.T) {
	root, err := testpki.NewCA("legacy trust anchor")
	if err != nil {
		t.Fatal(err)
	}
	template := *root.Cert
	template.SignatureAlgorithm = x509.SHA1WithRSA
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, root.Key.Public(), root.Key)
	if err != nil {
		t.Fatal(err)
	}
	root.Cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	intermediate, err := testpki.NewCA("intermediate")
	if err != nil {
		t.Fatal(err)
	}
	der, err = x509.CreateCertificate(rand.Reader, intermediate.Cert, root.Cert, intermediate.Key.Public(), root.Key)
	if err != nil {
		t.Fatal(err)
	}
	intermediate.Cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	vendor, err := intermediate.IssuePush("com.apple.mgmt.vendor", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	leaf, key, err := vendor.PEM()
	if err != nil {
		t.Fatal(err)
	}
	chain := append(append(leaf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediate.Cert.Raw})...), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Cert.Raw})...)
	_, csr, err := pushcert.GenerateCSR(pkix.Name{CommonName: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pushcert.SignCSR(csr, chain, key, root.Pool(), time.Now()); err != nil {
		t.Fatal(err)
	}
	untrusted, err := testpki.NewCA("untrusted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pushcert.SignCSR(csr, chain, key, untrusted.Pool(), time.Now()); err == nil {
		t.Fatal("untrusted legacy root accepted")
	}
	// SHA-1 on an issued intermediate must still be rejected.
	weak := *intermediate.Cert
	weak.SignatureAlgorithm = x509.SHA1WithRSA
	der, err = x509.CreateCertificate(rand.Reader, &weak, root.Cert, intermediate.Key.Public(), root.Key)
	if err != nil {
		t.Fatal(err)
	}
	chain = append(append(append([]byte(nil), leaf...), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Cert.Raw})...)
	if _, err = pushcert.SignCSR(csr, chain, key, root.Pool(), time.Now()); err == nil {
		t.Fatal("SHA-1 intermediate accepted")
	}
}
