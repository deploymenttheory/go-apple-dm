package validation

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
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
	// Availability is the key-level availability issue behind a RuleSupport
	// error or deprecation warning, and nil for every other rule.
	Availability *AvailabilityIssue
}

// Severity grades an availability issue.
type Severity string

// Severities.
const (
	// SeverityError: the key blocks delivery to the target.
	SeverityError Severity = "error"
	// SeverityWarning: the key is delivered, but the target has deprecated it.
	SeverityWarning Severity = "warning"
)

// AvailabilityIssue reports one present key whose availability rejects the
// target or warns about it. Path is the key's schema key path, as used by
// support.Lookup, so an issue never contains configuration values: wire map
// keys and array positions are not part of it.
type AvailabilityIssue struct {
	Path     string
	OS       support.OS
	Rule     support.Rule
	Severity Severity
	// Introduced, Deprecated and Removed are the key's availability window
	// on OS. A zero version means the window has no such bound; all three
	// are zero when the key is not available on OS.
	Introduced osversion.Version
	Deprecated osversion.Version
	Removed    osversion.Version
}

// Error implements error. A failure of the top-level value itself has an
// empty path and reports only its message.
func (e *Error) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

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

// add appends a path-qualified validation issue to the collector.
func (c *Collector) add(path, rule, msg string) {
	c.errs = append(c.errs, &Error{Path: path, Rule: rule, Message: msg})
}

// Required records an error when a required key is absent.
func (c *Collector) Required(path string, present bool) {
	if !present {
		c.add(path, RuleRequired, "required key is missing")
	}
}

// Require records a required relationship between fields when condition is false.
func (c *Collector) Require(path string, condition bool, message string) {
	if !condition {
		c.add(path, RuleRequired, message)
	}
}

// LocalDateTime checks an Apple device-local deadline, without a timezone or
// fractional seconds. The calendar date must also exist.
func (c *Collector) LocalDateTime(path string, present bool, value string) {
	if !present {
		return
	}
	const layout = "2006-01-02T15:04:05"
	parsed, err := time.Parse(layout, value)
	if err != nil || parsed.Format(layout) != value {
		c.add(path, RuleFormat, "expected device-local yyyy-mm-ddThh:mm:ss without offset or fractional seconds")
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

// equalValue compares supported schema values without requiring identical Go numeric types.
func equalValue(a, b any) bool {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// toFloat converts a supported numeric value for schema range comparison.
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
// target, and a warning when it is deprecated there. Both carry the key's
// availability issue.
func (c *Collector) Support(path string, present bool, e *support.Entry) {
	if !present || e == nil || c.target.OS == "" {
		return
	}
	r := e.Check(c.target)
	switch {
	case !r.Supported:
		c.errs = append(c.errs, &Error{Path: path, Rule: RuleSupport, Message: r.Reason, Availability: c.issue(e, r, SeverityError)})
	case r.Deprecated:
		c.warnings = append(c.warnings, &Error{Path: path, Rule: RuleSupport, Message: r.Reason, Availability: c.issue(e, r, SeverityWarning)})
	}
}

// SupportValue checks a present subkey-valued string, such as a
// DeviceInformation query, against its entry e. A value without an entry is
// recorded as unvalidated under schemaPath, the value's schema key path
// without the value itself.
func (c *Collector) SupportValue(path, schemaPath string, e *support.Entry) {
	if e == nil {
		c.Unvalidated(path, schemaPath)
		return
	}
	c.Support(path, true, e)
}

// Unvalidated records a warning for a present key or value that has no
// availability data, identified by its schema key path. Like support checks,
// it records nothing for a target without an OS.
func (c *Collector) Unvalidated(path, schemaPath string) {
	if c.target.OS == "" {
		return
	}
	c.warnings = append(c.warnings, &Error{
		Path: path, Rule: RuleSupport, Message: schemaPath + ": no availability data",
		Availability: &AvailabilityIssue{Path: schemaPath, OS: c.target.OS, Rule: support.RuleUnvalidated, Severity: SeverityWarning},
	})
}

// issue describes the availability check r of entry e for the collector's target.
func (c *Collector) issue(e *support.Entry, r support.Result, severity Severity) *AvailabilityIssue {
	issue := &AvailabilityIssue{Path: e.Path, OS: c.target.OS, Rule: r.Rule, Severity: severity}
	if r.OS != nil && !r.OS.NotAvailable {
		issue.Introduced, issue.Deprecated, issue.Removed = r.OS.Introduced, r.OS.Deprecated, r.OS.Removed
	}
	return issue
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

// Availability returns the availability issues behind the collected errors
// and warnings: errors first, then by path and rule, with one issue for each
// key and rule however many values of that key were checked.
func (c *Collector) Availability() []AvailabilityIssue {
	return availability(slices.Concat(c.errs, c.warnings))
}

// AvailabilityIssues returns the availability issues carried by the
// validation errors in err, ordered as Collector.Availability orders them.
// It returns nil when err holds no Errors value.
func AvailabilityIssues(err error) []AvailabilityIssue {
	var es Errors
	if !errors.As(err, &es) {
		return nil
	}
	return availability(es)
}

// availability collects the distinct availability issues carried by es in a
// stable order.
func availability(es Errors) []AvailabilityIssue {
	var out []AvailabilityIssue
	for _, e := range es {
		if e.Availability != nil && !slices.Contains(out, *e.Availability) {
			out = append(out, *e.Availability)
		}
	}
	slices.SortFunc(out, func(a, b AvailabilityIssue) int {
		return cmp.Or(cmp.Compare(a.Severity, b.Severity), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Rule, b.Rule))
	})
	return out
}

// Join concatenates key paths.
func Join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// Index formats an array element path.
func Index(prefix string, i int) string { return fmt.Sprintf("%s[%d]", prefix, i) }
