package app

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// TestInventorySerialHTTP exercises checked enrichment, original provenance and
// strict public/raw separation through the real administrative authorization stack.
func TestInventorySerialHTTP(t *testing.T) {
	a, err := Build(t.Context(), Config{Storage: "inmem", BootstrapToken: "bootstrap-secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := bootstrapRBAC(t, a)
	grant := func(actions string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"Source": `permit(principal, action in [` + actions + `], resource);`})
		w := rbacRequest(a, "PUT", "/policies/inspection", root, string(body))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	now := time.Now().UTC()
	observe := func(kind, id, raw string, identity *inventory.NativeIdentity, at time.Time) inventory.ResourceRecord {
		t.Helper()
		ref := source.Reference{Kind: kind, ResourceID: id}
		if kind == "axm.device" {
			ref.AccountID = "apple"
		}
		o, err := inventory.ResourceObservation(ref, json.RawMessage(raw), at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		o.Native = identity
		d, err := a.Inventory.Observe(t.Context(), "", o)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cloud := observe("axm.device", "opaque", `{ "id":"opaque", "type":"orgDevices", "attributes":{"serialNumber":"SER","deviceModel":"Apple Model","future":9007199254740993},"relationships":{"x":{"data":null}}}`, nil, now)
	if w := rbacRequest(a, "GET", "/devices?account=apple;foreign", root, ""); w.Code != 403 {
		t.Fatal("query parsing bypassed authorization", w.Code)
	}
	if w := rbacRequest(a, "GET", "/inventory/serials/SER", root, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	grant(`MDM::Action::"readInventory"`)
	w := rbacRequest(a, "GET", "/inventory/serials/ser", root, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), cloud.ID) || strings.Contains(w.Body.String(), "Apple Model") || strings.Contains(w.Body.String(), "future") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/inventory/raw/serials/SER", "/inventory/raw/devices/" + cloud.ID + "/evidence"} {
		if w := rbacRequest(a, "GET", path, root, ""); w.Code != 403 {
			t.Fatal(path, w.Code)
		}
	}
	if w := rbacRequest(a, "POST", "/inventory/raw/devices/"+cloud.ID+"/resolve", root, `{}`); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := rbacRequest(a, "POST", "/inventory/correlations", root, `{}`); w.Code != 403 {
		t.Fatal(w.Code)
	}
	identity := &inventory.NativeIdentity{Serial: "SER", Authenticated: true, Method: correlation.ProfileBasedDeviceEnrollment}
	native := observe("mdm.DeviceInformation", "device:enrollment", `{"QueryResponses":{"Model":"Native Model","False":false,"Zero":0,"Empty":[]}}`, identity, now.Add(time.Second))
	// Later omission retains older meaningful false/zero/empty-array values.
	observe("mdm.DeviceInformation", "device:enrollment", `{"QueryResponses":{"New":true}}`, identity, now.Add(2*time.Second))
	grant(`MDM::Action::"readInventory",MDM::Action::"readRawInventory",MDM::Action::"manageInventory"`)
	correlate := func(n string, want int) {
		t.Helper()
		body := fmt.Sprintf(`{"apple":%q,"native":%q}`, cloud.ID, n)
		w := rbacRequest(a, "POST", "/inventory/correlations", root, body)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	correlate(native.ID, 200)
	mappings := fmt.Sprintf(`{"mappings":[{"name":"model","inputs":[{"resource_id":%q,"path":"deviceModel"},{"resource_id":%q,"path":"QueryResponses.Model"}]},{"name":"false","inputs":[{"resource_id":%q,"path":"QueryResponses.False"}]},{"name":"zero","inputs":[{"resource_id":%q,"path":"QueryResponses.Zero"}]},{"name":"empty","inputs":[{"resource_id":%q,"path":"QueryResponses.Empty"}]}]}`, cloud.ID, native.ID, native.ID, native.ID, native.ID)
	resolve := func(want int) string {
		t.Helper()
		w := rbacRequest(a, "POST", "/inventory/raw/devices/"+cloud.ID+"/resolve", root, mappings)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	out := resolve(200)
	var resolved map[string]inventoryResolution
	if err := json.Unmarshal([]byte(out), &resolved); err != nil {
		t.Fatal(err)
	}
	if inventory.String(resolved["model"].Selected.Value) != "Native Model" || !resolved["model"].Disagrees || string(resolved["false"].Selected.Value) != "false" || string(resolved["zero"].Selected.Value) != "0" || string(resolved["empty"].Selected.Value) != "[]" {
		t.Fatal(out)
	}
	cloud = observe("axm.device", "opaque", `{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"SER","deviceModel":"Later Apple"}}`, nil, now.Add(time.Hour))
	resolve(400) // New original requires fresh explicit correlation.
	correlate(native.ID, 200)
	if !strings.Contains(resolve(200), `"value":"Native Model"`) {
		t.Fatal("ABM overwrote native")
	}
	user := observe("mdm.DeviceInformation", "user-enrollment-device:user", `{"QueryResponses":{"Model":"User"}}`, &inventory.NativeIdentity{Serial: "SER", Authenticated: true, Method: correlation.UserEnrollment}, now)
	correlate(user.ID, 409)
	unauth := observe("mdm.DeviceInformation", "device:unknown", `{}`, nil, now)
	correlate(unauth.ID, 400)
	w = rbacRequest(a, "GET", "/inventory/raw/devices/"+cloud.ID+"/evidence", root, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "9007199254740993") || !strings.Contains(w.Body.String(), "original_json") || !strings.Contains(w.Body.String(), "relationships") {
		t.Fatal(w.Code, w.Body.String())
	}
	rawPreset := `{"name":"Private","format":"csv","columns":["data.private"],"query":{"conditions":[{"field":"data.private","operator":"eq","value":"raw-filter-secret"}]}}`
	if w := rbacRequest(a, "PUT", "/inventory/raw/presets/private", root, rawPreset); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "PUT", "/inventory/presets/private", root, rawPreset); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}

	grant(`MDM::Action::"readInventory",MDM::Action::"manageInventory"`)
	if w := rbacRequest(a, "PUT", "/inventory/raw/presets/private", root, rawPreset); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "GET", "/inventory/presets", root, ""); w.Code != 200 || strings.Contains(w.Body.String(), "raw-filter-secret") || strings.Contains(w.Body.String(), "Private") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "GET", "/inventory/raw/presets", root, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	grant(`MDM::Action::"readInventory"`)
	w = rbacRequest(a, "GET", "/devices/"+cloud.ID+"/evidence", root, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "Later Apple") || strings.Contains(w.Body.String(), "history") {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestInventoryQueryContinuations verifies actual filter/page boundaries and rejects
// retired filter names, ambiguous selectors and foreign query continuations.
func TestInventoryQueryContinuations(t *testing.T) {
	repo, err := inventory.New(inventory.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Inventory: repo}
	mux := inventoryTestMux(a)
	for _, id := range []string{"one", "two", "three"} {
		o, err := inventory.ResourceObservation(source.Reference{Kind: "axm.device", AccountID: "a", ResourceID: id}, json.RawMessage(fmt.Sprintf(`{"id":%q,"type":"orgDevices","attributes":{"serialNumber":"SER"}}`, id)), time.Now().UTC(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Observe(t.Context(), "", o); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
		if w.Code != want {
			t.Fatal(path, w.Code, w.Body.String())
		}
		return w
	}
	seen := map[string]bool{}
	cursor := ""
	for range 3 {
		w := get("/devices?account=a&limit=1&cursor="+url.QueryEscape(cursor), 200)
		var page inventory.ResourcePage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || seen[page.Items[0].ID] {
			t.Fatal(page)
		}
		seen[page.Items[0].ID] = true
		if page.NextCursor != "" {
			get("/devices?account=other&cursor="+url.QueryEscape(page.NextCursor), 400)
		}
		cursor = page.NextCursor
	}
	if cursor != "" {
		t.Fatal("unexpected continuation")
	}
	for _, path := range []string{"/devices?account=a;b", "/devices?account=a&where=%zz", "/devices?account=%ff%", "/inventory/exports?account=a;b", "/inventory/reports?where=%zz", "/inventory/raw/devices?account=a;b", "/inventory/serials/SER?where=%zz", "/devices?serial=SER", "/devices?uuid=old", "/devices?managed=true", "/devices?source=axm.device&source=mdm.identity", "/devices?cursor=old-uuid", "/devices?cursor=e30", "/devices?where=" + url.QueryEscape(`[{"field":"serial","operator":"eq","value":"SER"}]`), "/devices?where=" + url.QueryEscape(`[{"field":"data.attributes.serialNumber","operator":"eq","value":"SER"}]`), "/inventory/serials/SER?source=axm.device"} {
		get(path, 400)
	}
	get("/devices/old-uuid", 404)
	get("/inventory/serials/missing", 404)
}

// TestInventoryHTTPExportEscaping preserves CSV cells and fails closed for raw
// projections and source predicates on ordinary export/report endpoints.
func TestInventoryHTTPExportEscaping(t *testing.T) {
	repo, err := inventory.New(inventory.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Inventory: repo}
	mux := inventoryTestMux(a)
	label := "=SUM(1,2)\n\"quoted\""
	raw, err := json.Marshal(map[string]any{"id": "csv", "type": "orgDevices", "attributes": map[string]any{"serialNumber": "SER", "label": label}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := inventory.ResourceObservation(source.Reference{Kind: "axm.device", AccountID: "apple", ResourceID: "csv"}, raw, time.Now().UTC(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Observe(t.Context(), "", observation); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/inventory/raw/exports?format=csv&columns=data.attributes.label", nil))
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if w.Code != 200 || err != nil || len(rows) != 2 || rows[1][0] != "'"+label {
		t.Fatal(w.Code, rows, err, w.Body.String())
	}
	for _, path := range []string{"/inventory/exports?format=csv&columns=data.attributes.label", "/inventory/exports?where=" + url.QueryEscape(`[{"field":"data.attributes.label","operator":"exists"}]`), "/inventory/reports?where=" + url.QueryEscape(`[{"field":"data.attributes.label","operator":"exists"}]`)} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
		if w.Code != 400 || strings.Contains(w.Body.String(), "quoted") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
