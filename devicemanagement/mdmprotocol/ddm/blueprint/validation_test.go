package blueprint_test

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
)

// TestCompileRejectsInvalidDeclarationAndActivationInputs checks that compile rejects invalid
// declaration and activation inputs.
func TestCompileRejectsInvalidDeclarationAndActivationInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*blueprint.Spec)
	}{
		{"profile with type", func(s *blueprint.Spec) {
			s.Declarations[0].ConfigurationProfile = &blueprint.ConfigurationProfileReference{Revision: "profile"}
		}},
		{"profile with payload", func(s *blueprint.Spec) {
			s.Declarations[0].Type = ""
			s.Declarations[0].ConfigurationProfile = &blueprint.ConfigurationProfileReference{Revision: "profile"}
		}},
		{"null payload", func(s *blueprint.Spec) { s.Declarations[0].Payload = jsontext.Value(`null`) }},
		{"array payload", func(s *blueprint.Spec) { s.Declarations[0].Payload = jsontext.Value(`[]`) }},
		{"malformed payload", func(s *blueprint.Spec) { s.Declarations[0].Payload = jsontext.Value(`{`) }},
		{"invalid activation identifier", func(s *blueprint.Spec) { s.Activations[0].Identifier = "../activation" }},
		{"empty activation", func(s *blueprint.Spec) { s.Activations[0].StandardConfigurations = nil }},
		{"duplicate activation", func(s *blueprint.Spec) { s.Activations = append(s.Activations, s.Activations[0]) }},
		{"unreferenced configuration", func(s *blueprint.Spec) {
			s.Declarations = append(s.Declarations, declaration(t, "unused", &schema.MathSettings{}))
		}},
		{"activation referencing an asset", func(s *blueprint.Spec) {
			s.Declarations[0] = blueprint.Declaration{Identifier: "math", Type: schema.DeclarationTypeAssetData, Payload: jsontext.Value(`{"Reference":{"DataURL":"https://example.test/data","ContentType":"application/xml"}}`)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := blueprint.Spec{Identifier: "validation", Declarations: []blueprint.Declaration{declaration(t, "math", &schema.MathSettings{})}, Activations: []blueprint.Activation{{Identifier: "activate", StandardConfigurations: []string{"math"}}}}
			tc.change(&spec)
			compiled, err := blueprint.Compile(spec, blueprint.Options{})
			if !errors.Is(err, ddm.ErrInvalidDeclaration) || compiled != nil {
				t.Fatalf("Compile = %v, %v; want no publication and ErrInvalidDeclaration", compiled, err)
			}
		})
	}
}

// TestManagedAppExtensionAssetReferences checks managed app extension asset references.
func TestManagedAppExtensionAssetReferences(t *testing.T) {
	payload := jsontext.Value(`{"BundleID":"com.example.app","ExtensionConfigs":{"com.example.app.extension":{"DataAssetReference":"data"},"com.example.app.second":{"DataAssetReference":"data"}}}`)
	spec := blueprint.Spec{Identifier: "extensions", Declarations: []blueprint.Declaration{
		{Identifier: "data", Type: schema.DeclarationTypeAssetData, Payload: jsontext.Value(`{"Reference":{"DataURL":"https://example.test/data","ContentType":"application/xml"}}`)},
		{Identifier: "app", Type: schema.DeclarationTypeAppManaged, Payload: payload},
	}}
	original := bytes.Clone(payload)
	compiled, err := blueprint.Compile(spec, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, raw := range compiled.Publication.Declarations {
		var envelope struct {
			Type    string
			Payload schema.AppManaged
		}
		// Decode only the managed app; other declaration payloads have different fields.
		var header struct{ Type string }
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Fatal(err)
		}
		if header.Type != schema.DeclarationTypeAppManaged {
			continue
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Payload.ExtensionConfigs) != 2 {
			t.Fatal("extension configurations lost", envelope.Payload)
		}
		for key, extension := range envelope.Payload.ExtensionConfigs {
			if extension.DataAssetReference == nil || *extension.DataAssetReference != compiled.Identifiers["data"] {
				t.Fatalf("extension %s does not reference the compiled asset: %+v", key, extension)
			}
		}
		found = true
	}
	if !found || !bytes.Equal(payload, original) {
		t.Fatal("managed app missing or caller payload mutated")
	}
	for _, raw := range []string{
		`{"ExtensionConfigs":{"com.example.app.extension":{"DataAssetReference":"missing"}}}`,
		`{"ExtensionConfigs":{"com.example.app.extension":{"DataAssetReference":12}}}`,
		`{"ExtensionConfigs":{"com.example.app.extension":12}}`,
		`{"AppConfig":12}`,
	} {
		spec.Declarations[1].Payload = jsontext.Value(raw)
		if out, err := blueprint.Compile(spec, blueprint.Options{}); !errors.Is(err, ddm.ErrInvalidDeclaration) || out != nil {
			t.Fatalf("invalid extension configuration accepted: %s: %v", raw, err)
		}
	}
	// Array reference elements must also be strings.
	spec.Declarations[1] = blueprint.Declaration{Identifier: "relay", Type: schema.DeclarationTypeNetworkRelay, Payload: jsontext.Value(`{"VisibleName":"Relay","Relays":[{"HTTP2RelayURL":"https://example.test/relay","PublicKeyData":[12]}]}`)}
	if _, err := blueprint.Compile(spec, blueprint.Options{}); !errors.Is(err, ddm.ErrInvalidDeclaration) {
		t.Fatal("non-string array asset reference accepted", err)
	}
}

// TestConfigurationProfileDescriptorValidation checks configuration profile descriptor validation.
func TestConfigurationProfileDescriptorValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*blueprint.ConfigurationProfileDescriptor)
	}{
		{"HTTP URL", func(d *blueprint.ConfigurationProfileDescriptor) { d.URL = "http://example.test/profile" }},
		{"missing host", func(d *blueprint.ConfigurationProfileDescriptor) { d.URL = "https:/profile" }},
		{"malformed URL", func(d *blueprint.ConfigurationProfileDescriptor) { d.URL = "https://%/profile" }},
		{"URL credentials", func(d *blueprint.ConfigurationProfileDescriptor) {
			d.URL = "https://user:password@example.test/profile"
		}},
		{"URL fragment", func(d *blueprint.ConfigurationProfileDescriptor) { d.URL += "#fragment" }},
		{"signed data", func(d *blueprint.ConfigurationProfileDescriptor) { d.Signed = true }},
		{"unsupported media type", func(d *blueprint.ConfigurationProfileDescriptor) { d.ContentType = "application/json" }},
		{"empty data", func(d *blueprint.ConfigurationProfileDescriptor) { d.Size = 0 }},
		{"short digest", func(d *blueprint.ConfigurationProfileDescriptor) { d.SHA256 = "abc" }},
		{"non-hex digest", func(d *blueprint.ConfigurationProfileDescriptor) { d.SHA256 = strings.Repeat("g", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := blueprint.ConfigurationProfileDescriptor{URL: "https://example.test/profile", ContentType: "application/xml", Size: 100, SHA256: strings.Repeat("a", 64)}
			tc.change(&d)
			spec := blueprint.Spec{Identifier: "profiles", Declarations: []blueprint.Declaration{{Identifier: "settings", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: "uploaded", UseProfileAssetReference: true}}}}
			if out, err := blueprint.Compile(spec, blueprint.Options{ConfigurationProfiles: map[string]blueprint.ConfigurationProfileDescriptor{"uploaded": d}}); !errors.Is(err, ddm.ErrInvalidDeclaration) || out != nil {
				t.Fatalf("Compile = %v, %v; want rejected descriptor", out, err)
			}
		})
	}
}

type unencodableDeclaration struct{ schema.MathSettings }

// MarshalJSON returns a synthetic JSON encoding failure.
func (*unencodableDeclaration) MarshalJSON() ([]byte, error) {
	return nil, errors.New("cannot encode test payload")
}

// TestNewDeclarationRejectsNilAndEncodingFailure checks that new declaration rejects nil and
// encoding failure.
func TestNewDeclarationRejectsNilAndEncodingFailure(t *testing.T) {
	for _, payload := range []schema.Declaration{nil, (*schema.MathSettings)(nil), &unencodableDeclaration{}} {
		if got, err := blueprint.NewDeclaration("math", payload); !errors.Is(err, ddm.ErrInvalidDeclaration) || got.Identifier != "" {
			t.Fatalf("NewDeclaration(%T) = %+v, %v", payload, got, err)
		}
	}
}

// TestCompiledNamespaceOwnership checks that compiled identifiers stay within the reserved
// blueprint namespace.
func TestCompiledNamespaceOwnership(t *testing.T) {
	compiled, err := blueprint.Compile(blueprint.Spec{Identifier: "names", Declarations: []blueprint.Declaration{declaration(t, "math", &schema.MathSettings{})}}, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !blueprint.OwnsSet(compiled.Publication.Name) || !blueprint.OwnsIdentifier(compiled.Identifiers["math"]) || !blueprint.OwnsIdentifier(compiled.Activations["default"]) {
		t.Fatal("compiled declarations or set escape the reserved namespace")
	}
	for _, other := range []string{"", "blueprints.test", "com.example.math", "com.deploymenttheory.blueprintx.test"} {
		if blueprint.OwnsIdentifier(other) || blueprint.OwnsSet(other) {
			t.Errorf("unrelated identifier %q is reserved", other)
		}
	}
}
