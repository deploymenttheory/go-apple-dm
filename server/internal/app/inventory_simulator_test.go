package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/discovery"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestInventorySimulatorComposition crosses profile issuance, SCEP, authenticated
// check-in, queue polling and real administrative inspection/correlation/resolution.
// Its collected ABM fixture is independent of enrollment and never uses a UUID as
// physical identity. This automated fixture makes no hardware compatibility claim.
func TestInventorySimulatorComposition(t *testing.T) {
	f := newEnrollFixture(t, "", func(cfg *app.Config) {
		cfg.BootstrapToken = "inventory-operator"
		cfg.Enroll.Discovery[discovery.ModelFamilyIPad] = discovery.VersionADDE
	})
	ctx := t.Context()
	request := func(method, path, body string, want int) []byte {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, method, "/admin/v1"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer inventory-operator")
		w := httptest.NewRecorder()
		f.app.Handler.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	for _, tc := range []struct {
		name, product string
		user          bool
	}{{"ade", "Mac16,1", false}, {"profile", "Mac16,1", false}, {"account", "iPad16,2", false}, {"user", "iPhone17,2", true}} {
		t.Run(tc.name, func(t *testing.T) {
			serial := "INVENTORY-" + tc.name
			udid := "enrollment-" + tc.name
			ref := source.Reference{Kind: "axm.device", AccountID: "apple", ResourceID: "opaque-" + tc.name}
			original := json.RawMessage(fmt.Sprintf(`{"id":%q,"type":"orgDevices","attributes":{"serialNumber":%q,"deviceModel":"Apple Model","unknown":9007199254740993}}`, ref.ResourceID, serial))
			observation, err := inventory.ResourceObservation(ref, original, time.Now().UTC(), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			cloud, err := f.app.Inventory.Observe(ctx, "", observation)
			if err != nil {
				t.Fatal(err)
			}
			before := request("GET", "/inventory/raw/serials/"+serial, "", 200)
			if !strings.Contains(string(before), "9007199254740993") {
				t.Fatal(string(before))
			}
			d := f.device(t, udid, tc.product)
			d.SerialNumber = serial
			if tc.user || tc.name == "account" {
				if _, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: f.publicURL, Authenticate: f.signIn(t)}); err != nil {
					t.Fatal(err)
				}
			} else if tc.name == "profile" {
				data := request("POST", "/enrollment-profiles", fmt.Sprintf(`{"DeviceID":%q,"Serial":%q,"Product":%q,"OSVersion":"26.0"}`, udid, serial, tc.product), 200)
				if err := d.ApplyProfile(ctx, data, profile.ParseOptions{}); err != nil {
					t.Fatal(err)
				}
				if err := d.Enroll(ctx); err != nil {
					t.Fatal(err)
				}
			} else if err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
				t.Fatal(err)
			}
			channel := mdm.ChannelDevice
			if tc.user {
				channel = mdm.ChannelUserEnrollmentDevice
			}
			enrollmentRef := inventory.EnrollmentReference("enrollment", mdm.EnrollmentID{Channel: channel, ID: udid})
			// User Enrollment uses Apple's enrollment ID, which is unrelated to the UDID.
			if tc.user {
				page, err := f.app.Inventory.Resources(ctx, inventory.ResourceQuery{Source: "enrollment"})
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range page.Items {
					if record.Snapshot.Native != nil && record.Snapshot.Native.Method == "user-enrollment" {
						enrollmentRef = record.Source
					}
				}
			}
			enrollment, err := f.app.Inventory.SourceResource(ctx, enrollmentRef)
			if err != nil {
				t.Fatal(err)
			}
			if enrollment.Snapshot.Native == nil || !enrollment.Snapshot.Native.Authenticated {
				t.Fatal("issuance/check-in provenance missing", enrollment)
			}
			if tc.user && enrollment.Snapshot.Native.Method != "user-enrollment" {
				t.Fatal("user provenance", enrollment)
			}
			request("POST", "/devices/"+enrollment.ID+"/collect", "", 200)
			d.Responder = func(cmd *mdm.Command) simulator.Reply {
				if cmd.RequestType == "DeviceInformation" {
					return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.DeviceInformationResponse{QueryResponses: commands.DeviceInformationResponseQueryResponses{SerialNumber: new(serial), Model: new("Native Model"), OSVersion: new("26.0")}}}
				}
				return simulator.AcknowledgeAll(cmd)
			}
			if _, err := d.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			nativeRef := enrollmentRef
			nativeRef.Kind = "mdm.DeviceInformation"
			native, err := f.app.Inventory.SourceResource(ctx, nativeRef)
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"apple":%q,"native":%q}`, cloud.ID, native.ID)
			if tc.user {
				request("POST", "/inventory/correlations", body, 409)
				return
			}
			request("POST", "/inventory/correlations", body, 200)
			mapping := fmt.Sprintf(`{"mappings":[{"name":"model","inputs":[{"resource_id":%q,"path":"deviceModel"},{"resource_id":%q,"path":"QueryResponses.Model"}]}]}`, cloud.ID, native.ID)
			resolved := request("POST", "/inventory/raw/devices/"+cloud.ID+"/resolve", mapping, 200)
			var result map[string]struct {
				Selected struct {
					Value  string `json:"value"`
					Native bool   `json:"native"`
				} `json:"selected"`
				Disagrees bool `json:"disagrees"`
			}
			if err := json.Unmarshal(resolved, &result); err != nil {
				t.Fatal(err)
			}
			if result["model"].Selected.Value != "Native Model" || !result["model"].Selected.Native || !result["model"].Disagrees {
				t.Fatal(string(resolved))
			}
			after := request("GET", "/inventory/raw/serials/"+serial, "", 200)
			expectedMethod := map[string]string{"ade": "automated-device-enrollment", "account": "account-driven-device-enrollment", "profile": "profile-based-device-enrollment"}[tc.name]
			if !strings.Contains(string(after), "linked_at") || !strings.Contains(string(after), expectedMethod) {
				t.Fatal(string(after))
			}
		})
	}
	// The actual TLS reference listener also enforces administrative credentials.
	req, err := http.NewRequestWithContext(ctx, "GET", f.publicURL+"/admin/v1/inventory/raw/serials/INVENTORY-ade", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal(response.StatusCode)
	}
}
