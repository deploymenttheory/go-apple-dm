package profile_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func TestProfileServiceWireAndSignature(t *testing.T) {
	p := &profile.Profile{Identifier: "service", UUID: "uuid", Service: &profile.ProfileService{URL: "https://mdm.example/ota", Challenge: "challenge", DeviceAttributes: []string{"UDID", "SERIAL"}}}
	ca, err := testpki.NewCA("profile-signer")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := p.Sign(ca.Cert, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := profile.Parse(signed, profile.ParseOptions{RequireSignature: true, Verify: cms.VerifyOptions{Roots: ca.Pool()}})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := plist.Unmarshal(parsed.Plist, &wire); err != nil {
		t.Fatal(err)
	}
	content, ok := wire["PayloadContent"].(map[string]any)
	if wire["PayloadType"] != "Profile Service" || !ok || content["Challenge"] != "challenge" {
		t.Fatalf("wrong Apple envelope: %v", wire)
	}
	if parsed.Profile.Service == nil || parsed.Profile.Service.URL != p.Service.URL {
		t.Fatal("service was not parsed")
	}
	p.Payloads = []profile.Payload{{Identifier: "mixed", UUID: "mixed", Content: &profile.Raw{Type: "com.example"}}}
	if _, err := p.Marshal(); !errors.Is(err, profile.ErrInvalid) {
		t.Fatalf("mixed profile accepted: %v", err)
	}
}

func TestProfileServiceRejectsMalformedContent(t *testing.T) {
	for _, content := range []any{
		[]any{map[string]any{"URL": "https://mdm.example"}},
		map[string]any{"URL": "http://mdm.example", "DeviceAttributes": []string{"UDID"}},
		map[string]any{"URL": "https://mdm.example", "DeviceAttributes": []any{1}},
		map[string]any{"URL": "https://mdm.example", "DeviceAttributes": []string{"UDID"}, "Challenge": 1},
	} {
		raw, err := plist.Marshal(map[string]any{"PayloadType": "Profile Service", "PayloadIdentifier": "service", "PayloadUUID": "uuid", "PayloadContent": content})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := profile.Parse(raw, profile.ParseOptions{}); !errors.Is(err, profile.ErrParse) {
			t.Fatalf("malformed service accepted: %v", err)
		}
	}
}
