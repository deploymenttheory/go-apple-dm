package schemagen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func osTree(entries map[string]map[string]string) *Tree {
	t := &Tree{}
	for title, byOS := range entries {
		s := &Schema{Title: title}
		for family, introduced := range byOS {
			v := &OSSupport{Introduced: introduced}
			switch family {
			case "iOS":
				s.Payload.SupportedOS.IOS = v
			case "macOS":
				s.Payload.SupportedOS.MacOS = v
			case "tvOS":
				s.Payload.SupportedOS.TvOS = v
			case "visionOS":
				s.Payload.SupportedOS.VisionOS = v
			case "watchOS":
				s.Payload.SupportedOS.WatchOS = v
			}
		}
		t.Schemas = append(t.Schemas, s)
	}
	return t
}

// TestNewestIntroducedTakesTheHighestPerFamily is what an update is reported
// in terms of: a pin moving iOS from 26.0 to 26.4 says more than a commit hash.
func TestNewestIntroducedTakesTheHighestPerFamily(t *testing.T) {
	t.Parallel()
	got := NewestIntroduced(osTree(map[string]map[string]string{
		"old":    {"iOS": "26.0", "macOS": "15.0"},
		"newer":  {"iOS": "26.4", "macOS": "14.0"},
		"absent": {"tvOS": "n/a", "watchOS": ""},
		"double": {"iOS": "9.3"},
	}))
	for family, want := range map[string]string{"iOS": "26.4", "macOS": "15.0"} {
		if got[family] != want {
			t.Errorf("NewestIntroduced()[%s] = %q, want %q", family, got[family], want)
		}
	}
	// "n/a" is Apple's way of saying a schema never shipped on an OS, so the
	// family must be absent rather than present and empty: a caller has to be
	// able to tell "no support" from "supported since the beginning".
	for _, family := range []string{"tvOS", "watchOS", "visionOS"} {
		if v, ok := got[family]; ok {
			t.Errorf("NewestIntroduced()[%s] = %q, want the family to be absent", family, v)
		}
	}
}

func TestNewestIntroducedOnNothing(t *testing.T) {
	t.Parallel()
	if got := NewestIntroduced(nil); len(got) != 0 {
		t.Errorf("NewestIntroduced(nil) = %v, want empty", got)
	}
	if got := NewestOSVersion(nil); got != "" {
		t.Errorf("NewestOSVersion(nil) = %q, want empty", got)
	}
}

func TestNewestOSVersionAcrossFamilies(t *testing.T) {
	t.Parallel()
	got := NewestOSVersion(osTree(map[string]map[string]string{
		"a": {"iOS": "26.4", "tvOS": "18.4"},
		"b": {"watchOS": "10.0"},
	}))
	if got != "26.4" {
		t.Errorf("NewestOSVersion = %q, want 26.4", got)
	}
}

// TestCompareVersionsOrdersNumerically covers the part that would otherwise
// sort 26.10 below 26.9 as strings, and the unparsable component that must not
// be able to claim to be the newest version Apple ships.
func TestCompareVersionsOrdersNumerically(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"26.4", "26.0", 1},
		{"26.10", "26.9", 1},
		{"26.0", "26.0.0", 0},
		{"26", "26.1", -1},
		{"", "26.0", -1},
		{"", "", 0},
		{"nonsense", "26.0", -1},
		{"18.4", "26.4", -1},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestYAMLSHA256IdentifiesTheSchema proves the digest follows the YAML rather
// than the checkout: the same files in a different directory hash the same,
// and a changed byte or a renamed file does not.
func TestYAMLSHA256IdentifiesTheSchema(t *testing.T) {
	t.Parallel()
	write := func(dir string, files map[string]string) string {
		t.Helper()
		for name, body := range files {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		sum, err := yamlSHA256(dir)
		if err != nil {
			t.Fatalf("yamlSHA256: %v", err)
		}
		return sum
	}
	base := map[string]string{"a/one.yaml": "x: 1", "b/two.yaml": "y: 2", "notes.md": "ignored"}
	first := write(t.TempDir(), base)
	same := write(t.TempDir(), base)
	if first != same {
		t.Error("the same schema in a different directory hashed differently")
	}
	changed := write(t.TempDir(), map[string]string{"a/one.yaml": "x: 2", "b/two.yaml": "y: 2"})
	if first == changed {
		t.Error("a changed YAML byte did not change the digest")
	}
	renamed := write(t.TempDir(), map[string]string{"a/renamed.yaml": "x: 1", "b/two.yaml": "y: 2"})
	if first == renamed {
		t.Error("a renamed YAML file did not change the digest: the path is part of the schema")
	}
}

func TestYAMLSHA256OnAMissingDirectory(t *testing.T) {
	t.Parallel()
	if _, err := yamlSHA256(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("yamlSHA256 on a missing directory returned no error")
	}
}

// TestDescribeIsAFunctionOfTheCheckout is the property make verify relies on:
// regenerating the same pin must produce the same bytes, or the determinism
// check would have to skip this file, which is how the hand-maintained version
// came to record a commit the tree had moved past.
func TestDescribeIsAFunctionOfTheCheckout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.yaml"), []byte("x: 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree := osTree(map[string]map[string]string{"a": {"iOS": "26.4"}})
	first, err := describe(dir, tree, "abc123")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	second, err := describe(dir, tree, "abc123")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if string(first) != string(second) {
		t.Error("describe is not deterministic")
	}
	var got GeneratedFrom
	if err := json.Unmarshal(first, &got); err != nil {
		t.Fatalf("describe produced invalid JSON: %v", err)
	}
	if got.Commit != "abc123" || got.OSVersions != "26.4" || got.Ref != upstreamRef {
		t.Errorf("describe = %+v, want the commit, versions and ref of the checkout", got)
	}
	if got.YAMLSHA256 == "" || got.Generator == "" || got.Source == "" {
		t.Errorf("describe left a field empty: %+v", got)
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Error("describe should end with a newline so the file is diffable")
	}
}

func TestDescribeOnAMissingCheckout(t *testing.T) {
	t.Parallel()
	if _, err := describe(filepath.Join(t.TempDir(), "absent"), nil, "abc"); err == nil {
		t.Fatal("describe on a missing checkout returned no error")
	}
}

// TestGitHelpersOnANonCheckout covers the fallback: a schema root that is not
// a git checkout, which is how the tests and any vendored copy run.
func TestGitHelpersOnANonCheckout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := gitHEAD(dir); got != "" {
		t.Errorf("gitHEAD on a non-checkout = %q, want empty", got)
	}
	if got := gitCommitDate(dir); got != "" {
		t.Errorf("gitCommitDate on a non-checkout = %q, want empty", got)
	}
}
