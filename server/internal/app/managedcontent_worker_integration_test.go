package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
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

// contentWorkerNativeStore observes completed native journal reads without
// changing their result, identity locks, or SQL transaction behavior.
type contentWorkerNativeStore struct {
	storage.Store
	lock  func(context.Context, mdm.EnrollmentID) (*storage.Enrollment, error)
	reads chan struct{}
}

// GetForUpdate preserves the actual SQL enrollment lock used by inspection.
func (s contentWorkerNativeStore) GetForUpdate(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	return s.lock(ctx, id)
}

// Commands signals that a native journal read finished; it never queues work or
// replaces readback with a manufactured observation.
func (s contentWorkerNativeStore) Commands(ctx context.Context, id mdm.EnrollmentID, q storage.CommandQuery, p paging.Page) (paging.Result[storage.QueuedCommand], error) {
	out, err := s.Store.Commands(ctx, id, q, p)
	select {
	case s.reads <- struct{}{}:
	default:
	}
	return out, err
}

// contentWorkerWaitOperation polls only the SDK's ordinary read-only state. It
// never takes an inspection transaction or drives native device connections.
func contentWorkerWaitOperation(t *testing.T, a *App, id string, predicate func(managedcontent.Operation) bool) managedcontent.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		op, err := a.ManagedContent.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(op) {
			return op
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker operation did not reach expected state", op.Phase, op.Blocked, ctx.Err())
		case <-tick.C:
		}
	}
}

// TestManagedContentWorkerNonterminalReadback proves that only changed native
// facts advance the SDK. Unchanged worker ticks preserve lease, fence, source
// timestamp and operation bytes before explicit owned queries complete the app.
func TestManagedContentWorkerNonterminalReadback(t *testing.T) {
	f := contentFixtureService(t)
	f.assets.Store(true)
	f.assignments.Store(true)
	cfg := contentConfig(t, f)
	ca, err := testpki.NewCA("worker-native")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CARoots = ca.Pool()
	wake := make(chan struct{}, 8)
	var wakeCount atomic.Int32
	cfg.Push = PushConfig{Coalesce: -1, Pusher: contentWakePusher(func(ctx context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
		results := map[mdm.EnrollmentID]push.Result{}
		for _, target := range targets {
			results[target.ID] = push.Result{Outcome: push.OutcomeSent}
			wakeCount.Add(1)
			select {
			case wake <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return results, nil
	})}
	a, token := contentBuild(t, cfg)
	registerContent(t, a, token)
	identity, err := ca.Issue("worker-native", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "worker-native"}
	seedContentEnrollment(t, a, id, cms.Fingerprint(identity.Cert), "S-WORKER")
	e, err := a.Store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	e.Push = mdm.Push{Token: []byte{1}, Topic: "com.example.mdm", Magic: "worker"}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	locker, ok := a.Store.(interface {
		GetForUpdate(context.Context, mdm.EnrollmentID) (*storage.Enrollment, error)
	})
	if !ok {
		t.Fatal("missing SQL locker")
	}
	reads := make(chan struct{}, 64)
	a.Store = contentWorkerNativeStore{Store: a.Store, lock: locker.GetForUpdate, reads: reads}
	server := httptest.NewServer(a.Handler)
	t.Cleanup(server.Close)
	device := simulator.New(id.ID, simulator.WithURLs(server.URL+"/mdm", server.URL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
	device.Responder = func(c *mdm.Command) simulator.Reply {
		switch c.RequestType {
		case "InstallApplication":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstallApplicationResponse{Identifier: new("com.example.worker"), State: new("Queued")}}
		case "InstalledApplicationList":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstalledApplicationListResponse{InstalledApplicationList: []commands.InstalledApplicationListResponseInstalledApplicationList{{Identifier: new("com.example.worker"), Version: new("7.1"), Installing: new(false)}}}}
		case "ManagedApplicationList":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.ManagedApplicationListResponse{ManagedApplicationList: map[string]commands.ManagedApplicationListManagedApplicationItem{"com.example.worker": {Status: "Managed"}}}}
		default:
			return simulator.AcknowledgeAll(c)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	workerDone := make(chan error, 2)
	driverDone := make(chan struct{})
	rounds := make(chan []*mdm.Command, 4)
	driverError := make(chan error, 1)
	go func() { workerDone <- a.runManagedContent(ctx) }()
	deliveryRun := contentDeliveryWorker(t, a)
	go func() { workerDone <- deliveryRun(ctx) }()
	go func() {
		defer close(driverDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				processed, err := device.Connect(ctx)
				if err != nil {
					if ctx.Err() == nil {
						driverError <- err
					}
					return
				}
				select {
				case rounds <- processed:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			if err := <-workerDone; err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		}
		<-driverDone
	})
	awaitRound := func(want int) {
		t.Helper()
		phase, stop := context.WithTimeout(t.Context(), 10*time.Second)
		defer stop()
		select {
		case got := <-rounds:
			if len(got) != want {
				t.Fatal("unexpected APNs-driven commands", len(got), want)
			}
		case err := <-driverError:
			t.Fatal(err)
		case <-phase.Done():
			t.Fatal("native actor did not complete", phase.Err())
		}
	}
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "S-WORKER"}, Method: managedcontent.MDM, MaxPages: 2}
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/worker-observation", map[string]any{"mode": "install", "plan": plan}, 200)
	awaitRound(1)
	ack := contentWorkerWaitOperation(t, a, "worker-observation", func(op managedcontent.Operation) bool {
		return op.Observation != nil && op.Observation.CommandAcknowledged && op.Worker == ""
	})
	if ack.Observation.Terminal || ack.Observation.Installed || ack.Observation.AppVersion != "" {
		t.Fatal("queued ACK inferred completion", ack.Observation)
	}
	stable := contentProviderSnapshot(t, a, "worker-observation")
	// Drain reads belonging to the changed-fact Step. Subsequent signals come
	// from actual later worker inspections, not from a writer-polling test loop.
	for len(reads) > 0 {
		<-reads
	}
	for range 2 {
		phase, stop := context.WithTimeout(t.Context(), 10*time.Second)
		select {
		case <-reads:
		case <-phase.Done():
			stop()
			t.Fatal("worker stopped inspecting nonterminal native facts")
		}
		stop()
	}
	if !bytes.Equal(stable, contentProviderSnapshot(t, a, "worker-observation")) {
		t.Fatal("unchanged ticks renewed lease/fence or retimestamped facts")
	}
	contentFailureReply(t, a, token, "POST", "/managed-content/operations/worker-observation/status-refresh", map[string]any{"requestId": "completion"}, 200)
	awaitRound(2)
	done := contentWorkerWaitOperation(t, a, "worker-observation", func(op managedcontent.Operation) bool {
		return op.Observation != nil && op.Observation.Installed && op.Observation.Terminal && op.Worker == ""
	})
	if done.Observation.AppVersion != "7.1" || !done.Observation.ObservedAt.After(ack.Observation.ObservedAt) || wakeCount.Load() != 2 || f.posts.Load() != 0 {
		t.Fatal("completion lacked fresh native facts or replayed effects", done.Observation, wakeCount.Load(), f.posts.Load())
	}
}

// contentWorkerTransport intercepts only the controlled fixture's event-status
// endpoint, leaving configuration, preflight and mutation HTTP on the TLS server.
type contentWorkerTransport struct {
	base        http.RoundTripper
	fixture     *contentFixture
	statusReads *atomic.Int32
}

// RoundTrip supplies an aggregate event status through the SDK's real HTTP seam.
func (v contentWorkerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path != "/status" {
		return v.base.RoundTrip(r)
	}
	v.statusReads.Add(1)
	raw, err := json.Marshal(map[string]any{"uId": v.fixture.uid, "mdmInfo": map[string]string{"id": "ours"}, "eventStatus": "COMPLETE", "eventType": "ASSOCIATE", "numRequested": 1, "numCompleted": 1})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), Request: r}, nil
}

// TestManagedContentWorkerLicensingSchedule verifies early polling remains local
// and a due aggregate status plus exact current assignment permits dispatch.
func TestManagedContentWorkerLicensingSchedule(t *testing.T) {
	f := contentFixtureService(t)
	f.assets.Store(true)
	cfg := contentConfig(t, f)
	var statusReads atomic.Int32
	original := cfg.AppsBooks.HTTPClient
	cfg.AppsBooks.HTTPClient = &http.Client{Transport: contentWorkerTransport{base: original.Transport, fixture: f, statusReads: &statusReads}}
	a, token := contentBuild(t, cfg)
	registerContent(t, a, token)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "worker-licensing"}
	seedContentEnrollment(t, a, id, "worker-license-pin", "S-LICENSING")
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "S-LICENSING"}, Method: managedcontent.MDM, MaxPages: 2}
	contentFailureReply(t, a, token, "PUT", "/managed-content/operations/worker-license", map[string]any{"mode": "assign-and-install", "plan": plan}, 200)
	stop := contentWorkerStart(t, a)
	waiting := contentWorkerWaitOperation(t, a, "worker-license", func(op managedcontent.Operation) bool {
		return op.Phase == managedcontent.WaitingLicense && op.Worker == ""
	})
	job, err := a.AppsBooks.LoadJob(t.Context(), "london", waiting.LicensingJobID)
	if err != nil || job.State != appsbooks.JobAccepted {
		t.Fatal("job was not durably accepted", job.State, err)
	}
	// Wait for the next actual SDK worker pass using its persisted fence, while
	// only ordinary SDK reads are polled. PollJob must not hit Apple before due.
	contentWorkerWaitOperation(t, a, "worker-license", func(op managedcontent.Operation) bool { return op.Fence > waiting.Fence && op.Worker == "" })
	if statusReads.Load() != 0 || f.posts.Load() != 1 {
		t.Fatal("early worker poll contacted status or replayed association", statusReads.Load(), f.posts.Load())
	}
	stop()
	// Advance ONLY the actual queued job's due timestamp. Identity, fence,
	// lease, request, event, source proof and all other sealed fields stay exact.
	key := fmt.Sprintf("appsbooks/location/%x/jobs/%x", sha256.Sum256([]byte("london")), sha256.Sum256([]byte(job.ID)))
	record, err := a.protocol.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(record.Value, &fields); err != nil {
		t.Fatal(err)
	}
	due, err := json.Marshal(time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	fields["nextPollAt"] = due
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	contentFailurePut(t, a, key, raw)
	f.assignments.Store(true)
	contentWorkerStart(t, a)
	contentWorkerWaitOperation(t, a, "worker-license", func(op managedcontent.Operation) bool {
		return op.Phase == managedcontent.Dispatched && op.Worker == ""
	})
	current, err := a.AppsBooks.LoadJob(t.Context(), "london", job.ID)
	if err != nil || current.State != appsbooks.JobSucceeded || statusReads.Load() != 1 || f.posts.Load() != 1 {
		t.Fatal("due polling failed to reconcile aggregate licensing", current.State, statusReads.Load(), f.posts.Load(), err)
	}
}

// TestManagedContentStateReadDeleteCapture exercises the actual sealed workflow
// decorator: pure reads emit no event and required capture failure restores a
// participating deletion before a later successful delete can commit.
func TestManagedContentStateReadDeleteCapture(t *testing.T) {
	f := contentFixtureService(t)
	a, _ := contentBuild(t, contentConfig(t, f))
	store := contentState{Store: a.protocol, a: a}
	key := "managedcontent/test-required-delete"
	if err := store.Update(t.Context(), []string{key}, func(tx state.Tx) error {
		return tx.Put(t.Context(), state.Record{Key: key, Value: []byte("private-state")})
	}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		var n int
		if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_records").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	if err := store.Update(t.Context(), []string{key}, func(tx state.Tx) error {
		record, err := tx.Get(t.Context(), key)
		if err == nil && !bytes.Equal(record.Value, []byte("private-state")) {
			t.Fatal("read value changed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if count() != before {
		t.Fatal("read-only update emitted capture")
	}
	a.eventPublisher.CaptureAdditional = func(context.Context, event.Event) error { return errors.New("required capture unavailable") }
	err := store.Update(t.Context(), []string{key}, func(tx state.Tx) error { return tx.Delete(t.Context(), key) })
	if !errors.Is(err, event.ErrCapture) {
		t.Fatal("delete bypassed required capture", err)
	}
	record, err := store.Get(t.Context(), key)
	if err != nil || !bytes.Equal(record.Value, []byte("private-state")) || count() != before {
		t.Fatal("failed deletion escaped atomic rollback", err)
	}
	a.eventPublisher.CaptureAdditional = nil
	if err := store.Update(t.Context(), []string{key}, func(tx state.Tx) error { return tx.Delete(t.Context(), key) }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), key); !errors.Is(err, state.ErrNotFound) || count() != before+1 {
		t.Fatal("committed deletion absent or emitted wrong event count", err)
	}
}

// contentWorkerStart owns one actual supervised worker lifetime. Its stop is
// idempotent so failed assertions and explicit restart cannot double-wait a job.
func contentWorkerStart(t *testing.T, a *App) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.runManagedContent(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}
