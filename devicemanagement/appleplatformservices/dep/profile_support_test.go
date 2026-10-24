package dep_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// profileTarget supplies explicit platform, release and protocol facts.
func profileTarget(os support.OS, version string) dep.ProfileTarget {
	return dep.ProfileTarget{Target: support.Target{OS: os, Version: osversion.MustParse(version)}, ProtocolVersion: 2}
}

// baseProfile returns a structurally valid minimal caller profile.
func baseProfile() *dep.Profile {
	return &dep.Profile{ProfileName: "Test", URL: "https://mdm.example", OrgMagic: "test"}
}

// hasIssue finds a field/dimension observation with a nonempty explanation.
func hasIssue(issues []dep.ProfileSupportIssue, field, dimension string) bool {
	return slices.ContainsFunc(issues, func(i dep.ProfileSupportIssue) bool {
		return i.Field == field && i.Dimension == dimension && i.Reason != ""
	})
}

// TestProfileTargetAvailability checks documented ADE field release restrictions.
func TestProfileTargetAvailability(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		os          support.OS
		version     string
		field       string
		set         func(*dep.Profile)
		unsupported bool
	}{
		{"Mac before auto", support.MacOS, "10.15", "auto_advance_setup", func(p *dep.Profile) { p.AutoAdvanceSetup = new(true) }, true},
		{"Mac auto boundary", support.MacOS, "11", "auto_advance_setup", func(p *dep.Profile) { p.AutoAdvanceSetup = new(false) }, false},
		{"iOS auto", support.IOS, "26", "auto_advance_setup", func(p *dep.Profile) { p.AutoAdvanceSetup = new(false) }, true},
		{"tvOS language", support.TvOS, "26", "language", func(p *dep.Profile) { p.Language = "en" }, false},
		{"vision region", support.VisionOS, "26", "region", func(p *dep.Profile) { p.Region = "GB" }, true},
		{"restore before", support.IOS, "18.7", "do_not_use_profile_from_backup", func(p *dep.Profile) { p.DoNotUseProfileFromBackup = new(true) }, true},
		{"restore boundary", support.IOS, "26", "do_not_use_profile_from_backup", func(p *dep.Profile) { p.DoNotUseProfileFromBackup = new(false) }, false},
		{"return vision", support.VisionOS, "26", "is_return_to_service", func(p *dep.Profile) { p.IsReturnToService = new(true) }, false},
		{"return mac", support.MacOS, "26", "is_return_to_service", func(p *dep.Profile) { p.IsReturnToService = new(true) }, true},
		{"web iOS before", support.IOS, "12.9", "configuration_web_url", func(p *dep.Profile) { p.ConfigurationWebURL = "https://mdm.example/auth" }, true},
		{"web iOS boundary", support.IOS, "13", "configuration_web_url", func(p *dep.Profile) { p.ConfigurationWebURL = "https://mdm.example/auth" }, false},
		{"web Mac before", support.MacOS, "10.14.9", "configuration_web_url", func(p *dep.Profile) { p.ConfigurationWebURL = "https://mdm.example/auth" }, true},
		{"web Mac boundary", support.MacOS, "10.15", "configuration_web_url", func(p *dep.Profile) { p.ConfigurationWebURL = "https://mdm.example/auth" }, false},
		{"web tvOS", support.TvOS, "26", "configuration_web_url", func(p *dep.Profile) { p.ConfigurationWebURL = "https://mdm.example/auth" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := baseProfile()
			tc.set(p)
			snapshot, err := dep.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			r, err := p.ValidateForTarget(profileTarget(tc.os, tc.version))
			if err != nil {
				t.Fatal(err)
			}
			if got := hasIssue(r.Unsupported, tc.field, "availability"); got != tc.unsupported {
				t.Fatalf("unsupported=%v report=%+v", got, r)
			}
			after, err := dep.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, snapshot) {
				t.Fatal("validation mutated profile")
			}
		})
	}
}

// TestProfileSkipMetadataContract keeps all skip-key checks aligned with generated metadata.
func TestProfileSkipMetadataContract(t *testing.T) {
	t.Parallel()
	// Exercise every generated key on every platform at release boundaries. The
	// validator must use the same generated source as standalone support checks.
	for _, k := range dep.SkipKeys() {
		e := support.Lookup("other", "SkipKeys."+k)
		if e == nil {
			t.Fatalf("missing generated metadata for %s", k)
		}
		for _, os := range support.AllOS {
			versions := []osversion.Version{osversion.New(1, 0, 0), osversion.New(27, 0, 0)}
			if m := e.OS[os]; m != nil {
				if !m.Introduced.IsZero() {
					versions = append(versions, m.Introduced)
				}
				if !m.Removed.IsZero() {
					versions = append(versions, m.Removed)
				}
				if !m.Deprecated.IsZero() {
					versions = append(versions, m.Deprecated)
				}
			}
			for _, v := range versions {
				p := baseProfile()
				p.SkipSetupItems = []string{k}
				target := dep.ProfileTarget{Target: support.Target{OS: os, Version: v}, ProtocolVersion: 2}
				r, err := p.ValidateForTarget(target)
				if err != nil {
					t.Fatal(err)
				}
				want := e.Check(target.Target)
				if hasIssue(r.Unsupported, "skip_setup_items[0]", "availability") == want.Supported {
					t.Fatalf("%s %s %s: %+v want %+v", k, os, v, r, want)
				}
				if hasIssue(r.Deprecated, "skip_setup_items[0]", "version") != want.Deprecated {
					t.Fatalf("deprecated %s %s %s: %+v", k, os, v, r)
				}
			}
		}
	}
}

// TestProfileTargetUnknownsAndErrors checks incomplete facts and structural failure fields.
func TestProfileTargetUnknownsAndErrors(t *testing.T) {
	t.Parallel()
	for _, os := range []support.OS{"", "iPadOS", "futureOS"} {
		p := baseProfile()
		p.AutoAdvanceSetup = new(true)
		p.SkipSetupItems = []string{"Siri"}
		p.Extra = map[string]any{"z_future": false, "a_future": nil}
		r, err := p.ValidateForTarget(dep.ProfileTarget{Target: support.Target{OS: os}})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Unsupported) != 0 || !hasIssue(r.Unchecked, "*", "platform") || !hasIssue(r.Unchecked, "*", "version") || !hasIssue(r.Unchecked, "auto_advance_setup", "protocol_version") || !hasIssue(r.Unchecked, "a_future", "field") {
			t.Fatalf("%s: %+v", os, r)
		}
		if r.Unchecked[len(r.Unchecked)-1].Field != "z_future" {
			t.Fatalf("unstable order: %+v", r)
		}
	}
	p := baseProfile()
	p.Language = "en"
	p.SkipSetupItems = []string{"Accessibility"}
	r, err := p.ValidateForTarget(profileTarget(support.MacOS, "26"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(r.Unchecked, "skip_setup_items[0]", "setup_context") {
		t.Fatalf("conditional pane: %+v", r)
	}
	target := profileTarget(support.TvOS, "26")
	r, err = p.ValidateForTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(r.Unchecked, "language", "version") {
		t.Fatalf("unspecified floor: %+v", r)
	}
	target.Version = osversion.New(-1, 0, 0)
	r, err = p.ValidateForTarget(target)
	if err != nil || !hasIssue(r.Unchecked, "*", "version") {
		t.Fatalf("invalid version: %+v %v", r, err)
	}
	var nilProfile *dep.Profile
	if _, err = nilProfile.ValidateForTarget(target); !errors.Is(err, dep.ErrProfileInvalid) {
		t.Fatalf("nil: %v", err)
	}
	for _, tc := range []struct {
		p     *dep.Profile
		field string
	}{{&dep.Profile{}, "profile_name"}, {&dep.Profile{ProfileName: "x"}, "url"}, {&dep.Profile{ProfileName: "x", URL: "https://x"}, "org_magic"}} {
		_, err := tc.p.ValidateForTarget(target)
		var pe *dep.ProfileError
		if !errors.As(err, &pe) || pe.Field != tc.field {
			t.Fatalf("structural: %+v", err)
		}
	}
	p = baseProfile()
	p.SkipSetupItems = []string{"Siri", "not-real"}
	_, err = p.ValidateForTarget(target)
	var pe *dep.ProfileError
	if !errors.As(err, &pe) || pe.Field != "skip_setup_items[1]" {
		t.Fatalf("unknown skip key: %v", err)
	}
}

// TestProfileSharedIPad distinguishes iPad identity, hardware capability and enrollment content.
func TestProfileSharedIPad(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                   string
		os                     support.OS
		ipad, capable          *bool
		unsupported, unchecked string
	}{
		{"Mac", support.MacOS, nil, nil, "platform", ""},
		{"iPhone", support.IOS, new(false), nil, "device_family", ""},
		{"unknown family", support.IOS, nil, nil, "", "device_family"},
		{"unknown hardware", support.IOS, new(true), nil, "", "hardware"},
		{"old iPad", support.IOS, new(true), new(false), "hardware", ""},
		{"capable iPad", support.IOS, new(true), new(true), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := baseProfile()
			p.IsMultiUser = new(true)
			target := profileTarget(tc.os, "26")
			target.IsIPad = tc.ipad
			target.SharedIPadCapable = tc.capable
			r, err := p.ValidateForTarget(target)
			if err != nil {
				t.Fatal(err)
			}
			if tc.unsupported != "" && !hasIssue(r.Unsupported, "is_multi_user", tc.unsupported) {
				t.Fatalf("unsupported: %+v", r)
			}
			if tc.unchecked != "" && !hasIssue(r.Unchecked, "is_multi_user", tc.unchecked) {
				t.Fatalf("unchecked: %+v", r)
			}
			if !hasIssue(r.Unchecked, "is_multi_user", "enrollment_profile") {
				t.Fatalf("missing enrollment-profile check: %+v", r)
			}
		})
	}
}

// TestProfileProtocolAndIOSBehavior checks service protocol floors and ignored iOS flags.
func TestProfileProtocolAndIOSBehavior(t *testing.T) {
	t.Parallel()
	p := baseProfile()
	p.AutoAdvanceSetup = new(false)
	p.AwaitDeviceConfigured = new(true)
	p.IsMultiUser = new(false)
	p.SupportEmailAddress = "support@example.com"
	target := profileTarget(support.IOS, "12")
	target.ProtocolVersion = 1
	r, err := p.ValidateForTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"auto_advance_setup", "await_device_configured", "is_multi_user", "support_email_address"} {
		if !hasIssue(r.Unsupported, field, "protocol_version") {
			t.Fatalf("%s: %+v", field, r)
		}
	}
	if !hasIssue(r.Ignored, "await_device_configured", "supervision") {
		t.Fatalf("unsupervised: %+v", r)
	}
	p = baseProfile()
	p.AllowPairing = new(false)
	p.IsSupervised = new(false)
	p.IsMandatory = new(false)
	p.AwaitDeviceConfigured = new(true)
	p.SupervisingHostCerts = []string{"cert"}
	for _, v := range []string{"10", "11", "12", "13"} {
		target = profileTarget(support.IOS, v)
		r, err = p.ValidateForTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		if got := hasIssue(r.Ignored, "is_mandatory", "version"); got != (v == "13") {
			t.Fatalf("mandatory %s: %+v", v, r)
		}
		if got := hasIssue(r.Deprecated, "is_supervised", "version"); got != (v != "10") {
			t.Fatalf("supervised %s: %+v", v, r)
		}
		if got := hasIssue(r.Deprecated, "allow_pairing", "version"); got != (v == "13") {
			t.Fatalf("pairing %s: %+v", v, r)
		}
	}
	target.Version = osversion.Version{}
	r, err = p.ValidateForTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(r.Unchecked, "await_device_configured", "supervision") || !hasIssue(r.Unchecked, "is_supervised", "version") || !hasIssue(r.Unchecked, "supervising_host_certs", "supervision") {
		t.Fatalf("unknown context: %+v", r)
	}
	r, err = baseProfile().ValidateForTarget(profileTarget(support.WatchOS, "26"))
	if err != nil || !hasIssue(r.Unchecked, "*", "platform") {
		t.Fatalf("watch: %+v %v", r, err)
	}
}

// ExampleProfile_ValidateForTarget demonstrates explicit caller-owned target facts.
func ExampleProfile_ValidateForTarget() {
	p := &dep.Profile{ProfileName: "Lab", URL: "https://mdm.example/enroll", OrgMagic: "lab", SkipSetupItems: []string{"AppleID"}}
	report, err := p.ValidateForTarget(dep.ProfileTarget{Target: support.Target{OS: support.MacOS, Version: osversion.New(26, 0, 0)}, ProtocolVersion: 2})
	if err != nil {
		panic(err)
	}
	// Check both report.Unsupported and report.Unchecked before assignment.
	_ = report
}
