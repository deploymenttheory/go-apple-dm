package predicate

import (
	"errors"
	"reflect"
	"testing"
)

// FuzzParse checks that Parse never panics, that every failure is a
// *SyntaxError wrapping one of the sentinels, and that the canonical
// rendering of a successful parse re-parses to the same tree and evaluates
// identically under an empty environment.
func FuzzParse(f *testing.F) {
	for _, tc := range parseCases {
		f.Add(tc.input)
	}
	for _, tc := range errorCases {
		f.Add(tc.input)
	}
	for _, tc := range evalCases {
		f.Add(tc.input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		p, err := Parse(input)
		if err != nil {
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error %T is not *SyntaxError", input, err)
			}
			if !errors.Is(err, ErrSyntax) && !errors.Is(err, ErrUnsupported) {
				t.Fatalf("Parse(%q) error %v wraps neither sentinel", input, err)
			}
			if se.Offset < 0 || se.Offset > len(input) {
				t.Fatalf("Parse(%q) Offset %d outside input", input, se.Offset)
			}
			if p != nil {
				t.Fatalf("Parse(%q) returned a predicate with an error", input)
			}
			return
		}
		canonical := p.String()
		again, err := Parse(canonical)
		if err != nil {
			t.Fatalf("canonical %q of %q does not parse: %v", canonical, input, err)
		}
		if got := again.String(); got != canonical {
			t.Fatalf("canonical form unstable: %q then %q", canonical, got)
		}
		if !reflect.DeepEqual(p.root, again.root) {
			t.Fatalf("canonical %q of %q yields a different tree", canonical, input)
		}
		r1, e1 := p.Eval(MapEnv{})
		r2, e2 := again.Eval(MapEnv{})
		if r1 != r2 || (e1 == nil) != (e2 == nil) || errors.Is(e1, ErrType) != errors.Is(e2, ErrType) {
			t.Fatalf("%q and %q evaluate differently: (%v, %v) vs (%v, %v)", input, canonical, r1, e1, r2, e2)
		}
		if e1 != nil && !errors.Is(e1, ErrType) {
			t.Fatalf("Eval(%q) error %v does not wrap ErrType", input, e1)
		}
	})
}
