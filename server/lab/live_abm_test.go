package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/server/axmassignment"
)

// abmTestConfig writes operator scope into the test-owned private workspace.
func abmTestConfig(t *testing.T, s *Session, c abmAcceptance) {
	t.Helper()
	if err := os.MkdirAll(s.Env.Workspace.path("abm"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.Env.Workspace.path("abm", "acceptance.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Real SDK OAuth, pagination, synchronization workers, SQL persistence, source
// export and retained assignment operations execute behind the actual runtime.
func TestABMLiveWorkflowsThroughRuntime(t *testing.T) {
	t.Parallel()
	w := testWorkspace(t, "simulated")
	e, err := Start(t.Context(), w, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	e.ABM.AddOrgDeviceResource("opaque-designated", benchSerial, map[string]any{"futureNull": nil, "futureFalse": false, "futureLarge": json.Number("9007199254740993"), "futureNested": map[string]any{"items": []any{nil, false}}})
	e.ABM.AddAppleCareCoverage("opaque-designated", "coverage-designated", map[string]any{"agreementNumber": "PRIVATE", "future": false})
	var services struct{ Items []struct{ ID string } }
	if err = e.api(t.Context(), "GET", "/axm/accounts/default/services", nil, &services); err != nil {
		t.Fatal(err)
	}
	var a inventory.Account
	if err = e.api(t.Context(), "GET", "/axm/accounts/default", nil, &a); err != nil {
		t.Fatal(err)
	}
	if err = e.api(t.Context(), "PUT", "/axm/accounts/default/assignment-service", map[string]any{"account_revision": a.Revision, "service_id": services.Items[0].ID, "log_columns": map[string]string{"Serial": "serialNumber", "Outcome": "status", "Success": "SUCCESS"}}, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"BENCH-ABM-1", "BENCH-ABM-2", "BENCH-ABM-3", "opaque-designated"} {
		e.ABM.Assign(id, services.Items[0].ID)
	}
	s := &Session{Env: e, Evidence: t.TempDir()}
	c := abmAcceptance{Account: "default", Serial: benchSerial, Service: services.Items[0].ID, Mutate: true}
	abmTestConfig(t, s, c)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	if err = liveABMCollection(ctx, s); err != nil {
		var jobs any
		_ = e.api(ctx, "GET", "/axm/accounts/default/jobs", nil, &jobs)
		t.Logf("fixture jobs: %+v", jobs)
		t.Fatal(err)
	}
	// Old coverage [A] must not satisfy a current-generation export for [A,B].
	oldExport, err := os.ReadFile(filepath.Join(s.Evidence, "axm.coverage-export.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oldRows []struct {
		Original []byte                `json:"original_json"`
		Snapshot inventory.Observation `json:"snapshot"`
	}
	if err = json.Unmarshal(oldExport, &oldRows); err != nil || len(oldRows) != 1 {
		t.Fatal("old coverage fixture", err)
	}
	e.ABM.AddAppleCareCoverage("opaque-designated", "coverage-second", map[string]any{"agreementNumber": "PRIVATE-SECOND", "future": nil})
	originalTransport := e.Client.Transport
	injectedOld, currentMetadata := false, false
	e.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		response, requestErr := originalTransport.RoundTrip(r)
		if requestErr != nil || !strings.HasSuffix(r.URL.Path, "/exports") || r.URL.Query().Get("source") != "axm.coverage" {
			return response, requestErr
		}
		raw, requestErr := io.ReadAll(response.Body)
		if closeErr := response.Body.Close(); requestErr == nil {
			requestErr = closeErr
		}
		if requestErr != nil {
			return nil, requestErr
		}
		var rows []map[string]json.RawMessage
		if requestErr = json.Unmarshal(raw, &rows); requestErr != nil {
			return nil, requestErr
		}
		for _, row := range rows {
			var snapshot inventory.Observation
			if requestErr = json.Unmarshal(row["snapshot"], &snapshot); requestErr != nil {
				return nil, requestErr
			}
			var current []json.RawMessage
			if requestErr = json.Unmarshal(snapshot.Raw, &current); requestErr != nil {
				return nil, requestErr
			}
			currentMetadata = snapshot.Generation != oldRows[0].Snapshot.Generation && len(current) == 2
			row["original_json"], requestErr = json.Marshal(oldRows[0].Original)
			if requestErr != nil {
				return nil, requestErr
			}
			injectedOld = true
		}
		raw, requestErr = json.Marshal(rows)
		if requestErr != nil {
			return nil, requestErr
		}
		response.Body = io.NopCloser(bytes.NewReader(raw))
		return response, nil
	})
	err = liveABMCollection(ctx, s)
	e.Client.Transport = originalTransport
	if err == nil || !injectedOld || !currentMetadata || !strings.Contains(err.Error(), "byte-exact source export missing") {
		t.Fatalf("stale history export passed or wrong boundary: %v injected=%v current=%v", err, injectedOld, currentMetadata)
	}
	if err = liveABMCollection(ctx, s); err != nil {
		t.Fatal(err)
	}
	// Exact unknown/null/false/large-integer evidence must survive the real export.
	b, err := os.ReadFile(filepath.Join(s.Evidence, "axm.device-export.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exported []struct {
		Original []byte `json:"original_json"`
	}
	if err = json.Unmarshal(b, &exported); err != nil {
		t.Fatal(err)
	}
	if len(exported) != 1 {
		t.Fatalf("exported %d", len(exported))
	}
	var original struct {
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	if err = json.Unmarshal(exported[0].Original, &original); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"futureNull": "null", "futureFalse": "false", "futureLarge": "9007199254740993"} {
		if string(original.Attributes[key]) != want {
			t.Fatalf("%s lost: %s", key, original.Attributes[key])
		}
	}
	for _, assigned := range []bool{false, true} {
		if assigned {
			e.ABM.Assign("opaque-designated", c.Service)
		} else {
			e.ABM.Assign("opaque-designated", "")
		}
		if err = liveABMAssignment(ctx, s); err != nil {
			t.Fatal(err)
		}
		observed, err := abmAssigned(ctx, s, c, "opaque-designated")
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if assigned {
			want = c.Service
		}
		if observed != want {
			t.Fatal("original assignment not restored")
		}
	}
	if err = liveABMCorrelation(ctx, s); !errors.Is(err, ErrBlocked) {
		t.Fatalf("missing native: %v", err)
	}
	c.Native = "nonexistent"
	abmTestConfig(t, s, c)
	if err = liveABMCorrelation(ctx, s); !errors.Is(err, ErrBlocked) {
		t.Fatalf("missing serial match: %v", err)
	}
	device, err := e.device(ctx)
	if err != nil {
		t.Fatal(err)
	}
	device.Responder = func(cmd *mdm.Command) simulator.Reply {
		reply := simulator.AcknowledgeAll(cmd)
		if cmd.RequestType == "DeviceInformation" {
			reply.Payload = &commands.DeviceInformationResponse{QueryResponses: commands.DeviceInformationResponseQueryResponses{SerialNumber: new(benchSerial), OSVersion: new("27.0")}}
		}
		return reply
	}
	if _, err = enqueue(ctx, e, device, &commands.DeviceInformation{Queries: []string{"SerialNumber", "OSVersion"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = device.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	var nativePage inventory.ResourcePage
	if err = e.api(ctx, "GET", "/inventory/raw/devices?source=mdm.DeviceInformation", nil, &nativePage); err != nil {
		t.Fatal(err)
	}
	if len(nativePage.Items) != 1 {
		t.Fatalf("native evidence: %d", len(nativePage.Items))
	}
	c.Native = nativePage.Items[0].ID
	abmTestConfig(t, s, c)
	if err = liveABMCorrelation(ctx, s); err != nil {
		t.Fatal(err)
	}
	// A held unassigned resource can collect and correlate without mutation.
	// Retain the real partial sync outcome: assigned-service 404 is not null.
	e.ABM.Assign("opaque-designated", "")
	if err = liveABMCollection(ctx, s); !errors.Is(err, ErrBlocked) || !strings.Contains(err.Error(), "required populated sources verified") {
		t.Fatal("unassigned collection did not retain required evidence and honest partial outcome", err)
	}
	if err = liveABMCorrelation(ctx, s); err != nil {
		t.Fatal("unassigned serial correlation failed", err)
	}
	assertions, err := os.ReadFile(filepath.Join(s.Evidence, "source-assertions.json"))
	if err != nil || !bytes.Contains(assertions, []byte(`"required_sources":"passed"`)) || !bytes.Contains(assertions, []byte(`"synchronization":"partial"`)) {
		t.Fatal("exercised sources mislabeled unexercised", err)
	}
	// Corrupt one actual completed workflow response. Cleanup must still restore
	// the original assignment, and the primary assertion failure must survive.
	e.ABM.Assign("opaque-designated", "")
	base := e.Client.Transport
	injected := false
	e.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		response, e := base.RoundTrip(r)
		if e != nil || injected || r.Method != "GET" || !strings.Contains(r.URL.Path, "/assignments/") || strings.HasSuffix(r.URL.Path, "/log") {
			return response, e
		}
		raw, e := io.ReadAll(response.Body)
		if closeErr := response.Body.Close(); e == nil {
			e = closeErr
		}
		if e != nil {
			return nil, e
		}
		var workflow axmassignment.Workflow
		if e = json.Unmarshal(raw, &workflow); e != nil {
			return nil, e
		}
		if workflow.State == "complete" {
			injected = true
			workflow.Targets[0].Outcome = "BROKEN"
			raw, e = json.Marshal(workflow)
			if e != nil {
				return nil, e
			}
		}
		response.Body = io.NopCloser(bytes.NewReader(raw))
		return response, nil
	})
	err = liveABMAssignment(ctx, s)
	e.Client.Transport = base
	if err == nil || !injected {
		t.Fatal("intermediate failure was hidden", err)
	}
	if e.ABM.AssignedServer("opaque-designated") != "" {
		t.Fatal("failed assertion left changed assignment")
	}
	scopes, err := filepath.Glob(filepath.Join(s.Evidence, "assignment-*", "cleanup.json"))
	if err != nil || len(scopes) != 3 {
		t.Fatal("independent assignment evidence scopes missing", err, len(scopes))
	}
	for _, path := range scopes {
		cleanup, readErr := os.ReadFile(path) // #nosec G304 -- The glob is confined to this test's private t.TempDir evidence directory.
		if readErr != nil || !bytes.Contains(cleanup, []byte("restored")) {
			t.Fatal("cleanup evidence missing", readErr)
		}
	}
	abmRuntimeFailures(t, s, c)
	var binding axmassignment.Binding
	if err = e.api(ctx, "GET", "/axm/accounts/default/assignment-service", nil, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.ServiceID != c.Service {
		t.Fatal("acceptance changed configured MDM binding")
	}
}
