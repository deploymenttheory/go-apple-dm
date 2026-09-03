package conformance

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

// TestJSONPolicy pins decision record 0018: encoding/json/v2 rejects
// duplicate object names and invalid UTF-8 (the behaviour new untrusted
// decoders rely on) while the v1 API the generated code uses keeps its
// lenient, backwards-compatible behaviour.
func TestJSONPolicy(t *testing.T) {
	t.Parallel()
	duplicate := []byte(`{"StandardConfigurations":["a"],"StandardConfigurations":["b"]}`)
	invalidUTF8 := []byte("{\"Predicate\":\"\xff\"}")

	var v2 ddm.ActivationSimple
	if err := jsonv2.Unmarshal(duplicate, &v2); err == nil {
		t.Fatal("json/v2 accepted a duplicate object name")
	}
	if err := jsonv2.Unmarshal(invalidUTF8, &v2); err == nil {
		t.Fatal("json/v2 accepted invalid UTF-8")
	}
	var generic map[string]any
	if err := jsonv2.Unmarshal(duplicate, &generic); err == nil {
		t.Fatal("json/v2 accepted a duplicate object name into a map")
	}

	var v1 ddm.ActivationSimple
	if err := json.Unmarshal(duplicate, &v1); err != nil || len(v1.StandardConfigurations) != 1 || v1.StandardConfigurations[0] != "b" {
		t.Fatalf("v1 compatibility (last duplicate wins) changed: %+v %v", v1, err)
	}
	if err := json.Unmarshal(invalidUTF8, &v1); err != nil {
		t.Fatalf("v1 compatibility (invalid UTF-8 replaced) changed: %v", err)
	}
	var syntax *jsonv2.SemanticError
	if err := jsonv2.Unmarshal([]byte(`{"Predicate": 1}`), &v2); err == nil || !errors.As(err, &syntax) {
		t.Fatalf("json/v2 type mismatch is not a SemanticError: %v", err)
	}
}
