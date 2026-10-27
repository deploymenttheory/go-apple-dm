package app_test

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// Values a verdict must never disclose.
const (
	verdictAppStoreID = "987654321"
	verdictExtension  = "com.example.private-extension"
)

// verdictMac imports an enabled, supervised macOS enrollment at version.
func verdictMac(t *testing.T, a *app.App, udid, version string) {
	t.Helper()
	err := a.Core.ImportEnrollment(context.Background(), storage.EnrollmentExport{
		Enrollment: storage.Enrollment{
			ID: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: udid}, Enabled: true,
			Push:         mdm.Push{Topic: "com.apple.mgmt.External.test", Token: []byte("tok"), Magic: "magic"},
			Device:       storage.DeviceInfo{SerialNumber: "SERIAL-" + udid, ProductName: "Mac16,1", OSVersion: version},
			Capabilities: storage.Capabilities{Supervised: storage.CapabilityTrue},
		},
	})
	if err != nil {
		t.Fatalf("ImportEnrollment: %v", err)
	}
}

// verdictCall sends an admin request, requires 200 and returns the raw body
// and its decoded object.
func verdictCall(t *testing.T, srv, method, path, body string) (string, map[string]any) {
	t.Helper()
	resp := adminReq(t, srv, method, path, "t", body)
	defer func(body io.Closer) { _ = body.Close() }(resp.Body)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s: status %d: %s", method, path, resp.StatusCode, raw)
	}
	return string(raw), jsonBody(t, &http.Response{Body: io.NopCloser(strings.NewReader(string(raw)))})
}

// member returns the named member of the object at index i of list, or nil.
func member(list []any, i int, name string) any {
	if i >= len(list) {
		return nil
	}
	object, _ := list[i].(map[string]any)
	return object[name]
}

// TestAssignmentReturnsAvailabilityVerdict checks that assigning a set returns
// each enrollment's verdict for the set's declarations: a macOS 27.0 key is
// withheld from macOS 26.4 with its window and eligible on 27.2, unbounded
// limits render as "latest", and no configuration value is disclosed.
func TestAssignmentReturnsAvailabilityVerdict(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	verdictMac(t, a, "MAC-26", "26.4")
	verdictMac(t, a, "MAC-27", "27.2")
	verdictCall(t, srv, http.MethodPut, "/admin/v1/declarations", `{"Type":"com.apple.configuration.app.managed","Identifier":"extensions","Payload":{"AppStoreID":"`+verdictAppStoreID+`","ExtensionConfigs":{"`+verdictExtension+`":{}}}}`)
	verdictCall(t, srv, http.MethodPut, "/admin/v1/sets/apps/declarations/extensions", "")

	raw, body := verdictCall(t, srv, http.MethodPut, "/admin/v1/enrollments/device/MAC-26/sets/apps", "")
	want := map[string]any{
		"Changed": true,
		"Verdict": map[string]any{
			"Enforced": true,
			"Eligible": []any{},
			"Issues": []any{map[string]any{
				"Identifier": "extensions", "Reason": "unsupported-target", "Withheld": true,
				"Availability": []any{map[string]any{
					"Path": "AppManaged.ExtensionConfigs", "OS": "macOS", "Rule": "introduced", "Severity": "error",
					"Introduced": "27.0", "Deprecated": "latest", "Removed": "latest",
				}},
			}},
		},
	}
	verdict, _ := body["Verdict"].(map[string]any)
	delete(verdict, "Target")
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("macOS 26.4 verdict = %s", raw)
	}
	for _, value := range []string{verdictAppStoreID, verdictExtension} {
		if strings.Contains(raw, value) {
			t.Fatalf("verdict discloses %q: %s", value, raw)
		}
	}
	preview, _ := verdictCall(t, srv, http.MethodGet, "/admin/v1/enrollments/device/MAC-26/compatibility", "")
	if !strings.Contains(preview, `"Introduced":"27.0","Deprecated":"latest","Removed":"latest"`) {
		t.Fatalf("compatibility preview = %s", preview)
	}

	_, body = verdictCall(t, srv, http.MethodPut, "/admin/v1/enrollments/device/MAC-27/sets/apps", "")
	verdict, _ = body["Verdict"].(map[string]any)
	eligible, _ := verdict["Eligible"].([]any)
	issues, _ := verdict["Issues"].([]any)
	if len(eligible) != 1 || member(eligible, 0, "Identifier") != "extensions" || len(issues) != 0 {
		t.Fatalf("macOS 27.2 verdict = %v", verdict)
	}

	_, body = verdictCall(t, srv, http.MethodDelete, "/admin/v1/enrollments/device/MAC-27/sets/apps", "")
	if _, ok := body["Verdict"]; ok || body["Changed"] != true {
		t.Fatalf("unassignment = %v", body)
	}
	// Without inventory the declaration waits for it; the assignment stands.
	_, body = verdictCall(t, srv, http.MethodPut, "/admin/v1/enrollments/device/UNKNOWN/sets/apps", "")
	verdict, _ = body["Verdict"].(map[string]any)
	issues, _ = verdict["Issues"].([]any)
	if body["Changed"] != true || len(issues) != 1 || member(issues, 0, "Reason") != "inventory-required" {
		t.Fatalf("assignment without inventory = %v", body)
	}
}

// TestEnqueueReturnsAvailability checks that an enqueued command reports a
// key the target has deprecated as a delivered warning, and a key the target
// has removed as the reason the target was skipped, each with its window.
func TestEnqueueReturnsAvailability(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	verdictMac(t, a, "MAC-26", "26.4")
	verdictMac(t, a, "MAC-27", "27.0")
	command := func(uuid string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Command</key><dict><key>RequestType</key><string>DeviceInformation</string>
<key>Queries</key><array><string>OSUpdateSettings</string></array></dict>
<key>CommandUUID</key><string>` + uuid + `</string>
</dict></plist>`
	}
	issue := func(rule, severity string) []any {
		return []any{map[string]any{
			"Path": "DeviceInformation.Queries.QueriesItem.OSUpdateSettings", "OS": "macOS", "Rule": rule, "Severity": severity,
			"Introduced": "10.11", "Deprecated": "26.0", "Removed": "27.0",
		}}
	}
	_, body := verdictCall(t, srv, http.MethodPost, "/admin/v1/enrollments/device/MAC-26/commands", command("DEPRECATED"))
	if body["Queued"] != float64(1) || body["Skipped"] != nil || !reflect.DeepEqual(body["Availability"], issue("deprecated", "warning")) {
		t.Fatalf("deprecated query = %v", body)
	}
	_, body = verdictCall(t, srv, http.MethodPost, "/admin/v1/enrollments/device/MAC-27/commands", command("REMOVED"))
	if body["Queued"] != float64(0) || body["Skipped"] == nil || !reflect.DeepEqual(body["Availability"], issue("removed", "error")) {
		t.Fatalf("removed query = %v", body)
	}
}
