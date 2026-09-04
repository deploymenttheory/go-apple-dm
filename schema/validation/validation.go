package validation

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
)

// Rule names used in Error.Rule.
const (
	RuleRequired   = "required"
	RuleEnum       = "rangelist"
	RuleRange      = "range"
	RuleFormat     = "format"
	RuleRepetition = "repetition"
	RuleSupport    = "support"
)

// Error is one validation failure.
type Error struct {
	Path    string // dotted wire-key path from the top-level type
	Rule    string
	Message string
}

// Error implements error.
func (e *Error) Error() string { return e.Path + ": " + e.Message }

// Errors is every failure found. It implements error.
type Errors []*Error

// Error implements error.
func (es Errors) Error() string {
	if len(es) == 1 {
		return es[0].Error()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d validation errors:", len(es))
	for _, e := range es {
		sb.WriteString("\n  ")
		sb.WriteString(e.Error())
	}
	return sb.String()
}

// ErrValidation is the sentinel every Errors value unwraps to.
var ErrValidation = errors.New("schema validation failed")

// Unwrap lets errors.Is(err, ErrValidation) succeed.
func (es Errors) Unwrap() error { return ErrValidation }

// Collector accumulates errors and deprecation warnings.
type Collector struct {
	target   support.Target
	errs     Errors
	warnings Errors
}

// New starts a collector for a target. The zero Target skips support checks.
func New(t support.Target) *Collector { return &Collector{target: t} }

// Target returns the collector's target.
func (c *Collector) Target() support.Target { return c.target }

func (c *Collector) add(path, rule, msg string) {
	c.errs = append(c.errs, &Error{Path: path, Rule: rule, Message: msg})
}

// Required records an error when a required key is absent.
func (c *Collector) Required(path string, present bool) {
	if !present {
		c.add(path, RuleRequired, "required key is missing")
	}
}

// Enum records an error when a present value is not in the allowed list.
// Values are compared as strings, or numerically when both sides are numbers.
func (c *Collector) Enum(path string, present bool, v any, allowed []any) {
	if !present || len(allowed) == 0 {
		return
	}
	for _, a := range allowed {
		if equalValue(v, a) {
			return
		}
	}
	c.add(path, RuleEnum, fmt.Sprintf("value %v is not one of %v", v, allowed))
}

func equalValue(a, b any) bool {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}

// Range records an error when a present number is outside [min, max].
func (c *Collector) Range(path string, present bool, v float64, minValue, maxValue *float64) {
	if !present || math.IsNaN(v) {
		return
	}
	if minValue != nil && v < *minValue {
		c.add(path, RuleRange, fmt.Sprintf("value %v is below minimum %v", v, *minValue))
	}
	if maxValue != nil && v > *maxValue {
		c.add(path, RuleRange, fmt.Sprintf("value %v is above maximum %v", v, *maxValue))
	}
}

// Pattern records an error when a present string does not match re.
func (c *Collector) Pattern(path string, present bool, v string, re *regexp.Regexp) {
	if !present || re == nil {
		return
	}
	if !re.MatchString(v) {
		c.add(path, RuleFormat, fmt.Sprintf("value %q does not match %s", v, re))
	}
}

// Repetition records an error when a present array's length is outside
// [min, max]. A max of 0 means unbounded.
func (c *Collector) Repetition(path string, present bool, n, minLen, maxLen int) {
	if !present {
		return
	}
	if n < minLen {
		c.add(path, RuleRepetition, fmt.Sprintf("%d items, minimum is %d", n, minLen))
	}
	if maxLen > 0 && n > maxLen {
		c.add(path, RuleRepetition, fmt.Sprintf("%d items, maximum is %d", n, maxLen))
	}
}

// Support records an error when a present key is not supported by the
// target, and a warning when it is deprecated there.
func (c *Collector) Support(path string, present bool, e *support.Entry) {
	if !present || e == nil || c.target.OS == "" {
		return
	}
	r := e.Check(c.target)
	switch {
	case !r.Supported:
		c.add(path, RuleSupport, r.Reason)
	case r.Deprecated:
		c.warnings = append(c.warnings, &Error{Path: path, Rule: RuleSupport, Message: r.Reason})
	}
}

// Err returns the collected errors, or nil when there are none.
func (c *Collector) Err() error {
	if len(c.errs) == 0 {
		return nil
	}
	return c.errs
}

// Warnings returns deprecation warnings collected so far.
func (c *Collector) Warnings() Errors { return c.warnings }

// Join concatenates key paths.
func Join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// Index formats an array element path.
func Index(prefix string, i int) string { return fmt.Sprintf("%s[%d]", prefix, i) }
