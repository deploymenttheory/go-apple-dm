package buildinfo

import (
	"runtime/debug"
	"testing"
)

// TestVersion checks checkout version discovery and explicit release stamps.
func TestVersion(t *testing.T) {
	if Version() == "" {
		t.Fatal("checkout build has no version")
	}
	original := releaseVersion
	t.Cleanup(func() { releaseVersion = original })
	releaseVersion = "0.9.1"
	if got := Version(); got != "0.9.1" {
		t.Fatalf("release stamp = %q", got)
	}
}

// TestModuleVersion checks module version selection from build metadata.
func TestModuleVersion(t *testing.T) {
	for _, tc := range []struct {
		info *debug.BuildInfo
		want string
	}{
		{nil, "devel"},
		{&debug.BuildInfo{}, "devel"},
		{&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "(devel)"},
		{&debug.BuildInfo{Main: debug.Module{Version: "v0.9.1"}}, "v0.9.1"},
	} {
		if got := moduleVersion(tc.info); got != tc.want {
			t.Errorf("moduleVersion(%v) = %q, want %q", tc.info, got, tc.want)
		}
	}
}
