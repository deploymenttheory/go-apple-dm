package app

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

// TestInventoryWorkerPreservesReportProvenance follows the actual background
// sweep after legacy command/status persistence and later certificate issuance.
// Only a fresh pinned command receipt and a fresh validated DDM report qualify;
// subsequent sweeps preserve their captured provenance without duplicate history.
func TestInventoryWorkerPreservesReportProvenance(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			for _, captured := range []bool{false, true} {
				t.Run(fmt.Sprintf("captured-status-%t", captured), func(t *testing.T) { testInventoryWorkerPreservesReportProvenance(t, backend, captured) })
			}
		})
	}
}

// testInventoryWorkerPreservesReportProvenance exercises the production sweep
// against independent memory transactions and the shared encrypted SQL pool.
func testInventoryWorkerPreservesReportProvenance(t *testing.T, backend string, capturedStatus bool) {
	t.Helper()
	cfg := Config{Storage: backend, Clock: clock.NewFake(time.Now().UTC())}
	if backend == "sqlite" {
		cfg.DSN = filepath.Join(t.TempDir(), "backfill.db")
		cfg.StorageKeys = []string{"test"}
		cfg.Secrets = secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}
	}
	a, err := Build(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "native"}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, Device: storage.DeviceInfo{SerialNumber: "SER", ProductName: "Mac16,1", OSVersion: "27.0"}, EnrolledAt: cfg.Clock.Now()}}); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	oldAt := cfg.Clock.Now()
	command, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.Enqueue(ctx, []mdm.EnrollmentID{id}, command, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	responseBody := func(uuid, version string) []byte {
		t.Helper()
		raw, err := plist.Marshal(map[string]any{"Status": "Acknowledged", "UDID": id.ID, "CommandUUID": uuid, "QueryResponses": map[string]any{"SerialNumber": "SER", "OSVersion": version}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	oldResponse, err := mdm.DecodeResponse(responseBody(command.UUID, "27.0"), "")
	if err != nil {
		t.Fatal(err)
	}
	// Bypass observers deliberately to model reports retained before provenance capture.
	if err := a.Store.StoreResult(ctx, id, oldResponse, oldAt); err != nil {
		t.Fatal(err)
	}
	legacyEngine, err := ddm.New(ddm.Config{Store: a.Engine.Store(), Clock: cfg.Clock})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyEngine.Status(ctx, id, []byte(`{"StatusItems":{"device":{"operating-system":{"version":"27.0"}}}}`)); err != nil {
		t.Fatal(err)
	}
	if capturedStatus {
		if _, err := a.Engine.Status(ctx, id, []byte(`{"StatusItems":{"device":{"operating-system":{"version":"27.0"}}}}`)); err != nil {
			t.Fatal(err)
		}
	}
	authority, err := testpki.NewCA("inventory identity")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := authority.Issue(id.ID, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := a.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	enrollment.CertHash = cms.Fingerprint(identity.Cert)
	enrollment.CertHashAt = time.Now().UTC()
	if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *enrollment}); err != nil {
		t.Fatal(err)
	}
	setupJSON(t, a.protocol, "issued-identity:"+enrollment.CertHash, identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, UDID: id.ID, Serial: "SER", NotAfter: identity.Cert.NotAfter})
	cloudObservation, err := inventory.ResourceObservation(source.Reference{Kind: "axm.device", AccountID: "apple", ResourceID: "opaque"}, json.RawMessage(`{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"SER"}}`), oldAt, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := a.Inventory.Observe(ctx, "", cloudObservation)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := a.refreshNativeInventory(ctx); err != nil {
			t.Fatal(err)
		}
	}
	nativeRefs := []source.Reference{inventory.EnrollmentReference("mdm.DeviceInformation", id), inventory.EnrollmentReference("ddm.status", id)}
	for _, ref := range append(append([]source.Reference{}, nativeRefs...), inventory.EnrollmentReference("mdm.identity", id), inventory.EnrollmentReference("enrollment", id)) {
		record, err := a.Inventory.SourceResource(ctx, ref)
		if err != nil || !record.Incomplete || record.Snapshot.Native != nil {
			t.Fatal("background sweep qualified a legacy original", record, err)
		}
		history, err := a.Inventory.History(ctx, ref)
		if err != nil || len(history) != 1 {
			t.Fatal("sweep duplicated a legacy original", history, err)
		}
	}
	mux := inventoryTestMux(a)
	post := func(path, body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(ctx, "POST", path, strings.NewReader(body)))
		if w.Code != want {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	mapping := fmt.Sprintf(`{"mappings":[{"name":"native_version","inputs":[{"resource_id":%q,"path":"QueryResponses.OSVersion"}]}]}`, nativeRefs[0].Key())
	for _, ref := range nativeRefs {
		post("/inventory/correlations", fmt.Sprintf(`{"apple":%q,"native":%q}`, cloud.ID, ref.Key()), 400)
	}
	post("/inventory/raw/devices/"+cloud.ID+"/resolve", mapping, 400)
	// A new tracked command has identical returned hardware facts and the
	// same injected receipt time. Core correctly rejects already-terminal UUIDs;
	// the new UUID retains its protocol addressing role for this fresh receipt.
	fresh, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Core.Enqueue(ctx, []mdm.EnrollmentID{id}, fresh, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	freshResponse, err := mdm.DecodeResponse(responseBody(fresh.UUID, "27.0"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Core.Connect(ctx, &mdm.Request{Certificate: identity.Cert}, freshResponse); err != nil {
		t.Fatal(err)
	}
	// With capturedStatus, this is byte-identical to an earlier unchecked
	// original at the identical injected time; its new authenticated receipt
	// must establish its own provenance through the production engine observer.
	if _, err := a.Engine.Status(ctx, id, []byte(`{"StatusItems":{"device":{"operating-system":{"version":"27.0"}}}}`)); err != nil {
		t.Fatal(err)
	}
	histories := map[source.Reference]int{}
	for _, ref := range nativeRefs {
		record, err := a.Inventory.SourceResource(ctx, ref)
		if err != nil || record.Incomplete || record.Snapshot.Native == nil || !record.Snapshot.Native.Authenticated || record.Snapshot.Native.Method != correlation.AutomatedDeviceEnrollment {
			t.Fatal("fresh report lacks captured provenance", record, err)
		}
		history, err := a.Inventory.History(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		histories[ref] = len(history)
		if len(history) != 2 {
			t.Fatal("legacy original lost or duplicated", history)
		}
		post("/inventory/correlations", fmt.Sprintf(`{"apple":%q,"native":%q}`, cloud.ID, ref.Key()), 200)
	}
	post("/inventory/raw/devices/"+cloud.ID+"/resolve", mapping, 200)
	for range 2 {
		if err := a.refreshNativeInventory(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range nativeRefs {
		record, err := a.Inventory.SourceResource(ctx, ref)
		if err != nil || record.Incomplete || record.Snapshot.Native == nil || !record.Snapshot.Native.Authenticated {
			t.Fatal("sweep downgraded a fresh receipt", record, err)
		}
		history, err := a.Inventory.History(ctx, ref)
		if err != nil || len(history) != histories[ref] {
			t.Fatal("sweep duplicated or rewrote report provenance", history, err)
		}
		links, err := a.Inventory.Links(ctx, ref)
		if err != nil || len(links) != 1 {
			t.Fatal("exact backfill invalidated checked evidence", links, err)
		}
	}
	post("/inventory/raw/devices/"+cloud.ID+"/resolve", mapping, 200)
}
