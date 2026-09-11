package enroll_test

import (
	"crypto/x509/pkix"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

func TestEnrollmentErrorsAreNotCacheable(t *testing.T) {
	for _, handler := range []http.Handler{(&enroll.OTAService{}).Handler()} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(
			w,
			httptest.NewRequest(
				http.MethodPost,
				"https://mdm.example/enroll",
				strings.NewReader("invalid"),
			),
		)
		if w.Code < 400 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable enrollment error", w.Code, w.Header())
		}
	}
}

func TestAppleACMEHardwareMatrix(t *testing.T) {
	for _, tc := range []struct {
		version              string
		hardware             enroll.MacHardware
		bound, attest, valid bool
	}{
		{"13.1", enroll.MacAppleSilicon, false, false, true},
		{"13.6", enroll.MacAppleSilicon, true, true, false},
		{"14", enroll.MacAppleSilicon, true, true, true},
		{"14", enroll.MacT2, true, false, true},
		{"14", enroll.MacT2, true, true, false},
		{"14", enroll.MacIntel, true, false, false},
		{"14", enroll.MacIntel, false, false, true},
		{"14", "typo", false, false, false},
	} {
		t.Run(
			tc.version+"/"+string(
				tc.hardware,
			)+"/"+strings.Repeat(
				"b",
				btoi(tc.bound),
			)+strings.Repeat(
				"a",
				btoi(tc.attest),
			),
			func(t *testing.T) {
				p := base()
				p.Target.Version = support.MustVersion(tc.version)
				p.MacHardware = tc.hardware
				p.ACME = &enroll.ACME{
					DirectoryURL:     "https://mdm.example/acme",
					ClientIdentifier: "one-device-code",
					Subject:          pkix.Name{CommonName: "device"},
					KeyType:          enroll.KeyTypeEC,
					KeySize:          384,
					HardwareBound:    tc.bound,
					Attest:           tc.attest,
				}
				built, err := p.Build()
				if (err == nil) != tc.valid {
					t.Fatalf("Build: %v", err)
				}
				if err != nil {
					return
				}
				a, _ := profile.Find[*profiles.ACMECertificate](built)
				if a.Attest == nil || *a.Attest != tc.attest || a.KeyIsExtractable == nil ||
					*a.KeyIsExtractable ||
					a.AllowAllAppsAccess == nil ||
					*a.AllowAllAppsAccess {
					t.Fatalf("unsafe or omitted flags: %+v", a)
				}
			},
		)
	}
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestIdentityKeyDefaultsAndOverrides(t *testing.T) {
	for _, identity := range []string{"scep", "pkcs12"} {
		for _, version := range []string{"10.11", "10.13.4", "10.15", "26"} {
			for _, override := range []bool{false, true} {
				p := base()
				p.Target.Version = support.MustVersion(version)
				var value *bool
				if override {
					value = new(true)
				}
				if identity == "scep" {
					p.SCEP = &enroll.SCEP{URL: "https://mdm.example/scep", KeyIsExtractable: value}
				} else {
					p.PKCS12 = &enroll.PKCS12{Data: []byte("identity"), KeyIsExtractable: value}
				}
				built, err := p.Build()
				minimum := support.V(10, 13, 4)
				if identity == "pkcs12" {
					minimum = support.V(10, 15, 0)
				}
				if override && p.Target.Version.Compare(minimum) < 0 {
					if err == nil {
						t.Fatal("unsupported override accepted")
					}
					continue
				}
				if err != nil {
					t.Fatal(identity, version, err)
				}
				var extract *bool
				if identity == "scep" {
					s, _ := profile.Find[*profiles.SCEP](built)
					extract = s.PayloadContent.KeyIsExtractable
					if *s.PayloadContent.Keysize != 2048 || *s.PayloadContent.KeyType != "RSA" {
						t.Fatal("Apple SCEP key defaults changed")
					}
				} else {
					s, _ := profile.Find[*profiles.CertificatePKCS12](built)
					extract = s.KeyIsExtractable
				}
				if p.Target.Version.Compare(minimum) >= 0 &&
					(extract == nil || *extract != override) {
					t.Fatal(identity, version, "incorrect extractability")
				}
			}
		}
	}
}
