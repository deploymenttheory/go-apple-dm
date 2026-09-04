package enroll_test

import (
	"crypto/x509/pkix"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
)

func base() enroll.Profile {
	return enroll.Profile{
		Identifier: "com.example.mdm", DisplayName: "Example MDM", Organization: "Example",
		Topic: "com.apple.mgmt.example", ServerURL: "https://mdm.example.com/mdm", CheckInURL: "https://mdm.example.com/mdm",
		ServerCapabilities: []string{enroll.CapabilityBootstrapToken, enroll.CapabilityToken},
		Target:             support.Target{OS: support.OS("macOS"), Version: support.V(15, 0, 0)},
	}
}

func TestEnrollmentProfile(t *testing.T) {
	t.Parallel()
	root, _, err := ca.NewSelfSigned(ca.SelfSignedOptions{Subject: pkix.Name{CommonName: "Example Root"}})
	if err != nil {
		t.Fatal(err)
	}
	in := base()
	in.Roots = append(in.Roots, root)
	in.SCEP = &enroll.SCEP{URL: "https://mdm.example.com/scep", Name: "example", Challenge: "one-time", Subject: pkix.Name{CommonName: "UDID-1", Organization: []string{"Example"}, Country: []string{"GB"}}, Retries: 3, RetryDelay: 10, CAFingerprint: []byte{1, 2}}
	in.AccessRights = enroll.RightInspectProfiles | enroll.RightInstallProfiles | enroll.RightQueryDeviceInfo
	in.CheckOutWhenRemoved = true
	in.UseDevelopmentAPNS = true
	in.EnrollmentMode = "BYOD"
	in.AssignedManagedAppleID = "user@example.com"

	built, err := in.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Payloads) != 3 || built.Scope != profile.ScopeSystem {
		t.Fatalf("payloads %d", len(built.Payloads))
	}
	m, _ := profile.Find[*profiles.MDM](built)
	if *m.SignMessage != true || *m.AccessRights != int64(in.AccessRights) || m.IdentityCertificateUUID == "" || !*m.CheckOutWhenRemoved || !*m.UseDevelopmentAPNS {
		t.Fatalf("mdm: %+v", m)
	}
	s, _ := profile.Find[*profiles.SCEP](built)
	if *s.PayloadContent.Keysize != 2048 || *s.PayloadContent.KeyUsage != 5 || *s.PayloadContent.KeyType != "RSA" || *s.PayloadContent.Retries != 3 {
		t.Fatalf("scep: %+v", s.PayloadContent)
	}
	if id, ok := built.FindUUID(m.IdentityCertificateUUID); !ok || id.Content != s {
		t.Fatal("identity UUID does not point at the SCEP payload")
	}

	data, err := built.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Marshal(); err != nil {
		t.Fatal(err)
	}
	back, err := enroll.Parse(data, profile.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if back.Topic != in.Topic || back.ServerURL != in.ServerURL || back.CheckInURL != in.CheckInURL || back.AccessRights != in.AccessRights || !back.CheckOutWhenRemoved || !back.UseDevelopmentAPNS {
		t.Fatalf("parsed: %+v", back)
	}
	if back.SCEP == nil || back.SCEP.URL != in.SCEP.URL || back.SCEP.Challenge != "one-time" || back.SCEP.Subject.CommonName != "UDID-1" || back.SCEP.Subject.Organization[0] != "Example" || back.SCEP.KeySize != 2048 || back.SCEP.Retries != 3 || back.SCEP.RetryDelay != 10 {
		t.Fatalf("scep parsed: %+v", back.SCEP)
	}
	if len(back.Roots) != 1 || !back.Roots[0].Equal(root) || len(back.RootUUIDs) != 1 || back.MDMUUID == "" || back.IdentityUUID != m.IdentityCertificateUUID {
		t.Fatalf("roots: %+v", back)
	}
	if !reflect.DeepEqual(back.ServerCapabilities, in.ServerCapabilities) || back.EnrollmentMode != "BYOD" || back.AssignedManagedAppleID != "user@example.com" {
		t.Fatalf("caps: %+v", back)
	}
	// Rebuilding from the parsed profile with its UUIDs is byte-stable.
	again, err := back.Marshal()
	if err != nil || string(again) != string(data) {
		t.Fatalf("rebuild differs: %v", err)
	}
	if !back.AccessRights.Has(enroll.RightInspectProfiles) || back.AccessRights.Has(enroll.RightErase) {
		t.Fatal("Has")
	}
}

func TestPKCS12AndDefaults(t *testing.T) {
	t.Parallel()
	in := base()
	in.PKCS12 = &enroll.PKCS12{Data: []byte{1, 2, 3}, Password: "pw"}
	in.SignMessage = new(false)
	in.CheckInURL = ""
	built, err := in.Build()
	if err != nil {
		t.Fatal(err)
	}
	m, _ := profile.Find[*profiles.MDM](built)
	if *m.AccessRights != int64(enroll.AccessRightsAll) || *m.SignMessage || m.CheckInURL != nil {
		t.Fatalf("defaults: %+v", m)
	}
	p12, _ := profile.Find[*profiles.CertificatePKCS12](built)
	if *p12.PayloadCertificateFileName != "identity.p12" || *p12.Password != "pw" {
		t.Fatalf("pkcs12: %+v", p12)
	}
	data, _ := in.Marshal()
	back, err := enroll.Parse(data, profile.ParseOptions{})
	if err != nil || back.PKCS12 == nil || back.PKCS12.Password != "pw" || back.PKCS12.FileName != "identity.p12" || back.SCEP != nil {
		t.Fatalf("parse pkcs12: %v %+v", err, back)
	}
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]func(p *enroll.Profile){
		"missing identifier": func(p *enroll.Profile) { p.Identifier = "" },
		"bad topic":          func(p *enroll.Profile) { p.Topic = "example" },
		"no identity":        func(p *enroll.Profile) { p.SCEP = nil },
		"both identities":    func(p *enroll.Profile) { p.PKCS12 = &enroll.PKCS12{Data: []byte{1}} },
		"http server":        func(p *enroll.Profile) { p.ServerURL = "http://mdm.example.com" },
		"empty scep url":     func(p *enroll.Profile) { p.SCEP.URL = "" },
		"empty pkcs12":       func(p *enroll.Profile) { p.SCEP = nil; p.PKCS12 = &enroll.PKCS12{} },
		"schema":             func(p *enroll.Profile) { p.ServerURL = "https://x"; p.Topic = "com.apple.mgmt.x"; p.SCEP.KeySize = -1 },
	}
	for name, mutate := range cases {
		p := base()
		p.SCEP = &enroll.SCEP{URL: "https://mdm.example.com/scep"}
		mutate(&p)
		if _, err := p.Build(); !errors.Is(err, enroll.ErrProfile) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := p.Marshal(); !errors.Is(err, enroll.ErrProfile) {
			t.Errorf("%s marshal: %v", name, err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	if _, err := enroll.Parse([]byte("junk"), profile.ParseOptions{}); !errors.Is(err, enroll.ErrProfile) {
		t.Fatal("junk")
	}
	noMDM := &profile.Profile{Identifier: "i", UUID: "u", Payloads: []profile.Payload{{Identifier: "a", UUID: "b", Content: &profiles.CertificateRoot{PayloadContent: []byte{1}}}}}
	data, _ := noMDM.Marshal()
	if _, err := enroll.Parse(data, profile.ParseOptions{}); err == nil || !strings.Contains(err.Error(), "no com.apple.mdm") {
		t.Fatalf("no mdm: %v", err)
	}
	mdm := &profiles.MDM{IdentityCertificateUUID: "missing", Topic: "com.apple.mgmt.x", ServerURL: "https://x"}
	dangling := &profile.Profile{Identifier: "i", UUID: "u", Payloads: []profile.Payload{{Identifier: "a", UUID: "b", Content: mdm}}}
	data, _ = dangling.Marshal()
	if _, err := enroll.Parse(data, profile.ParseOptions{}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("dangling: %v", err)
	}
	mdm.IdentityCertificateUUID = "c"
	wrongType := &profile.Profile{Identifier: "i", UUID: "u", Payloads: []profile.Payload{
		{Identifier: "a", UUID: "b", Content: mdm},
		{Identifier: "c", UUID: "c", Content: &profiles.CertificatePEM{PayloadContent: []byte{1}}},
	}}
	data, _ = wrongType.Marshal()
	if _, err := enroll.Parse(data, profile.ParseOptions{}); err == nil || !strings.Contains(err.Error(), "has type") {
		t.Fatalf("wrong type: %v", err)
	}
	badRoot := &profile.Profile{Identifier: "i", UUID: "u", Payloads: []profile.Payload{
		{Identifier: "a", UUID: "b", Content: mdm},
		{Identifier: "c", UUID: "c", Content: &profiles.SCEP{PayloadContent: profiles.SCEPPayloadContent{URL: "https://s"}}},
		{Identifier: "r", UUID: "r", Content: &profiles.CertificateRoot{PayloadContent: []byte("not a cert")}},
	}}
	data, _ = badRoot.Marshal()
	if _, err := enroll.Parse(data, profile.ParseOptions{}); err == nil || !strings.Contains(err.Error(), "root payload") {
		t.Fatalf("bad root: %v", err)
	}
}

func TestSubjectConversion(t *testing.T) {
	t.Parallel()
	n := pkix.Name{Country: []string{"GB"}, Organization: []string{"Ex"}, OrganizationalUnit: []string{"IT"}, Locality: []string{"Cardiff"}, Province: []string{"Wales"}, CommonName: "cn"}
	s := enroll.SubjectFromName(n)
	if len(s) != 6 || s[5][0][0] != "CN" {
		t.Fatalf("%v", s)
	}
	if got := enroll.NameFromSubject(s); !reflect.DeepEqual(got, n) {
		t.Fatalf("%+v != %+v", got, n)
	}
	if got := enroll.NameFromSubject([][][]string{{{"junk"}}, {{"cn", "lower"}}}); got.CommonName != "lower" {
		t.Fatalf("%+v", got)
	}
	if enroll.SubjectFromName(pkix.Name{}) != nil {
		t.Fatal("empty name")
	}
}
