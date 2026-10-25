package storagetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// jobTimeStore changes only the transaction's authoritative clock; all read,
// locking, encryption and rollback semantics still belong to the tested backend.
type jobTimeStore struct {
	state.Store
	now *atomic.Int64
}
type jobTimeTx struct {
	state.Tx
	at time.Time
}

// Update preserves backend transaction/lock semantics while controlling its authoritative time.
func (s *jobTimeStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(jobTimeTx{Tx: tx, at: time.Unix(0, s.now.Load()).UTC()}) })
}

// Now returns the fixed time sampled for this contract transaction.
func (t jobTimeTx) Now() time.Time { return t.at }

type licensingService struct {
	mu        sync.Mutex
	posts     int
	operation string
	status    string
	completed int
	requested int
	owner     string
	product   string
	retired   bool
	failure   bool
	invalid   bool
	block     chan struct{}
	entered   chan struct{}
}

// licensingFixture serves controlled source, asynchronous event and aggregate-status responses over TLS.
func licensingFixture(t *testing.T) (*licensingService, appsbooks.Config, string) {
	t.Helper()
	f := &licensingService{operation: "ASSOCIATE", status: "PENDING", requested: 2, completed: 1, owner: "ours", product: "App"}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		owner, product, retired, invalid := f.owner, f.product, f.retired, f.invalid
		operation, status, completed, requested, failure := f.operation, f.status, f.completed, f.requested, f.failure
		block, entered := f.block, f.entered
		if r.Method == http.MethodPost {
			f.posts++
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/service/config" {
			urls := map[string]string{}
			for _, key := range []string{"clientConfig", "getAssets", "getUsers", "associateAssets", "disassociateAssets", "revokeAssets", "createUsers", "updateUsers", "retireUsers", "eventStatus"} {
				urls[key] = server.URL + "/" + key
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"urls": urls, "limits": map[string]int{"maxAssets": 100, "maxUsers": 100, "maxClientUserIds": 100, "maxSerialNumbers": 100, "maxRevokeClientUserIds": 100, "maxRevokeSerialNumbers": 100, "maxRequestPerSecond": 1000000}}); err != nil {
				t.Error(err)
			}
			return
		}
		if r.Method == http.MethodPost && block != nil {
			close(entered)
			select {
			case <-block:
			case <-r.Context().Done():
				return
			}
		}
		if invalid && r.Method == http.MethodPost {
			if _, err := fmt.Fprint(w, `{"uId":"UID"}`); err != nil {
				t.Error(err)
			}
			return
		}
		out := map[string]any{"uId": "UID", "mdmInfo": map[string]string{"id": owner}, "versionId": "v1", "future": json.RawMessage(`{"integer":9007199254740993,"null":null,"false":false}`)}
		switch r.URL.Path {
		case "/getAssets":
			out["currentPageIndex"] = 0
			out["totalPages"] = 1
			out["assets"] = []any{map[string]any{"adamId": "1", "pricingParam": "STDQ", "productType": product, "deviceAssignable": true, "revocable": true}}
		case "/getUsers":
			s := "Associated"
			if retired {
				s = "Retired"
			}
			out["currentPageIndex"] = 0
			out["totalPages"] = 1
			out["users"] = []any{map[string]string{"clientUserId": "u", "status": "Retired"}, map[string]string{"clientUserId": "u", "status": s}}
		case "/eventStatus":
			out["eventStatus"] = status
			out["eventType"] = operation
			out["numRequested"] = requested
			out["numCompleted"] = completed
			if failure {
				out["failures"] = []any{map[string]any{"errorNumber": 9609, "errorInfo": map[string]any{"clientUserIds": []string{"u"}}}}
			}
		default:
			if r.Method == http.MethodPost {
				out["eventId"] = "event"
			}
		}
		if err := json.NewEncoder(w).Encode(out); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	cfg := appsbooks.Config{MDMID: "ours", BaseURL: server.URL, HTTPClient: server.Client()}
	return f, cfg, appsBooksToken("jobs", "2035-01-01T00:00:00Z")
}

// jobRequest supplies an explicit two-device app assignment without inferred user IDs.
func jobRequest() appsbooks.JobRequest {
	return appsbooks.JobRequest{Operation: appsbooks.JobAssociate, MaxPages: 2, Assets: &appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{{AdamID: "1", PricingParam: "STDQ"}}, SerialNumbers: []string{"s1", "s2"}}}
}

// jobCallback retains exact source bytes for a single target batch observation.
func jobCallback(id, event, result, serial string) string {
	return fmt.Sprintf(`{"notificationId":%q,"notificationType":"ASSET_MANAGEMENT","uId":"UID","future":{"n":9007199254740993},"notification":{"eventId":%q,"result":%q,"type":"ASSOCIATE","assignments":[{"adamId":"1","pricingParam":"STDQ","serialNumber":%q}]}}`, id, event, result, serial)
}

// deliverJobCallback exercises the authenticated receiver rather than bypassing payload decoding.
func deliverJobCallback(t *testing.T, w *appsbooks.Workflow, id, body string) (appsbooks.NotificationReceipt, error) {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://receiver.example/callback", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer receiver")
	return w.ReceiveNotification(t.Context(), id, "receiver", r)
}

// RunAppsBooksJobs exercises the same durable licensing/notification contracts
// against memory and sealed SQL, with two independent handles and restart.
func RunAppsBooksJobs(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	t.Run("validation", func(t *testing.T) { runLicensingValidation(t, factory) })
	t.Run("persistence rollback", func(t *testing.T) { runLicensingFailures(t, factory) })
	setup := func(t *testing.T) (*appsbooks.Workflow, *appsbooks.Workflow, *licensingService, *atomic.Int64, appsbooks.Location, appsbooks.Config, string) {
		t.Helper()
		a, b := factory(t)
		now := &atomic.Int64{}
		now.Store(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC).UnixNano())
		f, cfg, token := licensingFixture(t)
		wa, err := appsbooks.NewWorkflow(&jobTimeStore{Store: a, now: now}, cfg)
		failAppsBooksIf(t, err != nil, err)
		wb, err := appsbooks.NewWorkflow(&jobTimeStore{Store: b, now: now}, cfg)
		failAppsBooksIf(t, err != nil, err)
		l, err := wa.Register(t.Context(), "location", 0, token)
		failAppsBooksIf(t, err != nil, err)
		return wa, wb, f, now, l, cfg, token
	}
	t.Run("licensing callbacks restart partial counts", func(t *testing.T) {
		w, restarted, f, now, l, _, _ := setup(t)
		j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobQueued, j, err)
		again, err := restarted.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
		failAppsBooksIf(t, err != nil || again.ID != j.ID, again, err)
		changed := jobRequest()
		changed.MaxPages = 3
		_, err = w.CreateJob(t.Context(), l.ID, l.Revision, "job", changed)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrInput), err)
		// A callback may beat persistence of the accepted-event response.
		receipt, err := deliverJobCallback(t, w, l.ID, jobCallback("n1", "event", "SUCCESS", "s1"))
		failAppsBooksIf(t, err != nil || receipt.AppliedJobID != "", receipt, err)
		j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobAccepted || len(j.Targets) != 1 || len(j.EventRaw) == 0, j, err)
		receipt, err = deliverJobCallback(t, restarted, l.ID, jobCallback("n1", "event", "SUCCESS", "s1"))
		failAppsBooksIf(t, err != nil || receipt.AppliedJobID != j.ID, receipt, err)
		_, err = deliverJobCallback(t, restarted, l.ID, jobCallback("n2", "event", "SUCCESS", "s1"))
		failAppsBooksIf(t, err != nil, err)
		j, err = restarted.LoadJob(t.Context(), l.ID, j.ID)
		failAppsBooksIf(t, err != nil || len(j.Targets) != 1 || len(j.NotificationIDs) != 2 || j.State != appsbooks.JobAccepted, j, err)
		_, err = w.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrPollEarly), err)
		now.Add(int64(5 * time.Minute))
		j, err = restarted.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobAccepted || j.Status == nil, j, err)
		_, err = w.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrPollEarly), err)
		f.mu.Lock()
		f.status = "COMPLETE"
		f.completed = 1
		f.requested = 1
		f.mu.Unlock()
		now.Add(int64(30 * time.Second))
		j, err = restarted.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobFailed, j, err)
		f.mu.Lock()
		posts := f.posts
		f.mu.Unlock()
		failAppsBooksIf(t, posts != 1, posts)
		jobs, cursor, err := w.Jobs(t.Context(), l.ID, "", 10)
		failAppsBooksIf(t, err != nil || len(jobs) != 1 || cursor == "", jobs, cursor, err)
		jobs, _, err = w.Jobs(t.Context(), l.ID, cursor, 10)
		failAppsBooksIf(t, err != nil || len(jobs) != 0, jobs, err)
	})
	t.Run("aggregate success late failure monotonic", func(t *testing.T) {
		w, other, f, now, l, _, token := setup(t)
		j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
		failAppsBooksIf(t, err != nil, err)
		j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
		failAppsBooksIf(t, err != nil, err)
		// Accepted remote work does not prevent ordinary validated token renewal.
		_, err = other.Register(t.Context(), l.ID, l.Revision, token)
		failAppsBooksIf(t, err != nil, err)
		f.mu.Lock()
		f.status = "COMPLETE"
		f.completed = 2
		f.mu.Unlock()
		now.Add(int64(5 * time.Minute))
		j, err = other.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobSucceeded || len(j.StatusRaw) == 0, j, err)
		_, err = deliverJobCallback(t, w, l.ID, jobCallback("fail", "event", "FAILURE", "s1"))
		failAppsBooksIf(t, err != nil, err)
		_, err = deliverJobCallback(t, w, l.ID, jobCallback("older", "event", "SUCCESS", "s1"))
		failAppsBooksIf(t, err != nil, err)
		j, err = other.LoadJob(t.Context(), l.ID, j.ID)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobFailed || len(j.Targets) != 1, j, err)
		for _, target := range j.Targets {
			failAppsBooksIf(t, target.Result != "FAILURE", target)
		}
	})
	t.Run("crashed submission stale fence renewal reconciliation", func(t *testing.T) {
		w, other, f, now, l, _, token := setup(t)
		j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
		failAppsBooksIf(t, err != nil, err)
		block, entered := make(chan struct{}), make(chan struct{})
		f.mu.Lock()
		f.block = block
		f.entered = entered
		f.mu.Unlock()
		done := make(chan error, 1)
		go func() { _, err := w.SubmitJob(t.Context(), l.ID, j.ID, "old", time.Minute); done <- err }()
		<-entered
		_, err = other.SubmitJob(t.Context(), l.ID, j.ID, "replica", time.Minute)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrJobBusy), err)
		_, err = other.Register(t.Context(), l.ID, l.Revision, token)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrJobBusy), err)
		_, err = other.ReconcileJob(t.Context(), l.ID, j.ID, "", "inspect", false)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrJobBusy), err)
		// Simulate a lease expiring; the original call is explicitly stopped/released.
		now.Add(int64(2 * time.Minute))
		close(block)
		err = <-done
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrJobFence), err)
		j, err = other.LoadJob(t.Context(), l.ID, j.ID)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobSubmitting || j.EventID != "", j, err)
		_, err = other.RecoverLicensingToken(t.Context(), l.ID, l.Revision, j.ID, token)
		failAppsBooksIf(t, err != nil, err)
		j, err = other.LoadJob(t.Context(), l.ID, j.ID)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobUncertain, j, err)
		_, err = other.SubmitJob(t.Context(), l.ID, j.ID, "replica", time.Minute)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrJobBusy), err)
		j, err = other.ReconcileJob(t.Context(), l.ID, j.ID, "event", "read back event after stopping worker", false)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobAccepted, j, err)
		f.mu.Lock()
		f.block = nil
		posts := f.posts
		f.mu.Unlock()
		failAppsBooksIf(t, posts != 1, posts)
	})
	t.Run("uncertain response never replays and explicit abandonment", func(t *testing.T) {
		w, other, f, _, l, _, _ := setup(t)
		f.mu.Lock()
		f.invalid = true
		f.mu.Unlock()
		j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
		failAppsBooksIf(t, err != nil, err)
		j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrProtocol) || j.State != appsbooks.JobUncertain, j, err)
		j, err = other.ReconcileJob(t.Context(), l.ID, j.ID, "", "still investigating", false)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobUncertain, j, err)
		_, err = other.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrJobBusy), err)
		j, err = other.ReconcileJob(t.Context(), l.ID, j.ID, "", "operator stopped worker and accepts unresolved remote state", true)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobAbandoned, j, err)
		_, err = other.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrReconciliation), err)
	})
	t.Run("notification authentication test replay and correlation", func(t *testing.T) {
		w, _, _, _, l, _, _ := setup(t)
		test := `{"notificationId":"test","notificationType":"TEST_NOTIFICATION","uId":"UID","notification":{}}`
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://receiver.example", strings.NewReader(test))
		r.Header.Set("Authorization", "Bearer wrong")
		_, err := w.ReceiveNotification(t.Context(), l.ID, "receiver", r)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrNotificationAuth), err)
		_, err = w.LoadNotification(t.Context(), l.ID, "test")
		failAppsBooksIf(t, !errors.Is(err, state.ErrNotFound), err)
		receipt, err := deliverJobCallback(t, w, l.ID, test)
		failAppsBooksIf(t, err != nil || string(receipt.Raw) != test, receipt, err)
		stored, err := w.LoadNotification(t.Context(), l.ID, "test")
		failAppsBooksIf(t, err != nil || string(stored.Raw) != test, stored, err)
		_, err = deliverJobCallback(t, w, l.ID, strings.Replace(test, `"notification":{}`, `"notification":{"changed":true}`, 1))
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrProtocol), err)
		j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
		failAppsBooksIf(t, err != nil, err)
		j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
		failAppsBooksIf(t, err != nil, err)
		_, err = deliverJobCallback(t, w, l.ID, jobCallback("unrequested", "event", "SUCCESS", "other"))
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrProtocol), err)
		_, err = w.LoadNotification(t.Context(), l.ID, "unrequested")
		failAppsBooksIf(t, !errors.Is(err, state.ErrNotFound), err)
		_, err = deliverJobCallback(t, w, l.ID, strings.Replace(jobCallback("wrongtype", "event", "SUCCESS", "s1"), `"type":"ASSOCIATE"`, `"type":"DISASSOCIATE"`, 1))
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrProtocol), err)
	})
	t.Run("expired credentials preserve reconciliation and renew", func(t *testing.T) {
		a, _ := factory(t)
		now := &atomic.Int64{}
		now.Store(time.Now().UnixNano())
		f, cfg, token := licensingFixture(t)
		testClock := &appsBooksClock{Fake: clock.NewFake(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))}
		cfg.Clock = testClock
		w, err := appsbooks.NewWorkflow(&jobTimeStore{Store: a, now: now}, cfg)
		failAppsBooksIf(t, err != nil, err)
		l, err := w.Register(t.Context(), "location", 0, token)
		failAppsBooksIf(t, err != nil, err)
		j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
		failAppsBooksIf(t, err != nil, err)
		f.mu.Lock()
		f.invalid = true
		f.mu.Unlock()
		j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrProtocol), err)
		testClock.Set(time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC))
		_, err = w.RecoverLicensingToken(t.Context(), l.ID, l.Revision, j.ID, token)
		failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrExpired), err)
		still, err := w.LoadJob(t.Context(), l.ID, j.ID)
		failAppsBooksIf(t, err != nil || still.Fence != j.Fence || still.State != appsbooks.JobUncertain, still, err)
		replacement := appsBooksToken("renewed", "2040-01-01T00:00:00Z")
		renewed, err := w.RecoverLicensingToken(t.Context(), l.ID, l.Revision, j.ID, replacement)
		failAppsBooksIf(t, err != nil || renewed.UID != l.UID || renewed.Revision <= l.Revision, renewed, err)
		j, err = w.ReconcileJob(t.Context(), l.ID, j.ID, "event", "stopped worker and recovered returned event", false)
		failAppsBooksIf(t, err != nil, err)
		now.Add(int64(5 * time.Minute))
		j, err = w.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
		failAppsBooksIf(t, err != nil || j.State != appsbooks.JobAccepted, j, err)
	})

	for _, scenario := range []string{"device book", "revoke book", "retired user", "foreign owner", "unknown asset"} {
		t.Run(scenario, func(t *testing.T) {
			w, _, f, _, l, _, _ := setup(t)
			request := jobRequest()
			f.mu.Lock()
			switch scenario {
			case "device book":
				f.product = "Book"
			case "revoke book":
				f.product = "Book"
				request.Operation = appsbooks.JobDisassociate
				request.Assets.SerialNumbers = nil
				request.Assets.ClientUserIDs = []string{"u"}
			case "retired user":
				f.retired = true
				request.Assets.SerialNumbers = nil
				request.Assets.ClientUserIDs = []string{"u"}
			case "foreign owner":
				f.owner = "foreign"
			case "unknown asset":
				request.Assets.Assets[0].AdamID = "missing"
			}
			f.mu.Unlock()
			j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", request)
			failAppsBooksIf(t, err != nil, err)
			_, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
			failAppsBooksIf(t, err == nil, "invalid operation accepted")
			f.mu.Lock()
			posts := f.posts
			f.mu.Unlock()
			failAppsBooksIf(t, posts != 0, posts)
		})
	}
	for _, op := range []appsbooks.JobOperation{appsbooks.JobAssociate, appsbooks.JobDisassociate, appsbooks.JobRevoke, appsbooks.JobCreateUsers, appsbooks.JobUpdateUsers, appsbooks.JobRetireUsers} {
		t.Run(string(op), func(t *testing.T) {
			w, _, f, now, l, _, _ := setup(t)
			request := jobRequest()
			request.Operation = op
			request.Assets.ClientUserIDs = []string{"u"}
			request.Assets.SerialNumbers = nil
			if op == appsbooks.JobRevoke {
				request.Assets = nil
				request.Revoke = &appsbooks.RevokeAssetsRequest{ClientUserIDs: []string{"u"}}
			}
			if op == appsbooks.JobCreateUsers || op == appsbooks.JobUpdateUsers || op == appsbooks.JobRetireUsers {
				request.Assets = nil
				request.Users = &appsbooks.ManageUsersRequest{Users: []appsbooks.RequestUser{{ClientUserID: "u"}}}
			}
			f.mu.Lock()
			f.operation = string(op)
			f.status = "COMPLETE"
			f.completed = 1
			f.requested = 1
			f.mu.Unlock()
			j, err := w.CreateJob(t.Context(), l.ID, l.Revision, "job", request)
			failAppsBooksIf(t, err != nil, err)
			j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
			failAppsBooksIf(t, err != nil, err)
			if request.Users != nil {
				body := fmt.Sprintf(`{"notificationId":"users","notificationType":"USER_MANAGEMENT","uId":"UID","notification":{"eventId":"event","type":%q,"result":"SUCCESS","users":[{"clientUserId":"u","status":"Registered"}]}}`, op)
				_, err = deliverJobCallback(t, w, l.ID, body)
				failAppsBooksIf(t, err != nil, err)
			}
			now.Add(int64(5 * time.Minute))
			j, err = w.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
			failAppsBooksIf(t, err != nil || j.State != appsbooks.JobSucceeded, j, err)
		})
	}
}
