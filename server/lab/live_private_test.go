package lab

import (
	"errors"
	"strings"
	"testing"
)

// TestLiveAreaPrivateFailures reports unopenable private input and an
// uncreatable evidence scope as failed operations without private paths.
func TestLiveAreaPrivateFailures(t *testing.T) {
	s, _ := appsFixtureSession(t)
	if _, err := appsArea.read(s, "invalid\x00name", "missing"); !errors.Is(err, errOperation) || errors.Is(err, ErrBlocked) || strings.Contains(err.Error(), s.Env.Workspace.Directory) {
		t.Fatal("unopenable private input was not a private operation failure", err)
	}
	// A component longer than the file system name limit cannot be created.
	if _, _, err := adeArea.scope(s, strings.Repeat("x", 300)); !errors.Is(err, errOperation) || strings.Contains(err.Error(), s.Evidence) {
		t.Fatal("uncreatable evidence scope was not a private operation failure", err)
	}
}
