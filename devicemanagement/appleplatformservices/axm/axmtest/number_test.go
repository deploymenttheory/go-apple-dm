package axmtest

import (
	"encoding/json"
	"testing"
)

// Fixture normalization must not manufacture rounded source evidence before an
// SDK or persistence assertion sees the resource.
func TestNormalizePreservesNumericTokens(t *testing.T) {
	attrs := normalize(map[string]any{"large": json.Number("9007199254740993"), "unknown": map[string]any{"false": false, "null": nil}})
	b, err := json.Marshal(attrs)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["large"]) != "9007199254740993" || string(got["unknown"]) != `{"false":false,"null":null}` {
		t.Fatalf("source changed: %s", b)
	}
}
