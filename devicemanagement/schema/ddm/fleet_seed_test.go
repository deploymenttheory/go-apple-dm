//go:build schema_seed_os_27

package ddm_test

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"testing"

	protocol "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func TestSeedOS27LegacyProfileCompatibility(t *testing.T) {
	t.Parallel()
	for _, interactive := range []bool{false, true} {
		for _, version := range []int{15, 26, 27} {
			for _, asset := range []bool{false, true} {
				var payload ddm.Declaration
				url := "https://mdm.example.test/profile"
				var reference *string
				if asset {
					url = ""
					reference = new("com.example.profile")
				}
				if interactive {
					payload = &ddm.LegacyInteractiveProfile{
						ProfileURL:            url,
						ProfileAssetReference: reference,
						VisibleName:           "Managed profile",
					}
				} else {
					payload = &ddm.LegacyProfile{ProfileURL: url, ProfileAssetReference: reference}
				}
				target := support.Target{
					OS:      support.MacOS,
					Version: support.V(version, 0, 0),
					Channel: support.ChannelDevice,
				}
				allowed := !asset || version >= 27
				if err := payload.Validate(target); (err == nil) != allowed {
					t.Fatalf("version %d asset=%t: %v", version, asset, err)
				}
				for _, marshal := range []func(any) ([]byte, error){json.Marshal, func(v any) ([]byte, error) { return jsonv2.Marshal(v) }, plist.Marshal} {
					raw, err := marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					var decoded map[string]any
					if plist.DetectFormat(raw) != plist.FormatUnknown {
						err = plist.Unmarshal(raw, &decoded)
					} else {
						err = json.Unmarshal(raw, &decoded)
					}
					if err != nil {
						t.Fatal(err)
					}
					_, hasURL := decoded["ProfileURL"]
					if hasURL == asset {
						t.Fatalf("optional URL wire presence: %s", raw)
					}
				}
				raw, err := jsonv2.Marshal(
					map[string]any{
						"Type":       payload.DeclarationTypeName(),
						"Identifier": "com.example.legacy",
						"Payload":    payload,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := protocol.ParseDeclaration(raw, target); (err == nil) != allowed {
					t.Fatalf("declaration admission: %v", err)
				}
			}
		}
	}
	if err := (&ddm.LegacyProfile{}).Validate(support.Target{}); err == nil {
		t.Fatal("neither URL nor asset accepted")
	}
	if err := (&ddm.LegacyInteractiveProfile{VisibleName: "Profile"}).Validate(
		support.Target{},
	); err == nil {
		t.Fatal("neither URL nor asset accepted")
	}
}
