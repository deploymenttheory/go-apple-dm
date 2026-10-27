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
