package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// contentFailureReply requires public errors to withhold both source credentials
// and extension-provider details, regardless of the failing local boundary.
func contentFailureReply(t *testing.T, a *App, token, method, path string, body any, want int) {
	t.Helper()
	w := contentRequest(t, a, token, method, path, body)
	if w.Code != want {
		t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	for _, secret := range []string{"private-callback", "fixture-provider-secret", "sql-secret", "native-authenticated-pin"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("public error exposed %q", secret)
		}
	}
}

// TestManagedContentMalformedPublicIntents ensures every body-bearing family
// rejects malformed/unknown input before remote service or native admission.
func TestManagedContentMalformedPublicIntents(t *testing.T) {
	a, token, f, id, _, plan := contentBoundaryBench(t)
	before := f.requests.Load()
	for _, request := range []struct{ method, path string }{
		{"PUT", "/managed-content/users"},
		{"PUT", "/appsbooks/locations/invalid"},
		{"PUT", "/appsbooks/locations/london/receiver"},
		{"POST", "/appsbooks/locations/london/configure"},
		{"POST", "/appsbooks/locations/london/refresh"},
		{"POST", "/appsbooks/locations/london/recover-configuration"},
		{"POST", "/appsbooks/locations/london/recover-licensing"},
		{"PUT", "/appsbooks/locations/london/jobs/invalid"},
		{"POST", "/appsbooks/locations/london/jobs/invalid/submit"},
		{"POST", "/appsbooks/locations/london/jobs/invalid/poll"},
		{"POST", "/appsbooks/locations/london/jobs/invalid/reconcile"},
		{"PUT", "/managed-content/operations/invalid"},
		{"POST", "/managed-content/operations/invalid/step"},
		{"POST", "/managed-content/operations/invalid/status-refresh"},
	} {
		contentFailureReply(t, a, token, request.method, request.path, map[string]any{"unexpected": "fixture-provider-secret"}, 400)
	}
	contentFailureReply(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": id, "clientUserId": ""}, 400)
	contentFailureReply(t, a, token, "PUT", "/appsbooks/locations/london/receiver", map[string]any{"token": ""}, 400)
	contentFailureReply(t, a, token, "POST", "/appsbooks/locations/london/configure", map[string]any{"configuration": map[string]any{"notificationAuthToken": "different-receiver"}}, 404)
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/invalid", map[string]any{"mode": "unsupported", "plan": plan}, 400)
	if _, err := a.protocol.Get(t.Context(), contentServerKey("admission", "invalid")); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("invalid mode left admission", err)
	}
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 0 || f.requests.Load() != before {
		t.Fatal("invalid intent caused native or Apple work", err)
	}
}

// contentFailurePut models corrupt retained local bytes at the real sealed store,
// rather than replacing the workflow or supplying fabricated positive evidence.
func contentFailurePut(t *testing.T, a *App, key string, value []byte) {
	t.Helper()
	if err := a.protocol.Update(t.Context(), []string{key}, func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: value}) }); err != nil {
		t.Fatal(err)
	}
}

// TestManagedContentAdmissionRecoveryFailures covers idempotent resubmission,
// conflicting retained identity, missing/corrupt admission, and queued inspection.
func TestManagedContentAdmissionRecoveryFailures(t *testing.T) {
	a, token, f, _, _, plan := contentBoundaryBench(t)
	path := "/managed-content/operations/admitted"
	body := map[string]any{"mode": "install", "plan": plan}
	contentFailureReply(t, a, token, "PUT", path, body, 200)
	original, err := a.ManagedContent.Load(t.Context(), "admitted")
	if err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "PUT", path, body, 200)
	contentFailureReply(t, a, token, "GET", path+"/native-observation", nil, 400)
	contentFailureReply(t, a, token, "POST", path+"/status-refresh", map[string]any{"requestId": "queued"}, 409)
	contentFailureReply(t, a, token, "GET", path+"/evidence", nil, 200)
	contentFailureReply(t, a, token, "GET", "/managed-content/operations/missing/native-observation", nil, 404)
	contentFailureReply(t, a, token, "POST", "/managed-content/operations/missing/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 404)
	admission := contentServerKey("admission", "admitted")
	record, err := a.protocol.Get(t.Context(), admission)
	if err != nil {
		t.Fatal(err)
	}
	changed := plan
	changed.Target.Serial = "different"
	contentFailureReply(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": changed}, 409)
	contentFailurePut(t, a, admission, []byte(`{"Identity":`))
	before := f.requests.Load()
	contentFailureReply(t, a, token, "PUT", path, body, 409)
	contentFailureReply(t, a, token, "GET", path+"/native-observation", nil, 503)
	contentFailureReply(t, a, token, "POST", path+"/status-refresh", map[string]any{"requestId": "corrupt"}, 503)
	contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 503)
	contentFailurePut(t, a, admission, record.Value)
	current, err := a.ManagedContent.Load(t.Context(), "admitted")
	if err != nil {
		t.Fatal(err)
	}
	x, _ := json.Marshal(original)
	y, _ := json.Marshal(current)
	if !bytes.Equal(x, y) || f.requests.Load() != before {
		t.Fatal("failed recovery changed operation or contacted Apple")
	}
}

// TestManagedContentUnavailableNativePrincipal denies disabled devices and
// parents and user aliases that no longer belong to the authenticated principal.
func TestManagedContentUnavailableNativePrincipal(t *testing.T) {
	a, token, f, id, _, plan := contentBoundaryBench(t)
	before := f.requests.Load()
	contentFailureReply(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "missing"}, "clientUserId": "apple-user"}, 404)
	e, err := a.Store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	e.Enabled = false
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": id, "clientUserId": "apple-user"}, 503)
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/disabled", map[string]any{"mode": "install", "plan": plan}, 503)
	e.Enabled = true
	e.CertHash = ""
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/no-certificate", map[string]any{"mode": "install", "plan": plan}, 503)
	user := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "native-user", ParentID: "user-parent"}
	parent := user.Device()
	seedContentEnrollment(t, a, parent, "native-authenticated-pin", "")
	seedContentEnrollment(t, a, user, "", "")
	plan.Target = managedcontent.Selection{Enrollment: user, ClientUserID: "apple-user"}
	device, err := a.Store.Get(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	device.Enabled = false
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *device}); err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/disabled-parent", map[string]any{"mode": "install", "plan": plan}, 503)
	device.Enabled = true
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *device}); err != nil {
		t.Fatal(err)
	}
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/unbound-user", map[string]any{"mode": "install", "plan": plan}, 404)
	contentFailureReply(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": user, "clientUserId": "apple-user"}, 200)
	contentFailurePut(t, a, contentUserKey(user), []byte(`{"clientUserId":"apple-user","principal":"foreign"}`))
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/foreign-user", map[string]any{"mode": "install", "plan": plan}, 503)
	if f.requests.Load() != before {
		t.Fatal("unavailable principal contacted Apple")
	}
}

// contentFailureInspector represents the supported trusted delivery extension.
// Failures are deliberately returned through this real provider boundary.
type contentFailureInspector struct {
	inspect func(managedcontent.Dispatch) (managedcontent.Observation, error)
}

func (v contentFailureInspector) Inspect(_ context.Context, d managedcontent.Dispatch) (managedcontent.Observation, error) {
	return v.inspect(d)
}

func (v contentFailureInspector) Submit(context.Context, managedcontent.Dispatch) (managedcontent.Observation, error) {
	return managedcontent.Observation{}, errors.New("fixture-provider-secret unexpected submit")
}

// TestManagedContentConfiguredInspectionFailure rejects untrusted provider
// observations and errors while leaving the actual admitted operation unchanged.
func TestManagedContentConfiguredInspectionFailure(t *testing.T) {
	a, token, f, id, _, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "extension", plan)
	op, evidence, err := a.ManagedContent.LoadEvidence(t.Context(), "extension")
	if err != nil {
		t.Fatal(err)
	}
	baseline, _ := json.Marshal(struct {
		Operation managedcontent.Operation `json:"Operation"`
		Evidence  managedcontent.Evidence  `json:"Evidence"`
	}{op, evidence})
	before := f.requests.Load()
	for _, scenario := range []string{"provider-error", "wrong-id", "wrong-target", "wrong-method", "wrong-digest", "not-authoritative", "not-owned", "zero-time"} {
		t.Run(scenario, func(t *testing.T) {
			a.cfg.AppsBooks.Delivery = contentFailureInspector{inspect: func(d managedcontent.Dispatch) (managedcontent.Observation, error) {
				out := dispatchObservation(d)
				out.Found = true
				out.ObservedAt = op.UpdatedAt
				switch scenario {
				case "provider-error":
					return out, errors.New("fixture-provider-secret")
				case "wrong-id":
					out.ID = "foreign"
				case "wrong-target":
					out.Enrollment.ID = "foreign"
				case "wrong-method":
					out.Method = managedcontent.DDM
				case "wrong-digest":
					out.Digest = "foreign"
				case "not-authoritative":
					out.Authoritative = false
				case "not-owned":
					out.OwnsIdentity = false
				case "zero-time":
					out.ObservedAt = time.Time{}
				}
				return out, nil
			}}
			contentFailureReply(t, a, token, "GET", "/managed-content/operations/extension/native-observation", nil, 503)
			contentFailureReply(t, a, token, "POST", "/managed-content/operations/extension/status-refresh", map[string]any{"requestId": "extension"}, 400)
		})
	}
	a.cfg.AppsBooks.Delivery = nil
	current, private, err := a.ManagedContent.LoadEvidence(t.Context(), "extension")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(struct {
		Operation managedcontent.Operation `json:"Operation"`
		Evidence  managedcontent.Evidence  `json:"Evidence"`
	}{current, private})
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 1 || !bytes.Equal(baseline, after) || before != f.requests.Load() {
		t.Fatal("read-only provider failure caused effects", err)
	}
}

// TestManagedContentStatusRefreshFailureAtomicity verifies that required capture
// failure rolls back both native query commands and the durable idempotency key.
func TestManagedContentStatusRefreshFailureAtomicity(t *testing.T) {
	a, token, f, id, device, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "refresh-failure", plan)
	path := "/managed-content/operations/refresh-failure/status-refresh"
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": ""}, 400)
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": strings.Repeat("x", 257)}, 400)
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "before-ack"}, 409)
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := f.requests.Load()
	a.eventPublisher.CaptureAdditional = func(_ context.Context, e event.Event) error {
		if e.Type == "managed-content-status-query" {
			return errors.New("fixture-provider-secret")
		}
		return nil
	}
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "query"}, 503)
	key := contentServerKey("query", "refresh-failure\x00query")
	if _, err := a.protocol.Get(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed capture retained query key", err)
	}
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 1 {
		t.Fatal("failed capture retained queries", err)
	}
	a.eventPublisher.CaptureAdditional = nil
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "query"}, 200)
	record, err := a.protocol.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	contentFailurePut(t, a, key, []byte(`{"operationId":"foreign"}`))
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "query"}, 503)
	contentFailurePut(t, a, key, record.Value)
	ownerKey := contentAssetKey(id, "1")
	owner, err := a.protocol.Get(t.Context(), ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	contentFailurePut(t, a, ownerKey, []byte(`{"id":"foreign"}`))
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "other"}, 409)
	contentFailurePut(t, a, ownerKey, owner.Value)
	rows, err = a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 3 || f.requests.Load() != before {
		t.Fatal("refresh failure replayed queue or contacted Apple", err)
	}
}

// TestManagedContentStorageUnavailable fails local persistence before exposing
// remote configuration or admission effects; public errors contain no SQL details.
func TestManagedContentStorageUnavailable(t *testing.T) {
	a, token, f, _, _, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "storage-down", plan)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	before := f.requests.Load()
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/managed-content/operations/storage-down/native-observation", nil},
		{http.MethodPost, "/managed-content/operations/storage-down/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}},
		{http.MethodPut, "/managed-content/operations/new-down", map[string]any{"mode": "install", "plan": plan}},
		{http.MethodPut, "/managed-content/users", map[string]any{"enrollment": plan.Target.Enrollment, "clientUserId": "apple-user"}},
		{http.MethodPost, "/appsbooks/locations/london/configure", map[string]any{"configuration": map[string]any{"notificationAuthToken": "private-callback"}}},
	} {
		contentFailureReply(t, a, token, r.method, r.path, r.body, 503)
	}
	if f.requests.Load() != before {
		t.Fatal("unavailable storage contacted Apple")
	}
}

// TestManagedContentWorkerNativeProgress runs the supervised worker over genuine
// dispatched, cancelled and corrupt-admission operations. A signed native result
// advances only the owned dispatched operation, without replaying installation.
func TestManagedContentWorkerNativeProgress(t *testing.T) {
	a, token, f, id, device, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "worker-owned", plan)
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	// These independent assets cannot share or overwrite the dispatched lane.
	cancelled := plan
	cancelled.Content.Asset.AdamID = "2"
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/worker-cancelled", map[string]any{"mode": "install", "plan": cancelled}, 200)
	contentFailureReply(t, a, token, "POST", "/managed-content/operations/worker-cancelled/cancel", nil, 200)
	corrupt := plan
	corrupt.Content.Asset.AdamID = "3"
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/worker-corrupt", map[string]any{"mode": "install", "plan": corrupt}, 200)
	contentFailurePut(t, a, contentServerKey("admission", "worker-corrupt"), []byte(`{"Identity":`))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.runManagedContent(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		op, err := a.ManagedContent.Load(t.Context(), "worker-owned")
		if err != nil {
			t.Fatal(err)
		}
		if op.Observation != nil && op.Observation.Terminal {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("worker did not consume signed terminal result")
		case <-tick.C:
		}
	}
	// A second iteration must leave terminal ownership stable, skip the cancelled
	// operation and retain the corrupt admission for explicit operator recovery.
	select {
	case <-deadline.C:
		t.Fatal("worker failed to settle")
	case <-time.After(1100 * time.Millisecond):
	}
	cancel()
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 1 || f.posts.Load() != 0 {
		t.Fatal("worker replayed installation or licensed content", err)
	}
	current, err := a.ManagedContent.Load(t.Context(), "worker-corrupt")
	if err != nil || current.Phase != managedcontent.Queued {
		t.Fatal("worker advanced corrupt admission", current.Phase, err)
	}
}
