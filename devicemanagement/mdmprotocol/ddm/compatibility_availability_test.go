package ddm_test

import (
	"context"
	json "encoding/json/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// Values that a compatibility report must never disclose.
const (
	privateAppStoreID = "987654321"
	privateExtension  = "com.example.private-extension"
)

var (
	// extensionsDecl uses ExtensionConfigs, available from macOS 27.0 and iOS 18.4.
	extensionsDecl = declJSON(schema.DeclarationTypeAppManaged, "extensions", map[string]any{
		"AppStoreID":       privateAppStoreID,
		"ExtensionConfigs": map[string]any{privateExtension: map[string]any{}},
	})
	// vppDecl uses VPPType, removed in iOS 18.0 and not available on macOS.
	vppDecl = declJSON(schema.DeclarationTypeAppManaged, "vpp", map[string]any{
		"AppStoreID":      privateAppStoreID,
		"InstallBehavior": map[string]any{"License": map[string]any{"VPPType": "User"}},
	})
	// intelligenceDecl uses AllowVisualIntelligenceSummary, deprecated in iOS
	// 27.0 and not available on macOS.
	intelligenceDecl = declJSON(schema.DeclarationTypeIntelligenceSettings, "intelligence", map[string]any{
		"AllowVisualIntelligenceSummary": true,
	})
)

// supervised returns a supervised device-channel target.
func supervised(os support.OS, version string) support.Target {
	return support.Target{OS: os, Version: osversion.MustParse(version), Channel: support.ChannelDevice, Supervised: true}
}

// keyIssue builds an expected availability issue.
func keyIssue(path string, os support.OS, rule support.Rule, severity validation.Severity, introduced, deprecated, removed string) validation.AvailabilityIssue {
	parse := func(s string) osversion.Version {
		if s == "" {
			return osversion.Version{}
		}
		return osversion.MustParse(s)
	}
	return validation.AvailabilityIssue{
		Path: path, OS: os, Rule: rule, Severity: severity,
		Introduced: parse(introduced), Deprecated: parse(deprecated), Removed: parse(removed),
	}
}

// TestParseDeclarationReportsKeyAvailability checks that declaration validation
// exposes the blocking keys of an unsupported target, and that a deprecated
// key still validates.
func TestParseDeclarationReportsKeyAvailability(t *testing.T) {
	t.Parallel()
	_, err := ddm.ParseDeclaration(extensionsDecl, supervised(support.MacOS, "26.4"))
	want := []validation.AvailabilityIssue{
		keyIssue("AppManaged.ExtensionConfigs", support.MacOS, support.RuleIntroduced, validation.SeverityError, "27.0", "", ""),
	}
	if got := validation.AvailabilityIssues(err); !reflect.DeepEqual(got, want) {
		t.Fatalf("macOS 26.4 issues = %+v (%v), want %+v", got, err, want)
	}
	if _, err := ddm.ParseDeclaration(extensionsDecl, supervised(support.MacOS, "27.2")); err != nil {
		t.Fatalf("macOS 27.2: %v", err)
	}
	if _, err := ddm.ParseDeclaration(intelligenceDecl, supervised(support.IOS, "27.0")); err != nil {
		t.Fatalf("deprecated key rejected: %v", err)
	}
}

// TestCompatibilityReportsKeyAvailability checks the key-level availability in
// compatibility reports: blocking keys of withheld declarations, removed and
// unavailable keys, deprecated keys of eligible declarations, and that reports
// never disclose configuration values.
func TestCompatibilityReportsKeyAvailability(t *testing.T) {
	t.Parallel()
	var target support.Target
	h := newHarness(t, func(c *ddm.Config) {
		c.EnrollmentTarget = func(context.Context, mdm.EnrollmentID) (support.Target, error) { return target, nil }
	})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "availability"}
	for _, raw := range [][]byte{extensionsDecl, vppDecl, intelligenceDecl} {
		d, _, err := h.engine.PutDeclaration(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignDeclaration(t.Context(), id, d.Identifier); err != nil {
			t.Fatal(err)
		}
	}
	const (
		vppType      = "AppManaged.InstallBehavior.License.VPPType"
		visual       = "IntelligenceSettings.AllowVisualIntelligenceSummary"
		withheld     = "unsupported-target"
		deprecated   = "deprecated"
		errorLevel   = validation.SeverityError
		warningLevel = validation.SeverityWarning
	)
	for _, tc := range []struct {
		name     string
		target   support.Target
		eligible []string
		issues   []ddm.CompatibilityIssue
	}{
		{
			name:   "macOS 26.4 predates the extension keys",
			target: supervised(support.MacOS, "26.4"),
			issues: []ddm.CompatibilityIssue{
				{Identifier: "extensions", Reason: withheld, Withheld: true, Availability: []validation.AvailabilityIssue{
					keyIssue("AppManaged.ExtensionConfigs", support.MacOS, support.RuleIntroduced, errorLevel, "27.0", "", ""),
				}},
				{Identifier: "intelligence", Reason: withheld, Withheld: true, Availability: []validation.AvailabilityIssue{
					keyIssue(visual, support.MacOS, support.RuleNotAvailable, errorLevel, "", "", ""),
				}},
				{Identifier: "vpp", Reason: withheld, Withheld: true, Availability: []validation.AvailabilityIssue{
					keyIssue(vppType, support.MacOS, support.RuleNotAvailable, errorLevel, "", "", ""),
				}},
			},
		},
		{
			name:     "macOS 27.2 takes the extension keys",
			target:   supervised(support.MacOS, "27.2"),
			eligible: []string{"extensions"},
			issues: []ddm.CompatibilityIssue{
				{Identifier: "intelligence", Reason: withheld, Withheld: true, Availability: []validation.AvailabilityIssue{
					keyIssue(visual, support.MacOS, support.RuleNotAvailable, errorLevel, "", "", ""),
				}},
				{Identifier: "vpp", Reason: withheld, Withheld: true, Availability: []validation.AvailabilityIssue{
					keyIssue(vppType, support.MacOS, support.RuleNotAvailable, errorLevel, "", "", ""),
				}},
			},
		},
		{
			name:     "iOS 27.0 removed VPPType and deprecated the visual summary",
			target:   supervised(support.IOS, "27.0"),
			eligible: []string{"extensions", "intelligence"},
			issues: []ddm.CompatibilityIssue{
				{Identifier: "intelligence", Reason: deprecated, Availability: []validation.AvailabilityIssue{
					keyIssue(visual, support.IOS, support.RuleDeprecated, warningLevel, "26.4", "27.0", ""),
				}},
				{Identifier: "vpp", Reason: withheld, Withheld: true, Availability: []validation.AvailabilityIssue{
					keyIssue(vppType, support.IOS, support.RuleRemoved, errorLevel, "17.2", "", "18.0"),
				}},
			},
		},
	} {
		target = tc.target
		report, err := h.engine.Compatibility(t.Context(), id)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var eligible []string
		for _, ref := range report.Eligible {
			eligible = append(eligible, ref.Identifier)
		}
		if !reflect.DeepEqual(eligible, tc.eligible) {
			t.Errorf("%s: eligible = %v, want %v", tc.name, eligible, tc.eligible)
		}
		if !reflect.DeepEqual(report.Issues, tc.issues) {
			t.Errorf("%s: issues = %+v, want %+v", tc.name, report.Issues, tc.issues)
		}
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{privateAppStoreID, privateExtension} {
			if strings.Contains(string(body), value) {
				t.Errorf("%s: report discloses %q: %s", tc.name, value, body)
			}
		}
	}
}

// A value of keys without availability data, which reports must not disclose.
const futureValue = "private-future-value"

var (
	// futureDecl adds keys absent from the generated schema at the payload
	// root and under a map value.
	futureDecl = declJSON(schema.DeclarationTypeAppManaged, "future", map[string]any{
		"AppStoreID":       privateAppStoreID,
		"FutureKey":        futureValue,
		"ExtensionConfigs": map[string]any{privateExtension: map[string]any{"FutureExtensionKey": futureValue}},
	})
	// futureRangesDecl adds a key absent from the generated schema to an array item.
	futureRangesDecl = declJSON(schema.DeclarationTypeContentCaching, "future-ranges", map[string]any{
		"ListenRanges": []any{map[string]any{"type": "IPv4", "first": "192.0.2.1", "last": "192.0.2.9", "FutureRangeKey": futureValue}},
	})
)

// TestCompatibilityReportsUnvalidatedKeys checks that keys absent from the
// generated schema leave a declaration eligible, are reported as unvalidated
// by schema key path, follow the blocking keys of withheld declarations and
// never disclose map keys or values.
func TestCompatibilityReportsUnvalidatedKeys(t *testing.T) {
	t.Parallel()
	if _, err := ddm.ParseDeclaration(futureDecl, supervised(support.MacOS, "27.2")); err != nil {
		t.Fatalf("unknown keys rejected: %v", err)
	}
	var target support.Target
	h := newHarness(t, func(c *ddm.Config) {
		c.EnrollmentTarget = func(context.Context, mdm.EnrollmentID) (support.Target, error) { return target, nil }
	})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "unvalidated"}
	for _, raw := range [][]byte{futureDecl, futureRangesDecl} {
		d, _, err := h.engine.PutDeclaration(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignDeclaration(t.Context(), id, d.Identifier); err != nil {
			t.Fatal(err)
		}
	}
	unvalidated := func(paths ...string) []validation.AvailabilityIssue {
		var keys []validation.AvailabilityIssue
		for _, path := range paths {
			keys = append(keys, keyIssue(path, support.MacOS, support.RuleUnvalidated, validation.SeverityWarning, "", "", ""))
		}
		return keys
	}
	// blocking lists keys introduced in macOS 27.0, followed by warnings.
	blocking := func(warnings []validation.AvailabilityIssue, paths ...string) []validation.AvailabilityIssue {
		var keys []validation.AvailabilityIssue
		for _, path := range paths {
			keys = append(keys, keyIssue(path, support.MacOS, support.RuleIntroduced, validation.SeverityError, "27.0", "", ""))
		}
		return append(keys, warnings...)
	}
	appKeys := unvalidated("AppManaged.ExtensionConfigs.ANY.FutureExtensionKey", "AppManaged.FutureKey")
	rangeKeys := unvalidated("ContentCaching.ListenRanges.RangesItem.FutureRangeKey")
	for _, tc := range []struct {
		name     string
		target   support.Target
		eligible []string
		issues   []ddm.CompatibilityIssue
	}{
		{
			name:     "macOS 27.2 delivers both declarations",
			target:   supervised(support.MacOS, "27.2"),
			eligible: []string{"future", "future-ranges"},
			issues: []ddm.CompatibilityIssue{
				{Identifier: "future", Reason: "unvalidated", Availability: appKeys},
				{Identifier: "future-ranges", Reason: "unvalidated", Availability: rangeKeys},
			},
		},
		{
			name:   "macOS 26.4 lists warnings after blocking keys",
			target: supervised(support.MacOS, "26.4"),
			issues: []ddm.CompatibilityIssue{
				{
					Identifier: "future", Reason: "unsupported-target", Withheld: true,
					Availability: blocking(appKeys, "AppManaged.ExtensionConfigs"),
				},
				{
					Identifier: "future-ranges", Reason: "unsupported-target", Withheld: true,
					Availability: blocking(rangeKeys, "ContentCaching", "ContentCaching.ListenRanges",
						"ContentCaching.ListenRanges.RangesItem.first", "ContentCaching.ListenRanges.RangesItem.last",
						"ContentCaching.ListenRanges.RangesItem.type"),
				},
			},
		},
	} {
		target = tc.target
		report, err := h.engine.Compatibility(t.Context(), id)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var eligible []string
		for _, ref := range report.Eligible {
			eligible = append(eligible, ref.Identifier)
		}
		if !reflect.DeepEqual(eligible, tc.eligible) || !reflect.DeepEqual(report.Issues, tc.issues) {
			t.Errorf("%s: eligible = %v, issues = %+v; want %v, %+v", tc.name, eligible, report.Issues, tc.eligible, tc.issues)
		}
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{privateAppStoreID, privateExtension, futureValue} {
			if strings.Contains(string(body), value) {
				t.Errorf("%s: report discloses %q: %s", tc.name, value, body)
			}
		}
	}
}

// TestCompatibilityReportsSupportTier checks that a report marks release 26 and
// every later release of an OS as guaranteed and earlier releases as best
// effort, that a best-effort target still receives declarations its version
// supports, and that an unknown version claims neither tier.
func TestCompatibilityReportsSupportTier(t *testing.T) {
	t.Parallel()
	var target support.Target
	h := newHarness(t, func(c *ddm.Config) {
		c.EnrollmentTarget = func(context.Context, mdm.EnrollmentID) (support.Target, error) { return target, nil }
	})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "tier"}
	// Deferrals are available from macOS 15.0 and not on iOS; ExtensionConfigs
	// from macOS 27.0.
	updatesDecl := declJSON(schema.DeclarationTypeSoftwareUpdateSettings, "updates", map[string]any{"Deferrals": map[string]any{"MajorPeriodInDays": 30}})
	for _, raw := range [][]byte{updatesDecl, extensionsDecl} {
		d, _, err := h.engine.PutDeclaration(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignDeclaration(t.Context(), id, d.Identifier); err != nil {
			t.Fatal(err)
		}
	}
	unknownVersion := supervised(support.MacOS, "26.0")
	unknownVersion.Version = osversion.Version{}
	for _, tc := range []struct {
		name     string
		target   support.Target
		tier     support.Tier
		eligible []string
	}{
		{"macOS 15.7 is best effort", supervised(support.MacOS, "15.7"), support.TierBestEffort, []string{"updates"}},
		{"macOS 26.0 is the guaranteed floor", supervised(support.MacOS, "26.0"), support.TierGuaranteed, []string{"updates"}},
		{"macOS 26.4 is guaranteed", supervised(support.MacOS, "26.4"), support.TierGuaranteed, []string{"updates"}},
		{"macOS 27.2 is guaranteed", supervised(support.MacOS, "27.2"), support.TierGuaranteed, []string{"extensions", "updates"}},
		{"an unknown version claims no tier", unknownVersion, "", nil},
		{"iOS 18.6 is best effort", supervised(support.IOS, "18.6"), support.TierBestEffort, []string{"extensions"}},
		{"iOS 27.0 is guaranteed", supervised(support.IOS, "27.0"), support.TierGuaranteed, []string{"extensions"}},
		{"tvOS 26.0 is guaranteed", supervised(support.TvOS, "26.0"), support.TierGuaranteed, nil},
	} {
		target = tc.target
		report, err := h.engine.Compatibility(t.Context(), id)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var eligible []string
		for _, ref := range report.Eligible {
			eligible = append(eligible, ref.Identifier)
		}
		if report.Tier != tc.tier || !reflect.DeepEqual(eligible, tc.eligible) {
			t.Errorf("%s: tier = %q, eligible = %v; want %q, %v", tc.name, report.Tier, eligible, tc.tier, tc.eligible)
		}
	}
}
