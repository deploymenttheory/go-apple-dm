package support_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// entry returns support metadata covering version, enrollment, supervision, and channel
// restrictions.
func entry() *support.Entry {
	return &support.Entry{
		Path: "DeviceLock.Message",
		OS: map[support.OS]*support.OSSupport{
			support.IOS: {
				Introduced: osversion.New(7, 0, 0), Deprecated: osversion.New(20, 0, 0), Removed: osversion.New(25, 0, 0),
				Supervised: support.Bool(true), SharedIPadMode: support.ModeIgnored, UserEnrollmentMode: support.ModeForbidden,
				DeviceChannel: support.Bool(true), UserChannel: support.Bool(false),
			},
			support.MacOS: {
				Introduced: osversion.New(10, 14, 0), RequiresDEP: support.Bool(true), UserApprovedMDM: support.Bool(true),
				SharedIPadDeviceChannel: support.Bool(false), SharedIPadUserChannel: support.Bool(false),
			},
			support.TvOS:     {NotAvailable: true},
			support.VisionOS: {SharedIPadMode: support.ModeRequired},
		},
	}
}

// TestCheck checks version, enrollment, channel, and supervision support rules with explanatory
// rejection reasons.
func TestCheck(t *testing.T) {
	t.Parallel()
	e := entry()
	cases := []struct {
		name       string
		target     support.Target
		supported  bool
		deprecated bool
		rule       support.Rule
	}{
		{"nil entry", support.Target{OS: support.IOS}, true, false, ""},
		{"no target OS", support.Target{}, true, false, ""},
		{"not available", support.Target{OS: support.TvOS}, false, false, support.RuleNotAvailable},
		{"unknown OS", support.Target{OS: support.WatchOS}, false, false, support.RuleNotAvailable},
		{"before introduced", support.Target{OS: support.IOS, Version: osversion.New(6, 0, 0), Supervised: true}, false, false, support.RuleIntroduced},
		{"supported", support.Target{OS: support.IOS, Version: osversion.New(15, 0, 0), Supervised: true}, true, false, ""},
		{"deprecated", support.Target{OS: support.IOS, Version: osversion.New(21, 0, 0), Supervised: true}, true, true, support.RuleDeprecated},
		{"removed", support.Target{OS: support.IOS, Version: osversion.New(25, 0, 0), Supervised: true}, false, false, support.RuleRemoved},
		{"unsupervised", support.Target{OS: support.IOS, Version: osversion.New(15, 0, 0)}, false, false, support.RuleContext},
		{"user channel", support.Target{OS: support.IOS, Version: osversion.New(15, 0, 0), Supervised: true, Channel: support.ChannelUser}, false, false, support.RuleChannel},
		{"device channel", support.Target{OS: support.IOS, Version: osversion.New(15, 0, 0), Supervised: true, Channel: support.ChannelDevice}, true, false, ""},
		{"user enrollment forbidden", support.Target{OS: support.IOS, Supervised: true, UserEnrollment: true}, false, false, support.RuleContext},
		{"needs DEP", support.Target{OS: support.MacOS, Version: osversion.New(osversion.MacOS14, 0, 0)}, false, false, support.RuleContext},
		{"needs UAMDM", support.Target{OS: support.MacOS, Version: osversion.New(osversion.MacOS14, 0, 0), DEP: true}, false, false, support.RuleContext},
		{"mac ok", support.Target{OS: support.MacOS, Version: osversion.New(osversion.MacOS14, 0, 0), DEP: true, UserApproved: true}, true, false, ""},
		{"mac shared ipad device channel", support.Target{OS: support.MacOS, DEP: true, UserApproved: true, SharedIPad: true, Channel: support.ChannelDevice}, false, false, support.RuleChannel},
		{"mac shared ipad user channel", support.Target{OS: support.MacOS, DEP: true, UserApproved: true, SharedIPad: true, Channel: support.ChannelUser}, false, false, support.RuleChannel},
		{"shared ipad required", support.Target{OS: support.VisionOS}, false, false, support.RuleContext},
		{"shared ipad required ok", support.Target{OS: support.VisionOS, SharedIPad: true}, true, false, ""},
	}
	for _, c := range cases {
		var r support.Result
		if c.name == "nil entry" {
			r = (*support.Entry)(nil).Check(c.target)
		} else {
			r = e.Check(c.target)
		}
		if r.Supported != c.supported || r.Deprecated != c.deprecated || r.Rule != c.rule {
			t.Errorf("%s: got supported=%v deprecated=%v rule=%q (%s), want %v/%v/%q", c.name, r.Supported, r.Deprecated, r.Rule, r.Reason, c.supported, c.deprecated, c.rule)
		}
		if !r.Supported && r.Reason == "" {
			t.Errorf("%s: unsupported without reason", c.name)
		}
	}
}

// TestRegistry checks support registry lookup, family and path listing, and OS enumeration.
func TestRegistry(t *testing.T) {
	t.Parallel()
	support.Register("testfam", map[string]*support.Entry{"B": entry(), "A": entry()})
	if support.Lookup("testfam", "A") == nil || support.Lookup("testfam", "nope") != nil || support.Lookup("nofam", "A") != nil {
		t.Error("Lookup")
	}
	found := false
	for _, f := range support.Families() {
		if f == "testfam" {
			found = true
		}
	}
	if !found {
		t.Error("Families missing testfam")
	}
	if p := support.Paths("testfam"); len(p) != 2 || p[0] != "A" || p[1] != "B" {
		t.Errorf("Paths = %v", p)
	}
	if len(support.Paths("nofam")) != 0 {
		t.Error("Paths nofam")
	}
	if len(support.AllOS) != 5 {
		t.Error("AllOS")
	}
}

// TestDeclarationContexts checks declaration support across enrollment contexts.
func TestDeclarationContexts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		os     support.OSSupport
		target support.Target
		want   bool
	}{
		{"unspecified metadata", support.OSSupport{}, support.Target{Channel: support.ChannelDevice}, true},
		{"system scope", support.OSSupport{AllowedScopes: []string{"system"}}, support.Target{Channel: support.ChannelDevice}, true},
		{"user scope on device", support.OSSupport{AllowedScopes: []string{"user"}}, support.Target{Channel: support.ChannelDevice}, false},
		{"user scope", support.OSSupport{AllowedScopes: []string{"user"}}, support.Target{Channel: support.ChannelUser}, true},
		{"empty scopes", support.OSSupport{AllowedScopes: []string{}}, support.Target{Channel: support.ChannelDevice}, false},
		{"shared override", support.OSSupport{AllowedScopes: []string{"system"}, SharedIPadScopes: []string{"user"}}, support.Target{Channel: support.ChannelUser, SharedIPad: true}, true},
		{"shared system forbidden", support.OSSupport{AllowedScopes: []string{"system"}, SharedIPadScopes: []string{"user"}}, support.Target{Channel: support.ChannelDevice, SharedIPad: true}, false},
		{"empty shared scopes", support.OSSupport{AllowedScopes: []string{"system"}, SharedIPadScopes: []string{}}, support.Target{Channel: support.ChannelDevice, SharedIPad: true}, false},
		{"shared override inactive", support.OSSupport{AllowedScopes: []string{"system"}, SharedIPadScopes: []string{}}, support.Target{Channel: support.ChannelDevice}, true},
		{"supervision required", support.OSSupport{AllowedEnrollments: []string{"supervised"}}, support.Target{Channel: support.ChannelDevice}, false},
		{"supervised", support.OSSupport{AllowedEnrollments: []string{"supervised"}}, support.Target{Channel: support.ChannelDevice, Supervised: true}, true},
		{"supervised user channel", support.OSSupport{AllowedEnrollments: []string{"supervised"}}, support.Target{Channel: support.ChannelUser, Supervised: true}, true},
		{"device enrollment", support.OSSupport{AllowedEnrollments: []string{"device"}}, support.Target{Channel: support.ChannelUser}, true},
		{"user enrollment", support.OSSupport{AllowedEnrollments: []string{"user"}}, support.Target{Channel: support.ChannelDevice, UserEnrollment: true}, true},
		{"user enrollment is distinct", support.OSSupport{AllowedEnrollments: []string{"supervised"}}, support.Target{Channel: support.ChannelUser, Supervised: true, UserEnrollment: true}, false},
		{"MDM is not local enrollment", support.OSSupport{AllowedEnrollments: []string{"local"}}, support.Target{Channel: support.ChannelDevice}, false},
		{"empty enrollments", support.OSSupport{AllowedEnrollments: []string{}}, support.Target{Channel: support.ChannelDevice}, false},
		{"offline OS only", support.OSSupport{AllowedEnrollments: []string{"supervised"}, AllowedScopes: []string{"user"}}, support.Target{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.target
			target.OS = support.IOS
			entry := support.Entry{Path: "Declaration.Key", OS: map[support.OS]*support.OSSupport{support.IOS: &tc.os}}
			got := entry.Check(target)
			if got.Supported != tc.want || (!tc.want && got.Reason == "") {
				t.Fatalf("Check(%+v) = %+v, want supported=%t", target, got, tc.want)
			}
			// Each rejected case restricts either scopes or enrollments.
			wantRule := support.Rule("")
			if !tc.want {
				wantRule = support.RuleEnrollment
				if tc.os.AllowedScopes != nil {
					wantRule = support.RuleScope
				}
			}
			if got.Rule != wantRule {
				t.Fatalf("Check(%+v) rule = %q, want %q", target, got.Rule, wantRule)
			}
		})
	}
}

// TestTier checks that release 26 and every later release of each OS are
// guaranteed, earlier releases are best effort, and an unknown OS or version
// claims neither tier.
func TestTier(t *testing.T) {
	t.Parallel()
	for os, floor := range map[support.OS]int{
		support.MacOS:    osversion.MacOSSupportedVersionFloor,
		support.IOS:      osversion.IOSSupportedVersionFloor,
		support.TvOS:     osversion.TvOSSupportedVersionFloor,
		support.VisionOS: osversion.VisionOSSupportedVersionFloor,
		support.WatchOS:  osversion.WatchOSSupportedVersionFloor,
	} {
		if floor != 26 {
			t.Errorf("%s supported version floor = %d, want 26", os, floor)
		}
	}
	for _, tc := range []struct {
		os      support.OS
		version string
		want    support.Tier
	}{
		{support.MacOS, "10.15.7", support.TierBestEffort},
		{support.MacOS, "15.7", support.TierBestEffort},
		{support.MacOS, "26.0", support.TierGuaranteed},
		{support.MacOS, "26.4", support.TierGuaranteed},
		{support.MacOS, "27.0", support.TierGuaranteed},
		{support.MacOS, "27.2", support.TierGuaranteed},
		{support.MacOS, "28.0", support.TierGuaranteed},
		{support.MacOS, "", ""},
		{support.IOS, "18.6", support.TierBestEffort},
		{support.IOS, "26.0", support.TierGuaranteed},
		{support.IOS, "27.0", support.TierGuaranteed},
		{support.IOS, "", ""},
		{support.TvOS, "18.4", support.TierBestEffort},
		{support.TvOS, "26.0", support.TierGuaranteed},
		{support.VisionOS, "2.4", support.TierBestEffort},
		{support.VisionOS, "26.0", support.TierGuaranteed},
		{support.WatchOS, "11.6", support.TierBestEffort},
		{support.WatchOS, "27.0", support.TierGuaranteed},
		{support.OS("unknownOS"), "26.0", ""},
	} {
		target := support.Target{OS: tc.os}
		if tc.version != "" {
			target.Version = osversion.MustParse(tc.version)
		}
		if got := target.Tier(); got != tc.want {
			t.Errorf("%s %q tier = %q, want %q", tc.os, tc.version, got, tc.want)
		}
	}
}
