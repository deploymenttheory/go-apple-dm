package profiles_test

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

func TestSSOValueFloors(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"NewUserAuthenticationMethods", "FileVaultPolicy", "LoginPolicy", "UnlockPolicy"} {
		values := []string{"RequireTouchID", "RequireTouchIDOrWatch", "AllowOpenIDForTouchIDFallback"}
		if field == "NewUserAuthenticationMethods" {
			values = []string{"OpenID"}
		}
		for _, value := range values {
			path := "ExtensibleSingleSignOn.PlatformSSO." + field
			entry := profiles.ValueSupport(path, value)
			if entry == nil || entry.OS[support.MacOS].Introduced != osversion.New(osversion.MacOS27, 0, 0) {
				t.Fatalf("missing floor: %s=%s", path, value)
			}
			for _, version := range []string{"26.6.2", "27.0", "27.0.1", "28.0", ""} {
				target := support.Target{OS: support.MacOS, UserApproved: true}
				if version != "" {
					target.Version = osversion.MustParse(version)
				}
				var payload profiles.ExtensibleSingleSignOn
				body := fmt.Sprintf(`{"ExtensionIdentifier":"example","Type":"Redirect","PlatformSSO":{%q:[%q]}}`, field, value)
				if err := json.Unmarshal([]byte(body), &payload); err != nil {
					t.Fatal(err)
				}
				err := payload.Validate(target)
				if version == "26.6.2" {
					var issues validation.Errors
					if !errors.As(err, &issues) {
						t.Fatalf("missing support failure for %s=%s: %v", path, value, err)
					}
					found := false
					for _, issue := range issues {
						found = found || (issue.Path == "PlatformSSO."+field+"[0]" && issue.Rule == validation.RuleSupport)
					}
					if !found {
						t.Fatalf("wrong support path: %v", err)
					}
				} else if err != nil {
					t.Fatalf("%s %s=%s: %v", version, path, value, err)
				}
			}
		}
	}
	if profiles.ValueSupport("unknown", "OpenID") != nil || profiles.ValueSupport("ExtensibleSingleSignOn.AuthenticationMethod", "Password") != nil {
		t.Fatal("unreviewed values acquired an extra floor")
	}
}

func TestNilProfileValidation(t *testing.T) {
	t.Parallel()
	var payload *profiles.ExtensibleSingleSignOn
	if !errors.Is(payload.Validate(support.Target{}), validation.ErrValidation) {
		t.Fatal("nil profile accepted")
	}
}
