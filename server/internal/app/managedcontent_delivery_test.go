package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

// TestManagedContentNativeDeliverySimulator proves the actual server queue and
// signed simulator command exchange, preserving historical processing while a
// fresh native report changes current installed state and version independently.
func TestManagedContentNativeDeliverySimulator(t *testing.T) {
	f := contentFixtureService(t)
	cfg := contentConfig(t, f)
	ca, err := testpki.NewCA("content-delivery")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CARoots = ca.Pool()
	a, _ := contentBuild(t, cfg)
	identity, err := ca.Issue("content-device", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "content-device"}
	seedContentEnrollment(t, a, id, cms.Fingerprint(identity.Cert), "S1")
	server := httptest.NewServer(a.Handler)
	t.Cleanup(server.Close)
	device := simulator.New(id.ID, simulator.WithURLs(server.URL+"/mdm", server.URL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
	installed := true
	device.Responder = func(c *mdm.Command) simulator.Reply {
		switch c.RequestType {
		case "InstallApplication":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstallApplicationResponse{Identifier: new("com.example.selected"), State: new("Managed")}}
		case "InstalledApplicationList":
			items := []commands.InstalledApplicationListResponseInstalledApplicationList{}
			if installed {
				items = append(items, commands.InstalledApplicationListResponseInstalledApplicationList{Identifier: new("com.example.selected"), Version: new("2.4"), Installing: new(false)})
			}
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstalledApplicationListResponse{InstalledApplicationList: items}}
		default:
			return simulator.AcknowledgeAll(c)
		}
	}
	command, err := mdm.NewCommand(&commands.InstallApplication{ITunesStoreID: new(int64(1)), Options: &commands.InstallApplicationOptions{PurchaseMethod: new(int64(1))}}, mdm.WithUUID("install-selected"))
	if err != nil {
		t.Fatal(err)
	}
	dispatch := managedcontent.Dispatch{ID: command.UUID, Enrollment: id, Serial: "S1", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Method: managedcontent.MDM, MDM: command.Raw, Digest: "immutable-sdk-wire"}
	adapter := contentDelivery{a}
	observation, err := adapter.Submit(t.Context(), dispatch)
	if err != nil || !observation.Found || observation.Installed || observation.CommandAcknowledged {
		t.Fatal(observation, err)
	}
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	observation, err = adapter.Inspect(t.Context(), dispatch)
	if err != nil || !observation.CommandAcknowledged || !observation.Terminal || observation.Installed {
		t.Fatal("historical processing became current installation", observation, err)
	}
	query := func() {
		t.Helper()
		cmd, err := mdm.NewCommand(&commands.InstalledApplicationList{Identifiers: []string{"com.example.selected"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := device.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	query()
	observation, err = adapter.Inspect(t.Context(), dispatch)
	if err != nil || !observation.Installed || !observation.Terminal || observation.AppVersion != "2.4" || !bytes.Contains(observation.Raw, []byte("bundleId")) {
		t.Fatal("native installation missing", observation, err)
	}
	installed = false
	query()
	observation, err = adapter.Inspect(t.Context(), dispatch)
	if err != nil || observation.Installed || !observation.Terminal || observation.AppVersion != "" {
		t.Fatal("removal erased historical processing or retained current version", observation, err)
	}
	// Repeated same identity does not enqueue again, even after acknowledgment.
	if _, err := adapter.Submit(t.Context(), dispatch); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 3 {
		t.Fatal("dispatch duplicated", len(rows.Items), err)
	}
	changed := dispatch
	changed.Digest = "different"
	if _, err := adapter.Submit(t.Context(), changed); !errors.Is(err, managedcontent.ErrConflict) {
		t.Fatal("conflicting wire accepted", err)
	}
	// A required event failure rolls back both the native queue and its receipt.
	another := dispatch
	another.ID = "failed-install"
	cmd, err := mdm.NewCommand(&commands.InstallApplication{ITunesStoreID: new(int64(2))}, mdm.WithUUID(another.ID))
	if err != nil {
		t.Fatal(err)
	}
	another.MDM = cmd.Raw
	another.Content.Asset.AdamID = "2"
	a.eventPublisher.CaptureAdditional = func(context.Context, event.Event) error { return errors.New("capture unavailable") }
	if _, err := adapter.Submit(t.Context(), another); !errors.Is(err, event.ErrCapture) {
		t.Fatal("native dispatch escaped failed capture", err)
	}
	if _, err := a.protocol.Get(t.Context(), deliveryKey(another.ID)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("native receipt escaped rollback", err)
	}
	a.eventPublisher.CaptureAdditional = nil
}

// TestManagedContentDeclarativeDelivery keeps declaration publication, token
// acknowledgment, terminal app processing and current installation separate.
func TestManagedContentDeclarativeDelivery(t *testing.T) {
	f := contentFixtureService(t)
	cfg := contentConfig(t, f)
	ca, err := testpki.NewCA("content-ddm")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CARoots = ca.Pool()
	a, _ := contentBuild(t, cfg)
	identity, err := ca.Issue("ddm-content", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "ddm-content"}
	seedContentEnrollment(t, a, id, cms.Fingerprint(identity.Cert), "S-DDM")
	server := httptest.NewServer(a.Handler)
	t.Cleanup(server.Close)
	device := simulator.New(id.ID, simulator.WithURLs(server.URL+"/mdm", server.URL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
	if _, err := device.SyncDDM(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := device.PostDDMStatus(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	target, err := service.EnrollmentTarget(t.Context(), a.Store, id)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := managedcontent.Dispatch{ID: "ddm-selected", Enrollment: id, Serial: "S-DDM", Method: managedcontent.DDM, Digest: "ddm-immutable", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}}
	for _, raw := range []string{`{"Identifier":"ddm-selected","Type":"com.apple.configuration.app.managed","Payload":{"AppStoreID":"1","InstallBehavior":{"Install":"Required","License":{"Assignment":"Device"}}}}`, `{"Identifier":"ddm-selected-activation","Type":"com.apple.activation.simple","Payload":{"StandardConfigurations":["ddm-selected"]}}`} {
		decl, err := ddm.ParseDeclaration([]byte(raw), target)
		if err != nil {
			t.Fatal(err)
		}
		dispatch.Declarations = append(dispatch.Declarations, managedcontent.Document{Identifier: decl.Identifier, ServerToken: decl.ServerToken, Raw: []byte(raw)})
	}
	adapter := contentDelivery{a}
	out, err := adapter.Submit(t.Context(), dispatch)
	if err != nil || !out.DeclarationPublished || out.Installed || out.Terminal {
		t.Fatal(out, err)
	}
	if _, err := device.SyncDDM(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := device.PostDDMStatus(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	report := func(apps any) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"StatusItems": map[string]any{"app": map[string]any{"managed": map[string]any{"list": apps}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := device.DeclarativeManagement(t.Context(), "status", body); err != nil {
			t.Fatal(err)
		}
	}
	report([]map[string]any{{"identifier": "com.example.ddm", "declaration-identifier": "ddm-selected", "state": "managed", "version": "3.1"}})
	out, err = adapter.Inspect(t.Context(), dispatch)
	if err != nil || !out.Terminal || !out.Installed || out.AppVersion != "3.1" {
		t.Fatal("native DDM state missing", out, err)
	}
	report([]map[string]any{{"identifier": "com.example.ddm", "_removed": true}})
	out, err = adapter.Inspect(t.Context(), dispatch)
	if err != nil || !out.Terminal || out.Installed || out.AppVersion != "" {
		t.Fatal("DDM removal lost historical processing", out, err)
	}
	// A native DDM-owned app cannot silently switch to MDM delivery.
	mdmCommand, err := mdm.NewCommand(&commands.InstallApplication{ITunesStoreID: new(int64(1))}, mdm.WithUUID("method-switch"))
	if err != nil {
		t.Fatal(err)
	}
	switched := managedcontent.Dispatch{ID: mdmCommand.UUID, Enrollment: id, Serial: "S-DDM", Method: managedcontent.MDM, MDM: mdmCommand.Raw, Digest: "other-method", Content: dispatch.Content}
	if _, err := adapter.Submit(t.Context(), switched); !errors.Is(err, managedcontent.ErrConflict) {
		t.Fatal("native method switch accepted", err)
	}
	// Another declaration generation cannot borrow the prior app-status proof.
	changed, _, err := a.Engine.PutDeclaration(t.Context(), []byte(`{"Identifier":"ddm-selected","Type":"com.apple.configuration.app.managed","Payload":{"AppStoreID":"2","InstallBehavior":{"Install":"Required"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if changed.ServerToken == dispatch.Declarations[0].ServerToken {
		t.Fatal("fixture token unchanged")
	}
	if _, err := adapter.Inspect(t.Context(), dispatch); !errors.Is(err, managedcontent.ErrConflict) {
		t.Fatal("changed declaration inherited old proof", err)
	}
}

// TestManagedContentSDKStepNativeComposition reaches the actual SDK observation
// validator through the reference HTTP operation. A durable queue receipt needs
// an authoritative nonzero observation timestamp, independently of installation.
func TestManagedContentSDKStepNativeComposition(t *testing.T) {
	f := contentFixtureService(t)
	f.assets.Store(true)
	f.assignments.Store(true)
	cfg := contentConfig(t, f)
	a, token := contentBuild(t, cfg)
	registerContent(t, a, token)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "full-sdk-selected"}
	seedContentEnrollment(t, a, id, "native-sdk-pin", "S-SDK")
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "S-SDK"}, Method: managedcontent.MDM, MaxPages: 2}
	w := contentRequest(t, a, token, "PUT", "/managed-content/operations/full-sdk", map[string]any{"mode": "install", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", "/managed-content/operations/full-sdk/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 200 {
		t.Fatal("SDK rejected native dispatch observation", w.Code, w.Body.String())
	}
	op, evidence, err := a.ManagedContent.LoadEvidence(t.Context(), "full-sdk")
	if err != nil || op.Phase != managedcontent.Dispatched || op.Observation == nil || op.Observation.Installed || op.Observation.ObservedAt.IsZero() || evidence.Dispatch == nil {
		t.Fatal(op, err)
	}
	adapter := contentDelivery{a}
	observation, err := adapter.Submit(t.Context(), *evidence.Dispatch)
	if err != nil || !observation.Found || observation.ObservedAt.IsZero() || observation.Installed {
		t.Fatal("idempotent queue receipt rejected", observation, err)
	}
	if f.posts.Load() != 0 {
		t.Fatal("existing licensing triggered synthetic mutation")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, token = contentBuild(t, cfg)
	w = contentRequest(t, a, token, "POST", "/managed-content/operations/full-sdk/reconcile", contentWorkerInput{Worker: "operator", LeaseSeconds: 60, Note: "original worker stopped; inspect retained native queue receipt"})
	if w.Code != 200 {
		t.Fatal("restart reconciliation failed", w.Code, w.Body.String())
	}
	op, err = a.ManagedContent.Load(t.Context(), "full-sdk")
	if err != nil || op.Phase != managedcontent.Dispatched || op.Observation == nil || op.Observation.Installed {
		t.Fatal("restart inferred installation", op, err)
	}
}
