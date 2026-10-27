package profiles_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// TestRecursiveArrayValuesAreValidated validates values that Apple nests in
// recursive arrays and arrays of arrays: parental-controls helper applications,
// and Home Screen icons inside pages and folders. Each value is checked like any
// other, against the availability of the use that encloses it.
func TestRecursiveArrayValuesAreValidated(t *testing.T) {
	t.Parallel()
	mac := support.Target{OS: support.MacOS, Version: osversion.MustParse("26.0"), Channel: support.ChannelDevice, UserApproved: true}
	allowList := func(helper string) *profiles.ParentalControlsApplicationRestrictions {
		return &profiles.ParentalControlsApplicationRestrictions{AllowList: []profiles.ParentalControlsApplicationRestrictionsAllowListAllowListItem{{
			BundleID: "com.example.app", AppID: []byte{1},
			SubApps: []profiles.ParentalControlsApplicationRestrictionsAllowListAllowListItem{{BundleID: helper, AppID: []byte{1}}},
		}}}
	}
	whiteList := func(helper string) *profiles.ParentalControlsApplicationRestrictions {
		return &profiles.ParentalControlsApplicationRestrictions{WhiteList: []profiles.ParentalControlsApplicationRestrictionsApplicationItem{{
			BundleID: "com.example.app", AppID: []byte{1},
			SubApps: []profiles.ParentalControlsApplicationRestrictionsApplicationItem{{BundleID: helper, AppID: []byte{1}}},
		}}}
	}
	// Web clip URLs are available from iOS 11.3 in every icon use.
	webClip := new("https://example.com")
	pages := &profiles.HomeScreenLayout{Pages: [][]profiles.HomeScreenLayoutPagesItemIconItem{{{Type: "WebClip", URL: webClip}}}}
	pageFolder := &profiles.HomeScreenLayout{Pages: [][]profiles.HomeScreenLayoutPagesItemIconItem{{{
		Type: "Folder", Pages: [][]profiles.HomeScreenLayoutPagesItemIconItem{{{Type: "WebClip", URL: webClip}}},
	}}}}
	app := [][]profiles.HomeScreenLayoutPagesItemIconItem{{{Type: "Application", BundleID: new("com.example.app")}}}
	dockFolder := &profiles.HomeScreenLayout{Pages: app, Dock: []profiles.HomeScreenLayoutIconItem{{
		Type: "Folder", Pages: [][]profiles.HomeScreenLayoutIconItem{{{Type: "WebClip", URL: webClip}}},
	}}}
	unknownType := &profiles.HomeScreenLayout{Pages: [][]profiles.HomeScreenLayoutPagesItemIconItem{{{Type: "Widget"}}}}
	iOS := func(version string) support.Target {
		return support.Target{OS: support.IOS, Version: osversion.MustParse(version), Channel: support.ChannelDevice, Supervised: true}
	}
	for _, c := range []struct {
		name    string
		payload interface{ Validate(support.Target) error }
		target  support.Target
		path    string
		reject  bool
	}{
		{"helper without bundle ID", allowList(""), mac, "allowList[0].subApps[0].bundleID", true},
		{"helper with bundle ID", allowList("com.example.helper"), mac, "allowList[0].subApps[0]", false},
		{"legacy helper without bundle ID", whiteList(""), mac, "whiteList[0].subApps[0].bundleID", true},
		{"legacy helper with bundle ID", whiteList("com.example.helper"), mac, "whiteList[0].subApps[0]", false},
		{"web clip on a page before 11.3", pages, iOS("11.0"), "Pages[0][0].URL", true},
		{"web clip on a page from 11.3", pages, iOS("11.3"), "Pages[0][0]", false},
		{"web clip in a page folder before 11.3", pageFolder, iOS("11.0"), "Pages[0][0].Pages[0][0].URL", true},
		{"web clip in a page folder from 11.3", pageFolder, iOS("11.3"), "Pages[0][0].Pages", false},
		{"web clip in a Dock folder before 11.3", dockFolder, iOS("11.0"), "Dock[0].Pages[0][0].URL", true},
		{"web clip in a Dock folder from 11.3", dockFolder, iOS("11.3"), "Dock[0].Pages", false},
		{"unknown icon type on a page", unknownType, iOS("17.0"), "Pages[0][0].Type", true},
	} {
		err := c.payload.Validate(c.target)
		if got := reportsUnder(t, err, c.path); got != c.reject || (!c.reject && err != nil) {
			t.Errorf("%s: rejected %s = %v, want %v (%v)", c.name, c.path, got, c.reject, err)
		}
	}
}
