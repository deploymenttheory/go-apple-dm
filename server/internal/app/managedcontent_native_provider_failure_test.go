package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/ddmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

// contentProviderStore preserves the SQL enrollment locking capability while
// exercising the normal native Store extension's independently failing reads.
type contentProviderStore struct {
	*storagetest.Failing
	lock      func(context.Context, mdm.EnrollmentID) (*storage.Enrollment, error)
	lockError error
}

// GetForUpdate retains the SQL transaction lock contract while allowing the
// native storage extension to fail before any ownership evidence is returned.
func (s contentProviderStore) GetForUpdate(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	if s.lockError != nil {
		return nil, s.lockError
	}
	return s.lock(ctx, id)
}

// contentProviderSnapshot records domain state, excluding the intentionally
// separate audit occurrence produced by a denied administration request.
func contentProviderSnapshot(t *testing.T, a *App, name string) []byte {
	t.Helper()
	op, evidence, err := a.ManagedContent.LoadEvidence(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(struct {
		Operation managedcontent.Operation `json:"operation"`
		Evidence  managedcontent.Evidence  `json:"evidence"`
	}{op, evidence})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestManagedContentNativeProviderReadFailure proves a real admitted operation
// cannot advance or yield positive evidence while its native provider is down.
func TestManagedContentNativeProviderReadFailure(t *testing.T) {
	a, token, f, id, device, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "native-provider", plan)
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := "/managed-content/operations/native-provider"
	contentFailureReply(t, a, token, "GET", path+"/native-observation", nil, 200)
	before := contentProviderSnapshot(t, a, "native-provider")
	posts := f.requests.Load()
	original := a.Store
	locker, ok := original.(interface {
		GetForUpdate(context.Context, mdm.EnrollmentID) (*storage.Enrollment, error)
	})
	if !ok {
		t.Fatal("SQL native locker unavailable")
	}
	for _, method := range []string{"Commands", "Get", "GetForUpdate"} {
		t.Run(method, func(t *testing.T) {
			failing := contentProviderStore{Failing: &storagetest.Failing{Store: original, Fail: map[string]error{method: errors.New("fixture-provider-secret")}}, lock: locker.GetForUpdate}
			if method == "GetForUpdate" {
				failing.lockError = errors.New("fixture-provider-secret")
			}
			a.Store = failing
			contentFailureReply(t, a, token, "GET", path+"/native-observation", nil, 503)
			contentFailureReply(t, a, token, "POST", path+"/status-refresh", map[string]any{"requestId": method}, 503)
			a.Store = original
			if !bytes.Equal(before, contentProviderSnapshot(t, a, "native-provider")) {
				t.Fatal("native provider failure changed SDK state")
			}
		})
	}
	contentFailureReply(t, a, token, "GET", path+"/native-observation", nil, 200)
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 1 || f.requests.Load() != posts {
		t.Fatal("provider failure produced native or Apple work", err)
	}
}

// contentProviderDDMBench establishes publication and owned app evidence through
// the real SDK and authenticated simulator, before any failure is introduced.
func contentProviderDDMBench(t *testing.T) (*App, string, *contentFixture, mdm.EnrollmentID, *simulator.Device, managedcontent.Dispatch) {
	t.Helper()
	a, token, f, id, device, plan := contentBoundaryBench(t)
	if _, err := device.SyncDDM(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := device.PostDDMStatus(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	plan.Method = managedcontent.DDM
	dispatch := contentBoundaryStep(t, a, token, "ddm-provider", plan)
	if _, err := device.SyncDDM(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := device.PostDDMStatus(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	contentProviderOwnedApp(t, device, dispatch)
	out, err := (contentDelivery{a}).Inspect(t.Context(), dispatch)
	if err != nil || !out.Installed || out.AppVersion != "4.2" {
		t.Fatal("signed positive control missing", out, err)
	}
	return a, token, f, id, device, dispatch
}

// contentProviderOwnedApp sends complete owned incremental facts on the actual
// authenticated native protocol; the retained report is never invented locally.
func contentProviderOwnedApp(t *testing.T, device *simulator.Device, d managedcontent.Dispatch) {
	t.Helper()
	contentPostNativeStatus(t, device, map[string]any{"app.managed.list": []map[string]any{{"identifier": "com.example.provider", "declaration-identifier": d.Declarations[0].Identifier, "state": "managed", "version": "4.2"}}}, false)
}

// TestManagedContentDDMProviderReadFailure exercises failed declaration, status
// and effective-membership reads over the real DDM storage extension.
func TestManagedContentDDMProviderReadFailure(t *testing.T) {
	a, token, f, id, _, dispatch := contentProviderDDMBench(t)
	original := a.Engine
	before := contentProviderSnapshot(t, a, "ddm-provider")
	requests := f.requests.Load()
	for _, method := range []string{"GetDeclaration", "DeclarationStatus", "StatusReports"} {
		t.Run(method, func(t *testing.T) {
			engine, err := ddm.New(ddm.Config{Store: &ddmtest.Failing{Store: original.Store(), Fail: map[string]error{method: errors.New("fixture-provider-secret")}}})
			if err != nil {
				t.Fatal(err)
			}
			a.Engine = engine
			contentFailureReply(t, a, token, "GET", "/managed-content/operations/ddm-provider/native-observation", nil, 503)
			a.Engine = original
			if !bytes.Equal(before, contentProviderSnapshot(t, a, "ddm-provider")) {
				t.Fatal("DDM provider failure rewrote operation")
			}
		})
	}
	// An effective-membership outage must prevent an additional dispatch; it
	// cannot be interpreted as an empty native management scope.
	engine, err := ddm.New(ddm.Config{Store: &ddmtest.Failing{Store: original.Store(), Fail: map[string]error{"StaticDeclarations": errors.New("fixture-provider-secret")}}})
	if err != nil {
		t.Fatal(err)
	}
	a.Engine = engine
	candidate := contentBoundaryDispatch(t, id, "provider-unavailable", 2)
	candidate.Content.Asset.AdamID = "2"
	if _, err := (contentDelivery{a}).Submit(t.Context(), candidate); err == nil {
		t.Fatal("membership failure accepted native dispatch")
	}
	a.Engine = original
	for _, key := range []string{deliveryKey(candidate.ID), contentAssetKey(id, "2")} {
		if _, err := a.protocol.Get(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("membership failure retained owner", err)
		}
	}
	out, err := (contentDelivery{a}).Inspect(t.Context(), dispatch)
	if err != nil || !out.Installed || f.requests.Load() != requests {
		t.Fatal("provider recovery lost real evidence or contacted Apple", out, err)
	}
}

// TestManagedContentGenericDDMProviderFailure exercises the actual HTTP guard's
// dynamic engine dependency, including both assignment and set-membership edits.
func TestManagedContentGenericDDMProviderFailure(t *testing.T) {
	a, token, f, id, _, _ := contentProviderDDMBench(t)
	original := a.Engine
	raw := []byte(`{"Identifier":"provider-unrelated","Type":"com.apple.configuration.app.managed","Payload":{"AppStoreID":"2","InstallBehavior":{"Install":"Required","License":{"Assignment":"Device"}}}}`)
	if _, _, err := a.Engine.PutDeclaration(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Engine.PutSet(t.Context(), "provider-set"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Engine.AddToSet(t.Context(), "provider-set", "provider-unrelated"); err != nil {
		t.Fatal(err)
	}
	requests := f.requests.Load()
	for _, tc := range []struct{ method, path string }{
		{"GetDeclaration", "/sets/provider-new/declarations/provider-unrelated"},
		{"AffectedEnrollments", "/sets/provider-new/declarations/provider-unrelated"},
		{"SetDeclarations", "/enrollments/device/" + id.ID + "/sets/provider-set"},
		{"GetDeclaration", "/enrollments/device/" + id.ID + "/sets/provider-set"},
	} {
		engine, err := ddm.New(ddm.Config{Store: &ddmtest.Failing{Store: original.Store(), Fail: map[string]error{tc.method: errors.New("fixture-provider-secret")}}})
		if err != nil {
			t.Fatal(err)
		}
		a.Engine = engine
		contentFailureReply(t, a, token, "PUT", tc.path, nil, 503)
		a.Engine = original
	}
	if _, err := a.Engine.GetSet(t.Context(), "provider-new"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("denied member edit created set", err)
	}
	sets, err := a.Engine.EnrollmentSets(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range sets {
		if set == "provider-set" {
			t.Fatal("denied assignment persisted")
		}
	}
	contentFailureReply(t, a, token, "PUT", "/enrollments/device/"+id.ID+"/sets/provider-set", nil, 200)
	if f.requests.Load() != requests {
		t.Fatal("DDM provider failures contacted Apple")
	}
}

// TestManagedContentAuthenticatedDDMInvalidEvidence demonstrates that malformed
// app data and changed declaration generations cannot reuse prior owned proof.
func TestManagedContentAuthenticatedDDMInvalidEvidence(t *testing.T) {
	for _, scenario := range []string{"malformed-app", "malformed-parent", "changed-token", "inactive"} {
		t.Run(scenario, func(t *testing.T) {
			a, token, f, _, device, d := contentProviderDDMBench(t)
			before := contentProviderSnapshot(t, a, "ddm-provider")
			requests := f.requests.Load()
			items := map[string]any{}
			status := 200
			switch scenario {
			case "malformed-app":
				items["app.managed.list"] = false
				status = 503
			case "malformed-parent":
				items["app"] = false
				status = 503
			default:
				serverToken := d.Declarations[0].ServerToken
				active := true
				if scenario == "changed-token" {
					serverToken = "different-native-generation"
				} else {
					active = false
				}
				items["management.declarations"] = map[string]any{"configurations": []map[string]any{{"identifier": d.Declarations[0].Identifier, "server-token": serverToken, "active": active, "valid": "valid"}}}
			}
			contentPostNativeStatus(t, device, items, false)
			path := "/managed-content/operations/ddm-provider/native-observation"
			contentFailureReply(t, a, token, "GET", path, nil, status)
			if status == 200 {
				out, err := (contentDelivery{a}).Inspect(t.Context(), d)
				if err != nil || out.Installed || out.AppVersion != "" {
					t.Fatal("stale generation reused installed proof", out, err)
				}
			}
			if !bytes.Equal(before, contentProviderSnapshot(t, a, "ddm-provider")) || f.requests.Load() != requests {
				t.Fatal("invalid native evidence changed SDK state or contacted Apple")
			}
		})
	}
}

// TestManagedContentStoredNativeWireCorruption persists malformed bytes through
// the actual native storage contract, then checks readback and explicit refresh
// fail closed without replacing immutable SDK evidence or replaying commands.
func TestManagedContentStoredNativeWireCorruption(t *testing.T) {
	for _, scenario := range []string{"install-response", "installed-list-response", "managed-list-response", "foreign-command"} {
		t.Run(scenario, func(t *testing.T) {
			a, token, f, id, device, plan := contentBoundaryBench(t)
			dispatch := contentBoundaryStep(t, a, token, "native-wire", plan)
			before := contentProviderSnapshot(t, a, "native-wire")
			requests := f.requests.Load()
			uuid := dispatch.ID
			switch scenario {
			case "foreign-command":
				if _, err := a.Store.Enqueue(t.Context(), []mdm.EnrollmentID{id}, &mdm.Command{UUID: "corrupt-foreign", RequestType: "InstallApplication", Raw: []byte("fixture-provider-secret invalid plist")}, storage.EnqueueOptions{}); err != nil {
					t.Fatal(err)
				}
				candidate := contentBoundaryDispatch(t, id, "blocked-by-corruption", 2)
				candidate.Content.Asset.AdamID = "2"
				if _, err := (contentDelivery{a}).Submit(t.Context(), candidate); err == nil {
					t.Fatal("corrupt foreign wire admitted new install")
				}
				if _, err := a.protocol.Get(t.Context(), deliveryKey(candidate.ID)); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("corrupt foreign wire created receipt", err)
				}
			default:
				if scenario != "install-response" {
					if _, err := device.Connect(t.Context()); err != nil {
						t.Fatal(err)
					}
					var payload commands.Command = &commands.InstalledApplicationList{Identifiers: []string{"com.example.boundary"}}
					if scenario == "managed-list-response" {
						payload = &commands.ManagedApplicationList{Identifiers: []string{"com.example.boundary"}}
					}
					cmd, err := mdm.NewCommand(payload)
					if err != nil {
						t.Fatal(err)
					}
					uuid = cmd.UUID
					if _, err := a.Core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
						t.Fatal(err)
					}
				}
				if err := a.Store.StoreResult(t.Context(), id, &mdm.Response{ID: id, CommandUUID: uuid, Status: mdm.StatusAcknowledged, Raw: []byte("fixture-provider-secret invalid plist")}, time.Now()); err != nil {
					t.Fatal(err)
				}
				contentFailureReply(t, a, token, "GET", "/managed-content/operations/native-wire/native-observation", nil, 503)
				if scenario == "install-response" {
					contentFailureReply(t, a, token, "POST", "/managed-content/operations/native-wire/status-refresh", map[string]any{"requestId": "corrupt-result"}, 503)
				}
			}
			if !bytes.Equal(before, contentProviderSnapshot(t, a, "native-wire")) || f.requests.Load() != requests {
				t.Fatal("corrupt native wire changed SDK state or contacted Apple")
			}
		})
	}
}
