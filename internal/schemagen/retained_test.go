package schemagen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeGitmodules writes a .gitmodules fixture and returns its path.
func writeGitmodules(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".gitmodules")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// submodule renders one .gitmodules entry.
func submodule(name, path string) string {
	return "[submodule \"" + name + "\"]\n\tpath = " + path + "\n\turl = https://example.invalid/apple\n"
}

// TestRetainedReleasesOrdersVersionedSiblingsNewestFirst checks that only versioned checkouts beside
// the current checkout are retained, ordered by release version rather than name.
func TestRetainedReleasesOrdersVersionedSiblingsNewestFirst(t *testing.T) {
	t.Parallel()
	gitmodules := writeGitmodules(t, submodule("apple-device-management-26.4", "third_party/apple-device-management/26.4")+
		submodule("apple-device-management-current", "third_party/apple-device-management/current")+
		submodule("apple-device-management-9.3", "third_party/apple-device-management/9.3")+
		submodule("apple-device-management-27.4", "third_party/apple-device-management/27.4")+
		submodule("unrelated", "third_party/other/1.0"))
	got, err := RetainedReleases(gitmodules)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(gitmodules)
	want := []string{
		filepath.Join(dir, "third_party", "apple-device-management", "27.4"),
		filepath.Join(dir, "third_party", "apple-device-management", "26.4"),
		filepath.Join(dir, "third_party", "apple-device-management", "9.3"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RetainedReleases = %q, want %q", got, want)
	}
}

// TestRetainedReleasesFailures checks missing, empty, malformed and misnamed configurations.
func TestRetainedReleasesFailures(t *testing.T) {
	t.Parallel()
	if _, err := RetainedReleases(filepath.Join(t.TempDir(), ".gitmodules")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	for name, content := range map[string]string{
		"no submodules": "",
		"no current":    submodule("apple-device-management-26.4", "third_party/apple-device-management/26.4"),
		"current alone": submodule("apple-device-management-current", "third_party/apple-device-management/current"),
	} {
		if got, err := RetainedReleases(writeGitmodules(t, content)); err != nil || len(got) != 0 {
			t.Errorf("%s: RetainedReleases = %q, %v", name, got, err)
		}
	}
	if _, err := RetainedReleases(writeGitmodules(t, "[submodule \"broken\"\n\tpath = x\n")); err == nil ||
		errors.Is(err, ErrRetainedRelease) {
		t.Fatalf("malformed file: %v", err)
	}
	misnamed := submodule("apple-device-management-current", "third_party/apple-device-management/current") +
		submodule("apple-device-management-compatibility", "third_party/apple-device-management/compatibility")
	if _, err := RetainedReleases(writeGitmodules(t, misnamed)); !errors.Is(err, ErrRetainedRelease) {
		t.Fatalf("misnamed release: %v", err)
	}
}

// TestRepositoryRetainsInitializedReleases requires every retained release named in the
// repository's .gitmodules to be checked out, so missing source fails instead of silently
// dropping older-device support from generation.
func TestRepositoryRetainsInitializedReleases(t *testing.T) {
	t.Parallel()
	retained, err := RetainedReleases(filepath.Join("..", "..", ".gitmodules"))
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) == 0 {
		t.Fatal("no earlier major schema release is retained")
	}
	for _, root := range retained {
		if _, err := os.Stat(filepath.Join(root, "docs", "schema.yaml")); err != nil {
			t.Errorf("retained Apple schema unavailable (run git submodule update --init): %v", err)
		}
	}
}
