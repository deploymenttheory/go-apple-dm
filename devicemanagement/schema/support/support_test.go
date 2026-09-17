package support_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func TestVersionAPICompatibility(t *testing.T) {
	t.Parallel()
	var shared osversion.Version = support.V(osversion.MacOS26, 4, 0)
	var legacy support.Version = osversion.New(osversion.MacOS26, 4, 0)
	parsed, err := support.ParseVersion("26.4")
	if err != nil || parsed != shared || shared != legacy || support.MustVersion("26.4") != shared {
		t.Fatalf("version alias or wrappers differ: %v %v %v", parsed, shared, err)
	}
	_, err = support.ParseVersion("invalid")
	if !errors.Is(err, support.ErrVersion) || !errors.Is(err, osversion.ErrVersion) {
		t.Fatalf("sentinel identity was lost: %v", err)
	}
}

func entry() *support.Entry {
	return &support.Entry{
		Path: "DeviceLock.Message",
		OS: map[support.OS]*support.OSSupport{
			support.IOS: {
				Introduced: support.V(7, 0, 0), Deprecated: support.V(20, 0, 0), Removed: support.V(25, 0, 0),
				Supervised: support.Bool(true), SharedIPadMode: support.ModeIgnored, UserEnrollmentMode: support.ModeForbidden,
				DeviceChannel: support.Bool(true), UserChannel: support.Bool(false),
			},
			support.MacOS: {
				Introduced: support.V(10, 14, 0), RequiresDEP: support.Bool(true), UserApprovedMDM: support.Bool(true),
				SharedIPadDeviceChannel: support.Bool(false), SharedIPadUserChannel: support.Bool(false),
			},
			support.TvOS:     {NotAvailable: true},
			support.VisionOS: {SharedIPadMode: support.ModeRequired},
		},
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	e := entry()
	cases := []struct {
		name       string
		target     support.Target
		supported  bool
		deprecated bool
	}{
		{"nil entry", support.Target{OS: support.IOS}, true, false},
		{"no target OS", support.Target{}, true, false},
		{"not available", support.Target{OS: support.TvOS}, false, false},
		{"unknown OS", support.Target{OS: support.WatchOS}, false, false},
		{"before introduced", support.Target{OS: support.IOS, Version: support.V(6, 0, 0), Supervised: true}, false, false},
		{"supported", support.Target{OS: support.IOS, Version: support.V(15, 0, 0), Supervised: true}, true, false},
		{"deprecated", support.Target{OS: support.IOS, Version: support.V(21, 0, 0), Supervised: true}, true, true},
		{"removed", support.Target{OS: support.IOS, Version: support.V(25, 0, 0), Supervised: true}, false, false},
		{"unsupervised", support.Target{OS: support.IOS, Version: support.V(15, 0, 0)}, false, false},
		{"user channel", support.Target{OS: support.IOS, Version: support.V(15, 0, 0), Supervised: true, Channel: support.ChannelUser}, false, false},
		{"device channel", support.Target{OS: support.IOS, Version: support.V(15, 0, 0), Supervised: true, Channel: support.ChannelDevice}, true, false},
		{"user enrollment forbidden", support.Target{OS: support.IOS, Supervised: true, UserEnrollment: true}, false, false},
		{"needs DEP", support.Target{OS: support.MacOS, Version: support.V(14, 0, 0)}, false, false},
		{"needs UAMDM", support.Target{OS: support.MacOS, Version: support.V(14, 0, 0), DEP: true}, false, false},
		{"mac ok", support.Target{OS: support.MacOS, Version: support.V(14, 0, 0), DEP: true, UserApproved: true}, true, false},
		{"mac shared ipad device channel", support.Target{OS: support.MacOS, DEP: true, UserApproved: true, SharedIPad: true, Channel: support.ChannelDevice}, false, false},
		{"mac shared ipad user channel", support.Target{OS: support.MacOS, DEP: true, UserApproved: true, SharedIPad: true, Channel: support.ChannelUser}, false, false},
		{"shared ipad required", support.Target{OS: support.VisionOS}, false, false},
		{"shared ipad required ok", support.Target{OS: support.VisionOS, SharedIPad: true}, true, false},
	}
	for _, c := range cases {
		var r support.Result
		if c.name == "nil entry" {
			r = (*support.Entry)(nil).Check(c.target)
		} else {
			r = e.Check(c.target)
		}
		if r.Supported != c.supported || r.Deprecated != c.deprecated {
			t.Errorf("%s: got supported=%v deprecated=%v (%s), want %v/%v", c.name, r.Supported, r.Deprecated, r.Reason, c.supported, c.deprecated)
		}
		if !r.Supported && r.Reason == "" {
			t.Errorf("%s: unsupported without reason", c.name)
		}
	}
}

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
		})
	}
}
