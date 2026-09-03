package explain_test

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/internal/mdmctl/explain"
	"github.com/deploymenttheory/go-apple-mdm/schema/support"
)

// A key match renders the key as its heading rather than the type, and grades
// only that key's subtree.
func TestRenderKeyMatch(t *testing.T) {
	m, err := explain.Resolve("DeviceLock.Message", "commands")
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, m[0], support.Target{})
	if !strings.HasPrefix(out, "DeviceLock.Message") {
		t.Fatalf("heading is not the key:\n%s", out)
	}
	target, err := explain.ParseTarget("ios:18.0")
	if err != nil {
		t.Fatal(err)
	}
	graded := render(t, m[0], target)
	if !strings.Contains(graded, "DeviceLock.Message") {
		t.Fatalf("the key was not graded:\n%s", graded)
	}
}

// A match with no support entry says so rather than printing an empty table.
func TestRenderWithoutSupportData(t *testing.T) {
	m := explain.Match{Family: "commands", TypeName: "NotAThing", Path: "NotAThing"}
	out := render(t, m, support.Target{})
	if !strings.Contains(out, "No support data") {
		t.Fatalf("expected a no-data line:\n%s", out)
	}
}

// A declaration match shows its kind and its wire id, which a command match
// has no need of.
func TestRenderDeclaration(t *testing.T) {
	got, err := explain.Resolve("com.apple.configuration.passcode.settings", "ddm")
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, got[0], support.Target{})
	if !strings.Contains(out, "Kind:") {
		t.Fatalf("no kind line:\n%s", out)
	}
	if !strings.Contains(out, "Id:") {
		t.Fatalf("no id line:\n%s", out)
	}
}

// Every flag reaches the target line, so an operator can see what they asked
// about rather than guessing from the flags they typed.
func TestRenderTargetDescription(t *testing.T) {
	m, err := explain.Resolve("DeviceLock", "commands")
	if err != nil {
		t.Fatal(err)
	}
	target, err := explain.ParseTarget("ios:18.0,channel=user,supervised,shared-ipad,user-enrollment,dep,user-approved")
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, m[0], target)
	for _, want := range []string{"iOS 18.0", "user channel", "supervised", "Shared iPad", "user enrollment", "DEP", "user-approved"} {
		if !strings.Contains(out, want) {
			t.Fatalf("target line is missing %q:\n%s", want, out)
		}
	}
}

// A removed key reports the range it was available for, and a key Apple never
// shipped on any OS says so, rather than both rendering as blank.
func TestAvailabilityWording(t *testing.T) {
	var removed, notAvailable string
	for _, family := range explain.Families() {
		paths, err := explain.Paths(family)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range paths {
			e := support.Lookup(family, p)
			if e == nil {
				continue
			}
			var anyAvailable bool
			for _, os := range support.AllOS {
				s := e.OS[os]
				if s == nil || s.NotAvailable {
					continue
				}
				anyAvailable = true
				if !s.Removed.IsZero() && removed == "" {
					removed = family + "|" + p
				}
			}
			if !anyAvailable && notAvailable == "" && len(e.OS) > 0 {
				notAvailable = family + "|" + p
			}
		}
	}
	check := func(spec, want string) {
		if spec == "" {
			return
		}
		family, path, _ := strings.Cut(spec, "|")
		root, _, _ := strings.Cut(path, ".")
		m := explain.Match{Family: family, TypeName: root, Path: root}
		out := render(t, m, support.Target{})
		if !strings.Contains(out, want) {
			t.Fatalf("%s: output does not contain %q:\n%s", spec, want, out)
		}
	}
	check(removed, " to ")
	check(notAvailable, "not available")
}
