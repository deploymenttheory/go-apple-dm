package dep

import (
	"fmt"
	"slices"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// ProfileTarget supplies observed capabilities for an ADE profile check. Only
// Target.OS and Target.Version are used: enrollment flags describe the resulting
// enrollment and are determined by the profile, not by an existing enrollment.
// iPadOS uses support.IOS. Nil booleans and zero protocol/version values mean
// unknown; callers must supply facts rather than inferred defaults.
type ProfileTarget struct {
	support.Target
	IsIPad            *bool
	SharedIPadCapable *bool
	ProtocolVersion   int
}

// ProfileSupportIssue identifies a wire field (or indexed skip_setup_items
// entry), the dimension checked, and the reason for the result.
type ProfileSupportIssue struct {
	Field     string
	Dimension string
	Reason    string
}

// ProfileSupportReport separates unsupported settings from checks that could
// not be completed. Empty Unsupported does not establish complete support when
// Unchecked is nonempty. Deprecated and Ignored describe accepted settings whose
// behavior differs from their requested value. All slices have stable order.
type ProfileSupportReport struct {
	Unsupported []ProfileSupportIssue
	Unchecked   []ProfileSupportIssue
	Deprecated  []ProfileSupportIssue
	Ignored     []ProfileSupportIssue
}

// ValidateForTarget first applies Validate, returning its error unchanged, then
// checks supplied settings against generated skip-key metadata and documented
// Profile restrictions. Unsupported settings are returned in the report, not as
// an error. Unknown fields remain intact and are reported as unchecked. No
// network request, profile mutation, target inference or assignment occurs.
// A caller decides whether unchecked, deprecated or ignored settings are acceptable.
func (p *Profile) ValidateForTarget(t ProfileTarget) (ProfileSupportReport, error) {
	var r ProfileSupportReport
	if err := p.Validate(); err != nil {
		return r, err
	}
	// The schema has no separate iPadOS OS identifier. Unknown OS names must not
	// be interpreted as a known platform with no supported settings.
	knownOS := slices.Contains([]support.OS{support.IOS, support.MacOS, support.TvOS, support.VisionOS, support.WatchOS}, t.OS)
	if !knownOS {
		r.Unchecked = append(r.Unchecked, issue("*", "platform", "target OS is missing or unrecognized"))
	}
	if t.Version.IsZero() || t.Version.Major < 0 || t.Version.Minor < 0 || t.Version.Patch < 0 {
		r.Unchecked = append(r.Unchecked, issue("*", "version", "target OS version is missing or invalid"))
		t.Version = osversion.Version{}
	}
	if knownOS && t.OS == support.WatchOS {
		r.Unchecked = append(r.Unchecked, issue("*", "platform", "no local ADE Profile platform contract for watchOS"))
	}
	// Source: Apple's Profile object. These fields are maintained here because
	// ADE service objects are not part of the generated Apple YAML schema.
	for _, field := range []struct {
		name    string
		present bool
		kind    string
	}{
		{"auto_advance_setup", p.AutoAdvanceSetup != nil, "auto"},
		{"language", p.Language != "", "auto"},
		{"region", p.Region != "", "auto"},
		{"do_not_use_profile_from_backup", p.DoNotUseProfileFromBackup != nil, "restore"},
		{"is_return_to_service", p.IsReturnToService != nil, "restore"},
		{"configuration_web_url", p.ConfigurationWebURL != "", "web"},
	} {
		if !field.present || !knownOS {
			continue
		}
		e := profileFieldSupport(field.kind)
		r.checkEntry(field.name, e, t)
	}
	for _, field := range []struct {
		name    string
		present bool
	}{
		{"auto_advance_setup", p.AutoAdvanceSetup != nil},
		{"await_device_configured", p.AwaitDeviceConfigured != nil},
		{"is_multi_user", p.IsMultiUser != nil},
		{"support_email_address", p.SupportEmailAddress != ""},
	} {
		if !field.present {
			continue
		}
		switch {
		case t.ProtocolVersion <= 0:
			r.Unchecked = append(r.Unchecked, issue(field.name, "protocol_version", "X-Server-Protocol-Version is unknown"))
		case t.ProtocolVersion < 2:
			r.Unsupported = append(r.Unsupported, issue(field.name, "protocol_version", "requires X-Server-Protocol-Version 2 or later"))
		}
	}
	if p.IsMultiUser != nil && *p.IsMultiUser {
		switch {
		case knownOS && t.OS != support.IOS:
			r.Unsupported = append(r.Unsupported, issue("is_multi_user", "platform", "Shared iPad requires iPadOS"))
		case t.IsIPad == nil:
			r.Unchecked = append(r.Unchecked, issue("is_multi_user", "device_family", "whether the target is an iPad is unknown"))
		case !*t.IsIPad:
			r.Unsupported = append(r.Unsupported, issue("is_multi_user", "device_family", "Shared iPad requires an iPad"))
		default:
			if t.SharedIPadCapable == nil {
				r.Unchecked = append(r.Unchecked, issue("is_multi_user", "hardware", "Shared iPad minimum requirements have not been checked"))
			} else if !*t.SharedIPadCapable {
				r.Unsupported = append(r.Unsupported, issue("is_multi_user", "hardware", "target does not meet Shared iPad minimum requirements"))
			}
		}
		r.Unchecked = append(r.Unchecked, issue("is_multi_user", "enrollment_profile", "com.apple.mdm.per-user-connections must be present in the delivered MDM enrollment profile"))
	}
	if knownOS && t.OS == support.IOS {
		if p.AllowPairing != nil {
			r.checkIOSBoundary("allow_pairing", t, 13, true, "deprecated in iOS 13")
		}
		if p.IsSupervised != nil && !*p.IsSupervised {
			r.checkIOSBoundary("is_supervised", t, 11, true, "unsupervised ADE deprecated in iOS 11")
			r.checkIOSBoundary("is_supervised", t, 13, false, "iOS 13 and later supervise all ADE enrollments and ignore this flag")
		}
		if p.IsMandatory != nil && !*p.IsMandatory {
			r.checkIOSBoundary("is_mandatory", t, 13, false, "iOS 13 and later make ADE enrollment mandatory")
		}
		if p.AwaitDeviceConfigured != nil && *p.AwaitDeviceConfigured && (p.IsSupervised == nil || !*p.IsSupervised) {
			if t.Version.IsZero() {
				r.Unchecked = append(r.Unchecked, issue("await_device_configured", "supervision", "iOS behavior depends on version and the profile's supervision flag"))
			} else if t.Version.Compare(osversion.New(13, 0, 0)) < 0 {
				r.Ignored = append(r.Ignored, issue("await_device_configured", "supervision", "ignored on unsupervised iOS devices"))
			}
		}
	}
	if len(p.SupervisingHostCerts) > 0 && (p.IsSupervised == nil || !*p.IsSupervised) {
		r.Unchecked = append(r.Unchecked, issue("supervising_host_certs", "supervision", "unused without supervision; effective target supervision must be verified"))
	}
	for i, k := range p.SkipSetupItems {
		field := fmt.Sprintf("skip_setup_items[%d]", i)
		if !knownOS {
			continue
		}
		e := support.Lookup("other", "SkipKeys."+k)
		r.checkEntry(field, e, t)
		if e != nil {
			if os := e.OS[t.OS]; os != nil && !os.NotAvailable && os.AlwaysSkippable != nil && !*os.AlwaysSkippable {
				r.Unchecked = append(r.Unchecked, issue(field, "setup_context", "pane is not always skippable; device and Setup Assistant context must be checked"))
			}
		}
	}
	for _, k := range sortedProfileExtraKeys(p.Extra) {
		r.Unchecked = append(r.Unchecked, issue(k, "field", "unknown profile field has no local support contract"))
	}
	return r, nil
}

// issue builds a field-specific support observation.
func issue(field, dimension, reason string) ProfileSupportIssue {
	return ProfileSupportIssue{Field: field, Dimension: dimension, Reason: reason}
}

// sortedProfileExtraKeys gives unknown fields deterministic report order.
func sortedProfileExtraKeys(extra map[string]any) []string {
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// profileFieldSupport contains only restrictions documented for the ADE Profile
// object, never a duplicate of generated skip-key availability.
func profileFieldSupport(kind string) *support.Entry {
	e := &support.Entry{OS: map[support.OS]*support.OSSupport{}}
	switch kind {
	case "auto":
		e.OS[support.MacOS] = &support.OSSupport{Introduced: osversion.New(11, 0, 0)}
		e.OS[support.TvOS] = &support.OSSupport{}
	case "restore":
		e.OS[support.IOS] = &support.OSSupport{Introduced: osversion.New(26, 0, 0)}
		e.OS[support.VisionOS] = &support.OSSupport{Introduced: osversion.New(26, 0, 0)}
	case "web":
		e.OS[support.IOS] = &support.OSSupport{Introduced: osversion.New(13, 0, 0)}
		e.OS[support.MacOS] = &support.OSSupport{Introduced: osversion.New(10, 15, 0)}
		e.OS[support.TvOS] = &support.OSSupport{}
		e.OS[support.VisionOS] = &support.OSSupport{}
	}
	return e
}

// checkEntry evaluates only OS/version and retains unspecified release floors.
func (r *ProfileSupportReport) checkEntry(field string, e *support.Entry, t ProfileTarget) {
	if e == nil {
		r.Unchecked = append(r.Unchecked, issue(field, "metadata", "no generated support metadata"))
		return
	}
	// Only OS/version are relevant to these rows; do not interpret zero-valued
	// enrollment context booleans as observed facts.
	entry := *e
	entry.Path = field
	result := entry.Check(support.Target{OS: t.OS, Version: t.Version})
	if !result.Supported {
		r.Unsupported = append(r.Unsupported, issue(field, "availability", result.Reason))
		return
	}
	if result.Deprecated {
		r.Deprecated = append(r.Deprecated, issue(field, "version", result.Reason))
	}
	if result.OS.Introduced.IsZero() {
		r.Unchecked = append(r.Unchecked, issue(field, "version", "Apple does not specify an introduction release"))
	}
}

// checkIOSBoundary reports documented iOS behavior without assuming a version.
func (r *ProfileSupportReport) checkIOSBoundary(field string, t ProfileTarget, major int, deprecated bool, reason string) {
	if t.Version.IsZero() {
		r.Unchecked = append(r.Unchecked, issue(field, "version", reason+"; target version unknown"))
		return
	}
	if t.Version.Compare(osversion.New(major, 0, 0)) < 0 {
		return
	}
	if deprecated {
		r.Deprecated = append(r.Deprecated, issue(field, "version", reason))
	} else {
		r.Ignored = append(r.Ignored, issue(field, "version", reason))
	}
}
