package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/clock"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/state"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/testpki"
)

func TestCredentialPlatformConformance(t *testing.T) {
	for _, tc := range []struct {
		name, product, version string
		unattested             bool
		status                 int
		hardware               enroll.MacHardware
		bound, attest          bool
	}{
		{"iPad", "iPad14,1", "26.0", false, 200, "", true, true},
		{"AppleSilicon", "Mac16,1", "14.0", false, 200, enroll.MacAppleSilicon, true, true},
		{"AppleSiliconObserved", "Mac16,1", "14.0", false, 200, enroll.MacAppleSilicon, true, true},
		{"T2", "MacBookPro15,1", "14.0", false, 200, enroll.MacT2, true, false},
		{"Intel", "MacBookPro12,1", "14.0", false, 200, enroll.MacIntel, false, false},
		{"OldMac", "Mac16,1", "13.6", false, 403, enroll.MacAppleSilicon, false, false},
		{"InvalidHardware", "Mac16,1", "14.0", false, 500, "bad", false, false},
		{"MacDefaultPolicy", "Mac16,1", "26.0", false, 200, "", false, false},
		{"MacExplicitUnattested", "Mac16,1", "26.0", true, 200, "", false, false},
		{"UnknownPlatform", "unknown", "26.0", true, 403, "", false, false},
		{"MissingVersion", "iPad14,1", "", false, 403, "", false, false},
		{"UnsupportedVersion", "iPad14,1", "16.0", false, 403, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, id := replacementSecurityApp(t)
			authority, err := testpki.NewCA("credential-test")
			if err != nil {
				t.Fatal(err)
			}
			identity, err := authority.Issue("credential-device", time.Now().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			cert := identity.Cert
			a.revocations = nil // Isolate document generation from the registry middleware.
			store := inmem.New()
			err = store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{
				ID:       id,
				Enabled:  true,
				CertHash: cms.Fingerprint(cert),
				Capabilities: func() storage.Capabilities {
					if tc.name == "AppleSiliconObserved" {
						return storage.Capabilities{AppleSilicon: storage.CapabilityTrue}
					}
					return storage.Capabilities{}
				}(),
				Device: storage.DeviceInfo{
					SerialNumber: "serial",
					ProductName:  tc.product,
					OSVersion:    tc.version,
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			a.Store = store
			a.enroll.acme.cfg.AllowUnattested = tc.unattested
			a.enroll.acme.cfg.MacHardware = func(context.Context, acme.Binding) (enroll.MacHardware, error) {
				if tc.name == "AppleSiliconObserved" {
					return "", errors.New("inventory must not be needed")
				}
				return tc.hardware, nil
			}
			r := httptest.NewRequest(http.MethodGet, "https://mdm.example/credential", nil)
			r = r.WithContext(httpapi.WithCert(t.Context(), cert))
			w := httptest.NewRecorder()
			a.enroll.acme.credentialHandler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status: %d %s", w.Code, w.Body.String())
			}
			if w.Code != 200 {
				return
			}
			var credential ddm.ACMECredential
			if err := json.Unmarshal(w.Body.Bytes(), &credential); err != nil {
				t.Fatal(err)
			}
			target := support.Target{
				OS:      support.OSFromProduct(tc.product),
				Version: support.MustVersion(tc.version),
			}
			if err := credential.Validate(target); err != nil {
				t.Fatal(err)
			}
			if len(credential.Subject) == 0 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing Subject or cache protection")
			}
			attest := credential.Attest != nil && *credential.Attest
			if attest != tc.attest || credential.HardwareBound != tc.bound {
				t.Fatal("incorrect hardware flags", credential)
			}
			record, err := a.protocol.Get(
				t.Context(),
				credentialGrantKey(credential.ClientIdentifier),
			)
			if err != nil {
				t.Fatal(err)
			}
			var grant credentialGrant
			if err := json.Unmarshal(record.Value, &grant); err != nil {
				t.Fatal(err)
			}
			if grant.RequireAttestation != tc.attest {
				t.Fatal("attestation expectation missing from grant")
			}
			if tc.attest {
				d := &acme.Decision{
					Binding:    acme.Binding{EnrollmentID: id.ID, Serial: "serial"},
					Identifier: acme.Identifier{Value: credential.ClientIdentifier},
				}
				if err := a.enroll.acme.authorizeCredential(
					t.Context(),
					d,
					false,
				); !errors.Is(
					err,
					acme.ErrUnauthorized,
				) {
					t.Fatal("grant downgraded", err)
				}
			}
		})
	}
}

func TestCredentialGrantTracksEnrollmentAndIdentity(t *testing.T) {
	root, rootKey, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	authority := &testpki.CA{Identity: testpki.Identity{Cert: root, Key: rootKey}}
	if err != nil {
		t.Fatal(err)
	}
	identity, err := authority.Issue("device", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "missing", "wrong-enrollment", "wrong-serial", "not-mac", "expired", "revoked", "replaced", "disabled", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			a, id := replacementSecurityApp(t)
			fake := clock.NewFake(time.Now())
			a.cfg.Clock = fake
			reg, err := revocation.New(state.NewMemory(), revocation.Issuer{
				Certificate: authority.Cert, Signer: authority.Key,
				CRLTTL: time.Hour, CRLRefresh: time.Minute, OCSPTTL: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			reg.Now = fake.Now
			issuer := cms.Fingerprint(authority.Cert)
			if err := reg.Register(
				t.Context(),
				issuer,
				identity.Cert,
				revocation.Provenance{},
			); err != nil {
				t.Fatal(err)
			}
			a.revocations = reg
			record := storage.EnrollmentExport{Enrollment: storage.Enrollment{
				ID:       id,
				Enabled:  true,
				CertHash: cms.Fingerprint(identity.Cert),
				Device: storage.DeviceInfo{
					SerialNumber: "serial",
					ProductName:  "Mac16,1",
					OSVersion:    "26.0",
				},
			}}
			if mode == "not-mac" {
				record.Device.ProductName = "iPad14,1"
			}
			a.Store = inmem.New()
			if err := a.Store.Import(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			svc := a.enroll.acme
			identifier := "synthetic-credential-code"
			if err := svc.recordCredentialGrant(
				t.Context(),
				identifier,
				id,
				identity.Cert,
				false,
			); err != nil {
				t.Fatal(err)
			}
			decision := &acme.Decision{
				Binding:    acme.Binding{EnrollmentID: id.ID, Serial: "serial"},
				Identifier: acme.Identifier{Type: acme.IdentifierPermanent, Value: identifier},
			}
			switch mode {
			case "missing":
				decision.Identifier.Value = "another-code"
			case "wrong-enrollment":
				decision.Binding.EnrollmentID = "another-enrollment"
			case "wrong-serial":
				decision.Binding.Serial = "another-serial"
			case "expired":
				fake.Advance(credentialGrantTTL + time.Second)
			case "revoked":
				if err := reg.Revoke(
					t.Context(),
					issuer,
					identity.Cert.SerialNumber,
					1,
				); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				a.Store = inmem.New()
				record.CertHash = "replacement-certificate"
				if err := a.Store.Import(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if err := a.Store.Disable(t.Context(), id, fake.Now()); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				a.Store = inmem.New()
			}
			err = svc.authorizeCredential(t.Context(), decision, true)
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, acme.ErrUnauthorized) {
				t.Fatalf("unsafe grant %s: %v", mode, err)
			}
		})
	}
}

func TestDeviceBindingSeparatesMDMAndAttestation(t *testing.T) {
	mac := deviceBinding("mdm-udid", "serial", "Mac16,1", "subject")
	if mac.MDMUDID != "mdm-udid" || mac.UDID != "" || mac.Serial != "serial" {
		t.Fatalf("Mac binding: %+v", mac)
	}
	mac.UDID = "known-provisioning-udid"
	if mac.EnrollmentUDID() != "mdm-udid" {
		t.Fatal("attestation identifier replaced MDM identity")
	}
	ipad := deviceBinding("ipad-udid", "serial", "iPad14,1", "subject")
	if ipad.UDID != ipad.MDMUDID {
		t.Fatal("iPad hardware binding lost")
	}
	legacy := acme.Binding{UDID: "legacy"}
	if legacy.EnrollmentUDID() != "legacy" {
		t.Fatal("legacy binding changed")
	}
	if profileMetadataKey(
		mac,
		"profile",
	) != profileMetadataKey(
		acme.Binding{UDID: "mdm-udid"},
		"profile",
	) {
		t.Fatal("existing profile metadata key changed")
	}
	a, _ := replacementSecurityApp(t)
	if _, err := a.enroll.acme.acmePayload(
		deviceBinding("mdm", "", "Mac16,1", "subject"),
		"https://mdm.example/acme",
	); err == nil {
		t.Fatal("Mac without a hardware binding accepted")
	}
}
