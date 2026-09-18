package blueprint_test

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func declaration(t *testing.T, key string, payload schema.Declaration) blueprint.Declaration {
	t.Helper()
	c, err := blueprint.NewDeclaration(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestArrayReferencesAndTargetAvailability(t *testing.T) {
	spec := blueprint.Spec{Identifier: "relay", Declarations: []blueprint.Declaration{
		{Identifier: "key", Type: schema.DeclarationTypeAssetData, Payload: jsontext.Value(`{"Reference":{"DataURL":"https://example.test/key","ContentType":"application/octet-stream"}}`)},
		{Identifier: "relay", Type: schema.DeclarationTypeNetworkRelay, Payload: jsontext.Value(`{"VisibleName":"Relay","Relays":[{"HTTP2RelayURL":"https://relay.example.test/","PublicKeyData":["key"]}]}`)},
	}}
	out, err := blueprint.Compile(spec, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range out.Publication.Declarations {
		var d struct {
			Type    string
			Payload struct {
				Relays []struct{ PublicKeyData []string }
			}
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		if d.Type == schema.DeclarationTypeNetworkRelay && d.Payload.Relays[0].PublicKeyData[0] != out.Identifiers["key"] {
			t.Fatal("array reference was not resolved", string(raw))
		}
	}
	spec.Declarations[0].Identifier = "missing"
	if _, err := blueprint.Compile(spec, blueprint.Options{}); err == nil {
		t.Fatal("missing array reference accepted")
	}
	spec = blueprint.Spec{Identifier: "profile", Declarations: []blueprint.Declaration{{Identifier: "profile", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: "p", UseProfileAssetReference: true}}}}
	opts := blueprint.Options{ConfigurationProfiles: map[string]blueprint.ConfigurationProfileDescriptor{"p": {URL: "https://mdm.example/profile", ContentType: "application/xml", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 20}}, Target: support.Target{OS: support.MacOS, Version: osversion.Version{Major: 26}, Channel: support.ChannelDevice, Supervised: true}}
	if _, err := blueprint.Compile(spec, opts); err == nil {
		t.Fatal("OS 26 asset delivery accepted")
	}
	opts.Target.Version.Major = 27
	if _, err := blueprint.Compile(spec, opts); err != nil {
		t.Fatal("OS 27 asset delivery", err)
	}
}

func TestCompile(t *testing.T) {
	spec := blueprint.Spec{Identifier: "engineering", Declarations: []blueprint.Declaration{
		declaration(t, "disk", &schema.DiskManagementSettings{}),
		declaration(t, "math", &schema.MathSettings{}),
		declaration(t, "profile", &schema.LegacyProfile{ProfileURL: "https://example.test/profile"}),
	}}
	first, err := blueprint.Compile(spec, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Publication.Declarations) != 4 || len(first.Activations) != 1 {
		t.Fatalf("unexpected compilation: %+v", first)
	}
	slices.Reverse(spec.Declarations)
	second, err := blueprint.Compile(spec, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first, json.Deterministic(true))
	b, _ := json.Marshal(second, json.Deterministic(true))
	if !bytes.Equal(a, b) {
		t.Fatal("declaration ordering changed compilation")
	}
	spec.Identifier = "marketing"
	other, err := blueprint.Compile(spec, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if other.Identifiers["disk"] == first.Identifiers["disk"] {
		t.Fatal("Blueprints share identifiers")
	}
	spec.Identifier = "engineering"
	spec.Activations = []blueprint.Activation{{Identifier: "one", StandardConfigurations: []string{"disk", "math"}, Predicate: "@property(team) == 'engineering'"}, {Identifier: "two", StandardConfigurations: []string{"profile", "math"}}}
	if _, err := blueprint.Compile(spec, blueprint.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestReferencesAndProfiles(t *testing.T) {
	spec := blueprint.Spec{Identifier: "test", Declarations: []blueprint.Declaration{
		{Identifier: "data", Type: schema.DeclarationTypeAssetData, Payload: jsontext.Value(`{"Reference":{"DataURL":"https://example.test/profile","ContentType":"application/xml"}}`)},
		{Identifier: "profile", Type: schema.DeclarationTypeLegacyProfile, Payload: jsontext.Value(`{"ProfileAssetReference":"data"}`)},
	}}
	compiled, err := blueprint.Compile(spec, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(compiled.Publication.Declarations)
	if !bytes.Contains(encoded, []byte(compiled.Identifiers["data"])) {
		t.Fatal("missing rewritten asset")
	}
	if string(spec.Declarations[1].Payload) != `{"ProfileAssetReference":"data"}` {
		t.Fatal("input mutated")
	}
	spec.Declarations[0].Identifier = "missing"
	if _, err := blueprint.Compile(spec, blueprint.Options{}); err == nil {
		t.Fatal("dangling asset accepted")
	}
	spec.Declarations = []blueprint.Declaration{{Identifier: "profile", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: "file", UseProfileAssetReference: true}}}
	opts := blueprint.Options{ConfigurationProfiles: map[string]blueprint.ConfigurationProfileDescriptor{"file": {URL: "https://example.test/profile", ContentType: "application/xml", Size: 10, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	compiled, err = blueprint.Compile(spec, opts)
	if err != nil || len(compiled.Publication.Declarations) != 3 {
		t.Fatalf("profile asset: %+v %v", compiled, err)
	}
	d := opts.ConfigurationProfiles["file"]
	d.Signed = true
	opts.ConfigurationProfiles["file"] = d
	if _, err := blueprint.Compile(spec, opts); err == nil {
		t.Fatal("signed plist asset accepted")
	}
	spec.Declarations[0].ConfigurationProfile.UseProfileAssetReference = false
	if _, err := blueprint.Compile(spec, opts); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidInputs(t *testing.T) {
	base := declaration(t, "cfg", &schema.MathSettings{})
	for _, spec := range []blueprint.Spec{
		{},
		{Identifier: "../bad"},
		{Identifier: "x", Declarations: []blueprint.Declaration{base, base}},
		{Identifier: "x", Declarations: []blueprint.Declaration{{Identifier: "x", Type: "com.example.future"}}},
		{Identifier: "x", Declarations: []blueprint.Declaration{base}, Activations: []blueprint.Activation{{Identifier: "act", StandardConfigurations: []string{"absent"}}}},
		{Identifier: "x", Declarations: []blueprint.Declaration{base}, Activations: []blueprint.Activation{{Identifier: "act", StandardConfigurations: []string{"cfg"}, Predicate: "SELF MATCHES 'x'"}}},
		{Identifier: "x", Declarations: []blueprint.Declaration{{Identifier: "profile", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: "absent"}}}},
	} {
		if _, err := blueprint.Compile(spec, blueprint.Options{}); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Errorf("spec %+v: %v", spec, err)
		}
	}
	if _, err := blueprint.NewDeclaration("nil", (*schema.MathSettings)(nil)); err == nil {
		t.Fatal("nil accepted")
	}
	empty, err := blueprint.Compile(blueprint.Spec{Identifier: "clear"}, blueprint.Options{})
	if err != nil || len(empty.Publication.Declarations) != 0 {
		t.Fatalf("empty blueprint: %v", err)
	}
}
