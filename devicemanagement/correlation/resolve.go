package correlation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// Field identifies one exact source field; field names have no inferred aliases.
type Field struct {
	Source source.Reference
	Name   string
}

// Equivalent declares that Inputs have the same meaning and units. Name is a
// caller-owned result key. Input order, then report order, breaks timestamp ties.
// Incompatible meanings or units must be mapped to separate results.
type Equivalent struct {
	Name   string
	Inputs []Field
}

// Value retains original JSON and the precise source field and report time.
type Value struct {
	Field      Field
	Value      json.RawMessage
	ObservedAt time.Time
	Native     bool
}

// Resolution preserves every candidate, including historical values and nulls.
// Selected is nil when no mapped field was reported. Disagrees indicates distinct
// JSON values; no string, numeric or unit coercion is performed.
type Resolution struct {
	Selected   *Value
	Candidates []Value
	Disagrees  bool
}

// Resolve optionally selects explicitly equivalent facts from checked evidence.
// Latest native evidence wins before Apple regardless of Apple's refresh time.
// It requires at least one matched native report and rejects duplicate result
// names, empty mappings and fields referring to sources outside this correlation.
// It never changes evidence or fills missing fields by guessing their meanings.
func (c Correlation) Resolve(mappings []Equivalent) (map[string]Resolution, error) {
	if len(c.matched) == 0 {
		return nil, fmt.Errorf("%w: matching authenticated native evidence required", ErrInvalid)
	}
	known := map[source.Reference]bool{c.AppleReference(): true}
	for _, r := range c.matched {
		known[r.Source] = true
	}
	out := make(map[string]Resolution, len(mappings))
	for _, m := range mappings {
		if _, ok := out[m.Name]; ok || m.Name == "" || len(m.Inputs) == 0 {
			return nil, fmt.Errorf("%w: unique named nonempty equivalence required", ErrInvalid)
		}
		var result Resolution
		seen := map[Field]bool{}
		for _, field := range m.Inputs {
			if field.Name == "" || !known[field.Source] || seen[field] {
				return nil, fmt.Errorf("%w: unique field and correlated source required", ErrInvalid)
			}
			seen[field] = true
			if field.Source == c.AppleReference() {
				if value, ok := c.attributes[field.Name]; ok {
					result.Candidates = append(result.Candidates, Value{field, clone(value), c.apple.ObservedAt, false})
				}
			}
			for _, r := range c.matched {
				if r.Source != field.Source {
					continue
				}
				if value, ok := r.Fields[field.Name]; ok {
					result.Candidates = append(result.Candidates, Value{field, clone(value), r.ObservedAt, true})
				}
			}
		}
		for _, v := range result.Candidates {
			old := result.Selected
			if old == nil || (v.Native && !old.Native) || (v.Native == old.Native && v.ObservedAt.After(old.ObservedAt)) {
				selected := v
				selected.Value = clone(v.Value)
				result.Selected = &selected
			}
		}
		if result.Selected != nil {
			for _, v := range result.Candidates {
				if !equal(v.Value, result.Selected.Value) {
					result.Disagrees = true
					break
				}
			}
		}
		out[m.Name] = result
	}
	return out, nil
}

// equal preserves numeric lexemes with UseNumber while ignoring object member
// order and insignificant JSON whitespace. Inputs were validated at correlation.
func equal(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) any {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		_ = d.Decode(&v)
		return v
	}
	return reflect.DeepEqual(decode(a), decode(b))
}
