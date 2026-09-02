package enroll_test

import (
	"crypto/x509/pkix"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/enroll"
	"github.com/deploymenttheory/go-apple-mdm/profile"
	"github.com/deploymenttheory/go-apple-mdm/schema/profiles"
)

// acmeProfile is a profile whose identity comes from ACME, as a Mac on
// Apple silicon would be given it.
func acmeProfile() enroll.Profile {
	return enroll.Profile{
		Identifier: "com.example.mdm",
		Topic:      "com.apple.mgmt.External.abc",
		ServerURL:  "https://mdm.example/mdm",
		CheckInURL: "https://mdm.example/mdm",
		ACME: &enroll.ACME{
			DirectoryURL:     "https://mdm.example/acme/directory",
			ClientIdentifier: "eyJiIjp7fX0.sig",
			KeyType:          enroll.KeyTypeEC,
			KeySize:          384,
			HardwareBound:    true,
			Attest:           true,
			Subject:          pkix.Name{CommonName: "C02X1234", Organization: []string{"Example"}},
		},
	}
}

func TestACMEProfileRoundTrip(t *testing.T) {
	p := acmeProfile()
	built, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	acme, ok := profile.Find[*profiles.ACMECertificate](built)
	if !ok {
		t.Fatal("no ACME payload")
	}
	if acme.DirectoryURL != p.ACME.DirectoryURL || acme.ClientIdentifier != p.ACME.ClientIdentifier {
		t.Fatalf("payload = %+v", acme)
	}
	if acme.Attest == nil || !*acme.Attest || !acme.HardwareBound {
		t.Fatalf("attestation not requested: %+v", acme)
	}
	// The MDM payload has to point at the ACME payload, or the device has
	// no identity to authenticate with.
	mdm, ok := profile.Find[*profiles.MDM](built)
	if !ok {
		t.Fatal("no MDM payload")
	}
	identity, ok := built.FindUUID(mdm.IdentityCertificateUUID)
	if !ok {
		t.Fatalf("IdentityCertificateUUID %q resolves to nothing", mdm.IdentityCertificateUUID)
	}
	if identity.Content.PayloadTypeName() != profiles.PayloadTypeACMECertificate {
		t.Fatalf("identity payload is %s", identity.Content.PayloadTypeName())
	}
	if !strings.HasSuffix(identity.Identifier, ".acme") {
		t.Fatalf("identity payload identifier %q", identity.Identifier)
	}
	// A device reading the profile back sees what was put in it.
	data, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := enroll.Parse(data, profile.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ACME == nil || parsed.SCEP != nil || parsed.PKCS12 != nil {
		t.Fatalf("parsed identity = %+v", parsed)
	}
	got := parsed.ACME
	if got.DirectoryURL != p.ACME.DirectoryURL || got.ClientIdentifier != p.ACME.ClientIdentifier ||
		got.KeyType != p.ACME.KeyType || got.KeySize != p.ACME.KeySize ||
		!got.HardwareBound || !got.Attest {
		t.Fatalf("round trip = %+v", got)
	}
	if got.Subject.CommonName != "C02X1234" {
		t.Fatalf("subject = %+v", got.Subject)
	}
}

func TestACMEProfileValidation(t *testing.T) {
	// Apple's rules about key type, size, hardware binding, and
	// attestation. A profile that breaks one installs and then fails on the
	// device, where the reason is much harder to see.
	cases := []struct {
		name   string
		mutate func(*enroll.ACME)
		want   string
	}{
		{"no directory", func(a *enroll.ACME) { a.DirectoryURL = "" }, "DirectoryURL is required"},
		{"http directory", func(a *enroll.ACME) { a.DirectoryURL = "http://mdm.example/acme" }, "must use https"},
		{"no client identifier", func(a *enroll.ACME) { a.ClientIdentifier = "" }, "ClientIdentifier is required"},
		{"unknown key type", func(a *enroll.ACME) { a.KeyType = "Ed25519" }, "KeyType must be"},
		{"hardware bound RSA", func(a *enroll.ACME) {
			a.KeyType, a.KeySize = enroll.KeyTypeRSA, 2048
		}, "cannot be hardware bound"},
		{"RSA too small", func(a *enroll.ACME) {
			a.KeyType, a.KeySize, a.HardwareBound, a.Attest = enroll.KeyTypeRSA, 512, false, false
		}, "multiple of 8 between 1024 and 4096"},
		{"RSA not a multiple of eight", func(a *enroll.ACME) {
			a.KeyType, a.KeySize, a.HardwareBound, a.Attest = enroll.KeyTypeRSA, 2049, false, false
		}, "multiple of 8 between 1024 and 4096"},
		{"unknown curve", func(a *enroll.ACME) { a.KeySize = 512 }, "must be 192, 256, 384, or 521"},
		{"hardware bound P-521", func(a *enroll.ACME) { a.KeySize = 521 }, "must be 256 or 384"},
		{"attest without hardware binding", func(a *enroll.ACME) {
			a.HardwareBound, a.KeySize = false, 256
		}, "Attest requires HardwareBound"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := acmeProfile()
			tc.mutate(p.ACME)
			_, err := p.Build()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Build = %v, want something about %q", err, tc.want)
			}
			if !errors.Is(err, enroll.ErrProfile) {
				t.Fatalf("error does not wrap ErrProfile: %v", err)
			}
		})
	}
	t.Run("ValidCombinations", func(t *testing.T) {
		for _, ok := range []struct {
			name   string
			mutate func(*enroll.ACME)
		}{
			{"P-256 attested", func(a *enroll.ACME) { a.KeySize = 256 }},
			{"P-521 not hardware bound", func(a *enroll.ACME) {
				a.KeySize, a.HardwareBound, a.Attest = 521, false, false
			}},
			{"RSA 2048", func(a *enroll.ACME) {
				a.KeyType, a.KeySize, a.HardwareBound, a.Attest = enroll.KeyTypeRSA, 2048, false, false
			}},
		} {
			t.Run(ok.name, func(t *testing.T) {
				p := acmeProfile()
				ok.mutate(p.ACME)
				if _, err := p.Build(); err != nil {
					t.Fatalf("Build = %v", err)
				}
			})
		}
	})
}

func TestExactlyOneIdentitySource(t *testing.T) {
	p := acmeProfile()
	p.SCEP = &enroll.SCEP{URL: "https://mdm.example/scep"}
	if _, err := p.Build(); err == nil ||
		!strings.Contains(err.Error(), "exactly one of SCEP, ACME, or PKCS12") {
		t.Fatalf("two identity sources = %v", err)
	}
	p = acmeProfile()
	p.ACME = nil
	if _, err := p.Build(); err == nil ||
		!strings.Contains(err.Error(), "exactly one of SCEP, ACME, or PKCS12") {
		t.Fatalf("no identity source = %v", err)
	}
}
