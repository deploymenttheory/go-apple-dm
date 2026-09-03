package telemetry

import (
	"sort"

	"go.opentelemetry.io/otel/attribute"
)

// OtherValue replaces any value outside a Vocabulary. It matches the
// convention OpenTelemetry's own HTTP semantic conventions use for an
// unrecognised request method, and the leading underscore keeps it from
// colliding with anything Apple sends.
const OtherValue = "_OTHER"

// Vocabulary is a closed set of values for one metric attribute.
//
// It exists because almost every interesting label on this library's metrics
// starts life as a string a device sent us: a MessageType, a RequestType, a
// declaration reason code. A metric attribute built straight from one of
// those is an unbounded time series that a single malformed or hostile
// enrollment can grow without limit, and OpenTelemetry's SDK answers that by
// discarding the overflow into a synthetic series — so the metric stops
// being trustworthy long before anyone notices.
//
// A Vocabulary is built once from a set fixed at compile time, usually one
// of the generated registries in schema/, and maps everything else to
// OtherValue. A caller can then label freely without auditing the call site.
//
// The zero value is not usable; build one with NewVocabulary. A Vocabulary
// is immutable after construction and safe for concurrent use.
type Vocabulary struct {
	key     attribute.Key
	allowed map[string]struct{}
	values  []string
}

// NewVocabulary returns a Vocabulary for an attribute key over a closed set
// of values. Duplicate values are collapsed; an empty set maps everything to
// OtherValue, which is a usable if uninformative attribute rather than an
// error, because a generated registry that turns out to be empty should not
// take a server down.
func NewVocabulary(key string, values []string) *Vocabulary {
	v := &Vocabulary{key: attribute.Key(key), allowed: make(map[string]struct{}, len(values))}
	for _, s := range values {
		if _, dup := v.allowed[s]; dup {
			continue
		}
		v.allowed[s] = struct{}{}
		v.values = append(v.values, s)
	}
	sort.Strings(v.values)
	return v
}

// Attr returns the attribute for a value, replacing anything outside the
// vocabulary with OtherValue.
func (v *Vocabulary) Attr(value string) attribute.KeyValue {
	if _, ok := v.allowed[value]; ok {
		return v.key.String(value)
	}
	return v.key.String(OtherValue)
}

// Allows reports whether a value is in the vocabulary. Use it to decide
// whether a value is worth putting on a span, where cardinality is not a
// concern but a fabricated value is still worth knowing about.
func (v *Vocabulary) Allows(value string) bool {
	_, ok := v.allowed[value]
	return ok
}

// Key returns the attribute key.
func (v *Vocabulary) Key() string { return string(v.key) }

// Values returns the vocabulary in sorted order, excluding OtherValue. It is
// a copy: a caller cannot widen the set through it.
func (v *Vocabulary) Values() []string {
	out := make([]string, len(v.values))
	copy(out, v.values)
	return out
}

// Cardinality is the number of series one Vocabulary can produce for its
// key, which is its size plus OtherValue. A caller multiplying the
// cardinality of every attribute on an instrument gets that instrument's
// worst case, which is the number worth checking against a backend's limit
// before shipping.
func (v *Vocabulary) Cardinality() int { return len(v.values) + 1 }
