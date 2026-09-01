package support_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/schema/support"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()
	good := map[string]support.Version{
		"26":      support.V(26, 0, 0),
		"26.4":    support.V(26, 4, 0),
		"10.15.4": support.V(10, 15, 4),
		" 1.1 ":   support.V(1, 1, 0),
		"0.0.1":   support.V(0, 0, 1),
	}
	for in, want := range good {
		got, err := support.ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "n/a", "1.2.3.4", "a.b", "-1.0", "1..2"} {
		if _, err := support.ParseVersion(bad); !errors.Is(err, support.ErrVersion) {
			t.Errorf("ParseVersion(%q) err = %v, want ErrVersion", bad, err)
		}
	}
	if support.MustVersion("13.0").String() != "13.0" || support.V(1, 2, 3).String() != "1.2.3" {
		t.Error("String")
	}
	defer func() {
		if recover() == nil {
			t.Error("MustVersion did not panic")
		}
	}()
	support.MustVersion("bad")
}

func TestVersionCompare(t *testing.T) {
	t.Parallel()
	a, b := support.V(10, 15, 4), support.V(11, 0, 0)
	if a.Compare(b) != -1 || b.Compare(a) != 1 || a.Compare(a) != 0 {
		t.Error("major compare")
	}
	if support.V(1, 2, 0).Compare(support.V(1, 3, 0)) != -1 || support.V(1, 2, 5).Compare(support.V(1, 2, 4)) != 1 {
		t.Error("minor/patch compare")
	}
	if !(support.Version{}).IsZero() || support.V(0, 1, 0).IsZero() {
		t.Error("IsZero")
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
