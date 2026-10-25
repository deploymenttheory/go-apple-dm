package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

// TestManagedContentGenericDDMOwnershipHTTP exercises the ordinary administration
// routes against ownership established by the actual SDK and native publication.
// Unrelated content remains editable; none of these local requests licenses apps.
func TestManagedContentGenericDDMOwnershipHTTP(t *testing.T) {
	f := contentFixtureService(t)
	f.assets.Store(true)
	f.assignments.Store(true)
	cfg := contentConfig(t, f)
	ca, err := testpki.NewCA("ownership-http")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CARoots = ca.Pool()
	a, token := contentBuild(t, cfg)
	registerContent(t, a, token)
	identity, err := ca.Issue("ownership-http", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "ownership-http"}
	seedContentEnrollment(t, a, id, cms.Fingerprint(identity.Cert), "S-OWNER")
	server := httptest.NewServer(a.Handler)
	t.Cleanup(server.Close)
	device := simulator.New(id.ID, simulator.WithURLs(server.URL+"/mdm", server.URL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
	if _, err := device.SyncDDM(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := device.PostDDMStatus(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any, status int) {
		t.Helper()
		response := contentRequest(t, a, token, method, path, body)
		if response.Code != status {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, status, response.Body.String())
		}
	}
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "S-OWNER"}, Method: managedcontent.DDM, MaxPages: 2}
	request("PUT", "/managed-content/operations/http-owned", map[string]any{"mode": "install", "plan": plan}, 200)
	request("POST", "/managed-content/operations/http-owned/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 200)
	op, evidence, err := a.ManagedContent.LoadEvidence(t.Context(), "http-owned")
	if err != nil || op.Phase != managedcontent.Dispatched || evidence.Dispatch == nil || len(evidence.Dispatch.Declarations) != 2 {
		t.Fatal("SDK did not establish native declaration ownership", op.Phase, err)
	}
	owned := evidence.Dispatch.Declarations[0].Identifier
	reservedSet := "managedcontent:" + owned
	before, err := a.Engine.GetDeclaration(t.Context(), owned)
	if err != nil {
		t.Fatal(err)
	}
	declaration := func(name, asset string) map[string]any {
		return map[string]any{"Identifier": name, "Type": "com.apple.configuration.app.managed", "Payload": map[string]any{"AppStoreID": asset, "InstallBehavior": map[string]any{"Install": "Required", "License": map[string]any{"Assignment": "Device"}}}}
	}
	appleRequests := f.requests.Load()
	request("GET", "/declarations/"+owned, nil, 200)
	request("PUT", "/declarations", declaration(owned, "2"), 409)
	request("DELETE", "/declarations/"+owned, nil, 409)
	request("PUT", "/sets/operator/declarations/"+owned, nil, 409)
	request("DELETE", "/sets/"+reservedSet+"/declarations/"+owned, nil, 409)
	request("PUT", "/enrollments/device/"+id.ID+"/sets/"+reservedSet, nil, 409)
	request("DELETE", "/enrollments/device/"+id.ID+"/sets/"+reservedSet, nil, 409)
	after, err := a.Engine.GetDeclaration(t.Context(), owned)
	if err != nil || !bytes.Equal(before.Canonical, after.Canonical) || before.ServerToken != after.ServerToken {
		t.Fatal("generic mutation changed the owned declaration", err)
	}
	if _, err := a.Engine.GetSet(t.Context(), "operator"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("denied member creation left a partial set", err)
	}

	// An unassigned declaration may exist for another target. Assignment to the
	// owned target must fail without changing its existing set membership.
	request("PUT", "/declarations", declaration("foreign-app", "1"), 200)
	request("PUT", "/sets/foreign/declarations/foreign-app", nil, 200)
	setsBefore, err := a.Engine.EnrollmentSets(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	request("PUT", "/enrollments/device/"+id.ID+"/sets/foreign", nil, 409)
	setsAfter, err := a.Engine.EnrollmentSets(t.Context(), id)
	if err != nil || !reflect.DeepEqual(setsBefore, setsAfter) {
		t.Fatal("denied assignment changed native membership", err)
	}

	// Editing an already assigned set and editing its declaration are separate
	// entry points. Both must reject a new conflict while unrelated apps work.
	if _, err := a.Engine.PutSet(t.Context(), "editable"); err != nil {
		t.Fatal(err)
	}
	request("PUT", "/enrollments/device/"+id.ID+"/sets/editable", nil, 200)
	request("PUT", "/sets/editable/declarations/foreign-app", nil, 409)
	members, err := a.Engine.SetDeclarations(t.Context(), "editable")
	if err != nil || len(members) != 0 {
		t.Fatal("denied member edit changed native set", members, err)
	}
	request("PUT", "/declarations", declaration("unrelated-app", "2"), 200)
	request("PUT", "/sets/editable/declarations/unrelated-app", nil, 200)
	unrelated, err := a.Engine.GetDeclaration(t.Context(), "unrelated-app")
	if err != nil {
		t.Fatal(err)
	}
	request("PUT", "/declarations", declaration("unrelated-app", "1"), 409)
	retained, err := a.Engine.GetDeclaration(t.Context(), "unrelated-app")
	if err != nil || !bytes.Equal(unrelated.Canonical, retained.Canonical) {
		t.Fatal("denied declaration edit changed an assigned app", err)
	}
	request("DELETE", "/sets/editable/declarations/unrelated-app", nil, 200)
	request("DELETE", "/enrollments/device/"+id.ID+"/sets/editable", nil, 200)
	request("DELETE", "/declarations/unrelated-app", nil, 204)
	if f.requests.Load() != appleRequests {
		t.Fatal("generic ownership inspection contacted Apple")
	}
	current, err := a.ManagedContent.Load(t.Context(), "http-owned")
	if err != nil {
		t.Fatal(err)
	}
	originalJSON, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	currentJSON, err := json.Marshal(current)
	if err != nil || !bytes.Equal(originalJSON, currentJSON) {
		t.Fatal("generic administration rewrote SDK operation state", err)
	}
}
