package schemagen

import "testing"

func TestGoName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"UDID":                        "UDID",
		"OSUpdate":                    "OSUpdate",
		"eSIM":                        "ESIM",
		"device.model.family":         "DeviceModelFamily",
		"Hash-SHA-256":                "HashSHA256",
		"_Errors":                     "Errors",
		"OS_VERSION":                  "OSVERSION",
		"Passcode:Settings":           "PasscodeSettings",
		"Asset:Credential SCEP":       "AssetCredentialSCEP",
		"802.1X Global Ethernet":      "X8021XGlobalEthernet",
		"type":                        "Type",
		"func":                        "Func",
		"":                            "Empty",
		"   ":                         "Empty",
		"com.apple.mdm":               "ComAppleMdm",
		"lowercase":                   "Lowercase",
		"Status Device Model":         "StatusDeviceModel",
		"StandardConfigurationsItems": "StandardConfigurationsItems",
	}
	for in, want := range cases {
		if got := GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTypeNameForSchema(t *testing.T) {
	// Not parallel: mutates the package-level override map.
	cases := []struct {
		s    Schema
		want string
	}{
		{Schema{Family: FamilyCommands, Title: "Device Lock Command", Payload: Payload{RequestType: "DeviceLock"}}, "DeviceLock"},
		{Schema{Family: FamilyCheckin, Title: "Token Update", Payload: Payload{RequestType: "TokenUpdate"}}, "TokenUpdate"},
		{Schema{Family: FamilyCommands, Title: "No Request Type"}, "NoRequestType"},
		{Schema{Family: FamilyStatus, Title: "Status Device Model Family"}, "DeviceModelFamily"},
		{Schema{Family: FamilyErrors, Title: "Error Unrecognized Device"}, "UnrecognizedDevice"},
		{Schema{Family: FamilyDDM, Title: "Passcode:Settings"}, "PasscodeSettings"},
		{Schema{Family: FamilyProfiles, Title: "MDM"}, "MDM"},
		{Schema{Family: FamilyOther, Title: "MachineInfo"}, "MachineInfo"},
		{Schema{Family: FamilyDDMProto, Title: "Status Report"}, "StatusReport"},
	}
	for _, c := range cases {
		if got := TypeNameForSchema(&c.s); got != c.want {
			t.Errorf("TypeNameForSchema(%q) = %q, want %q", c.s.Title, got, c.want)
		}
	}
	typeNameOverrides["x/y.yaml"] = "Pinned"
	t.Cleanup(func() { delete(typeNameOverrides, "x/y.yaml") })
	if got := TypeNameForSchema(&Schema{Path: "x/y.yaml", Title: "Whatever"}); got != "Pinned" {
		t.Errorf("override not applied: %q", got)
	}
	if ResponseTypeName("DeviceLock") != "DeviceLockResponse" {
		t.Error("ResponseTypeName")
	}
}
