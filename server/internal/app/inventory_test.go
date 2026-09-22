package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// TestInventoryAPIAndNativeEnrichment checks raw-data permissions and native enrichment on memory and SQLite.
func TestInventoryAPIAndNativeEnrichment(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			cfg := Config{Storage: backend, BootstrapToken: "bootstrap-secret", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if backend == "sqlite" {
				cfg.DSN = filepath.Join(t.TempDir(), "server.db")
				cfg.StorageKeys = []string{"test"}
				cfg.Secrets = secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}
			}
			a, e := Build(t.Context(), cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = a.Close() }()
			root := bootstrapRBAC(t, a)
			raw := json.RawMessage(`{"id":"opaque","attributes":{"serialNumber":"SER","deviceModel":"Mac","futureSecret":"hidden","imei":["one","two"]}}`)
			o, e := inventory.ResourceObservation(inventory.SourceReference{Kind: "axm.device", AccountID: "apple", ResourceID: "opaque"}, raw, time.Now().UTC(), time.Hour)
			if e != nil {
				t.Fatal(e)
			}
			cloud, e := a.Inventory.Observe(t.Context(), "SER", o)
			if e != nil {
				t.Fatal(e)
			}
			grant := func(actions string) {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"Source": `permit(principal, action in [` + actions + `], resource);`})
				w := rbacRequest(a, "PUT", "/policies/inventory", root, string(body))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			if w := rbacRequest(a, "GET", "/devices", root, ""); w.Code != 403 {
				t.Fatal("implicit inventory authority", w.Code)
			}
			grant(`MDM::Action::"readInventory"`)
			w := rbacRequest(a, "GET", "/devices/"+cloud.ID, root, "")
			if w.Code != 200 || strings.Contains(w.Body.String(), "hidden") {
				t.Fatal("public projection", w.Code, w.Body.String())
			}
			if w := rbacRequest(a, "GET", "/inventory/raw/devices/"+cloud.ID, root, ""); w.Code != 403 {
				t.Fatal("raw permission bypass", w.Code)
			}
			grant(`MDM::Action::"readInventory",MDM::Action::"readRawInventory"`)
			w = rbacRequest(a, "GET", "/inventory/raw/devices/"+cloud.ID, root, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "hidden") {
				t.Fatal("raw response missing", w.Code, w.Body.String())
			}
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "enrolled"}
			if e := a.Core.ImportEnrollment(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, Device: storage.DeviceInfo{SerialNumber: "SER", ProductName: "Mac16,1", OSVersion: "26.0"}, EnrolledAt: time.Now()}}); e != nil {
				t.Fatal(e)
			}
			if e := a.observeInventoryEnrollment(t.Context(), id); e != nil {
				t.Fatal(e)
			}
			cmd, e := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion"}})
			if e != nil {
				t.Fatal(e)
			}
			if _, e := a.Core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); e != nil {
				t.Fatal(e)
			}
			body, e := plist.Marshal(map[string]any{"Status": "Acknowledged", "CommandUUID": cmd.UUID, "UDID": id.ID, "QueryResponses": map[string]any{"SerialNumber": "SER", "OSVersion": "27.0", "UnknownFuture": false}})
			if e != nil {
				t.Fatal(e)
			}
			response, e := mdm.DecodeResponse(body, "")
			if e != nil {
				t.Fatal(e)
			}
			if e := a.Store.StoreResult(t.Context(), id, response, time.Now()); e != nil {
				t.Fatal(e)
			}
			if e := a.observeInventoryResult(t.Context(), id, response, time.Now()); e != nil {
				t.Fatal(e)
			}
			if _, e := a.Engine.Status(t.Context(), id, []byte(`{"StatusItems":{"device":{"model":{"family":"Mac"}}}}`)); e != nil {
				t.Fatal(e)
			}
			page, e := a.Inventory.Devices(t.Context(), inventory.DeviceQuery{})
			if e != nil || len(page.Items) != 1 || page.Items[0].ID != cloud.ID {
				t.Fatal("native data duplicated device", e)
			}
			if inventory.String(page.Items[0].Fields["os_version"].Value) != "27.0" {
				t.Fatal("native OS missing")
			}
			if string(page.Items[0].Fields["mdm.DeviceInformation.QueryResponses.UnknownFuture"].Value) != "false" {
				t.Fatal("native unknown field lost")
			}
			before := page.Items[0].UpdatedAt
			response.CommandUUID = "unknown"
			if e := a.observeInventoryResult(t.Context(), id, response, time.Now().Add(time.Hour)); e != nil {
				t.Fatal(e)
			}
			after, e := a.Inventory.Device(t.Context(), cloud.ID)
			if e != nil || !after.UpdatedAt.Equal(before) {
				t.Fatal("unknown command changed inventory", e)
			}
			// User-channel status never overwrites the physical device's inventory.
			user := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "user", ParentID: id.ID}
			if e := a.observeInventoryStatus(t.Context(), user, ddm.StatusUpdate{Raw: []byte(`{}`), ReceivedAt: time.Now()}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
