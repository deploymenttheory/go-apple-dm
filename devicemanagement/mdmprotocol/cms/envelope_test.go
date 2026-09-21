package cms_test

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
)

// fixtureRecipient loads an envelope recipient certificate and private key from testdata.
func fixtureRecipient(t *testing.T, name string) (*x509.Certificate, crypto.Decrypter) {
	t.Helper()
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	data, err := os.ReadFile("testdata/envelopes/" + name + ".cert.pem")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(data)
	cert, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	data, err = os.ReadFile("testdata/envelopes/" + name + ".key.pem")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = pem.Decode(data)
	key, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert, requireType[crypto.Decrypter](t, key)
}

// TestDecryptEnvelope checks decryption of the envelope fixtures with their recipient identities.
func TestDecryptEnvelope(t *testing.T) {
	cert, key := fixtureRecipient(t, "recipient")
	other, otherKey := fixtureRecipient(t, "other")
	for _, name := range []string{"aes256.der", "aes128.ber", "des3.der", "oaep.der"} {
		t.Run(name, func(t *testing.T) {
			// #nosec G304 -- The test controls this fixture path within its private workspace.
			data, err := os.ReadFile("testdata/envelopes/" + name)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := cms.DecryptEnvelope(data, cert, key)
			if err != nil || string(plain) != "AAAA-BBBB-CCCC-DDDD-EEEE-FFFF" {
				t.Fatalf("decrypt: %v", err)
			}
			if _, err := cms.DecryptEnvelope(data, cert, otherKey); !errors.Is(err, cms.ErrRecipient) {
				t.Fatal(err)
			}
			if name == "oaep.der" {
				if _, err := cms.DecryptEnvelope(data, other, otherKey); !errors.Is(err, cms.ErrDecrypt) {
					t.Fatal(err)
				}
			}
			if _, err := cms.DecryptEnvelope(data[:len(data)/2], cert, key); !errors.Is(err, cms.ErrParse) {
				t.Fatal(err)
			}
		})
	}
	if _, err := cms.DecryptEnvelope(nil, cert, key); !errors.Is(err, cms.ErrParse) {
		t.Fatal(err)
	}
	if _, err := cms.DecryptEnvelope(nil, nil, nil); !errors.Is(err, cms.ErrRecipient) {
		t.Fatal(err)
	}
}
