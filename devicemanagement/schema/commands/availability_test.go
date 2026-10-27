package commands_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// TestSettingsVariantsUseTheirOwnAvailability checks that a Settings item is
// validated against its variant's own availability: accessibility settings
// exist on iOS 16 and watchOS 10, never on macOS.
func TestSettingsVariantsUseTheirOwnAvailability(t *testing.T) {
	t.Parallel()
	bold := true
	cmd := commands.Settings{Settings: []any{&commands.SettingsSettingsAccessibilitySettings{Item: "AccessibilitySettings", BoldTextEnabled: &bold}}}
	for _, c := range []struct {
		target support.Target
		reject bool
	}{
		{support.Target{OS: support.IOS, Version: osversion.MustParse("16.0"), Supervised: true}, false},
		{support.Target{OS: support.IOS, Version: osversion.MustParse("15.7"), Supervised: true}, true},
		{support.Target{OS: support.MacOS, Version: osversion.MustParse("26.4")}, true},
	} {
		err := cmd.Validate(c.target)
		var issues validation.Errors
		if err != nil && !errors.As(err, &issues) {
			t.Fatalf("%s %s: unexpected error type: %v", c.target.OS, c.target.Version, err)
		}
		rejected := false
		for _, issue := range issues {
			rejected = rejected || strings.HasPrefix(issue.Path, "Settings[0]")
		}
		if rejected != c.reject {
			t.Errorf("%s %s: rejected = %v, want %v (%v)", c.target.OS, c.target.Version, rejected, c.reject, err)
		}
	}
}
