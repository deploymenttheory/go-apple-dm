package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// replicaInventoryRequest crosses the real TLS listener and configured admin grants.
func replicaInventoryRequest(t *testing.T, f *enrollFixture, method, path, body string) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, f.publicURL+"/admin/v1"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer replica-inventory-operator")
	response, err := f.client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, data)
	}
	return data
}

// seedADEReplicaInventory retains independent ABM evidence before either browser
// session enrolls. No enrollment or certificate evidence is manufactured here.
func seedADEReplicaInventory(t *testing.T, f *enrollFixture, serial string) inventory.ResourceRecord {
	t.Helper()
	ref := source.Reference{Kind: "axm.device", AccountID: "replica-apple", ResourceID: "opaque-before-enrollment"}
	raw := json.RawMessage(fmt.Sprintf(`{"id":%q,"type":"orgDevices","attributes":{"serialNumber":%q,"deviceModel":"Apple source model","unknown":9007199254740993}}`, ref.ResourceID, serial))
	observation, err := inventory.ResourceObservation(ref, raw, time.Now().UTC(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := f.app.Inventory.Observe(t.Context(), "", observation)
	if err != nil {
		t.Fatal(err)
	}
	before := replicaInventoryRequest(t, f, http.MethodGet, "/inventory/raw/serials/"+url.PathEscape(serial), "")
	if !strings.Contains(string(before), "9007199254740993") || strings.Contains(string(before), "linked_at") {
		t.Fatal("ABM must be inspectable independently before enrollment", string(before))
	}
	return cloud
}

// verifyADEReplicaInventory uses only actual simulator-issued credentials and
// native Authenticate receipts, then explicitly declares a model mapping. This
// closes the durable browser -> profile hooks -> issuance -> inventory seam.
func verifyADEReplicaInventory(t *testing.T, f *enrollFixture, destination *app.App, d *simulator.Device, cloud inventory.ResourceRecord) {
	t.Helper()
	ctx := t.Context()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: d.UDID}
	enrollment, err := destination.Store.Get(ctx, id)
	if err != nil || d.Identity == nil || enrollment.CertHash != cms.Fingerprint(d.Identity.Cert) {
		t.Fatal("native receipt not pinned to issued browser identity", enrollment, err)
	}
	if err := d.Identity.Cert.CheckSignatureFrom(f.appCA); err != nil {
		t.Fatal("profile did not issue an application enrollment certificate", err)
	}
	identity, err := destination.Inventory.SourceResource(ctx, inventory.EnrollmentReference("mdm.identity", id))
	if err != nil || identity.Snapshot.Native == nil || !identity.Snapshot.Native.Authenticated || identity.Snapshot.Native.Method != correlation.AutomatedDeviceEnrollment || identity.Snapshot.Native.Serial != d.SerialNumber {
		t.Fatal("saved ADE browser did not preserve issuance method into native receipt", identity, err)
	}
	// The two pending sessions deliberately differ in product and UDID despite
	// sharing serial. A later cache entry must not supply either session's facts.
	if inventory.String(identity.Snapshot.Fields["ProductName"]) != d.ProductName || inventory.String(identity.Snapshot.Fields["UDID"]) != d.UDID {
		t.Fatal("replica mixed distinct browser/native identities", identity)
	}
	// Native enrollment creates no automatic cross-source relationship.
	links, err := destination.Inventory.Links(ctx, cloud.Source)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.Native == identity.Source {
			t.Fatal("enrollment silently correlated sources", link)
		}
	}
	correlationBody := fmt.Sprintf(`{"apple":%q,"native":%q}`, cloud.ID, identity.ID)
	replicaInventoryRequest(t, f, http.MethodPost, "/inventory/correlations", correlationBody)
	// A later successful ABM refresh must not override a declared equivalent
	// native value. Original Apple evidence and its large integer remain retained.
	later, err := inventory.ResourceObservation(cloud.Source, json.RawMessage(fmt.Sprintf(`{"id":%q,"type":"orgDevices","attributes":{"serialNumber":%q,"deviceModel":"Later Apple model","unknown":9007199254740993}}`, cloud.Source.ResourceID, d.SerialNumber)), identity.Snapshot.ObservedAt.Add(time.Minute), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.Inventory.Observe(ctx, "", later); err != nil {
		t.Fatal(err)
	}
	// Each new source original invalidates checked links; explicitly revalidate
	// the relationship before resolving refreshed evidence.
	replicaInventoryRequest(t, f, http.MethodPost, "/inventory/correlations", correlationBody)
	mapping := fmt.Sprintf(`{"mappings":[{"name":"model","inputs":[{"resource_id":%q,"path":"deviceModel"},{"resource_id":%q,"path":"ProductName"}]}]}`, cloud.ID, identity.ID)
	raw := replicaInventoryRequest(t, f, http.MethodPost, "/inventory/raw/devices/"+cloud.ID+"/resolve", mapping)
	var resolved map[string]struct {
		Selected struct {
			Value  string `json:"value"`
			Native bool   `json:"native"`
		} `json:"selected"`
		Candidates []json.RawMessage `json:"candidates"`
		Disagrees  bool              `json:"disagrees"`
	}
	if err := json.Unmarshal(raw, &resolved); err != nil {
		t.Fatal(err)
	}
	result := resolved["model"]
	if !result.Selected.Native || result.Selected.Value != d.ProductName || !result.Disagrees || len(result.Candidates) < 2 || !strings.Contains(string(raw), "Later Apple model") {
		t.Fatal("later Apple refresh overrode native or lost disagreement", string(raw))
	}
	after := replicaInventoryRequest(t, f, http.MethodGet, "/inventory/raw/serials/"+url.PathEscape(d.SerialNumber), "")
	if !strings.Contains(string(after), "linked_at") || !strings.Contains(string(after), string(correlation.AutomatedDeviceEnrollment)) || !strings.Contains(string(after), "9007199254740993") {
		t.Fatal("serial inspection lost source/provenance", string(after))
	}
}
