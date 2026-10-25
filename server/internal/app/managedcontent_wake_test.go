package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// TestManagedContentWakeRollbackRetryRestart verifies the actual durable outbox
// selection, native transaction rollback and bounded wake retry across restart.
func TestManagedContentWakeRollbackRetryRestart(t *testing.T) {
	f := contentFixtureService(t)
	f.assets.Store(true)
	f.assignments.Store(true)
	cfg := contentConfig(t, f)
	var calls atomic.Int32
	var appRef *App
	cfg.Push = PushConfig{Coalesce: -1, Pusher: contentWakePusher(func(ctx context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
		if _, inside := sqlcommon.CurrentTransaction(ctx, appRef.db); inside {
			return nil, errors.New("APNs called under SQL transaction")
		}
		attempt := calls.Add(1)
		results := map[mdm.EnrollmentID]push.Result{}
		for _, target := range targets {
			outcome := push.OutcomeSent
			if attempt == 1 {
				outcome = push.OutcomeUnavailable
			}
			results[target.ID] = push.Result{Outcome: outcome}
		}
		return results, nil
	})}
	a, token := contentBuild(t, cfg)
	appRef = a
	registerContent(t, a, token)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "wake-durable"}
	seedContentEnrollment(t, a, id, "wake-pin", "S-WAKE")
	enrolled, err := a.Store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	enrolled.Push = mdm.Push{Topic: "com.example.mdm", Token: []byte{8}, Magic: "durable"}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *enrolled}); err != nil {
		t.Fatal(err)
	}
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "S-WAKE"}, Method: managedcontent.MDM, MaxPages: 2}
	path := "/managed-content/operations/wake-durable"
	w := contentRequest(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.eventPublisher.CaptureAdditional = func(_ context.Context, e event.Event) error {
		if e.Type == "managed-content-dispatch" {
			return errors.New("required native capture failed")
		}
		return nil
	}
	w = contentRequest(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	var pending int
	if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_deliveries WHERE destination = ?", contentWakeDestination).Scan(&pending); err != nil || pending != 0 || calls.Load() != 0 {
		t.Fatal("rollback emitted a wake", pending, calls.Load(), err)
	}
	a.eventPublisher.CaptureAdditional = nil
	w = contentRequest(t, a, token, "POST", path+"/reconcile", contentWorkerInput{Worker: "operator", LeaseSeconds: 60, RetryAbsent: true, Note: "original worker stopped; atomic native state absent"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatal("native push escaped before outbox worker")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, token = contentBuild(t, cfg)
	appRef = a
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	run := contentDeliveryWorker(t, a)
	go func() { done <- run(ctx) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var state string
		var attempts int
		if err := a.db.QueryRowContext(t.Context(), "SELECT state, attempts FROM event_deliveries WHERE destination = ?", contentWakeDestination).Scan(&state, &attempts); err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if state == "pending" && attempts == 1 && calls.Load() == 1 {
			break
		}
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("first wake failure not durably scheduled")
		case <-tick.C:
		}
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, token = contentBuild(t, cfg)
	appRef = a
	ctx, cancel = context.WithCancel(t.Context())
	done = make(chan error, 1)
	run = contentDeliveryWorker(t, a)
	go func() { done <- run(ctx) }()
	for {
		var state string
		if err := a.db.QueryRowContext(t.Context(), "SELECT state FROM event_deliveries WHERE destination = ?", contentWakeDestination).Scan(&state); err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if state == "delivered" && calls.Load() == 2 {
			break
		}
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("durable wake did not retry after restart", state, calls.Load())
		case <-tick.C:
		}
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	commands, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 10})
	if err != nil || len(commands.Items) != 1 {
		t.Fatal("wake retry replayed native installation", len(commands.Items), err)
	}
	w = contentRequest(t, a, token, "GET", path, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var op managedcontent.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil || op.Observation == nil || op.Observation.Installed {
		t.Fatal("wake acceptance inferred installation", op, err)
	}
	before := calls.Load()
	err = a.sendContentWake(t.Context(), eventsink.Record{ID: id.ID, Channel: "foreign-channel"})
	var rejected *eventsink.HTTPError
	if !errors.As(err, &rejected) || rejected.Status != 400 || calls.Load() != before {
		t.Fatal("wrong-channel wake contacted APNs or retried", err, calls.Load())
	}
}

// contentDeliveryWorker returns the actual runtime's configured outbox worker.
func contentDeliveryWorker(t *testing.T, a *App) func(context.Context) error {
	t.Helper()
	for _, worker := range a.workers {
		if worker.name == "event-delivery" {
			return worker.run
		}
	}
	t.Fatal("configured event delivery worker absent")
	return nil
}
