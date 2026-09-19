package appsettings_test

import (
	json "encoding/json/v2"
	"fmt"
	"runtime"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appsettings"
)

func ExamplePrivacyDefaults() {
	target := support.Target{OS: support.MacOS, Version: osversion.New(27, 0, 0), Channel: support.ChannelUser, Supervised: true}
	payload, err := appsettings.PrivacyDefaults([]appsettings.PrivacyEntry{{
		BundleID: "com.example.meet", DesignatedRequirement: `identifier "com.example.meet" and anchor apple generic`,
		Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Video meetings", Camera: new("Allow")},
	}}, target)
	if err != nil {
		fmt.Println(err)
		return
	}
	for key, value := range payload.Privacy.PermissionDefaults {
		fmt.Println(key)
		fmt.Println(*value.Camera)
	}
	// Output:
	// com.example.meet {identifier "com.example.meet" and anchor apple generic}
	// Allow
}

// This integration checks the real discovery-to-payload boundary on macOS;
// portable rule and invalid-signature cases are covered by fixture tests.
func TestNativeInspectionToValidatedPayload(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native inspection requires macOS")
	}
	id, err := appidentity.Inspect(t.Context(), "/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	target := support.Target{OS: support.MacOS, Version: osversion.New(27, 0, 0), Channel: support.ChannelDevice, Supervised: true}
	payload, err := appsettings.DenyBinaries(id, appsettings.BinaryOptions{Match: appsettings.MatchCDHash}, target)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ddm.AppSettings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(target); err != nil {
		t.Fatal(err)
	}
	hashes := map[string]bool{}
	for _, a := range id.Architectures {
		hashes[a.CDHash] = true
	}
	if len(decoded.Allowed.DeniedBinaries) != len(hashes) {
		t.Fatal("architecture identity lost")
	}
	for _, r := range decoded.Allowed.DeniedBinaries {
		if r.CDHash == nil || !hashes[*r.CDHash] {
			t.Fatal("unexpected rule")
		}
	}
}
