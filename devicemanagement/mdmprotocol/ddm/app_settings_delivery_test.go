package ddm_test

import (
	json "encoding/json/v2"
	"reflect"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/ddmtest"
)

// A deny-only configuration must stay deny-only through authoring, publication
// and device delivery. Inserting an allow list or changing matching qualifiers
// changes execution policy, even when the declaration remains schema-valid.
func TestAppSettingsDenyOnlyDelivery(t *testing.T) {
	t.Parallel()
	signingID, teamID := "com.example.app", "ABCDEFGHIJ"
	cdhash, path := "0123456789abcdef0123456789abcdef01234567", "/Applications/Example.app/Contents/MacOS/"
	all := "All"
	for _, tc := range []struct {
		name string
		rule schema.AppSettingsAllowedDeniedBinaries
		want string
	}{
		{
			name: "SigningIDOnly",
			rule: schema.AppSettingsAllowedDeniedBinaries{SigningID: &signingID},
			want: `{"Allowed":{"DeniedBinaries":[{"SigningID":"com.example.app"}]}}`,
		},
		{
			name: "CDHashOnly",
			rule: schema.AppSettingsAllowedDeniedBinaries{CDHash: &cdhash},
			want: `{"Allowed":{"DeniedBinaries":[{"CDHash":"0123456789abcdef0123456789abcdef01234567"}]}}`,
		},
		{
			name: "CombinedIdentifiers",
			rule: schema.AppSettingsAllowedDeniedBinaries{CDHash: &cdhash, SigningID: &signingID, TeamID: &teamID, PathPrefix: &path},
			want: `{"Allowed":{"DeniedBinaries":[{"CDHash":"0123456789abcdef0123456789abcdef01234567","SigningID":"com.example.app","TeamID":"ABCDEFGHIJ","PathPrefix":"/Applications/Example.app/Contents/MacOS/"}]}}`,
		},
		{
			name: "ExplicitSigningState",
			rule: schema.AppSettingsAllowedDeniedBinaries{SigningID: &signingID, SigningState: &all},
			want: `{"Allowed":{"DeniedBinaries":[{"SigningID":"com.example.app","SigningState":"All"}]}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			target := support.Target{OS: support.MacOS, Version: osversion.Version{Major: 27}, Channel: support.ChannelDevice, Supervised: true}
			payload := &schema.AppSettings{Allowed: &schema.AppSettingsAllowed{DeniedBinaries: []schema.AppSettingsAllowedDeniedBinaries{tc.rule}}}
			source, err := blueprint.NewDeclaration("app-settings", payload)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := blueprint.Compile(blueprint.Spec{Identifier: "binary-isolation", Declarations: []blueprint.Declaration{source}}, blueprint.Options{Target: target})
			if err != nil {
				t.Fatal(err)
			}
			h := newHarness(t)
			if _, err := h.engine.PublishSet(ctx, compiled.Publication); err != nil {
				t.Fatal(err)
			}
			device := ddmtest.Device(1)
			if _, err := h.engine.AssignSet(ctx, device, compiled.Publication.Name); err != nil {
				t.Fatal(err)
			}
			advertised, _ := items(t, h, device)
			configs := advertised.Declarations.Configurations
			identifier := compiled.Identifiers["app-settings"]
			if len(configs) != 1 || configs[0].Identifier != identifier {
				t.Fatalf("unexpected configurations: %+v", configs)
			}
			body, err := h.engine.Declaration(ctx, device, schema.KindConfiguration, identifier)
			if err != nil {
				t.Fatal(err)
			}
			served := decode(t, body)
			if !reflect.DeepEqual(served["Payload"], decode(t, []byte(tc.want))) {
				t.Fatalf("execution policy changed in delivery:\n%s\nwant payload %s", body, tc.want)
			}
			var authored map[string]any
			if err := json.Unmarshal(source.Payload, &authored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(served["Payload"], authored) {
				t.Fatal("delivered payload differs from typed authoring output")
			}
			parsed, err := ddm.ParseDeclaration(body, target)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.ServerToken != configs[0].ServerToken || served["ServerToken"] != configs[0].ServerToken {
				t.Fatal("delivered policy does not match the advertised token")
			}
		})
	}
}
