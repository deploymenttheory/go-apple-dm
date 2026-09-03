package ddm_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

func TestParseDeclaration(t *testing.T) {
	t.Parallel()
	target := support.Target{}
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		d, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.activation.simple","Identifier":"com.example.act","Payload":{"StandardConfigurations":["com.example.cfg"]}}`), target)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"Identifier":"com.example.act","Payload":{"StandardConfigurations":["com.example.cfg"]},"Type":"com.apple.activation.simple"}`
		if string(d.Canonical) != want {
			t.Fatalf("canonical %s", d.Canonical)
		}
		if d.Kind != schemaddm.KindActivation || d.Type != schemaddm.DeclarationTypeActivationSimple || d.Identifier != "com.example.act" {
			t.Fatalf("declaration %+v", d)
		}
		if d.ServerToken != ddm.TokenFor([]byte(want)) {
			t.Fatalf("token %q", d.ServerToken)
		}
		if !d.CreatedAt.IsZero() || !d.UpdatedAt.IsZero() {
			t.Fatal("parse stamped times")
		}
		for _, raw := range [][]byte{
			configTest("com.example.cfg", "hi"),
			properties("com.example.props", map[string]any{"a": 1}),
			assetData("com.example.asset", "https://example.com/x"),
		} {
			if _, err := ddm.ParseDeclaration(raw, target); err != nil {
				t.Errorf("%s: %v", raw, err)
			}
		}
	})
	t.Run("UnknownType", func(t *testing.T) {
		t.Parallel()
		_, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.configuration.nope","Identifier":"x","Payload":{}}`), target)
		if !errors.Is(err, ddm.ErrUnknownType) {
			t.Fatalf("unknown type: %v", err)
		}
		if _, err := ddm.ParseDeclaration([]byte(`{"Identifier":"x","Payload":{}}`), target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("missing type: %v", err)
		}
	})
	t.Run("CredentialSubtypeRejected", func(t *testing.T) {
		t.Parallel()
		_, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.credential.acme","Identifier":"x","Payload":{}}`), target)
		if !errors.Is(err, ddm.ErrUnknownType) {
			t.Fatalf("credential subtype: %v", err)
		}
	})
	t.Run("BaseRejected", func(t *testing.T) {
		t.Parallel()
		_, err := ddm.ParseDeclaration([]byte(`{"Type":"","Identifier":"x","Payload":{}}`), target)
		if !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("base type: %v", err)
		}
		if len(schemaddm.ByID("")) == 0 {
			t.Skip("registry has no base entry")
		}
	})
	t.Run("BadIdentifier", func(t *testing.T) {
		t.Parallel()
		for name, id := range map[string]string{"Empty": "", "Slash": "com.example/x", "TooLong": strings.Repeat("a", 65)} {
			_, err := ddm.ParseDeclaration(configTest(id, "hi"), target)
			if !errors.Is(err, ddm.ErrInvalidDeclaration) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if _, err := ddm.ParseDeclaration(configTest(strings.Repeat("a", 64), "hi"), target); err != nil {
			t.Fatalf("64-byte identifier: %v", err)
		}
	})
	t.Run("TrailingGarbage", func(t *testing.T) {
		t.Parallel()
		raw := append(configTest("com.example.cfg", "hi"), []byte(" {}")...)
		if _, err := ddm.ParseDeclaration(raw, target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("trailing garbage: %v", err)
		}
		if _, err := ddm.ParseDeclaration([]byte("not json"), target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("not json: %v", err)
		}
	})
	t.Run("DuplicateKeys", func(t *testing.T) {
		t.Parallel()
		top := []byte(`{"Type":"com.apple.configuration.management.test","Identifier":"a","Identifier":"b","Payload":{"Echo":"hi"}}`)
		if _, err := ddm.ParseDeclaration(top, target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("duplicate top-level key: %v", err)
		}
		nested := []byte(`{"Type":"com.apple.configuration.management.test","Identifier":"a","Payload":{"Echo":"hi","Echo":"bye"}}`)
		if _, err := ddm.ParseDeclaration(nested, target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("duplicate payload key: %v", err)
		}
	})
	t.Run("UnknownTopLevelKey", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"Type":"com.apple.configuration.management.test","Identifier":"a","Payload":{"Echo":"hi"},"Extra":1}`)
		if _, err := ddm.ParseDeclaration(raw, target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("unknown top-level key: %v", err)
		}
	})
	t.Run("ValidateFails", func(t *testing.T) {
		t.Parallel()
		// com.apple.configuration.management.test requires Echo.
		raw := []byte(`{"Type":"com.apple.configuration.management.test","Identifier":"a","Payload":{"ReturnStatus":"x"}}`)
		if _, err := ddm.ParseDeclaration(raw, target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("missing required key: %v", err)
		}
		// A payload of the wrong shape fails before validation.
		if _, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.configuration.management.test","Identifier":"a","Payload":"str"}`), target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("string payload: %v", err)
		}
	})
	t.Run("GeneratedValidateRuns", func(t *testing.T) {
		t.Parallel()
		// activation.simple requires StandardConfigurations.
		if _, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.activation.simple","Identifier":"a","Payload":{}}`), target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("empty activation: %v", err)
		}
		if _, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.activation.simple","Identifier":"a"}`), target); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("activation without payload: %v", err)
		}
	})
	t.Run("ServerTokenIgnored", func(t *testing.T) {
		t.Parallel()
		with, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.configuration.management.test","Identifier":"a","ServerToken":"mine","Payload":{"Echo":"hi"}}`), target)
		if err != nil {
			t.Fatal(err)
		}
		without, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.configuration.management.test","Identifier":"a","Payload":{"Echo":"hi"}}`), target)
		if err != nil {
			t.Fatal(err)
		}
		if with.ServerToken != without.ServerToken || with.ServerToken == "mine" || !bytes.Equal(with.Canonical, without.Canonical) {
			t.Fatalf("uploaded ServerToken leaked: %+v vs %+v", with, without)
		}
		if bytes.Contains(with.Canonical, []byte("ServerToken")) {
			t.Fatalf("canonical carries ServerToken: %s", with.Canonical)
		}
	})
	t.Run("EmptyPayloadDefaultsToObject", func(t *testing.T) {
		t.Parallel()
		d, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.management.properties","Identifier":"a"}`), target)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"Identifier":"a","Payload":{},"Type":"com.apple.management.properties"}`
		if string(d.Canonical) != want {
			t.Fatalf("canonical %s", d.Canonical)
		}
		explicit, err := ddm.ParseDeclaration([]byte(`{"Type":"com.apple.management.properties","Identifier":"a","Payload":{}}`), target)
		if err != nil || explicit.ServerToken != d.ServerToken {
			t.Fatalf("explicit empty payload differs: %+v %v", explicit, err)
		}
	})
}

func TestRenderDeclaration(t *testing.T) {
	t.Parallel()
	t.Run("InsertsServerToken", func(t *testing.T) {
		t.Parallel()
		canonical := []byte(`{"Identifier":"a","Payload":{"Echo":"hi"},"Type":"com.apple.configuration.management.test"}`)
		out, err := ddm.RenderDeclaration(canonical, "tok")
		if err != nil {
			t.Fatal(err)
		}
		want := `{"Identifier":"a","Payload":{"Echo":"hi"},"ServerToken":"tok","Type":"com.apple.configuration.management.test"}`
		if string(out) != want {
			t.Fatalf("rendered %s", out)
		}
		// A stored declaration without a Payload member renders an empty object.
		out, err = ddm.RenderDeclaration([]byte(`{"Identifier":"a","Type":"t"}`), "tok")
		if err != nil || string(out) != `{"Identifier":"a","Payload":{},"ServerToken":"tok","Type":"t"}` {
			t.Fatalf("rendered %s, %v", out, err)
		}
	})
	t.Run("InvalidCanonical", func(t *testing.T) {
		t.Parallel()
		if _, err := ddm.RenderDeclaration([]byte("nope"), "tok"); !errors.Is(err, ddm.ErrInvalidDeclaration) {
			t.Fatalf("invalid canonical: %v", err)
		}
	})
}
