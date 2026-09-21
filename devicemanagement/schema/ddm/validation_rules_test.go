package ddm_test

import (
	json "encoding/json/v2"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// TestCrossFieldRules checks declarative payload cross-field constraints.
func TestCrossFieldRules(t *testing.T) {
	for _, tc := range []struct {
		name, schema, payload string
		allowed               bool
	}{
		{"downloads-disabled", "SoftwareUpdateSettings", `{"AutomaticActions":{"Download":"AlwaysOff","InstallOSUpdates":"AlwaysOn"}}`, false},
		{"security-downloads-disabled", "SoftwareUpdateSettings", `{"AutomaticActions":{"Download":"AlwaysOff","InstallSecurityUpdate":"AlwaysOn"}}`, false},
		{"downloads-enabled", "SoftwareUpdateSettings", `{"AutomaticActions":{"Download":"AlwaysOn","InstallOSUpdates":"AlwaysOn"}}`, true},
		{"no-caching", "ContentCaching", `{"AllowPersonalCaching":false,"AllowSharedCaching":false}`, false},
		{"personal-caching", "ContentCaching", `{"AllowPersonalCaching":true,"AllowSharedCaching":false}`, true},
		{"missing-trust-anchor", "ContentCaching", `{"ManagementSecurityConfig":"signedByCACert"}`, false},
		{"trust-anchor", "ContentCaching", `{"ManagementSecurityConfig":"specificServerCert","ManagementStatusCertificateReference":"cert"}`, true},
		{"no-app", "AppManaged", `{}`, false},
		{"ambiguous-app", "AppManaged", `{"BundleID":"com.example.app","AppStoreID":"123"}`, false},
		{"bundle-app", "AppManaged", `{"BundleID":"com.example.app"}`, true},
		{"empty-binary", "AppSettings", `{"Allowed":{"DeniedBinaries":[{}]}}`, false},
		{"team-binary", "AppSettings", `{"Allowed":{"AllowedBinaries":[{"TeamID":"EXAMPLE123"}]}}`, true},
		{"ordinary-declaration", "ManagementProperties", `{"Properties":{"test":"value"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := ddm.Registry[tc.schema]
			if !ok {
				t.Fatal(tc.schema)
			}
			value := entry.New()
			if err := json.Unmarshal([]byte(tc.payload), value); err != nil {
				t.Fatal(err)
			}
			if err := value.Validate(support.Target{}); (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v: %v", tc.allowed, err)
			}
		})
	}
}

// TestUpdateDeadline checks software-update deadline validation.
func TestUpdateDeadline(t *testing.T) {
	for _, deadline := range []string{"2026-10-01T18:00:00", "2026-10-01T18:00:00Z", "2026-10-01T18:00:00+01:00", "2026-10-01T18:00:00.5", "2026-02-30T18:00:00"} {
		err := (&ddm.SoftwareUpdateEnforcementSpecific{TargetOSVersion: "27.0", TargetLocalDateTime: deadline}).Validate(support.Target{})
		if (err == nil) != (deadline == "2026-10-01T18:00:00") {
			t.Fatalf("%s: %v", deadline, err)
		}
	}
}

// TestBinaryIdentifierRules checks allowed binary-identifier combinations.
func TestBinaryIdentifierRules(t *testing.T) {
	// Apple's app.settings.yaml "Binary identifier rules" requires a CDHash or
	// TeamID for an allow rule; a deny rule additionally permits SigningID.
	// PathPrefix and SigningState can only narrow a rule with that identity.
	for _, tc := range []struct {
		name, identifier string
		allow, deny      bool
	}{
		{"empty", `{}`, false, false},
		{"hash", `{"CDHash":"90bc96cd95be55c12e7d9b1611cbc677610bb70c"}`, true, true},
		{"team", `{"TeamID":"EXAMPLE1234"}`, true, true},
		{"signing-id", `{"SigningID":"com.example.app"}`, false, true},
		{"path-only", `{"PathPrefix":"/Applications/Example.app"}`, false, false},
		{"state-only", `{"SigningState":"All"}`, false, false},
		{"qualifiers-only", `{"PathPrefix":"/Applications/Example.app","SigningState":"DeveloperID"}`, false, false},
		{"empty-hash", `{"CDHash":"","PathPrefix":"/Applications/Example.app"}`, false, false},
		{"empty-team", `{"TeamID":"","SigningState":"All"}`, false, false},
		{"empty-signing-id", `{"SigningID":""}`, false, false},
		{"empty-hash-with-signing-id", `{"CDHash":"","SigningID":"com.example.app"}`, false, true},
		{"team-with-signing-id", `{"TeamID":"EXAMPLE1234","SigningID":"com.example.app"}`, true, true},
		{"hash-with-qualifiers", `{"CDHash":"90bc96cd95be55c12e7d9b1611cbc677610bb70c","PathPrefix":"/Applications/Example.app","SigningState":"All"}`, true, true},
		{"signing-id-with-qualifiers", `{"SigningID":"com.example.app","PathPrefix":"/Applications/Example.app","SigningState":"All"}`, false, true},
	} {
		for _, rule := range []struct {
			list    string
			allowed bool
		}{
			{"AllowedBinaries", tc.allow},
			{"DeniedBinaries", tc.deny},
		} {
			t.Run(rule.list+"/"+tc.name, func(t *testing.T) {
				var value ddm.AppSettings
				payload := `{"Allowed":{"` + rule.list + `":[` + tc.identifier + `]}}`
				if err := json.Unmarshal([]byte(payload), &value); err != nil {
					t.Fatal(err)
				}
				if err := value.Validate(support.Target{}); (err == nil) != rule.allowed {
					t.Fatalf("allowed=%v: %v", rule.allowed, err)
				}
			})
		}
	}
}
