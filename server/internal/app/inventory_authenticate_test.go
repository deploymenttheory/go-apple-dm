package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

// TestInventoryAuthenticateFactsFollowActualReceipts crosses real pinned Core
// Idle/Authenticate processing and background sweeps, including same-certificate
// retries whose persistent DeviceInfo still contains older unverified fields.
func TestInventoryAuthenticateFactsFollowActualReceipts(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			at := time.Now().UTC()
			ticker := clock.NewFake(at)
			cfg := Config{Storage: backend, Clock: ticker}
			if backend == "sqlite" {
				cfg.DSN = filepath.Join(t.TempDir(), "authenticate.db")
				cfg.StorageKeys = []string{"test"}
				cfg.Secrets = secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}
			}
			a, err := Build(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "receipt-device"}
			enrollment := storage.Enrollment{ID: id, Enabled: true, Device: storage.DeviceInfo{SerialNumber: "OLD-UNVERIFIED", DeviceName: "Unchanged", ProductName: "Mac16,1", OSVersion: "26.0"}, EnrolledAt: at.Add(-time.Hour)}
			if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: enrollment}); err != nil {
				t.Fatal(err)
			}
			if err := a.refreshNativeInventory(ctx); err != nil {
				t.Fatal(err)
			}
			ref := inventory.EnrollmentReference("mdm.identity", id)
			legacy, err := a.Inventory.SourceResource(ctx, ref)
			if err != nil || !legacy.Incomplete || legacy.Snapshot.Native != nil {
				t.Fatal(legacy, err)
			}
			authority, err := testpki.NewCA("receipt CA")
			if err != nil {
				t.Fatal(err)
			}
			certificate, err := authority.Issue(id.ID, at.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			enrollment.CertHash = cms.Fingerprint(certificate.Cert)
			enrollment.CertHashAt = at
			if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: enrollment}); err != nil {
				t.Fatal(err)
			}
			setupJSON(t, a.protocol, "issued-identity:"+enrollment.CertHash, identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, UDID: id.ID, Serial: "SER", NotAfter: certificate.Cert.NotAfter})
			cloudObservation, err := inventory.ResourceObservation(source.Reference{Kind: "axm.device", AccountID: "apple", ResourceID: "opaque"}, json.RawMessage(`{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"SER"}}`), at, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			cloud, err := a.Inventory.Observe(ctx, "", cloudObservation)
			if err != nil {
				t.Fatal(err)
			}
			ticker.Advance(time.Second)
			idleRaw, err := plist.Marshal(map[string]any{"Status": "Idle", "UDID": id.ID})
			if err != nil {
				t.Fatal(err)
			}
			idle, err := mdm.DecodeResponse(idleRaw, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Core.Connect(ctx, &mdm.Request{Certificate: certificate.Cert}, idle); err != nil {
				t.Fatal(err)
			}
			history, err := a.Inventory.History(ctx, ref)
			if err != nil || len(history) != 1 || history[0].Native != nil {
				t.Fatal("Idle poll qualified historical hardware facts", history, err)
			}
			if _, err := a.Inventory.Link(ctx, cloud.Source, ref, ticker.Now()); !errors.Is(err, inventory.ErrInvalid) {
				t.Fatal("Idle poll made legacy identity linkable", err)
			}
			information := func(version string) inventory.ResourceRecord {
				t.Helper()
				command, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion"}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.Core.Enqueue(ctx, []mdm.EnrollmentID{id}, command, storage.EnqueueOptions{}); err != nil {
					t.Fatal(err)
				}
				raw, err := plist.Marshal(map[string]any{"Status": "Acknowledged", "UDID": id.ID, "CommandUUID": command.UUID, "QueryResponses": map[string]any{"OSVersion": version}})
				if err != nil {
					t.Fatal(err)
				}
				response, err := mdm.DecodeResponse(raw, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.Core.Connect(ctx, &mdm.Request{Certificate: certificate.Cert}, response); err != nil {
					t.Fatal(err)
				}
				record, err := a.Inventory.SourceResource(ctx, inventory.EnrollmentReference("mdm.DeviceInformation", id))
				if err != nil {
					t.Fatal(err)
				}
				return record
			}
			ticker.Advance(time.Second)
			missingSerial := information("26.2")
			if missingSerial.Snapshot.Native == nil || missingSerial.Snapshot.Native.Serial != "" {
				t.Fatal("serial-omitting command borrowed unchecked stored serial", missingSerial)
			}
			if _, err := a.Engine.Status(ctx, id, []byte(`{"StatusItems":{"device":{"operating-system":{"version":"26.3"}}}}`)); err != nil {
				t.Fatal(err)
			}
			missingStatus, err := a.Inventory.SourceResource(ctx, inventory.EnrollmentReference("ddm.status", id))
			if err != nil || missingStatus.Snapshot.Native == nil || missingStatus.Snapshot.Native.Serial != "" {
				t.Fatal("DDM borrowed unchecked stored serial", missingStatus, err)
			}
			for _, record := range []inventory.ResourceRecord{missingSerial, missingStatus} {
				if _, err := a.Inventory.Link(ctx, cloud.Source, record.Source, ticker.Now()); !errors.Is(err, inventory.ErrConflict) {
					t.Fatal("serial-omitting fresh report enriched unchecked hardware", err)
				}
			}
			authenticate := func(version string, includeSerial bool) []byte {
				t.Helper()
				body := map[string]any{"MessageType": "Authenticate", "UDID": id.ID, "Topic": "com.apple.mgmt.External.test", "DeviceName": "Unchanged", "Model": "Mac16,1", "ModelName": "Mac", "FutureOpaque": uint64(9007199254740993), "FutureFalse": false}
				if includeSerial {
					body["SerialNumber"] = "SER"
				}
				if version != "" {
					body["OSVersion"] = version
				}
				raw, err := plist.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := mdm.DecodeCheckin(raw)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.Core.Checkin(ctx, &mdm.Request{Certificate: certificate.Cert}, receipt); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			original := authenticate("", true)
			current, err := a.Inventory.SourceResource(ctx, ref)
			if err != nil || current.Incomplete || current.Snapshot.Native == nil || !current.Snapshot.Native.Authenticated || !current.Snapshot.ObservedAt.Equal(ticker.Now()) {
				t.Fatal("actual Authenticate receipt not qualified at receipt time", current, err)
			}
			if _, ok := current.Snapshot.Fields["OSVersion"]; ok {
				t.Fatal("omitted old OS version was rebadged", current)
			}
			if string(current.Snapshot.Fields["FutureOpaque"]) != "9007199254740993" || string(current.Snapshot.Fields["FutureFalse"]) != "false" {
				t.Fatal("unknown receipt fields lost", current)
			}
			var retained struct {
				Plist []byte `json:"plist"`
			}
			if err := json.Unmarshal(current.Snapshot.Raw, &retained); err != nil || !bytes.Equal(retained.Plist, original) {
				t.Fatal("original Authenticate plist not preserved", err)
			}
			stored, err := a.Store.Get(ctx, id)
			if err != nil || stored.Device.OSVersion != "26.2" || stored.Device.SerialNumber != "OLD-UNVERIFIED" {
				t.Fatal("fixture did not retain older persistent facts", stored, err)
			}
			var mappings []inventoryMapping
			if err := json.Unmarshal([]byte(fmt.Sprintf(`[{"name":"os","inputs":[{"resource_id":%q,"path":"OSVersion"}]}]`, ref.Key())), &mappings); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Inventory.Link(ctx, cloud.Source, ref, ticker.Now()); err != nil {
				t.Fatal(err)
			}
			resolved, err := a.resolveInventory(ctx, cloud.ID, mappings)
			if err != nil || resolved["os"].Selected != nil {
				t.Fatal("unverified omitted facts entered resolution", resolved, err)
			}
			for _, record := range []inventory.ResourceRecord{missingSerial, missingStatus} {
				if _, err := a.Inventory.Link(ctx, cloud.Source, record.Source, ticker.Now()); !errors.Is(err, inventory.ErrConflict) {
					t.Fatal("new Authenticate retroactively qualified a serial-omitting report", err)
				}
			}
			ticker.Advance(time.Second)
			qualifiedOmission := information("26.4")
			if qualifiedOmission.Snapshot.Native == nil || qualifiedOmission.Snapshot.Native.Serial != "SER" {
				t.Fatal("genuine retained serial binding was lost", qualifiedOmission)
			}
			if _, err := a.Inventory.Link(ctx, cloud.Source, qualifiedOmission.Source, ticker.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Engine.Status(ctx, id, []byte(`{"StatusItems":{"device":{"operating-system":{"version":"26.5"}}}}`)); err != nil {
				t.Fatal(err)
			}
			qualifiedStatus, err := a.Inventory.SourceResource(ctx, missingStatus.Source)
			if err != nil || qualifiedStatus.Snapshot.Native == nil || qualifiedStatus.Snapshot.Native.Serial != "SER" {
				t.Fatal("DDM did not use genuine retained serial binding", qualifiedStatus, err)
			}
			if _, err := a.Inventory.Link(ctx, cloud.Source, qualifiedStatus.Source, ticker.Now()); err != nil {
				t.Fatal(err)
			}
			// A later actual native report supplies the fact. A subsequent omission
			// retains that verified history, never the older unverified stored value.
			ticker.Advance(time.Second)
			authenticate("27.1", false)
			ticker.Advance(time.Second)
			authenticate("", true)
			if _, err := a.Inventory.Link(ctx, cloud.Source, ref, ticker.Now()); err != nil {
				t.Fatal(err)
			}
			resolved, err = a.resolveInventory(ctx, cloud.ID, mappings)
			if err != nil || resolved["os"].Selected == nil || inventory.String(resolved["os"].Selected.Value) != "27.1" {
				t.Fatal("qualified native omission history lost", resolved, err)
			}
			before, err := a.Inventory.History(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			ticker.Advance(time.Second)
			if _, err := a.Core.Connect(ctx, &mdm.Request{Certificate: certificate.Cert}, idle); err != nil {
				t.Fatal(err)
			}
			if err := a.refreshNativeInventory(ctx); err != nil {
				t.Fatal(err)
			}
			after, err := a.Inventory.History(ctx, ref)
			if err != nil || len(after) != len(before) {
				t.Fatal("Idle or worker replay copied merged identity facts", after, err)
			}
			current, err = a.Inventory.SourceResource(ctx, ref)
			if err != nil || current.Snapshot.Native == nil || !current.Snapshot.Native.Authenticated {
				t.Fatal("captured Authenticate provenance downgraded", current, err)
			}
		})
	}
}
