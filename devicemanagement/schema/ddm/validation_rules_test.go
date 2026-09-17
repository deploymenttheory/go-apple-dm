package ddm_test

import (
	json "encoding/json/v2"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

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

func TestUpdateDeadline(t *testing.T) {
	for _, deadline := range []string{"2026-10-01T18:00:00", "2026-10-01T18:00:00Z", "2026-10-01T18:00:00+01:00", "2026-10-01T18:00:00.5", "2026-02-30T18:00:00"} {
		err := (&ddm.SoftwareUpdateEnforcementSpecific{TargetOSVersion: "27.0", TargetLocalDateTime: deadline}).Validate(support.Target{})
		if (err == nil) != (deadline == "2026-10-01T18:00:00") {
			t.Fatalf("%s: %v", deadline, err)
		}
	}
}
