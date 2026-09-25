package fault_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// TestOccurrenceRendersWithoutRepeatingItself covers the text rules: operation first,
// the condition once, the cause when it adds something, never a stacked label.
func TestOccurrenceRendersWithoutRepeatingItself(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"EntryAlone":           {fault.New(entry), "the thing is missing"},
		"EntryWithOperation":   {fault.New(entry, fault.WithOperation("read revision")), "read revision: the thing is missing"},
		"WrapPlainCause":       {fault.Wrap(errors.New("disk on fire"), fault.WithEntry(entry)), "the thing is missing: disk on fire"},
		"WrapCauseThatIsEntry": {fault.Wrap(fmt.Errorf("revision %q: %w", "17", entry), fault.WithEntry(entry)), `revision "17": the thing is missing`},
		"WrapWithKindOnly":     {fault.Wrap(errors.New("bad json"), fault.WithKind(fault.InvalidArgument)), "bad json"},
		"WrapContextOnly":      {fault.Wrap(errors.New("x"), fault.WithOperation("save")), "save: x"},
		"KindWithoutCause":     {fault.Wrap(errors.New("x"), fault.WithKind(fault.Gone), fault.WithOperation("op")), "op: x"},
		"OperationEntryAndCause": {
			fault.Wrap(errors.New("cause"), fault.WithEntry(entry), fault.WithOperation("op")),
			"op: the thing is missing: cause",
		},
		"DetailAfterEntry": {
			fault.New(other, fault.WithDetail("the limit must be between %d and %d", 1, 1000)),
			"the thing is unacceptable: the limit must be between 1 and 1000",
		},
		"DetailBeforeCause": {
			fault.Wrap(errors.New("EOF"), fault.WithKind(fault.InvalidArgument), fault.WithDetail("the body is not JSON")),
			"the body is not JSON: EOF",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("rendered %q, want %q", got, tc.want)
			}
		})
	}
	if fault.Wrap(nil) != nil {
		t.Fatal("Wrap(nil) is not nil")
	}
}

// TestOccurrenceClassifies checks that an occurrence matches its condition and kind, that
// a kind override wins over the entry, and that the cause stays matchable.
func TestOccurrenceClassifies(t *testing.T) {
	cause := errors.New("row missing")
	occ := fault.Wrap(cause, fault.WithEntry(entry))
	if !errors.Is(occ, entry) || !errors.Is(occ, fault.NotFound) || !errors.Is(occ, cause) {
		t.Fatal("occurrence lost its condition, kind or cause")
	}
	if e, ok := fault.EntryOf(occ); !ok || e != entry {
		t.Fatal("EntryOf did not find the condition")
	}
	if fault.AudienceOf(occ) != fault.Client || fault.CodeOf(occ) != "DM-TEST-MISSING" {
		t.Fatal("audience or code lost")
	}

	// An override re-kinds without changing the condition.
	// An override re-kinds the occurrence without changing the condition: KindOf and
	// errors.Is against the occurrence report the override, while the condition inside
	// the chain still matches its own kind, as any wrapped error would.
	over := fault.New(entry, fault.WithKind(fault.Gone))
	if fault.KindOf(over) != fault.Gone || !errors.Is(over, fault.Gone) {
		t.Fatal("kind override did not win", fault.KindOf(over))
	}
	if !errors.Is(over, entry) || fault.CodeOf(over) != "DM-TEST-MISSING" {
		t.Fatal("kind override hid the condition")
	}

	// An occurrence around a catalogued cause takes the cause's classification when it
	// declares none of its own, and its own when it does.
	inner := fault.Wrap(fmt.Errorf("x: %w", other), fault.WithOperation("op"))
	if fault.KindOf(inner) != fault.InvalidArgument || fault.CodeOf(inner) != "DM-TEST-REJECTED" {
		t.Fatal("context-only occurrence changed the classification")
	}
	var asOcc *fault.Error
	if !errors.As(inner, &asOcc) || asOcc.Kind() != nil || asOcc.Entry() != nil || asOcc.Operation() != "op" {
		t.Fatal("context-only occurrence reported a classification of its own")
	}
	outer := fault.Wrap(inner, fault.WithEntry(entry))
	if fault.KindOf(outer) != fault.NotFound || fault.CodeOf(outer) != "DM-TEST-MISSING" {
		t.Fatal("outer classification did not win over the cause's")
	}
	if !errors.Is(outer, other) {
		t.Fatal("outer occurrence hid the inner condition")
	}
	// Further wrapping with %w keeps all of it.
	deeper := fmt.Errorf("serve: %w", outer)
	if fault.KindOf(deeper) != fault.NotFound || !errors.Is(deeper, other) {
		t.Fatal("fmt wrapping lost the classification")
	}
}

// TestOccurrenceCarriesContext covers attributes, retry delay and stack: attached
// once, merged across the chain with the outermost winning, and rendered by LogValue.
func TestOccurrenceCarriesContext(t *testing.T) {
	inner := fault.Wrap(errors.New("busy"),
		fault.WithEntry(other),
		fault.WithAttrs(fault.EnrollmentID("UDID-1"), fault.CommandUUID("cmd-1")),
		fault.WithRetryAfter(3*time.Second),
	)
	outer := fmt.Errorf("deliver: %w", fault.Wrap(inner,
		fault.WithOperation("deliver command"),
		fault.WithAttrs(fault.EnrollmentID("UDID-OUTER")),
		fault.WithStack(),
	))

	attrs := fault.AttrsOf(outer)
	got := map[string]string{}
	for _, a := range attrs {
		got[a.Key] = a.Value.String()
	}
	if got[fault.KeyEnrollmentID] != "UDID-OUTER" || got[fault.KeyCommandUUID] != "cmd-1" || len(attrs) != 2 {
		t.Fatal("merged attributes wrong", got)
	}
	if fault.RetryAfterOf(outer) != 3*time.Second {
		t.Fatal("retry delay lost", fault.RetryAfterOf(outer))
	}
	stack := fault.StackOf(outer)
	if len(stack) == 0 || !strings.Contains(stack[0], "TestOccurrenceCarriesContext") {
		t.Fatal("stack not captured at the raising site", stack)
	}
	if fault.StackOf(inner) != nil {
		t.Fatal("stack captured without WithStack")
	}
	var occ *fault.Error
	if !errors.As(outer, &occ) || len(occ.Attrs()) != 1 || occ.RetryAfter() != 0 {
		t.Fatal("per-occurrence accessors changed")
	}
	occ.Attrs()[0] = slog.String("x", "y")
	if occ.Attrs()[0].Key != fault.KeyEnrollmentID {
		t.Fatal("Attrs exposed its backing array")
	}

	// A Delayed cause supplies the delay when no occurrence set one.
	delayed := fmt.Errorf("apns: %w", delayedErr{2 * time.Minute})
	if fault.RetryAfterOf(delayed) != 2*time.Minute {
		t.Fatal("Delayed ignored", fault.RetryAfterOf(delayed))
	}
	if fault.RetryAfterOf(errors.New("plain")) != 0 {
		t.Fatal("invented a delay")
	}
}

// delayedErr and classifiedErr stand in for an upstream package's error types.
type delayedErr struct{ d time.Duration }

// Error implements error.
func (d delayedErr) Error() string { return "delayed" }

// RetryDelay implements fault.Delayed.
func (d delayedErr) RetryDelay() time.Duration { return d.d }

type classifiedErr struct{ k *fault.Kind }

// Error implements error.
func (c classifiedErr) Error() string { return "classified" }

// Kind implements fault.Classified.
func (c classifiedErr) Kind() *fault.Kind { return c.k }

// TestUnclassifiedFailures checks the defaults. An error nobody catalogued has no code to
// publish and cannot be described to a client, so it reports Internal rather than being
// guessed at, and an error that classifies itself is believed.
func TestUnclassifiedFailures(t *testing.T) {
	plain := errors.New("something went wrong")
	if fault.CodeOf(plain) != "" {
		t.Fatal("invented a code for an unclassified failure", fault.CodeOf(plain))
	}
	if fault.KindOf(plain) != fault.Internal || fault.IsClassified(plain) {
		t.Fatal("unclassified failure was not Internal", fault.KindOf(plain))
	}
	if fault.AudienceOf(plain) != fault.Operator {
		t.Fatal("unclassified failure was published", fault.AudienceOf(plain))
	}
	if fault.CodeOf(nil) != "" || fault.KindOf(nil) != nil || fault.IsClassified(nil) || fault.ErrorType(nil) != "" {
		t.Fatal("nil reported a classification")
	}
	up := fmt.Errorf("call apple: %w", classifiedErr{fault.Upstream})
	if fault.KindOf(up) != fault.Upstream || !fault.IsClassified(up) || fault.CodeOf(up) != "" {
		t.Fatal("self-classified error not believed", fault.KindOf(up))
	}
	if fault.KindOf(classifiedErr{nil}) != fault.Internal {
		t.Fatal("a nil self-classification was believed")
	}
	if e, ok := fault.EntryOf(up); ok || e != nil {
		t.Fatal("EntryOf found an entry in an uncatalogued chain")
	}
}

// TestKindOverriddenTellsADecisionFromADefault checks the signal a transport uses to
// keep a handler's chosen status for an operator condition.
func TestKindOverriddenTellsADecisionFromADefault(t *testing.T) {
	if fault.KindOverridden(oper) || fault.KindOverridden(fmt.Errorf("x: %w", oper)) || fault.KindOverridden(errors.New("plain")) {
		t.Fatal("a declared or absent classification reported as an override")
	}
	if !fault.KindOverridden(fault.Wrap(oper, fault.WithKind(fault.Conflict))) {
		t.Fatal("an explicit kind was not reported")
	}
	if fault.KindOverridden(fault.Wrap(oper, fault.WithOperation("op"))) {
		t.Fatal("a context-only occurrence reported an override")
	}
	if fault.KindOverridden(nil) {
		t.Fatal("nil")
	}
}

// TestPublicNeverRendersACause pins what a transport may publish: the operation and
// the detail or the condition's message, never the cause a storage driver or the
// operating system wrote, which stays in Error for the operator.
func TestPublicNeverRendersACause(t *testing.T) {
	driver := errors.New("pq: relation \"declarations\" does not exist")
	cases := map[string]struct {
		err  error
		want string
	}{
		"Nil":              {nil, ""},
		"Plain":            {driver, "internal error"},
		"EntryAlone":       {entry, "the thing is missing"},
		"EntryWrapsDriver": {fault.Wrap(driver, fault.WithEntry(entry)), "the thing is missing"},
		"OperationAndEntry": {
			fault.Wrap(driver, fault.WithEntry(entry), fault.WithOperation("read declaration")),
			"read declaration: the thing is missing",
		},
		"DetailWinsOverEntry": {
			fault.Wrap(driver, fault.WithEntry(other), fault.WithDetail("DeviceID is required")),
			"DeviceID is required",
		},
		"KindOnly":        {fault.Wrap(driver, fault.WithKind(fault.InvalidArgument)), "invalid argument"},
		"OutermostOpWins": {fault.Wrap(fault.Wrap(driver, fault.WithOperation("inner")), fault.WithOperation("outer")), "outer: internal error"},
		"FmtWrapKeepsIt":  {fmt.Errorf("serve: %w", fault.New(entry, fault.WithDetail("x"))), "x"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := fault.Public(tc.err)
			if got != tc.want {
				t.Fatalf("Public = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "pq:") {
				t.Fatal("a cause was published")
			}
		})
	}
	var occ *fault.Error
	if err := fault.New(entry, fault.WithDetail("d")); !errors.As(err, &occ) || occ.Detail() != "d" {
		t.Fatal("Detail accessor")
	}
}

// TestShapeClassifiesUnclassifiedFailures checks that a context or network failure
// nobody classified still lands on the kind its shape implies, so a caller that went
// away is not counted as a failure of the deployment.
func TestShapeClassifiesUnclassifiedFailures(t *testing.T) {
	if fault.KindOf(fmt.Errorf("x: %w", context.Canceled)) != fault.Cancelled {
		t.Fatal("cancelled context")
	}
	if fault.KindOf(context.DeadlineExceeded) != fault.DeadlineExceeded {
		t.Fatal("expired context")
	}
	if fault.KindOf(timeoutErr{}) != fault.DeadlineExceeded {
		t.Fatal("network timeout")
	}
	if fault.IsClassified(context.Canceled) {
		t.Fatal("shape counted as a declared classification")
	}
	if fault.ErrorType(context.Canceled) != "cancelled" || fault.Cancelled.Server() {
		t.Fatal("cancelled reported as a server failure")
	}
	// A declared classification wins over the shape.
	if fault.KindOf(fault.Wrap(context.Canceled, fault.WithKind(fault.Unavailable))) != fault.Unavailable {
		t.Fatal("shape overrode a declaration")
	}
}

// timeoutErr is a net.Error that timed out.
type timeoutErr struct{}

// Error implements error.
func (timeoutErr) Error() string { return "i/o timeout" }

// Timeout implements net.Error.
func (timeoutErr) Timeout() bool { return true }

// Temporary implements net.Error.
func (timeoutErr) Temporary() bool { return false }

// TestJoinedOperandsAndReservedKeys covers errors.Join in operand order and the group
// keys an attached attribute may not shadow.
func TestJoinedOperandsAndReservedKeys(t *testing.T) {
	joined := errors.Join(other, entry)
	if fault.CodeOf(joined) != "DM-TEST-REJECTED" || fault.KindOf(joined) != fault.InvalidArgument {
		t.Fatal("errors.Join did not classify by its first operand")
	}
	shadow := fault.New(entry, fault.WithAttrs(slog.String("message", "spoofed"), slog.String("kind", "x"), fault.EnrollmentID("u")))
	var group map[string]any
	v := shadow.LogValue()
	group = map[string]any{}
	for _, a := range v.Group() {
		group[a.Key] = a.Value.String()
	}
	if group["message"] != "the thing is missing" || group["kind"] != "not_found" || group[fault.KeyEnrollmentID] != "u" {
		t.Fatal("an attached attribute shadowed a group key", group)
	}
}

// selfWrapping is an error whose chain never ends.
type selfWrapping struct{}

// Error implements error.
func (selfWrapping) Error() string { return "loop" }

// Unwrap returns the value itself, so the chain never ends.
func (s selfWrapping) Unwrap() error { return s }

// TestWalkIsBounded checks that a cyclic chain terminates with the default
// classification instead of hanging every lookup.
func TestWalkIsBounded(t *testing.T) {
	if fault.KindOf(selfWrapping{}) != fault.Internal || fault.CodeOf(selfWrapping{}) != "" {
		t.Fatal("cyclic chain")
	}
}
