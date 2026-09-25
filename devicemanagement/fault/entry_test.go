package fault_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// entry, other and device stand in for catalogue declarations. Test codes carry the
// DM-TEST domain, which the documentation table excludes.
var (
	entry  = fault.NewClient("DM-TEST-MISSING", fault.NotFound, "the thing is missing")
	other  = fault.NewClient("DM-TEST-REJECTED", fault.InvalidArgument, "the thing is unacceptable")
	device = fault.NewDevice("com.example.test.refused", fault.PermissionDenied, "the device is refused")
	bare   = fault.NewDevice("", fault.Gone, "the channel is gone")
	oper   = fault.NewOperator(fault.Unavailable, "the dependency is down")
)

// TestEntryMatchesItselfAndItsKind checks the two comparisons a caller makes: against the
// specific condition it knows, and against the classification every caller shares.
func TestEntryMatchesItselfAndItsKind(t *testing.T) {
	if entry.Error() != "the thing is missing" {
		t.Fatal("message changed", entry.Error())
	}
	if !errors.Is(entry, entry) || !errors.Is(entry, fault.NotFound) {
		t.Fatal("entry did not match itself or its kind")
	}
	// A different condition of a different kind must not match, or classification
	// would be meaningless.
	if errors.Is(entry, other) || errors.Is(entry, fault.InvalidArgument) {
		t.Fatal("entry matched an unrelated condition or kind")
	}
	if entry.Code() != "DM-TEST-MISSING" || entry.Kind() != fault.NotFound || entry.Audience() != fault.Client {
		t.Fatal("code, kind or audience changed", entry.Code(), entry.Kind(), entry.Audience())
	}
}

// TestAudiencesAreEnforcedByConstruction covers what each constructor accepts: an
// operator condition has no code, a client code has one form, a device code is Apple's.
func TestAudiencesAreEnforcedByConstruction(t *testing.T) {
	if oper.Code() != "" || oper.Audience() != fault.Operator {
		t.Fatal("operator condition carries a code or the wrong audience")
	}
	if device.Code() != "com.example.test.refused" || device.Audience() != fault.Device {
		t.Fatal("device condition lost its Apple code or audience")
	}
	if bare.Code() != "" || bare.Audience() != fault.Device || !errors.Is(bare, fault.Gone) {
		t.Fatal("codeless device condition changed")
	}
	for name, fn := range map[string]func(){
		"ClientCodeWithoutPrefix": func() { _ = fault.NewClient("TEST-MISSING", fault.NotFound, "x") },
		"ClientCodeLowerCase":     func() { _ = fault.NewClient("DM-test-missing", fault.NotFound, "x") },
		"ClientCodeOneSegment":    func() { _ = fault.NewClient("DM-MISSING", fault.NotFound, "x") },
		"DeviceCodeIsDM":          func() { _ = fault.NewDevice("DM-TEST-DEVICE", fault.Gone, "x") },
		"NilKind":                 func() { _ = fault.NewOperator(nil, "x") },
		"DuplicateDiffers":        func() { _ = fault.NewClient("DM-TEST-MISSING", fault.Conflict, "the thing is missing") },
		"DuplicateIdentical":      func() { _ = fault.NewClient("DM-TEST-MISSING", fault.NotFound, "the thing is missing") },
		"DuplicateDeviceCode": func() {
			_ = fault.NewDevice("com.example.test.refused", fault.PermissionDenied, "the device is refused")
		},
		"NewWithoutEntry": func() { _ = fault.New(nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			fn()
		})
	}
	if got, ok := fault.Lookup("DM-TEST-MISSING"); !ok || got != entry {
		t.Fatal("catalogue does not hold the declaration")
	}
	for _, a := range []fault.Audience{fault.Operator, fault.Client, fault.Device, fault.Audience(0)} {
		if a.String() == "" {
			t.Fatal("audience without a name", int(a))
		}
	}
}

// TestCatalogueListsCodedConditionsSorted checks the enumeration a documentation table
// and a client SDK are generated from.
func TestCatalogueListsCodedConditionsSorted(t *testing.T) {
	all := fault.Catalogue()
	codes := make([]string, 0, len(all))
	for _, e := range all {
		if e.Code() == "" || e.Audience() == fault.Operator {
			t.Fatal("catalogue listed an uncoded or operator condition", e)
		}
		codes = append(codes, string(e.Code()))
	}
	if !slices.IsSorted(codes) {
		t.Fatal("catalogue is not sorted", codes)
	}
	for _, want := range []string{"DM-TEST-MISSING", "com.example.test.refused", "DM-RECORD-NOT-FOUND"} {
		if !slices.Contains(codes, want) {
			t.Fatal("catalogue is missing", want)
		}
	}
	if _, ok := fault.Lookup("DM-TEST-NEVER"); ok {
		t.Fatal("Lookup found an undeclared code")
	}
	// Every shipped client code has the published form and a message a person can read.
	for _, e := range all {
		if strings.HasPrefix(string(e.Code()), "DM-") && e.Audience() != fault.Client {
			t.Fatal("a DM- code is not a client condition", e.Code())
		}
		if strings.TrimSpace(e.Error()) == "" {
			t.Fatal("empty message", e.Code())
		}
	}
}

// TestFirstOperandClassifies pins the rule every wrap site relies on: when two
// classified errors are joined with "%w: %w", the first operand decides the code and
// the kind, because the lookups walk the tree depth-first and in operand order. A
// package that re-classifies a cause therefore writes its own condition first.
func TestFirstOperandClassifies(t *testing.T) {
	err := fmt.Errorf("%w: %w", other, entry)
	if fault.CodeOf(err) != "DM-TEST-REJECTED" {
		t.Fatal("the second operand won", fault.CodeOf(err))
	}
	if fault.KindOf(err) != fault.InvalidArgument {
		t.Fatal("the second operand's kind won", fault.KindOf(err))
	}
	// Both conditions remain matchable: re-classifying does not hide the cause.
	if !errors.Is(err, entry) || !errors.Is(err, other) {
		t.Fatal("a joined operand stopped matching")
	}
}
