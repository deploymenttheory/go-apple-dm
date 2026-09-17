package profiles_test

import (
	json "encoding/json/v2"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func TestVersionedSSO(t *testing.T) {
	for _, version := range []string{"26.0", "26.4", "26.6.2", "27.0"} {
		target := support.Target{OS: support.MacOS, Version: osversion.MustParse(version), Channel: support.ChannelDevice, Supervised: true, UserApproved: true}
		for _, method := range []string{"Password", "UserSecureEnclaveKey", "OpenID"} {
			p := &profiles.ExtensibleSingleSignOn{ExtensionIdentifier: "com.example.sso", TeamIdentifier: new("EXAMPLE123"), Type: "Redirect", PlatformSSO: &profiles.ExtensibleSingleSignOnPlatformSSO{AuthenticationMethod: new(method)}}
			err := p.Validate(target)
			if (err == nil) != (method != "OpenID" || version == "27.0") {
				t.Fatalf("%s %s: %v", version, method, err)
			}
		}
	}
}

func TestSSOPolicyValuesAndExtensionData(t *testing.T) {
	target := support.Target{OS: support.MacOS, Version: osversion.MustParse("26.6.2"), UserApproved: true}
	for _, tc := range []struct {
		payload string
		allowed bool
	}{
		{`{"ExtensionIdentifier":"example","Type":"Redirect","AuthenticationMethod":"OpenID"}`, false},
		{`{"ExtensionIdentifier":"example","Type":"Redirect","PlatformSSO":{"LoginPolicy":["RequireTouchID"]}}`, false},
		{`{"ExtensionIdentifier":"example","Type":"Redirect","ExtensionData":{"custom":"OpenID"}}`, true},
	} {
		var p profiles.ExtensibleSingleSignOn
		if err := json.Unmarshal([]byte(tc.payload), &p); err != nil {
			t.Fatal(err)
		}
		if err := p.Validate(target); (err == nil) != tc.allowed {
			t.Fatalf("allowed=%v: %v", tc.allowed, err)
		}
	}
}
