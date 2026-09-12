package storage_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

func TestDeviceInfoObservations(t *testing.T) {
	t.Parallel()
	old := storage.DeviceInfo{OSVersion: "26.4", ProductName: "Mac15,1", BuildVersion: "old"}
	valid := `<plist><dict><key>Status</key><string>Acknowledged</string><key>CommandUUID</key><string>inventory</string><key>UDID</key><string>mac</string><key>QueryResponses</key><dict><key>OSVersion</key><string>27.0</string><key>ProductName</key><string>Mac16,1</string><key>BuildVersion</key><string>new</string></dict></dict></plist>`
	for _, tc := range []struct {
		name, raw, request string
		status             mdm.Status
		user               bool
		want               storage.DeviceInfo
	}{
		{"upgrade", valid, "DeviceInformation", mdm.StatusAcknowledged, false, storage.DeviceInfo{OSVersion: "27.0", ProductName: "Mac16,1", BuildVersion: "new"}},
		{"user cannot replace device", valid, "DeviceInformation", mdm.StatusAcknowledged, true, old},
		{"error", valid, "DeviceInformation", mdm.StatusError, false, old},
		{"wrong tracked command", valid, "ProfileList", mdm.StatusAcknowledged, false, old},
		{"malformed", "invalid", "DeviceInformation", mdm.StatusAcknowledged, false, old},
		{"partial", `<plist><dict><key>QueryResponses</key><dict><key>OSVersion</key><string>invalid</string><key>ProductName</key><string></string><key>BuildVersion</key><string></string></dict></dict></plist>`, "DeviceInformation", mdm.StatusAcknowledged, false, old},
		{"missing", `<plist><dict><key>QueryResponses</key><dict/></dict></plist>`, "DeviceInformation", mdm.StatusAcknowledged, false, old},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := mdm.EnrollmentID{ID: "mac", Channel: mdm.ChannelDevice}
			if tc.user {
				id.Channel = mdm.ChannelUser
			}
			got := storage.DeviceInfoFromResult(
				old,
				id,
				tc.request,
				&mdm.Response{Status: tc.status, CommandUUID: "inventory", Raw: []byte(tc.raw)},
			)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	if got := storage.DeviceInfoFromResult(
		old,
		mdm.EnrollmentID{},
		"DeviceInformation",
		nil,
	); got != old {
		t.Fatal(got)
	}
}
