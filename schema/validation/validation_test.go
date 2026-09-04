package validation_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/validation"
)

//go:fix inline
func f(v float64) *float64 { return new(v) }

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
	c.Range("I", true, 5, f(0), f(10))
	c.Range("J", true, -1, f(0), nil)
	c.Range("K", true, 11, nil, f(10))
	c.Range("L", false, 99, f(0), f(1))
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

func TestSupportChecks(t *testing.T) {
	t.Parallel()
	e := &support.Entry{Path: "X.Y", OS: map[support.OS]*support.OSSupport{
		support.IOS: {Introduced: support.V(15, 0, 0), Deprecated: support.V(17, 0, 0)},
	}}
	// No target: nothing recorded.
	c := validation.New(support.Target{})
	c.Support("X.Y", true, e)
	if c.Err() != nil || len(c.Warnings()) != 0 {
		t.Fatal("zero target should skip support checks")
	}
	// Unsupported.
	c = validation.New(support.Target{OS: support.IOS, Version: support.V(14, 0, 0)})
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
	c = validation.New(support.Target{OS: support.IOS, Version: support.V(18, 0, 0)})
	c.Support("X.Y", true, e)
	if c.Err() != nil || len(c.Warnings()) != 1 || c.Warnings()[0].Rule != validation.RuleSupport {
		t.Fatalf("expected one warning, got err=%v warnings=%v", c.Err(), c.Warnings())
	}
}
