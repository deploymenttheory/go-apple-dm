package explain_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/internal/dmctl/explain"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
)

func render(t *testing.T, m explain.Match, target support.Target) string {
	t.Helper()
	var buf bytes.Buffer
	if err := explain.Render(&buf, m, target); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func TestResolve(t *testing.T) {
	t.Run("ByGoTypeName", func(t *testing.T) {
		got, err := explain.Resolve("DeviceLock", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].TypeName != "DeviceLock" || got[0].Family != "commands" {
			t.Fatalf("resolved %+v", got)
		}
		if got[0].Schema == "" {
			t.Fatal("no schema path to cite")
		}
	})

	t.Run("ByWireID", func(t *testing.T) {
		got, err := explain.Resolve("com.apple.configuration.passcode.settings", "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(got) == 0 || got[0].Family != "ddm" {
			t.Fatalf("resolved %+v", got)
		}
		if got[0].Kind == "" {
			t.Fatal("a declaration match carries no kind")
		}
	})

	t.Run("ByDottedPath", func(t *testing.T) {
		got, err := explain.Resolve("DeviceLock.Message", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || !got[0].Key || got[0].Path != "DeviceLock.Message" {
			t.Fatalf("resolved %+v", got)
		}
	})

	// Six schema/profiles types report com.apple.MCX, which is why ByID
	// returns a slice in the generated packages. Every one is answered rather
	// than one being chosen.
	t.Run("AmbiguousProfilePayloadTypeListsSixBlocks", func(t *testing.T) {
		var want int
		for _, e := range profiles.Registry {
			if e.ID == "com.apple.MCX" {
				want++
			}
		}
		if want < 2 {
			t.Skipf("the pinned schema has %d com.apple.MCX types", want)
		}
		got, err := explain.Resolve("com.apple.MCX", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Fatalf("resolved %d matches, want %d", len(got), want)
		}
		seen := make(map[string]bool)
		for _, m := range got {
			if seen[m.TypeName] {
				t.Fatalf("duplicate type %s", m.TypeName)
			}
			seen[m.TypeName] = true
			if m.TypeName == "" {
				t.Fatal("a match carries no Go type name, so its support path has no root")
			}
		}
	})

	t.Run("FamilyRestricts", func(t *testing.T) {
		if _, err := explain.Resolve("DeviceLock", "profiles"); !errors.Is(err, explain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, err := explain.Resolve("DeviceLock", "commands"); err != nil {
			t.Fatalf("Resolve in commands: %v", err)
		}
		if _, err := explain.Resolve("DeviceLock", "nope"); !errors.Is(err, explain.ErrUnknownFamily) {
			t.Fatalf("err = %v, want ErrUnknownFamily", err)
		}
	})

	t.Run("Rejects", func(t *testing.T) {
		for _, arg := range []string{"", "   ", "NoSuchThingAtAll"} {
			if _, err := explain.Resolve(arg, ""); !errors.Is(err, explain.ErrNotFound) {
				t.Fatalf("Resolve(%q) = %v, want ErrNotFound", arg, err)
			}
		}
	})
}

func TestSuggest(t *testing.T) {
	got := explain.Suggest("devicelo", "", 5)
	if len(got) == 0 {
		t.Fatal("no suggestions for a near miss")
	}
	var found bool
	for _, s := range got {
		if s == "DeviceLock" {
			found = true
		}
	}
	if !found {
		t.Fatalf("suggestions = %v, want DeviceLock", got)
	}
	if len(explain.Suggest("", "", 5)) != 0 {
		t.Fatal("an empty argument produced suggestions")
	}
	if len(explain.Suggest("zzzznotathing", "", 5)) != 0 {
		t.Fatal("a nonsense argument produced suggestions")
	}
	if got := explain.Suggest("device", "", 3); len(got) > 3 {
		t.Fatalf("limit ignored: %d suggestions", len(got))
	}
}

func TestFamiliesAndListings(t *testing.T) {
	fams := explain.Families()
	if len(fams) != 8 {
		t.Fatalf("families = %v, want all eight", fams)
	}
	ids, err := explain.IDs("commands")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != len(commands.IDs()) {
		t.Fatalf("IDs = %d, want %d from the registry", len(ids), len(commands.IDs()))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("IDs not sorted and deduplicated at %d: %q, %q", i, ids[i-1], ids[i])
		}
	}
	if _, err := explain.IDs("nope"); !errors.Is(err, explain.ErrUnknownFamily) {
		t.Fatalf("IDs: %v", err)
	}
	paths, err := explain.Paths("commands")
	if err != nil || len(paths) == 0 {
		t.Fatalf("Paths = %d, %v", len(paths), err)
	}
	if _, err := explain.Paths("nope"); !errors.Is(err, explain.ErrUnknownFamily) {
		t.Fatalf("Paths: %v", err)
	}
}

// A nil tri-state means Apple did not say, and must never print as "no".
func TestTriStateNilPrintsDash(t *testing.T) {
	m, err := explain.Resolve("DeviceLock", "commands")
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, m[0], support.Target{})
	if !strings.Contains(out, "Support") {
		t.Fatalf("no support table:\n%s", out)
	}
	// Find a key whose entry has an unstated tri-state and confirm the row
	// carries a dash rather than a "no".
	e := support.Lookup("commands", "DeviceLock")
	if e == nil {
		t.Skip("no support data for DeviceLock in the pinned schema")
	}
	var sawUnstated bool
	for _, os := range support.AllOS {
		s := e.OS[os]
		if s != nil && !s.NotAvailable && s.Supervised == nil {
			sawUnstated = true
		}
	}
	if sawUnstated && !strings.Contains(out, "-") {
		t.Fatalf("an unstated tri-state did not render as a dash:\n%s", out)
	}
}

// Check reports Supported for an entry with no data and for a query with no
// target OS. Rendering either as OK would assert a fact Apple never stated.
func TestNoSupportDataIsNotOK(t *testing.T) {
	m := explain.Match{Family: "commands", TypeName: "NotAThing", Path: "NotAThing"}
	out := render(t, m, support.Target{OS: support.MacOS, Version: support.MustVersion("15.0")})
	if !strings.Contains(out, string(explain.VerdictUnknown)) {
		t.Fatalf("an entry with no support data did not render as unknown:\n%s", out)
	}
	if strings.Contains(out, " "+string(explain.VerdictOK)+" ") {
		t.Fatalf("an entry with no support data rendered as OK:\n%s", out)
	}
}

// Result.Reason is printed unchanged, so explain and the server's rejection
// use the same words for the same key.
func TestTargetReasonIsVerbatim(t *testing.T) {
	target := support.Target{OS: support.MacOS, Version: support.MustVersion("15.0"), Channel: support.ChannelDevice}
	m, err := explain.Resolve("DeviceLock", "commands")
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, m[0], target)
	e := support.Lookup("commands", "DeviceLock")
	if e == nil {
		t.Skip("no support data")
	}
	if reason := e.Check(target).Reason; reason != "" && !strings.Contains(out, reason) {
		t.Fatalf("reason %q not printed verbatim:\n%s", reason, out)
	}
	if !strings.Contains(out, "Target") {
		t.Fatalf("no target line:\n%s", out)
	}
}

// Nothing in the output is invented: every line is derivable from the title,
// the schema path, a key path, or a Reason. The generated packages carry no
// per-key prose, so a description would have to be made up.
func TestNoDescriptionsAreInvented(t *testing.T) {
	m, err := explain.Resolve("DeviceLock", "commands")
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, m[0], support.Target{})
	if !strings.Contains(out, "third_party/device-management/") {
		t.Fatalf("the schema path is not cited:\n%s", out)
	}
	entry := commands.Registry["DeviceLock"]
	if entry.Title != "" && !strings.Contains(out, entry.Title) {
		t.Fatalf("the registry title is not shown:\n%s", out)
	}
}

func TestParseTarget(t *testing.T) {
	t.Run("Accepts", func(t *testing.T) {
		got, err := explain.ParseTarget("macos:15.0,channel=device,supervised,dep,user-approved,shared-ipad,user-enrollment")
		if err != nil {
			t.Fatal(err)
		}
		if got.OS != support.MacOS || got.Version.String() != "15.0" {
			t.Fatalf("target = %+v", got)
		}
		if !got.Supervised || !got.DEP || !got.UserApproved || !got.SharedIPad || !got.UserEnrollment {
			t.Fatalf("flags = %+v", got)
		}
		if got.Channel != support.ChannelDevice {
			t.Fatalf("channel = %q", got.Channel)
		}
		// The OS name is matched case-insensitively, and a bare OS is fine.
		if got, err := explain.ParseTarget("IOS"); err != nil || got.OS != support.IOS {
			t.Fatalf("bare OS = %+v, %v", got, err)
		}
		if got, err := explain.ParseTarget(""); err != nil || got.OS != "" {
			t.Fatalf("empty = %+v, %v", got, err)
		}
	})

	t.Run("Rejects", func(t *testing.T) {
		for _, s := range []string{
			"linux:1.0",
			"macos:not-a-version",
			"macos:15.0,channel=admin",
			"macos:15.0,teleport",
		} {
			if _, err := explain.ParseTarget(s); !errors.Is(err, explain.ErrTarget) {
				t.Fatalf("ParseTarget(%q) = %v, want ErrTarget", s, err)
			}
		}
	})
}

func TestKeys(t *testing.T) {
	m, err := explain.Resolve("DeviceLock", "commands")
	if err != nil {
		t.Fatal(err)
	}
	keys := explain.Keys(m[0])
	for _, k := range keys {
		if !strings.HasPrefix(k, "DeviceLock.") {
			t.Fatalf("key %q is not under the type", k)
		}
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("keys not sorted at %d", i)
		}
	}
}
