package validation_test

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// TestCollectorRules checks validation error collection, field paths, and error formatting.
func TestCollectorRules(t *testing.T) {
	t.Parallel()
	c := validation.New(support.Target{})
	c.Required("A", true)
	c.Required("B", false)
	c.Enum("C", true, "x", []any{"x", "y"})
	c.Enum("D", true, "z", []any{"x", "y"})
	c.Enum("E", true, int64(2), []any{1, 2})
	c.Enum("F", true, 2.5, []any{1, 2})
	c.Enum("G", false, "ignored", []any{"x"})
	c.Enum("H", true, "anything", nil)
	c.Range("I", true, 5, new(float64(0)), new(float64(10)))
	c.Range("J", true, -1, new(float64(0)), nil)
	c.Range("K", true, 11, nil, new(float64(10)))
	c.Range("L", false, 99, new(float64(0)), new(float64(1)))
	c.Pattern("M", true, "abc", regexp.MustCompile(`^a`))
	c.Pattern("N", true, "xyz", regexp.MustCompile(`^a`))
	c.Pattern("O", true, "xyz", nil)
	c.Repetition("P", true, 2, 1, 3)
	c.Repetition("Q", true, 0, 1, 0)
	c.Repetition("R", true, 5, 0, 3)
	c.Repetition("S", false, 0, 1, 1)
	err := c.Err()
	if err == nil {
		t.Fatal("expected errors")
	}
	var es validation.Errors
	if !errors.As(err, &es) || !errors.Is(err, validation.ErrValidation) {
		t.Fatalf("error type: %T", err)
	}
	wantPaths := []string{"B", "D", "F", "J", "K", "N", "Q", "R"}
	if len(es) != len(wantPaths) {
		t.Fatalf("got %d errors: %v", len(es), es)
	}
	for i, e := range es {
		if e.Path != wantPaths[i] {
			t.Errorf("error %d path = %s, want %s", i, e.Path, wantPaths[i])
		}
	}
	if !strings.Contains(es.Error(), "8 validation errors") || !strings.Contains(es[0].Error(), "B: ") {
		t.Errorf("Error strings: %q / %q", es.Error(), es[0].Error())
	}
	if (validation.Errors{es[0]}).Error() != es[0].Error() {
		t.Error("single error formatting")
	}
	if validation.New(support.Target{}).Err() != nil {
		t.Error("empty collector should return nil")
	}
	if validation.Join("", "A") != "A" || validation.Join("A", "B") != "A.B" || validation.Index("A", 3) != "A[3]" {
		t.Error("path helpers")
	}
}

// TestSupportChecks checks support validation errors, deprecation warnings, and empty-target
// behavior.
func TestSupportChecks(t *testing.T) {
	t.Parallel()
	e := &support.Entry{Path: "X.Y", OS: map[support.OS]*support.OSSupport{
		support.IOS: {Introduced: osversion.New(15, 0, 0), Deprecated: osversion.New(17, 0, 0)},
	}}
	// No target: nothing recorded.
	c := validation.New(support.Target{})
	c.Support("X.Y", true, e)
	if c.Err() != nil || len(c.Warnings()) != 0 {
		t.Fatal("zero target should skip support checks")
	}
	// Unsupported.
	c = validation.New(support.Target{OS: support.IOS, Version: osversion.New(14, 0, 0)})
	c.Support("X.Y", true, e)
	c.Support("X.Z", true, nil)
	c.Support("X.W", false, e)
	if err := c.Err(); err == nil || !strings.Contains(err.Error(), "requires iOS 15.0") {
		t.Fatalf("expected unsupported error, got %v", err)
	}
	if c.Target().OS != support.IOS {
		t.Error("Target")
	}
	// Deprecated: warning only.
	c = validation.New(support.Target{OS: support.IOS, Version: osversion.New(18, 0, 0)})
	c.Support("X.Y", true, e)
	if c.Err() != nil || len(c.Warnings()) != 1 || c.Warnings()[0].Rule != validation.RuleSupport {
		t.Fatalf("expected one warning, got err=%v warnings=%v", c.Err(), c.Warnings())
	}
}

// TestRelationshipAndDeadlineRules checks field relationships and device-local
// deadline formats.
func TestRelationshipAndDeadlineRules(t *testing.T) {
	t.Parallel()
	c := validation.New(support.Target{})
	c.Require("Pair", true, "unused")
	c.LocalDateTime("Absent", false, "not checked")
	c.LocalDateTime("Valid", true, "2026-10-27T09:30:00")
	c.Enum("Single", true, float32(1.5), []any{1.5})
	if c.Err() != nil {
		t.Fatalf("valid values rejected: %v", c.Err())
	}
	c.Require("Pair", false, "Pair requires Partner")
	for _, value := range []string{"2026-10-27T09:30:00Z", "2026-10-27T09:30:00.5", "2026-02-30T09:30:00"} {
		c.LocalDateTime("Deadline", true, value)
	}
	var es validation.Errors
	if !errors.As(c.Err(), &es) || len(es) != 4 {
		t.Fatalf("errors = %v", c.Err())
	}
	if es[0].Rule != validation.RuleRequired || es[0].Message != "Pair requires Partner" {
		t.Fatalf("relationship error = %+v", es[0])
	}
	for _, e := range es[1:] {
		if e.Path != "Deadline" || e.Rule != validation.RuleFormat {
			t.Fatalf("deadline error = %+v", e)
		}
	}
}

// TestAvailabilityIssues checks the key-level issues behind support errors and
// deprecation warnings: their windows, order, de-duplication across values,
// and extraction from wrapped validation errors.
func TestAvailabilityIssues(t *testing.T) {
	t.Parallel()
	ios := func(s support.OSSupport) *support.Entry {
		return &support.Entry{Path: "Top.Key", OS: map[support.OS]*support.OSSupport{support.IOS: &s}}
	}
	introduced := ios(support.OSSupport{Introduced: osversion.New(17, 0, 0), Removed: osversion.New(20, 0, 0)})
	introduced.Path = "Top.New"
	deprecated := ios(support.OSSupport{Introduced: osversion.New(15, 0, 0), Deprecated: osversion.New(16, 0, 0)})
	deprecated.Path = "Top.Old"
	unavailable := &support.Entry{Path: "Top.Mac", OS: map[support.OS]*support.OSSupport{support.IOS: {NotAvailable: true}}}

	c := validation.New(support.Target{OS: support.IOS, Version: osversion.New(16, 4, 0)})
	c.Required("Top.Name", false)
	c.Support("Top.Old", true, deprecated)
	c.Support("Top.New[0]", true, introduced)
	c.Support("Top.New[1]", true, introduced)
	c.Support("Top.Mac.private-map-key", true, unavailable)
	want := []validation.AvailabilityIssue{
		{Path: "Top.Mac", OS: support.IOS, Rule: support.RuleNotAvailable, Severity: validation.SeverityError},
		{Path: "Top.New", OS: support.IOS, Rule: support.RuleIntroduced, Severity: validation.SeverityError, Introduced: osversion.New(17, 0, 0), Removed: osversion.New(20, 0, 0)},
		{Path: "Top.Old", OS: support.IOS, Rule: support.RuleDeprecated, Severity: validation.SeverityWarning, Introduced: osversion.New(15, 0, 0), Deprecated: osversion.New(16, 0, 0)},
	}
	if got := c.Availability(); !slices.Equal(got, want) {
		t.Fatalf("Availability() = %+v, want %+v", got, want)
	}
	var es validation.Errors
	if !errors.As(c.Err(), &es) || len(es) != 4 || es[0].Availability != nil || es[1].Availability == nil {
		t.Fatalf("errors = %+v", c.Err())
	}
	if c.Warnings()[0].Availability.Rule != support.RuleDeprecated {
		t.Fatalf("warning = %+v", c.Warnings()[0])
	}
	wrapped := fmt.Errorf("declaration: %w", c.Err())
	if got := validation.AvailabilityIssues(wrapped); !slices.Equal(got, want[:2]) {
		t.Fatalf("AvailabilityIssues(wrapped) = %+v, want %+v", got, want[:2])
	}
	if got := validation.AvailabilityIssues(errors.New("other")); got != nil {
		t.Fatalf("AvailabilityIssues(other) = %+v", got)
	}
	root := validation.New(support.Target{OS: support.IOS, Version: osversion.New(16, 4, 0)})
	root.Support("", true, introduced)
	if got := root.Err().Error(); got != "Top.New: requires iOS 17.0, target is 16.4" {
		t.Fatalf("root error = %q", got)
	}
	if got := validation.New(support.Target{OS: support.IOS}).Availability(); got != nil {
		t.Fatalf("empty collector issues = %+v", got)
	}
}
