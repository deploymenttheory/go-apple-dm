package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// contentPublicRaw exercises the actual generic administration parser and guard,
// without converting native plist wire into a JSON string or leaking errors.
func contentPublicRaw(t *testing.T, a *App, token, method, path string, raw []byte, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "/admin/v1"+path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: got%d want%d: %s", method, path, w.Code, want, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "fixture-provider-secret") {
		t.Fatal("public provider error exposed private detail")
	}
	return w
}

// contentPublicNativeSnapshot captures actual native rows and memberships. Audit
// occurrences for denied access are intentionally separate from domain mutations.
func contentPublicNativeSnapshot(t *testing.T, a *App, id mdm.EnrollmentID) []byte {
	t.Helper()
	declarations, err := a.Engine.ListDeclarations(t.Context(), ddm.DeclarationQuery{}, paging.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	sets, err := a.Engine.ListSets(t.Context(), paging.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := a.Engine.EnrollmentSets(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	members := map[string][]string{}
	for _, set := range sets.Items {
		items, err := a.Engine.SetDeclarations(t.Context(), set.Name)
		if err != nil {
			t.Fatal(err)
		}
		members[set.Name] = items
	}
	queue, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"declarations": declarations, "sets": sets, "assigned": assigned, "members": members, "commands": queue})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// contentPublicAppDeclaration produces an ordinary unowned declaration whose
// effective assignment is checked against genuine SDK-owned native scope.
func contentPublicAppDeclaration(t *testing.T, id, asset string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"Identifier": id, "Type": "com.apple.configuration.app.managed", "Payload": map[string]any{"AppStoreID": asset, "InstallBehavior": map[string]any{"Install": "Required", "License": map[string]any{"Assignment": "Device"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestManagedContentPublicOwnershipOutage verifies independent public mutation
// entry points cannot interpret unavailable ownership as an empty scope. Healthy
// retry is explicit; inspection and actual SDK intent remain unchanged.
func TestManagedContentPublicOwnershipOutage(t *testing.T) {
	for _, scenario := range []string{"coordination-command", "coordination-declaration", "coordination-query", "command-receipt", "command-asset", "declaration-owner", "assigned-declaration-asset", "assigned-set-member-asset", "set-assignment-asset"} {
		t.Run(scenario, func(t *testing.T) {
			a, token, f, id, device, plan := contentBoundaryBench(t)
			contentBoundaryStep(t, a, token, "public-owned", plan)
			if _, err := device.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			method, path, body := "PUT", "/declarations", contentPublicAppDeclaration(t, "public-candidate", "2")
			key, operation := contentDeclarationKey("public-candidate"), "get"
			if strings.Contains(scenario, "command") {
				command, err := mdm.NewCommand(&commands.InstallApplication{ITunesStoreID: new(int64(2))})
				if err != nil {
					t.Fatal(err)
				}
				method, path, body = "POST", "/enrollments/device/"+id.ID+"/commands", command.Raw
				key = deliveryKey(command.UUID)
				if scenario == "command-asset" {
					key = contentAssetKey(id, "2")
				}
			}
			if scenario == "coordination-query" {
				method, path, body = "POST", "/managed-content/operations/public-owned/status-refresh", []byte(`{"requestId":"public-coordination"}`)
			}
			if strings.HasPrefix(scenario, "coordination-") {
				operation, key = "update", contentServerKey("ownership", "native")
			}
			if strings.Contains(scenario, "assigned-") || scenario == "set-assignment-asset" {
				if _, _, err := a.Engine.PutDeclaration(t.Context(), contentPublicAppDeclaration(t, "public-candidate", "2")); err != nil {
					t.Fatal(err)
				}
				if _, err := a.Engine.PutSet(t.Context(), "public-editable"); err != nil {
					t.Fatal(err)
				}
				if scenario != "set-assignment-asset" {
					if _, err := a.Engine.AssignSet(t.Context(), id, "public-editable"); err != nil {
						t.Fatal(err)
					}
				}
				key = contentAssetKey(id, "2")
				switch scenario {
				case "assigned-declaration-asset":
					if _, err := a.Engine.AddToSet(t.Context(), "public-editable", "public-candidate"); err != nil {
						t.Fatal(err)
					}
					body = contentPublicAppDeclaration(t, "public-candidate", "3")
					key = contentAssetKey(id, "3")
				case "assigned-set-member-asset":
					path = "/sets/public-editable/declarations/public-candidate"
					body = nil
				case "set-assignment-asset":
					if _, err := a.Engine.AddToSet(t.Context(), "public-editable", "public-candidate"); err != nil {
						t.Fatal(err)
					}
					path = "/enrollments/device/" + id.ID + "/sets/public-editable"
					body = nil
				}
			}
			snapshot := contentPublicNativeSnapshot(t, a, id)
			intent := contentProviderSnapshot(t, a, "public-owned")
			requests := f.requests.Load()
			original := a.protocol
			calls := 0
			a.protocol = contentNativeStateFailure{Store: original, fail: func(op, address string) error {
				if op == operation && address == key {
					calls++
					return errors.New("fixture-provider-secret ownership unavailable")
				}
				return nil
			}}
			contentPublicRaw(t, a, token, method, path, body, 503)
			a.protocol = original
			if calls == 0 {
				t.Fatal("public mutation did not reach ownership provider")
			}
			if !bytes.Equal(snapshot, contentPublicNativeSnapshot(t, a, id)) || !bytes.Equal(intent, contentProviderSnapshot(t, a, "public-owned")) || f.requests.Load() != requests {
				t.Fatal("denied ownership mutation changed domain state or contacted Apple")
			}
			contentFailureReply(t, a, token, "GET", "/managed-content/operations/public-owned/native-observation", nil, 200)
			contentPublicRaw(t, a, token, method, path, body, 200)
			if f.requests.Load() != requests || f.posts.Load() != 0 {
				t.Fatal("ownership recovery contacted Apple")
			}
		})
	}
}

// contentPublicCorruptDeclaration delegates to actual stored declarations but
// models unreadable retained canonical evidence from a DDM provider.
type contentPublicCorruptDeclaration struct {
	ddm.Store
	identifier string
}

// GetDeclaration preserves the stored revision and changes only returned wire
// for the selected corrupt record; it does not create any positive ownership.
func (s contentPublicCorruptDeclaration) GetDeclaration(ctx context.Context, id string) (*ddm.Declaration, error) {
	record, err := s.Store.GetDeclaration(ctx, id)
	if err != nil || id != s.identifier {
		return record, err
	}
	copy := *record
	copy.Canonical = []byte("fixture-provider-secret invalid JSON")
	return &copy, nil
}

// TestManagedContentPublicDDMUnreadableEvidence rejects corrupt provider evidence
// at both set entry points while retaining ordinary non-app DDM administration.
func TestManagedContentPublicDDMUnreadableEvidence(t *testing.T) {
	a, token, f, id, _, _ := contentProviderDDMBench(t)
	original := a.Engine
	if _, _, err := a.Engine.PutDeclaration(t.Context(), contentPublicAppDeclaration(t, "public-corrupt", "2")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Engine.PutSet(t.Context(), "public-corrupt-set"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Engine.AddToSet(t.Context(), "public-corrupt-set", "public-corrupt"); err != nil {
		t.Fatal(err)
	}
	before := contentPublicNativeSnapshot(t, a, id)
	requests := f.requests.Load()
	replacement, err := ddm.New(ddm.Config{Store: contentPublicCorruptDeclaration{Store: original.Store(), identifier: "public-corrupt"}})
	if err != nil {
		t.Fatal(err)
	}
	a.Engine = replacement
	contentPublicRaw(t, a, token, "PUT", "/sets/public-new/declarations/public-corrupt", nil, 503)
	contentPublicRaw(t, a, token, "PUT", "/enrollments/device/"+id.ID+"/sets/public-corrupt-set", nil, 503)
	a.Engine = original
	contentPublicRaw(t, a, token, "PUT", "/declarations", []byte(`{"Identifier":`), 400)
	contentPublicRaw(t, a, token, "PUT", "/enrollments/invalid-channel/invalid/sets/public-corrupt-set", nil, 400)
	if !bytes.Equal(before, contentPublicNativeSnapshot(t, a, id)) {
		t.Fatal("unreadable DDM evidence changed native state")
	}
	properties := []byte(`{"Identifier":"public-properties","Type":"com.apple.management.properties","Payload":{"shard":7}}`)
	contentPublicRaw(t, a, token, "PUT", "/declarations", properties, 200)
	contentPublicRaw(t, a, token, "PUT", "/sets/public-properties-set/declarations/public-properties", nil, 200)
	contentPublicRaw(t, a, token, "PUT", "/enrollments/device/"+id.ID+"/sets/public-properties-set", nil, 200)
	if f.requests.Load() != requests {
		t.Fatal("ordinary DDM control or failure contacted Apple")
	}
}

// contentPublicRetainedRecord discovers the actual SDK record to be lost, using
// its decoded operation/dispatch identity rather than manufacturing new state.
func contentPublicRetainedRecord(t *testing.T, a *App, id string, private bool) state.Record {
	t.Helper()
	kind := "op"
	if private {
		kind = "evidence"
	}
	records, err := a.protocol.List(t.Context(), "managedcontent/"+kind+"/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if private {
			var value managedcontent.Evidence
			if json.Unmarshal(record.Value, &value) == nil && value.Dispatch != nil {
				op, _, err := a.ManagedContent.LoadEvidence(t.Context(), id)
				if err == nil && op.Dispatch != nil && value.Dispatch.ID == op.Dispatch.ID {
					return record
				}
			}
		} else {
			var value managedcontent.Operation
			if json.Unmarshal(record.Value, &value) == nil && value.ID == id {
				return record
			}
		}
	}
	t.Fatal("actual SDK record not found", kind, id)
	return state.Record{}
}

// TestManagedContentPublicRetainedStateLoss rejects inspection or query intent
// when actual retained operation, evidence or current owner records are missing.
// Restoring storage permits readback without reconstructing or replaying install.
func TestManagedContentPublicRetainedStateLoss(t *testing.T) {
	for _, kind := range []string{"operation", "evidence", "asset-owner"} {
		t.Run(kind, func(t *testing.T) {
			a, token, f, id, device, plan := contentBoundaryBench(t)
			dispatch := contentBoundaryStep(t, a, token, "retained-public", plan)
			if _, err := device.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			before := contentProviderSnapshot(t, a, "retained-public")
			requests := f.requests.Load()
			var record state.Record
			if kind == "asset-owner" {
				var err error
				record, err = a.protocol.Get(t.Context(), contentAssetKey(id, "1"))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				record = contentPublicRetainedRecord(t, a, "retained-public", kind == "evidence")
			}
			if err := a.protocol.Update(t.Context(), []string{record.Key}, func(tx state.Tx) error { return tx.Delete(t.Context(), record.Key) }); err != nil {
				t.Fatal(err)
			}
			path := "/managed-content/operations/retained-public"
			contentFailureReply(t, a, token, "GET", path+"/native-observation", nil, 404)
			contentFailureReply(t, a, token, "POST", path+"/status-refresh", map[string]any{"requestId": "loss"}, 404)
			contentFailurePut(t, a, record.Key, record.Value)
			contentFailureReply(t, a, token, "GET", path+"/native-observation", nil, 200)
			rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil || len(rows.Items) != 1 || !bytes.Equal(before, contentProviderSnapshot(t, a, "retained-public")) || f.requests.Load() != requests {
				t.Fatal("retained recovery replayed or changed SDK work", dispatch.ID, err)
			}
		})
	}
}

// TestManagedContentPublicSupersededInspection requires explicit current lane
// ownership even when a predecessor retains a genuine terminal native result.
func TestManagedContentPublicSupersededInspection(t *testing.T) {
	a, token, f, _, device, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "public-predecessor", plan)
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "POST", "/managed-content/operations/public-predecessor/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 200)
	plan.PredecessorID = "public-predecessor"
	contentBoundaryStep(t, a, token, "public-successor", plan)
	snapshot := contentProviderSnapshot(t, a, "public-predecessor")
	requests := f.requests.Load()
	contentFailureReply(t, a, token, "GET", "/managed-content/operations/public-predecessor/native-observation", nil, 409)
	contentFailureReply(t, a, token, "POST", "/managed-content/operations/public-predecessor/status-refresh", map[string]any{"requestId": "stale"}, 409)
	contentFailureReply(t, a, token, "GET", "/managed-content/operations/public-successor/native-observation", nil, 200)
	if !bytes.Equal(snapshot, contentProviderSnapshot(t, a, "public-predecessor")) || f.requests.Load() != requests {
		t.Fatal("superseded inspection changed SDK state or contacted Apple")
	}
}

// TestManagedContentPublicQueryReceiptOutage proves read and write failures of
// the explicit query idempotency receipt cannot commit half a native batch.
func TestManagedContentPublicQueryReceiptOutage(t *testing.T) {
	for _, operation := range []string{"tx-get", "put"} {
		t.Run(operation, func(t *testing.T) {
			a, token, f, id, device, plan := contentBoundaryBench(t)
			contentBoundaryStep(t, a, token, "public-query", plan)
			if _, err := device.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			key := contentServerKey("query", "public-query\x00receipt-retry")
			original := a.protocol
			calls := 0
			snapshot := contentPublicNativeSnapshot(t, a, id)
			intent := contentProviderSnapshot(t, a, "public-query")
			requests := f.requests.Load()
			a.protocol = contentNativeStateFailure{Store: original, fail: func(method, address string) error {
				if method == operation && address == key {
					calls++
					return errors.New("fixture-provider-secret query receipt unavailable")
				}
				return nil
			}}
			path := "/managed-content/operations/public-query/status-refresh"
			contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "receipt-retry"}, 503)
			a.protocol = original
			if calls == 0 {
				t.Fatal("query receipt provider failure did not fire")
			}
			if _, err := a.protocol.Get(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("query failure retained idempotency intent", err)
			}
			if !bytes.Equal(snapshot, contentPublicNativeSnapshot(t, a, id)) || !bytes.Equal(intent, contentProviderSnapshot(t, a, "public-query")) {
				t.Fatal("query receipt failure retained partial work")
			}
			contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "receipt-retry"}, 200)
			rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil || len(rows.Items) != 3 || f.requests.Load() != requests {
				t.Fatal("query receipt recovery replayed installation or contacted Apple", err)
			}
		})
	}
}

// TestManagedContentPublicNativeJournalPagination verifies signed installation
// provenance remains discoverable beyond a full page of newer pending commands;
// pending unrelated work cannot retimestamp or replace current installed facts.
func TestManagedContentPublicNativeJournalPagination(t *testing.T) {
	a, token, f, id, device, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "paged-native", plan)
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	device.Responder = func(c *mdm.Command) simulator.Reply {
		if c.RequestType == "InstalledApplicationList" {
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstalledApplicationListResponse{InstalledApplicationList: []commands.InstalledApplicationListResponseInstalledApplicationList{{Identifier: new("com.example.boundary"), Version: new("2.9"), Installing: new(false)}}}}
		}
		return simulator.AcknowledgeAll(c)
	}
	query, err := mdm.NewCommand(&commands.InstalledApplicationList{Identifiers: []string{"com.example.boundary"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, query, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := a.inspectContent(t.Context(), "paged-native")
	if err != nil || !before.Observation.Installed || before.Observation.AppVersion != "2.9" {
		t.Fatal("signed installed control absent", before, err)
	}
	requests := f.requests.Load()
	intent := contentProviderSnapshot(t, a, "paged-native")
	for range 100 {
		command, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, command, storage.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	response := contentRequest(t, a, token, "GET", "/managed-content/operations/paged-native/native-observation", nil)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var after ManagedContentNativeObservation
	if err := json.Unmarshal(response.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !after.Observation.Installed || after.Observation.AppVersion != "2.9" || !after.Observation.ObservedAt.Equal(before.Observation.ObservedAt) || !bytes.Equal(after.Observation.Raw, before.Observation.Raw) || !bytes.Equal(intent, contentProviderSnapshot(t, a, "paged-native")) || f.requests.Load() != requests {
		t.Fatal("journal page boundary lost or retimestamped native provenance")
	}
}

// TestManagedContentPublicConfigurationAndJobPrivacy prevents remote callback
// reconfiguration from implicitly rotating the durable receiver and verifies
// nonempty operational job lists omit privileged request/source evidence.
func TestManagedContentPublicConfigurationAndJobPrivacy(t *testing.T) {
	f := contentFixtureService(t)
	f.assets.Store(true)
	a, token := contentBuild(t, contentConfig(t, f))
	callbackServer := httptest.NewTLSServer(a.Handler)
	t.Cleanup(callbackServer.Close)
	f.callbackClient = callbackServer.Client()
	location := registerContent(t, a, token)
	contentFailureReply(t, a, token, "PUT", "/appsbooks/locations/london/receiver", map[string]any{"token": "original-callback-secret"}, 200)
	configuration := appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Explicit Operator", Metadata: "explicit-fixture"}, NotificationURL: callbackServer.URL + "/appsbooks/notifications/london", NotificationAuthToken: "proposed-callback-secret", NotificationTypes: []string{"TEST_NOTIFICATION"}}
	posts := f.posts.Load()
	before, err := a.AppsBooks.Load(t.Context(), "london")
	if err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "POST", "/appsbooks/locations/london/configure", map[string]any{"revision": location.Revision, "configuration": configuration}, 400)
	after, err := a.AppsBooks.Load(t.Context(), "london")
	if err != nil || after.Revision != before.Revision || f.posts.Load() != posts {
		t.Fatal("callback mismatch changed ownership or contacted Apple", err)
	}
	contentFailureReply(t, a, token, "PUT", "/appsbooks/locations/london/receiver", map[string]any{"token": configuration.NotificationAuthToken}, 200)
	contentFailureReply(t, a, token, "POST", "/appsbooks/locations/london/configure", map[string]any{"revision": location.Revision, "configuration": configuration}, 200)
	if f.posts.Load() != posts+1 {
		t.Fatal("explicit callback configuration did not mutate exactly once")
	}
	location, err = a.AppsBooks.Load(t.Context(), "london")
	if err != nil {
		t.Fatal(err)
	}
	request := appsbooks.JobRequest{Operation: appsbooks.JobAssociate, Assets: &appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{{AdamID: "1", PricingParam: "STDQ"}}, SerialNumbers: []string{"private-selected-target"}}, MaxPages: 2}
	contentFailureReply(t, a, token, "PUT", "/appsbooks/locations/london/jobs/public-privacy", map[string]any{"revision": location.Revision, "request": request}, 200)
	requests := f.requests.Load()
	ordinary := contentRequest(t, a, token, "GET", "/appsbooks/locations/london/jobs", nil)
	if ordinary.Code != 200 {
		t.Fatal(ordinary.Code, ordinary.Body.String())
	}
	var result struct {
		Items []map[string]any `json:"Items"`
	}
	if err := json.Unmarshal(ordinary.Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
		t.Fatal("nonempty safe job summary absent", err)
	}
	for _, secret := range []string{"private-selected-target", "proposed-callback-secret", "original-callback-secret", "initial-secret", contentToken("initial-secret")} {
		if strings.Contains(ordinary.Body.String(), secret) {
			t.Fatal("safe job list exposed privileged evidence", secret)
		}
	}
	for field := range result.Items[0] {
		switch strings.ToLower(field) {
		case "request", "preflight", "eventraw", "statusraw", "targets", "notificationids":
			t.Fatal("safe summary included private field", field)
		}
	}
	private := contentRequest(t, a, token, "GET", "/appsbooks/locations/london/jobs/public-privacy/evidence", nil)
	if private.Code != 200 || !strings.Contains(private.Body.String(), "private-selected-target") {
		t.Fatal("private control did not retain actual request")
	}
	if f.requests.Load() != requests || f.posts.Load() != posts+1 {
		t.Fatal("job inspection submitted licensing or contacted Apple")
	}
}

// TestManagedContentPublicDurableStartup refuses a process configuration that
// could admit durable content intent into volatile memory. The same configured
// workflow starts normally with the real sealed SQL fixture and no Apple call.
func TestManagedContentPublicDurableStartup(t *testing.T) {
	f := contentFixtureService(t)
	persistent := contentConfig(t, f)
	volatile := persistent
	volatile.Storage = "inmem"
	volatile.DSN = ""
	volatile.Sinks = SinkConfig{}
	instance, err := Build(t.Context(), volatile)
	if instance != nil {
		_ = instance.Close()
	}
	if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "Apps and Books requires sealed SQL storage") {
		t.Fatal("durable workflow accepted volatile storage", err)
	}
	healthy, _ := contentBuild(t, persistent)
	if healthy.AppsBooks == nil || healthy.ManagedContent == nil || f.requests.Load() != 0 {
		t.Fatal("sealed startup failed or contacted Apple")
	}
}

// TestManagedContentPublicAdmissionReadOutage preserves the immutable native
// principal when an idempotent admission cannot read its existing durable pin.
func TestManagedContentPublicAdmissionReadOutage(t *testing.T) {
	a, token, f, _, _, plan := contentBoundaryBench(t)
	path := "/managed-content/operations/public-admission"
	body := map[string]any{"mode": "install", "plan": plan}
	contentFailureReply(t, a, token, "PUT", path, body, 200)
	snapshot := contentProviderSnapshot(t, a, "public-admission")
	requests := f.requests.Load()
	key := contentServerKey("admission", "public-admission")
	original := a.protocol
	calls := 0
	a.protocol = contentNativeStateFailure{Store: original, fail: func(method, address string) error {
		if method == "tx-get" && address == key {
			calls++
			return errors.New("fixture-provider-secret admitted identity unavailable")
		}
		return nil
	}}
	contentFailureReply(t, a, token, "PUT", path, body, 503)
	a.protocol = original
	if calls != 1 || !bytes.Equal(snapshot, contentProviderSnapshot(t, a, "public-admission")) {
		t.Fatal("unreadable admission changed immutable SDK intent", calls)
	}
	contentFailureReply(t, a, token, "PUT", path, body, 200)
	if !bytes.Equal(snapshot, contentProviderSnapshot(t, a, "public-admission")) || f.requests.Load() != requests {
		t.Fatal("admission retry was not local and idempotent")
	}
}

// TestManagedContentPublicCancellationStorageFailure exercises the SDK's real
// lane deletion inside a failed SQL cancellation. A queued operation and its
// reservation survive intact until an explicit healthy cancellation succeeds.
func TestManagedContentPublicCancellationStorageFailure(t *testing.T) {
	a, token, f, _, _, plan := contentBoundaryBench(t)
	path := "/managed-content/operations/public-cancel"
	contentFailureReply(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan}, 200)
	lanes, err := a.protocol.List(t.Context(), "managedcontent/lane/", "", 100)
	if err != nil || len(lanes) != 1 {
		t.Fatal("actual queued SDK lane absent", len(lanes), err)
	}
	snapshot := contentProviderSnapshot(t, a, "public-cancel")
	requests := f.requests.Load()
	// The fixture owns this SQLite database and its sole lane. The trigger fails
	// the actual SDK Delete, rather than replacing transaction or capture behavior.
	if _, err := a.db.ExecContext(t.Context(), `CREATE TRIGGER public_cancel_storage BEFORE DELETE ON protocol_state WHEN OLD.record_key LIKE 'managedcontent/lane/%' BEGIN SELECT RAISE(ABORT, 'fixture-provider-secret cancellation persistence unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "POST", path+"/cancel", nil, 503)
	if !bytes.Equal(snapshot, contentProviderSnapshot(t, a, "public-cancel")) {
		t.Fatal("failed cancellation changed SDK operation/evidence")
	}
	retained, err := a.protocol.Get(t.Context(), lanes[0].Key)
	if err != nil || !bytes.Equal(retained.Value, lanes[0].Value) {
		t.Fatal("failed cancellation released native lane", err)
	}
	if _, err := a.db.ExecContext(t.Context(), "DROP TRIGGER public_cancel_storage"); err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "POST", path+"/cancel", nil, 200)
	if _, err := a.protocol.Get(t.Context(), lanes[0].Key); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("healthy cancellation retained logical ownership", err)
	}
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/public-after-cancel", map[string]any{"mode": "install", "plan": plan}, 200)
	if f.requests.Load() != requests || f.posts.Load() != 0 {
		t.Fatal("local cancellation or readmission contacted Apple")
	}
}

// contentPublicAssignmentReadFailure preserves the real DDM provider but reports
// an unavailable effective-target lookup, whose absence must never be assumed.
type contentPublicAssignmentReadFailure struct {
	ddm.Store
	calls int
}

// AffectedEnrollments fails the real lookup used by an assigned declaration edit,
// allowing the test to verify the guard reached the intended provider boundary.
func (s *contentPublicAssignmentReadFailure) AffectedEnrollments(context.Context, []string, []string) ([]mdm.EnrollmentID, error) {
	s.calls++
	return nil, errors.New("fixture-provider-secret effective targets unavailable")
}

// TestManagedContentPublicAssignedTargetReadFailure cannot update an assigned
// declaration while its effective native targets are unknown, even though its
// declaration bytes and all SDK ownership metadata remain readable.
func TestManagedContentPublicAssignedTargetReadFailure(t *testing.T) {
	a, token, f, id, _, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "public-target-owner", plan)
	if _, _, err := a.Engine.PutDeclaration(t.Context(), contentPublicAppDeclaration(t, "public-target-candidate", "2")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Engine.PutSet(t.Context(), "public-targets"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Engine.AddToSet(t.Context(), "public-targets", "public-target-candidate"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Engine.AssignSet(t.Context(), id, "public-targets"); err != nil {
		t.Fatal(err)
	}
	snapshot := contentPublicNativeSnapshot(t, a, id)
	intent := contentProviderSnapshot(t, a, "public-target-owner")
	requests := f.requests.Load()
	original := a.Engine
	failing := &contentPublicAssignmentReadFailure{Store: original.Store()}
	replacement, err := ddm.New(ddm.Config{Store: failing})
	if err != nil {
		t.Fatal(err)
	}
	a.Engine = replacement
	changed := contentPublicAppDeclaration(t, "public-target-candidate", "3")
	contentPublicRaw(t, a, token, "PUT", "/declarations", changed, 503)
	a.Engine = original
	if failing.calls != 1 || !bytes.Equal(snapshot, contentPublicNativeSnapshot(t, a, id)) || !bytes.Equal(intent, contentProviderSnapshot(t, a, "public-target-owner")) {
		t.Fatal("unknown effective targets changed native scope", failing.calls)
	}
	contentPublicRaw(t, a, token, "PUT", "/declarations", changed, 200)
	if f.requests.Load() != requests || f.posts.Load() != 0 {
		t.Fatal("target lookup outage or recovery contacted Apple")
	}
}
