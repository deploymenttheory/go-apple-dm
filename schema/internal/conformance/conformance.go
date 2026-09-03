package conformance

import (
	"encoding/json"
	"reflect"
	"testing"

	howett "howett.net/plist"

	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

// RoundTrip encodes v, decodes into a fresh value from newT, re-encodes, and
// requires the two encodings to decode to the same generic value, for JSON
// and, unless jsonOnly, for XML plist and for binary plist produced by an
// independent encoder (howett.net/plist) and decoded by ours. Comparing
// decoded generic values rather than bytes keeps the check meaningful for
// fields typed as any, where a typed value decodes to a map.
func RoundTrip(t *testing.T, v any, newT func() any, jsonOnly bool) {
	t.Helper()
	first, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	out := newT()
	if err := json.Unmarshal(first, out); err != nil {
		t.Fatalf("json unmarshal: %v\n%s", err, first)
	}
	second, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json re-marshal: %v", err)
	}
	if !sameJSON(first, second) {
		t.Fatalf("json round trip differs:\n%s\n%s", first, second)
	}
	if jsonOnly {
		return
	}

	xmlFirst, err := plist.Marshal(v)
	if err != nil {
		t.Fatalf("plist marshal: %v", err)
	}
	out = newT()
	if err := plist.Unmarshal(xmlFirst, out); err != nil {
		t.Fatalf("plist unmarshal: %v\n%s", err, xmlFirst)
	}
	xmlSecond, err := plist.Marshal(out)
	if err != nil {
		t.Fatalf("plist re-marshal: %v", err)
	}
	if !samePlist(xmlFirst, xmlSecond) {
		t.Fatalf("xml plist round trip differs:\n%s\n%s", xmlFirst, xmlSecond)
	}

	bin, err := howett.Marshal(v, howett.BinaryFormat)
	if err != nil {
		t.Fatalf("howett binary marshal: %v", err)
	}
	if plist.DetectFormat(bin) != plist.FormatBinary {
		t.Fatal("howett output not detected as binary")
	}
	out = newT()
	if err := plist.Unmarshal(bin, out); err != nil {
		t.Fatalf("binary unmarshal: %v", err)
	}
	xmlFromBinary, err := plist.Marshal(out)
	if err != nil {
		t.Fatalf("plist marshal after binary: %v", err)
	}
	if !samePlist(xmlFirst, xmlFromBinary) {
		t.Fatalf("binary plist round trip differs:\n%s\n%s", xmlFirst, xmlFromBinary)
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func samePlist(a, b []byte) bool {
	var x, y any
	if plist.Unmarshal(a, &x) != nil || plist.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// Validator is implemented by every generated top-level type.
type Validator interface {
	Validate(t support.Target) error
}

// Validates checks that a populated sample passes structural validation and
// that validating an empty value against a real target runs without panic.
func Validates(t *testing.T, name string, sample, empty any) {
	t.Helper()
	sv, ok := sample.(Validator)
	if !ok {
		t.Errorf("%s: sample does not implement Validate", name)
		return
	}
	if err := sv.Validate(support.Target{}); err != nil {
		t.Errorf("%s: populated sample fails validation: %v", name, err)
	}
	ev, ok := empty.(Validator)
	if !ok {
		t.Errorf("%s: empty value does not implement Validate", name)
		return
	}
	_ = ev.Validate(support.Target{OS: support.IOS, Version: support.V(26, 0, 0)})
}

// Deref returns the value a sample pointer points to, or the zero value when
// the sample was cut off by the recursion limit.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// SliceOf wraps a sample pointer in a one-element slice, or returns nil when
// the sample was cut off by the recursion limit.
func SliceOf[T any](p *T) []T {
	if p == nil {
		return nil
	}
	return []T{*p}
}
