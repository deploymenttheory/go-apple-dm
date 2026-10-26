package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
)

// TestManagedContentDDMReportOrder proves that signed current installation is
// terminal regardless of whether the app or its generation confirmation arrives first.
func TestManagedContentDDMReportOrder(t *testing.T) {
	for _, generationFirst := range []bool{false, true} {
		name := "app-first"
		if generationFirst {
			name = "generation-first"
		}
		t.Run(name, func(t *testing.T) {
			a, token, _, _, device, plan := contentBoundaryBench(t)
			if _, err := device.SyncDDM(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := device.PostDDMStatus(t.Context(), true); err != nil {
				t.Fatal(err)
			}
			plan.Method = managedcontent.DDM
			dispatch := contentBoundaryStep(t, a, token, "report-order", plan)
			if _, err := device.SyncDDM(t.Context()); err != nil {
				t.Fatal(err)
			}
			contentPostNativeStatus(t, device, map[string]any{}, true)
			confirm := func() {
				t.Helper()
				if err := device.PostDDMStatus(t.Context(), false); err != nil {
					t.Fatal(err)
				}
			}
			if generationFirst {
				confirm()
			}
			contentPostNativeStatus(t, device, map[string]any{"app.managed.list": []map[string]any{{"identifier": "com.example.selected", "declaration-identifier": dispatch.Declarations[0].Identifier, "state": "managed", "version": "2.4"}}}, false)
			before, err := (contentDelivery{a}).Inspect(t.Context(), dispatch)
			if err != nil {
				t.Fatal(err)
			}
			if !generationFirst {
				if before.Installed || before.Terminal {
					t.Fatal("app preceded owned generation confirmation", before)
				}
				confirm()
			}
			native, err := (contentDelivery{a}).Inspect(t.Context(), dispatch)
			if err != nil || !native.Installed || !native.Terminal || native.AppVersion != "2.4" || !native.Authoritative || !native.OwnsIdentity || len(native.Raw) == 0 {
				t.Fatal("signed current installation is not terminal", native, err)
			}
			if !native.ObservedAt.Equal(before.ObservedAt) {
				t.Fatal("generation heartbeat retimestamped app facts")
			}
			var proof ManagedContentDDMEvidence
			if err := json.Unmarshal(native.Raw, &proof); err != nil || len(proof.Reports) == 0 {
				t.Fatal("ordered native reports missing", err)
			}
			found := false
			for _, report := range proof.Reports {
				if bytes.Contains(report.Raw, []byte("com.example.selected")) && bytes.Contains(report.Raw, []byte("2.4")) {
					found = true
				}
			}
			if !found {
				t.Fatal("signed app report bytes missing")
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- a.runManagedContent(ctx) }()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			settled := contentWorkerWaitOperation(t, a, "report-order", func(op managedcontent.Operation) bool {
				return op.Observation != nil && op.Observation.Terminal && op.Observation.Installed && op.Observation.AppVersion == "2.4" && op.Worker == "" && op.LeaseUntil.IsZero()
			})
			if !settled.Observation.ObservedAt.Equal(native.ObservedAt) {
				t.Fatal("SDK retimestamped native app proof")
			}
			beforeState, err := json.Marshal(settled)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			case <-time.After(2200 * time.Millisecond):
			}
			current, err := a.ManagedContent.Load(t.Context(), "report-order")
			if err != nil {
				t.Fatal(err)
			}
			afterState, err := json.Marshal(current)
			if err != nil || !bytes.Equal(beforeState, afterState) {
				t.Fatal("settled terminal worker changed operation", err)
			}
			t.Logf("signed report ordering=%s; SDK terminal=true installed=true version=2.4; timestamp and settled bytes preserved", name)
		})
	}
}

// TestManagedContentDDMInheritedIdentifierStaleApp proves that a successor
// reusing its predecessor's declaration identifier is not settled by an app
// report sent before the device applied the successor's new server token.
func TestManagedContentDDMInheritedIdentifierStaleApp(t *testing.T) {
	a, token, _, _, device, plan := contentBoundaryBench(t)
	if _, err := device.SyncDDM(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := device.PostDDMStatus(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	plan.Method = managedcontent.DDM
	predecessor := contentBoundaryStep(t, a, token, "inherited-predecessor", plan)
	if _, err := device.SyncDDM(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := device.PostDDMStatus(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	identifier := predecessor.Declarations[0].Identifier
	// A changed license assignment is the successor change that alters the
	// content-addressed token while the identifier is inherited.
	raw := []byte(`{"Identifier":"` + identifier + `","Type":"com.apple.configuration.app.managed","Payload":{"AppStoreID":"1","InstallBehavior":{"Install":"Required","License":{"Assignment":"User"}}}}`)
	published, changed, err := a.Engine.PutDeclaration(t.Context(), raw)
	if err != nil || !changed || published.ServerToken == predecessor.Declarations[0].ServerToken {
		t.Fatal("successor generation was not published", changed, err)
	}
	successor := predecessor
	successor.Predecessor = &managedcontent.DispatchReference{ID: predecessor.ID, Digest: predecessor.Digest, Enrollment: predecessor.Enrollment}
	successor.Declarations = append([]managedcontent.Document(nil), predecessor.Declarations...)
	successor.Declarations[0].ServerToken = published.ServerToken
	app := func(version string) map[string]any {
		return map[string]any{"app.managed.list": []map[string]any{{"identifier": "com.example.selected", "declaration-identifier": identifier, "state": "managed", "version": version}}}
	}
	contentPostNativeStatus(t, device, app("1.0"), false)
	if _, err := device.SyncDDM(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := device.PostDDMStatus(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	stale, err := (contentDelivery{a}).declarativeContentObservation(t.Context(), successor, published.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Terminal || !stale.Installed || stale.AppVersion != "1.0" {
		t.Fatal("pre-generation app report settled the inherited successor", stale.Terminal, stale.Installed, stale.AppVersion)
	}
	fresh := successor
	fresh.Predecessor = nil
	if out, err := (contentDelivery{a}).declarativeContentObservation(t.Context(), fresh, published.UpdatedAt); err != nil || !out.Terminal {
		t.Fatal("fresh identifier lost order-independent settlement", out.Terminal, err)
	}
	contentPostNativeStatus(t, device, app("2.0"), false)
	settled, err := (contentDelivery{a}).declarativeContentObservation(t.Context(), successor, published.UpdatedAt)
	if err != nil || !settled.Terminal || !settled.Installed || settled.AppVersion != "2.0" {
		t.Fatal("confirmed-generation app report did not settle the successor", settled.Terminal, settled.Installed, settled.AppVersion, err)
	}
}
