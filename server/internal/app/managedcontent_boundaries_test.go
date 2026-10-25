package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

// contentBoundaryBench supplies genuine authenticated native and Apple-service
// fixtures; its plans use an existing license and cannot purchase or associate.
func contentBoundaryBench(t *testing.T) (*App, string, *contentFixture, mdm.EnrollmentID, *simulator.Device, managedcontent.Plan) {
	t.Helper()
	f := contentFixtureService(t)
	f.assets.Store(true)
	f.assignments.Store(true)
	cfg := contentConfig(t, f)
	ca, err := testpki.NewCA("content-boundary")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CARoots = ca.Pool()
	a, token := contentBuild(t, cfg)
	registerContent(t, a, token)
	identity, err := ca.Issue("boundary-device", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "boundary-device"}
	seedContentEnrollment(t, a, id, cms.Fingerprint(identity.Cert), "S-BOUNDARY")
	server := httptest.NewServer(a.Handler)
	t.Cleanup(server.Close)
	device := simulator.New(id.ID, simulator.WithURLs(server.URL+"/mdm", server.URL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
	device.Responder = func(c *mdm.Command) simulator.Reply {
		if c.RequestType == "InstallApplication" {
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstallApplicationResponse{Identifier: new("com.example.boundary"), State: new("Managed")}}
		}
		return simulator.AcknowledgeAll(c)
	}
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "S-BOUNDARY"}, Method: managedcontent.MDM, MaxPages: 2}
	return a, token, f, id, device, plan
}

// contentBoundaryDispatch builds immutable native wire without manufacturing an
// ownership record. The tested adapter must decide whether it can accept it.
func contentBoundaryDispatch(t *testing.T, id mdm.EnrollmentID, uuid string, asset int64) managedcontent.Dispatch {
	t.Helper()
	cmd, err := mdm.NewCommand(&commands.InstallApplication{ITunesStoreID: new(asset)}, mdm.WithUUID(uuid))
	if err != nil {
		t.Fatal(err)
	}
	return managedcontent.Dispatch{ID: uuid, Enrollment: id, Serial: "S-BOUNDARY", Method: managedcontent.MDM, Digest: "boundary-" + uuid, MDM: cmd.Raw, Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}}
}

// TestManagedContentPreexistingNativeConflicts verifies that native commands and
// both direct and set declarations are respected before any adapter admission.
func TestManagedContentPreexistingNativeConflicts(t *testing.T) {
	for _, scenario := range []string{"uuid", "same-asset-command", "direct-declaration", "set-declaration"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, f, id, _, _ := contentBoundaryBench(t)
			dispatch := contentBoundaryDispatch(t, id, "new-owned", 1)
			switch scenario {
			case "uuid", "same-asset-command":
				uuid, asset := "foreign-command", int64(1)
				if scenario == "uuid" {
					uuid, asset = dispatch.ID, 2
				}
				cmd, err := mdm.NewCommand(&commands.InstallApplication{ITunesStoreID: new(asset)}, mdm.WithUUID(uuid))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.Core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
					t.Fatal(err)
				}
			default:
				raw := []byte(`{"Identifier":"existing-app","Type":"com.apple.configuration.app.managed","Payload":{"AppStoreID":"1","InstallBehavior":{"Install":"Required","License":{"Assignment":"Device"}}}}`)
				if _, _, err := a.Engine.PutDeclaration(t.Context(), raw); err != nil {
					t.Fatal(err)
				}
				if scenario == "direct-declaration" {
					if _, err := a.Engine.AssignDeclaration(t.Context(), id, "existing-app"); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := a.Engine.PutSet(t.Context(), "existing-set"); err != nil {
						t.Fatal(err)
					}
					if _, err := a.Engine.AddToSet(t.Context(), "existing-set", "existing-app"); err != nil {
						t.Fatal(err)
					}
					if _, err := a.Engine.AssignSet(t.Context(), id, "existing-set"); err != nil {
						t.Fatal(err)
					}
				}
			}
			before, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			posts := f.posts.Load()
			var wakesBefore int
			if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_deliveries WHERE destination = ?", contentWakeDestination).Scan(&wakesBefore); err != nil {
				t.Fatal(err)
			}
			if _, err := (contentDelivery{a}).Submit(t.Context(), dispatch); !errors.Is(err, managedcontent.ErrConflict) {
				t.Fatal("foreign native work was adopted", err)
			}
			for _, key := range []string{deliveryKey(dispatch.ID), contentAssetKey(id, "1")} {
				if _, err := a.protocol.Get(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("denial persisted ownership", key, err)
				}
			}
			after, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil || len(before.Items) != len(after.Items) {
				t.Fatal("denial changed native queue", err)
			}
			var wakesAfter int
			if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_deliveries WHERE destination = ?", contentWakeDestination).Scan(&wakesAfter); err != nil {
				t.Fatal(err)
			}
			if wakesBefore != wakesAfter || posts != f.posts.Load() {
				t.Fatal("denial produced wake or Apple mutation")
			}
		})
	}
}

// contentBoundaryStep drives the actual public admission/worker composition and
// returns the SDK-generated immutable native dispatch for subsequent readback.
func contentBoundaryStep(t *testing.T, a *App, token, name string, plan managedcontent.Plan) managedcontent.Dispatch {
	t.Helper()
	path := "/managed-content/operations/" + name
	w := contentRequest(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, e, err := a.ManagedContent.LoadEvidence(t.Context(), name)
	if err != nil || e.Dispatch == nil {
		t.Fatal("missing SDK dispatch", err)
	}
	return *e.Dispatch
}

// TestManagedContentSuccessorNativeOwnership transfers the real SDK lane only
// after signed terminal native evidence, leaving predecessors unable to reclaim it.
func TestManagedContentSuccessorNativeOwnership(t *testing.T) {
	a, token, f, id, device, plan := contentBoundaryBench(t)
	first := contentBoundaryStep(t, a, token, "first", plan)
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := contentRequest(t, a, token, "POST", "/managed-content/operations/first/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	old, err := a.ManagedContent.Load(t.Context(), "first")
	if err != nil || old.Observation == nil || !old.Observation.Terminal {
		t.Fatal("native terminal evidence absent", old, err)
	}
	posts := f.posts.Load()
	plan.PredecessorID = "first"
	next := contentBoundaryStep(t, a, token, "next", plan)
	if next.Predecessor == nil || next.Predecessor.ID != first.ID || next.Predecessor.Digest != first.Digest {
		t.Fatal("successor lost exact predecessor", next.Predecessor)
	}
	current, err := a.ManagedContent.Load(t.Context(), "first")
	if err != nil || current.SupersededBy != "next" {
		t.Fatal("predecessor not fenced", current, err)
	}
	if _, err := (contentDelivery{a}).Inspect(t.Context(), first); !errors.Is(err, managedcontent.ErrConflict) {
		t.Fatal("stale predecessor still owns asset", err)
	}
	stale := next
	stale.ID = "stale-successor"
	if _, err := (contentDelivery{a}).Submit(t.Context(), stale); !errors.Is(err, managedcontent.ErrConflict) {
		t.Fatal("stale handoff reclaimed owner", err)
	}
	foreign := plan
	foreign.Target.Enrollment = mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "other-native"}
	foreign.Target.Serial = "S-OTHER"
	seedContentEnrollment(t, a, foreign.Target.Enrollment, "other-pin", "S-OTHER")
	w = contentRequest(t, a, token, "PUT", "/managed-content/operations/foreign-successor", map[string]any{"mode": "install", "plan": foreign})
	if w.Code != 409 {
		t.Fatal("cross-target predecessor accepted", w.Code, w.Body.String())
	}
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 2 || posts != f.posts.Load() {
		t.Fatal("handoff replayed queue or mutated licensing", len(rows.Items), err)
	}
}

// TestManagedContentRetainedReceiptIntegrity corrupts retained evidence only
// after genuine SDK/native admission; neither corruption nor identity drift can
// authorize a fresh enqueue or a positive ownership observation.
func TestManagedContentRetainedReceiptIntegrity(t *testing.T) {
	a, token, f, id, _, plan := contentBoundaryBench(t)
	dispatch := contentBoundaryStep(t, a, token, "retained", plan)
	record, err := a.protocol.Get(t.Context(), deliveryKey(dispatch.ID))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := a.protocol.Get(t.Context(), contentAssetKey(id, "1"))
	if err != nil {
		t.Fatal(err)
	}
	posts := f.posts.Load()
	for _, scenario := range []string{"malformed", "digest", "native-target"} {
		t.Run(scenario, func(t *testing.T) {
			altered := append([]byte(nil), record.Value...)
			if scenario == "malformed" {
				altered = []byte(`{"dispatch":`)
			} else {
				var saved contentDispatchRecord
				if err := json.Unmarshal(altered, &saved); err != nil {
					t.Fatal(err)
				}
				if scenario == "digest" {
					saved.Dispatch.Digest = "tampered"
				} else {
					saved.Dispatch.Enrollment.ID = "different-native"
				}
				altered, err = json.Marshal(saved)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := a.protocol.Update(t.Context(), []string{record.Key}, func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: record.Key, Value: altered}) }); err != nil {
				t.Fatal(err)
			}
			adapter := contentDelivery{a}
			if _, err := adapter.Inspect(t.Context(), dispatch); !errors.Is(err, managedcontent.ErrConflict) {
				t.Fatal("corrupt receipt inspected as owned", err)
			}
			if _, err := adapter.Submit(t.Context(), dispatch); !errors.Is(err, managedcontent.ErrConflict) {
				t.Fatal("corrupt receipt replayed", err)
			}
		})
	}
	retained, err := a.protocol.Get(t.Context(), owner.Key)
	if err != nil || !bytes.Equal(owner.Value, retained.Value) {
		t.Fatal("corruption replaced owner", err)
	}
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 1 || posts != f.posts.Load() {
		t.Fatal("corruption changed queue or licensing", len(rows.Items), err)
	}
}
