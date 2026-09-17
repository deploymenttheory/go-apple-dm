package inspect_test

import (
	"crypto/x509"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile/inspect"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func profileData(t *testing.T, change func(map[string]any, map[string]any)) []byte {
	t.Helper()
	payload := map[string]any{
		"PayloadType":       "com.apple.wifi.managed",
		"PayloadIdentifier": "example.wifi",
		"PayloadUUID":       "payload",
		"PayloadVersion":    int64(1),
		"SSID_STR":          "Example",
		"EncryptionType":    "WPA",
		"Password":          "DO-NOT-PRINT",
	}
	top := map[string]any{
		"PayloadType":       "Configuration",
		"PayloadIdentifier": "example",
		"PayloadUUID":       "profile",
		"PayloadVersion":    int64(1),
		"PayloadContent":    []any{payload},
	}
	if change != nil {
		change(top, payload)
	}
	data, err := plist.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInspect(t *testing.T) {
	target := support.Target{
		OS:         support.MacOS,
		Version:    support.V(26, 0, 0),
		Channel:    support.ChannelDevice,
		Supervised: true,
	}
	for _, tc := range []struct {
		name, severity, path string
		change               func(map[string]any, map[string]any)
	}{
		{name: "valid"},
		{name: "unknown", severity: "unvalidated", path: "Unknown", change: func(_, p map[string]any) { p["Unknown"] = "DO-NOT-PRINT" }},
		{name: "nested unknown", severity: "unvalidated", path: "EAPClientConfiguration.Unknown", change: func(_, p map[string]any) { p["EAPClientConfiguration"] = map[string]any{"Unknown": true} }},
		{name: "required", severity: "error", path: "PayloadIdentifier", change: func(_, p map[string]any) { delete(p, "PayloadIdentifier") }},
		{name: "enum", severity: "error", path: "EncryptionType", change: func(_, p map[string]any) { p["EncryptionType"] = "DO-NOT-PRINT" }},
		{name: "raw", severity: "unvalidated", path: "PayloadContent[0]", change: func(_, p map[string]any) { p["PayloadType"] = "com.example.future" }},
		{name: "type", severity: "error", path: "PayloadContent", change: func(_, p map[string]any) { p["Password"] = true }},
		{name: "envelope type", severity: "error", change: func(top, _ map[string]any) { top["PayloadVersion"] = true }},
		{name: "envelope identity", severity: "error", path: "PayloadUUID", change: func(top, _ map[string]any) {
			delete(top, "PayloadUUID")
			delete(top, "PayloadIdentifier")
		}},
		{name: "identity type", severity: "error", path: "PayloadContent", change: func(_, p map[string]any) { p["PayloadDisplayName"] = true }},
		{name: "encrypted", severity: "unvalidated", path: "EncryptedPayloadContent", change: func(top, _ map[string]any) {
			delete(top, "PayloadContent")
			top["EncryptedPayloadContent"] = []byte("opaque")
		}},
		{name: "duplicate", severity: "error", path: "PayloadContent[1]", change: func(top, p map[string]any) { top["PayloadContent"] = []any{p, p} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inspect.Inspect(profileData(t, tc.change), inspect.Options{Target: target})
			if tc.severity == "" && len(r.Issues) != 0 {
				t.Fatalf("valid: %+v", r)
			}
			found := false
			for _, issue := range r.Issues {
				if strings.Contains(issue.Message, "DO-NOT-PRINT") {
					t.Fatal("secret in diagnostics")
				}
				if issue.Severity == tc.severity && strings.Contains(issue.Path, tc.path) {
					found = true
				}
			}
			if tc.severity != "" && !found {
				t.Fatalf("missing %s %s: %+v", tc.severity, tc.path, r)
			}
		})
	}
	if r := inspect.Inspect(
		[]byte("broken"),
		inspect.Options{Target: target},
	); len(r.Issues) != 1 ||
		r.Issues[0].Rule != "parse" {
		t.Fatal(r)
	}
}

func TestInspectSizeLimit(t *testing.T) {
	r := inspect.Inspect(make([]byte, plist.DefaultMaxBytes+1), inspect.Options{})
	if len(r.Issues) != 1 || r.Issues[0].Rule != "size" || r.Issues[0].Severity != "error" {
		t.Fatalf("oversized profile: %+v", r)
	}
}

func TestSignatureAndTrust(t *testing.T) {
	ca, err := testpki.NewCA("lint")
	if err != nil {
		t.Fatal(err)
	}
	plain := profileData(t, nil)
	signed, err := cms.SignAttached(plain, ca.Cert, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	target := support.Target{OS: support.MacOS, Version: support.V(26, 0, 0)}
	for _, tc := range []struct {
		roots *x509.CertPool
		trust string
	}{{nil, "not-checked"}, {ca.Pool(), "trusted"}, {x509.NewCertPool(), "untrusted"}} {
		r := inspect.Inspect(signed, inspect.Options{Target: target, Roots: tc.roots})
		if r.Signature != "valid" || r.Trust != tc.trust {
			t.Fatal(r)
		}
	}
	if r := inspect.Inspect(
		plain,
		inspect.Options{Target: target, RequireSignature: true},
	); len(r.Issues) != 1 ||
		r.Issues[0].Rule != "signature" {
		t.Fatal(r)
	}
	pos := strings.Index(string(signed), "Example")
	if pos < 0 {
		t.Fatal("no embedded content")
	}
	signed[pos] = 'X'
	if r := inspect.Inspect(
		signed,
		inspect.Options{Target: target},
	); r.Signature != "invalid" {
		t.Fatal(r)
	}
}

func TestTargetAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, severity string
		target         support.Target
		change         func(map[string]any, map[string]any)
	}{
		{name: "unsupported platform field", severity: "error", target: support.Target{OS: support.MacOS, Version: support.V(26, 0, 0)}, change: func(_, p map[string]any) { p["CaptiveBypass"] = false }},
		{name: "removed nested field", severity: "error", target: support.Target{OS: support.IOS, Version: support.V(26, 0, 0)}, change: func(_, p map[string]any) {
			p["EAPClientConfiguration"] = map[string]any{"TLSAllowTrustExceptions": false}
		}},
		{name: "deprecated nested field", severity: "warning", target: support.Target{OS: support.MacOS, Version: support.V(26, 0, 0)}, change: func(_, p map[string]any) {
			p["QoSMarkingPolicy"] = map[string]any{"QoSMarkingWhitelistedAppIdentifiers": []any{"com.example.test"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := inspect.Inspect(
				profileData(t, tc.change),
				inspect.Options{Target: tc.target},
			)
			for _, issue := range report.Issues {
				if issue.Severity == tc.severity &&
					(issue.Rule == "support" || issue.Rule == "deprecated") {
					return
				}
			}
			t.Fatalf("missing availability finding: %+v", report)
		})
	}
}

func TestUnknownKeyInTypedDictionary(t *testing.T) {
	data := profileData(t, func(_, p map[string]any) {
		for key := range p {
			if !strings.HasPrefix(key, "Payload") {
				delete(p, key)
			}
		}
		p["PayloadType"] = "com.apple.ManagedClient.preferences"
		p["PayloadContent"] = map[string]any{
			"com.example.test": map[string]any{
				"Unknown": "DO-NOT-PRINT",
				"Forced": []any{
					map[string]any{"mcx_preference_settings": map[string]any{"arbitrary": true}},
				},
			},
		}
	})
	report := inspect.Inspect(
		data,
		inspect.Options{
			Target: support.Target{OS: support.MacOS, Version: support.V(26, 0, 0)},
		},
	)
	if len(report.Issues) != 1 || report.Issues[0].Severity != "unvalidated" ||
		!strings.HasSuffix(report.Issues[0].Path, "com.example.test.Unknown") {
		t.Fatalf("typed dictionary lost unknown input: %+v", report)
	}
}
