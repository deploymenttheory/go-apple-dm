package profile_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/cms"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	"github.com/deploymenttheory/go-apple-mdm/profile"
	"github.com/deploymenttheory/go-apple-mdm/schema/profiles"
	"github.com/deploymenttheory/go-apple-mdm/schema/support"
)

func sample() *profile.Profile {
	return &profile.Profile{
		Identifier: "com.example.test", UUID: "6C9B0C20-0000-7000-8000-000000000001",
		DisplayName: "Test", Organization: "Example", Scope: profile.ScopeSystem, RemovalDisallowed: true,
		Extra: map[string]any{"ConsentText": map[string]any{"default": "ok"}, "PayloadType": "ignored"},
		Payloads: []profile.Payload{
			{
				Identifier: "com.example.test.mdm", UUID: "6C9B0C20-0000-7000-8000-000000000002", DisplayName: "MDM",
				Content: &profiles.MDM{
					IdentityCertificateUUID: "6C9B0C20-0000-7000-8000-000000000003",
					Topic:                   "com.apple.mgmt.example", ServerURL: "https://mdm.example.com/mdm",
					AccessRights: new(int64(8191)), ServerCapabilities: []string{"com.apple.mdm.token"},
				},
			},
			{
				Identifier: "com.example.test.root", UUID: "6C9B0C20-0000-7000-8000-000000000003",
				Content: &profiles.CertificateRoot{PayloadContent: []byte{0x30, 0x03, 0x01, 0x01, 0xff}},
			},
			{
				Identifier: "com.example.test.raw", UUID: "6C9B0C20-0000-7000-8000-000000000004",
				Content: &profile.Raw{Type: "com.example.custom", Keys: map[string]any{"Flag": true, "Count": int64(3)}},
			},
		},
	}
}

func TestBuildAndParse(t *testing.T) {
	t.Parallel()
	p := sample()
	if err := p.Validate(support.Target{}); err != nil {
		t.Fatal(err)
	}
	data, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if plist.DetectFormat(data) != plist.FormatXML || !strings.Contains(string(data), "<key>ConsentText</key>") {
		t.Fatalf("not XML with extras: %.100s", data)
	}
	got, err := profile.Parse(data, profile.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Signer != nil || got.Profile.Identifier != p.Identifier || got.Profile.UUID != p.UUID || !got.Profile.RemovalDisallowed || got.Profile.Scope != profile.ScopeSystem {
		t.Fatalf("envelope: %+v", got.Profile)
	}
	if got.Profile.Version != 1 || got.Profile.Extra["PayloadType"] != nil || got.Profile.Extra["ConsentText"] == nil {
		t.Fatalf("version/extra: %+v", got.Profile)
	}
	m, ok := profile.Find[*profiles.MDM](got.Profile)
	if !ok || m.Topic != "com.apple.mgmt.example" || *m.AccessRights != 8191 || m.ServerCapabilities[0] != "com.apple.mdm.token" {
		t.Fatalf("mdm payload: %+v", m)
	}
	root, ok := profile.Find[*profiles.CertificateRoot](got.Profile)
	if !ok || len(root.PayloadContent) != 5 {
		t.Fatalf("root payload: %+v", root)
	}
	raw, ok := profile.Find[*profile.Raw](got.Profile)
	if !ok || raw.Type != "com.example.custom" || raw.Keys["Flag"] != true || raw.Keys["PayloadType"] != nil {
		t.Fatalf("raw payload: %+v", raw)
	}
	if pl, ok := got.Profile.FindUUID("6c9b0c20-0000-7000-8000-000000000003"); !ok || pl.Identifier != "com.example.test.root" {
		t.Fatal("FindUUID case-insensitive")
	}
	if _, ok := got.Profile.FindUUID("nope"); ok {
		t.Fatal("FindUUID miss")
	}
	if _, ok := profile.Find[*profiles.SCEP](got.Profile); ok {
		t.Fatal("Find miss")
	}
	// Re-marshalling the parsed profile is byte-identical: stable output.
	again, err := got.Profile.Marshal()
	if err != nil || string(again) != string(data) {
		t.Fatalf("round trip differs: %v\n%s\n%s", err, data, again)
	}
}

func TestStableUUIDs(t *testing.T) {
	t.Parallel()
	p := sample()
	a, _ := p.Marshal()
	p.Payloads[0].Content.(*profiles.MDM).ServerURL = "https://other.example.com/mdm"
	b, _ := p.Marshal()
	if string(a) == string(b) {
		t.Fatal("content change not reflected")
	}
	ga, _ := profile.Parse(a, profile.ParseOptions{})
	gb, _ := profile.Parse(b, profile.ParseOptions{})
	if ga.Profile.Payloads[0].UUID != gb.Profile.Payloads[0].UUID || ga.Profile.UUID != gb.Profile.UUID {
		t.Fatal("UUIDs changed with content")
	}
	u1, u2 := profile.NewUUID(), profile.NewUUID()
	if u1 == u2 || u1 != strings.ToUpper(u1) || len(u1) != 36 {
		t.Fatalf("NewUUID %q %q", u1, u2)
	}
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()
	p := &profile.Profile{Scope: "Galaxy", Payloads: []profile.Payload{
		{Content: nil},
		{Identifier: "a", UUID: "X", Content: &profiles.MDM{}},
		{Identifier: "b", UUID: "X", Content: &profiles.CertificateRoot{}},
	}}
	err := p.Validate(support.Target{})
	if err == nil || !errors.Is(err, profile.ErrInvalid) {
		t.Fatal(err)
	}
	for _, want := range []string{"PayloadIdentifier is required", "PayloadUUID is required", `PayloadScope "Galaxy"`, "nil content", "already used", "Topic"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	// Payload with nil content fails Map and Marshal.
	if _, err := (&profile.Payload{}).Map(); !errors.Is(err, profile.ErrInvalid) {
		t.Fatal("nil content Map")
	}
	if _, err := (&profile.Profile{Payloads: []profile.Payload{{}}}).Marshal(); !errors.Is(err, profile.ErrInvalid) {
		t.Fatal("nil content Marshal")
	}
	// Unmarshalable content (a channel inside a Raw) fails Map.
	bad := &profile.Profile{Payloads: []profile.Payload{{Content: &profile.Raw{Type: "x", Keys: map[string]any{"c": make(chan int)}}}}}
	if _, err := bad.Marshal(); !errors.Is(err, profile.ErrInvalid) {
		t.Fatalf("channel: %v", err)
	}
	empty := &profile.Raw{Type: "x"}
	if m, err := (&profile.Payload{Content: empty, UUID: "u", Identifier: "i"}).Map(); err != nil || m["PayloadType"] != "x" || m["PayloadVersion"] != int64(1) {
		t.Fatalf("empty raw: %v %v", m, err)
	}
	if empty.SchemaPath() != "" || empty.Validate(support.Target{}) != nil {
		t.Fatal("raw methods")
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	for name, data := range map[string][]byte{
		"garbage":       []byte("garbage"),
		"not a profile": []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>PayloadType</key><string>Other</string></dict></plist>`),
		"content item":  []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>PayloadType</key><string>Configuration</string><key>PayloadContent</key><array><string>x</string></array></dict></plist>`),
		"missing type":  []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>PayloadType</key><string>Configuration</string><key>PayloadContent</key><array><dict><key>PayloadUUID</key><string>x</string></dict></array></dict></plist>`),
		"wrong shape":   []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>PayloadType</key><string>Configuration</string><key>PayloadContent</key><array><dict><key>PayloadType</key><string>com.apple.mdm</string><key>Topic</key><array/></dict></array></dict></plist>`),
	} {
		if _, err := profile.Parse(data, profile.ParseOptions{}); !errors.Is(err, profile.ErrParse) {
			t.Errorf("%s: %v", name, err)
		}
	}
	data, _ := sample().Marshal()
	if _, err := profile.Parse(data, profile.ParseOptions{RequireSignature: true}); !errors.Is(err, profile.ErrParse) {
		t.Fatal("unsigned accepted")
	}
	if _, err := profile.Parse(data, profile.ParseOptions{MaxBytes: 10}); !errors.Is(err, profile.ErrParse) {
		t.Fatal("size limit")
	}
	// Ambiguous payload types (six share com.apple.MCX) stay raw; a custom
	// resolver can pick one.
	mcx := &profile.Profile{Identifier: "i", UUID: "u", Payloads: []profile.Payload{{Identifier: "i.mcx", UUID: "u2", Content: &profiles.TimeServer{}}}}
	mcxData, err := mcx.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := profile.Parse(mcxData, profile.ParseOptions{})
	if _, ok := got.Profile.Payloads[0].Content.(*profile.Raw); !ok {
		t.Fatalf("ambiguous type not raw: %T", got.Profile.Payloads[0].Content)
	}
	got, _ = profile.Parse(mcxData, profile.ParseOptions{Resolve: func(typ string, _ map[string]any) profiles.Payload {
		if typ == "com.apple.MCX" {
			return &profiles.TimeServer{}
		}
		return nil
	}})
	if _, ok := got.Profile.Payloads[0].Content.(*profiles.TimeServer); !ok {
		t.Fatalf("resolver ignored: %T", got.Profile.Payloads[0].Content)
	}
	if profile.DefaultResolver("no.such.type", nil) != nil {
		t.Fatal("unknown type resolved")
	}
	// Integer forms of PayloadVersion.
	for _, v := range []any{int64(2), uint64(2), 2, 2.0} {
		m := map[string]any{"PayloadType": "Configuration", "PayloadVersion": v, "PayloadIdentifier": "i", "PayloadUUID": "u"}
		d, _ := plist.Marshal(m)
		if got, err := profile.Parse(d, profile.ParseOptions{}); err != nil || got.Profile.Version != 2 {
			t.Fatalf("version %T: %v", v, err)
		}
	}
}

func TestSignAttached(t *testing.T) {
	t.Parallel()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "signer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	p := sample()
	signed, err := p.Sign(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	if !cms.IsSigned(signed) {
		t.Fatal("not CMS")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	got, err := profile.Parse(signed, profile.ParseOptions{RequireSignature: true, Verify: cms.VerifyOptions{Roots: pool}})
	if err != nil || got.Signer == nil || !got.Signer.Equal(cert) || got.Profile.Identifier != p.Identifier {
		t.Fatalf("parse signed: %v", err)
	}
	if plist.DetectFormat(got.Plist) != plist.FormatXML {
		t.Fatal("plist bytes not exposed")
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherDER, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &other.PublicKey, other)
	otherCert, _ := x509.ParseCertificate(otherDER)
	wrong := x509.NewCertPool()
	wrong.AddCert(otherCert)
	if _, err := profile.Parse(signed, profile.ParseOptions{Verify: cms.VerifyOptions{Roots: wrong}}); !errors.Is(err, profile.ErrParse) {
		t.Fatal("wrong root accepted")
	}
	if _, err := p.Sign(nil, nil); err == nil {
		t.Fatal("nil signer")
	}
	if _, err := (&profile.Profile{Payloads: []profile.Payload{{}}}).Sign(cert, key); !errors.Is(err, profile.ErrInvalid) {
		t.Fatal("invalid profile signed")
	}
}
