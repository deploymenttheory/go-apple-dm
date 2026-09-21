package pushcert_test

import (
	"crypto/x509/pkix"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

// TestVendorPortalRejectsNonRSAIdentity checks that vendor portal rejects non RSA identity.
func TestVendorPortalRejectsNonRSAIdentity(t *testing.T) {
	ca, err := testpki.NewCA("vendor test authority")
	if err != nil {
		t.Fatal(err)
	}
	vendor, err := ca.Issue("ECDSA vendor", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	certificate, key, err := vendor.PEM()
	if err != nil {
		t.Fatal(err)
	}
	_, csr, err := pushcert.GenerateCSR(pkix.Name{CommonName: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pushcert.SignCSR(
		csr,
		certificate,
		key,
		ca.Pool(),
		time.Now(),
	); !errors.Is(
		err,
		pushcert.ErrInvalid,
	) {
		t.Fatal("portal accepted a non-RSA signing identity", err)
	}
}
