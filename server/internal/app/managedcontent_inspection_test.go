package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// TestManagedContentQueuedACKExplicitStatusRefresh composes the real SDK with
// signed queued ACK, bounded owned queries and read-only native observations.
func TestManagedContentQueuedACKExplicitStatusRefresh(t *testing.T) {
	f := contentFixtureService(t)
	f.assets.Store(true)
	f.assignments.Store(true)
	wake := make(chan struct{}, 8)
	var wakes atomic.Int32
	var appRef *App
	cfg := contentConfig(t, f)
	cfg.Push = PushConfig{Coalesce: -1, Pusher: contentWakePusher(func(ctx context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
		if _, inside := sqlcommon.CurrentTransaction(ctx, appRef.db); inside {
			return nil, errors.New("wake held SQL transaction")
		}
		results := map[mdm.EnrollmentID]push.Result{}
		for _, target := range targets {
			results[target.ID] = push.Result{Outcome: push.OutcomeSent}
			wakes.Add(1)
			select {
			case wake <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return results, nil
	})}
	ca, err := testpki.NewCA("content-status")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CARoots = ca.Pool()
	a, token := contentBuild(t, cfg)
	appRef = a
	registerContent(t, a, token)
	identity, err := ca.Issue("status-selected", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "status-selected"}
	seedContentEnrollment(t, a, id, cms.Fingerprint(identity.Cert), "S-STATUS")
	enrolled, err := a.Store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	enrolled.Push = mdm.Push{Token: []byte{1, 2, 3}, Topic: "com.example.mdm", Magic: "wake-status"}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *enrolled}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler)
	t.Cleanup(server.Close)
	device := simulator.New(id.ID, simulator.WithURLs(server.URL+"/mdm", server.URL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
	device.Responder = func(c *mdm.Command) simulator.Reply {
		switch c.RequestType {
		case "InstallApplication":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstallApplicationResponse{Identifier: new("com.example.pending"), State: new("Queued")}}
		case "InstalledApplicationList":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstalledApplicationListResponse{InstalledApplicationList: []commands.InstalledApplicationListResponseInstalledApplicationList{{Identifier: new("com.example.pending"), Installing: new(false), Version: new("2.4")}}}}
		case "ManagedApplicationList":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.ManagedApplicationListResponse{ManagedApplicationList: map[string]commands.ManagedApplicationListManagedApplicationItem{"com.example.pending": {Status: "Managed"}}}}
		default:
			return simulator.AcknowledgeAll(c)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	workerStopped := make(chan struct{})
	driver := make(chan struct{})
	driverErrors := make(chan error, 1)
	driverRounds := make(chan []*mdm.Command, 8)
	var connects atomic.Int32
	var deliveryRun func(context.Context) error
	for _, worker := range a.workers {
		if worker.name == "event-delivery" {
			deliveryRun = worker.run
		}
	}
	if deliveryRun == nil {
		t.Fatal("real outbox worker missing")
	}
	go func() {
		defer close(workerStopped)
		done <- deliveryRun(ctx)
	}()
	go func() {
		defer close(driver)
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				connects.Add(1)
				processed, err := device.Connect(ctx)
				if err != nil {
					if ctx.Err() == nil {
						driverErrors <- err
					}
					return
				}
				select {
				case driverRounds <- processed:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		<-driver
	})
	waitNative := func(want []string, predicate func(ManagedContentNativeObservation) bool) {
		t.Helper()
		phase, stop := context.WithTimeout(t.Context(), 5*time.Second)
		defer stop()
		var processed []*mdm.Command
		// Connect completes after posting the signed responses and receiving the
		// empty next-command response. Wait for that actual exchange before reading
		// facts: repeated inspection itself takes a SQLite writer reservation.
		select {
		case processed = <-driverRounds:
		case err := <-driverErrors:
			t.Fatal("native driver failed", err)
		case <-workerStopped:
			t.Fatal("outbox worker stopped before native completion")
		case <-phase.Done():
			diagnostics, end := context.WithTimeout(t.Context(), time.Second)
			defer end()
			queued, err := a.Store.Commands(diagnostics, id, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil {
				t.Log("native queue diagnostics failed", err)
			}
			for _, row := range queued.Items {
				t.Logf("native command %s %s state=%s hasResult=%t", row.Command.UUID, row.Command.RequestType, row.State, row.Result != nil)
			}
			deliveries, err := a.eventStore.List(diagnostics, "", "", "", 100)
			if err != nil {
				t.Log("outbox diagnostics failed", err)
			}
			for _, delivery := range deliveries {
				if delivery.Destination == contentWakeDestination {
					t.Logf("wake state=%s attempts=%d lastCode=%s", delivery.State, delivery.Attempts, delivery.LastCode)
				}
			}
			t.Fatal("APNs-driven native exchange incomplete", "wakes", wakes.Load(), "connections", connects.Load(), phase.Err())
		}
		got := make(map[string]int)
		for _, command := range processed {
			got[command.RequestType]++
		}
		for _, kind := range want {
			if got[kind] != 1 {
				t.Fatal("native exchange did not process the expected commands", got, want)
			}
		}
		if len(processed) != len(want) {
			t.Fatal("native exchange processed unrelated commands", got, want)
		}
		proof, err := a.inspectContent(phase, "status-operation")
		if err != nil || !predicate(proof) {
			t.Fatal("completed native exchange lacks authoritative observation", err, proof.Observation.CommandAcknowledged, proof.Observation.Installed, proof.Observation.AppVersion)
		}
	}
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "S-STATUS"}, Method: managedcontent.MDM, MaxPages: 2}
	path := "/managed-content/operations/status-operation"
	w := contentRequest(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitNative([]string{"InstallApplication"}, func(proof ManagedContentNativeObservation) bool { return proof.Observation.CommandAcknowledged })
	before, err := a.protocol.List(t.Context(), "managedcontent/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	requests := f.requests.Load()
	_, denied, err := a.admin.CreatePrincipal(t.Context(), adminauth.Root, adminauth.Principal{Name: "status-denied"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, string(denied), "GET", path+"/native-observation", nil)
	if w.Code != 403 {
		t.Fatal("native evidence implicitly granted", w.Code)
	}
	w = contentRequest(t, a, token, "GET", path+"/native-observation", map[string]any{"dispatch": "client override rejected by construction"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var observed ManagedContentNativeObservation
	if err := json.Unmarshal(w.Body.Bytes(), &observed); err != nil {
		t.Fatal(err)
	}
	if observed.OperationID != "status-operation" || !observed.Observation.CommandAcknowledged || observed.Observation.Installed || observed.Observation.Terminal {
		t.Fatal(observed)
	}
	after, err := a.protocol.List(t.Context(), "managedcontent/", "", 100)
	if err != nil || !reflect.DeepEqual(before, after) || f.requests.Load() != requests {
		t.Fatal("native read changed domain records or contacted Apple", err)
	}
	w = contentRequest(t, a, token, "POST", path+"/status-refresh", map[string]any{"requestId": "query-one"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var batch ManagedContentStatusQuery
	if err := json.Unmarshal(w.Body.Bytes(), &batch); err != nil || len(batch.Commands) != 2 {
		t.Fatal(batch, err)
	}
	w = contentRequest(t, a, token, "POST", path+"/status-refresh", map[string]any{"requestId": "query-one"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	queued, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(queued.Items) != 3 {
		t.Fatal("status refresh duplicated native queries", len(queued.Items), err)
	}
	waitNative([]string{"InstalledApplicationList", "ManagedApplicationList"}, func(proof ManagedContentNativeObservation) bool {
		return proof.Observation.Installed && proof.Observation.Terminal
	})
	before, err = a.protocol.List(t.Context(), "managedcontent/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, token, "GET", path+"/native-observation", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &observed); err != nil {
		t.Fatal(err)
	}
	if !observed.Observation.Terminal || !observed.Observation.Installed || observed.Observation.AppVersion != "2.4" || !observed.Observation.Authoritative || !observed.Observation.OwnsIdentity || !bytes.Contains(observed.Observation.Raw, []byte("com.example.pending")) {
		t.Fatal(observed)
	}
	after, err = a.protocol.List(t.Context(), "managedcontent/", "", 100)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("native evidence read changed operation/evidence bytes", err)
	}
	w = contentRequest(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	op, err := a.ManagedContent.Load(t.Context(), "status-operation")
	if err != nil || op.Observation == nil || !op.Observation.Installed || !op.Observation.Terminal {
		t.Fatal(op, err)
	}
	if wakes.Load() != 2 {
		t.Fatal("unexpected content wake count", wakes.Load())
	}
	if f.posts.Load() != 0 {
		t.Fatal("status completion licensed content")
	}
}

// TestManagedContentBookNativeStatus uses an already assigned user book without
// purchases/reclamation and chooses the narrower typed native media protocol.
func TestManagedContentBookNativeStatus(t *testing.T) {
	f := contentFixtureService(t)
	f.assets.Store(true)
	f.assignments.Store(true)
	f.assetKind.Store("Book")
	cfg := contentConfig(t, f)
	ca, err := testpki.NewCA("book-status")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CARoots = ca.Pool()
	a, token := contentBuild(t, cfg)
	registerContent(t, a, token)
	identity, err := ca.Issue("book-selected", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "book-selected"}
	seedContentEnrollment(t, a, id, cms.Fingerprint(identity.Cert), "")
	w := contentRequest(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": id, "clientUserId": "reader"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	server := httptest.NewServer(a.Handler)
	t.Cleanup(server.Close)
	device := simulator.New(id.ID, simulator.WithURLs(server.URL+"/mdm", server.URL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
	device.Responder = func(c *mdm.Command) simulator.Reply {
		switch c.RequestType {
		case "InstallMedia":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstallMediaResponse{ITunesStoreID: new(int64(1)), MediaType: new("Book"), State: new("Queued")}}
		case "ManagedMediaList":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.ManagedMediaListResponse{Books: []commands.ManagedMediaListResponseBooks{{ITunesStoreID: 1, State: new("Installed"), Version: new("book-2")}}}}
		default:
			t.Errorf("book used app protocol %s", c.RequestType)
			return simulator.AcknowledgeAll(c)
		}
	}
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "Book"}, Target: managedcontent.Selection{Enrollment: id, ClientUserID: "reader"}, Method: managedcontent.MDM, MaxPages: 2}
	path := "/managed-content/operations/selected-book"
	w = contentRequest(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, token, "POST", path+"/status-refresh", map[string]any{"requestId": "book-check"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, token, "GET", path+"/native-observation", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var proof ManagedContentNativeObservation
	if err := json.Unmarshal(w.Body.Bytes(), &proof); err != nil {
		t.Fatal(err)
	}
	if !proof.Observation.Installed || !proof.Observation.Terminal || proof.Observation.AppVersion != "book-2" {
		t.Fatal(proof)
	}
	foreign, err := mdm.NewCommand(&commands.InstallMedia{ITunesStoreID: new(int64(1)), MediaType: "Book"}, mdm.WithUUID("unowned-book-reinstall"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/v1/enrollments/device/"+id.ID+"/commands", bytes.NewReader(foreign.Raw))
	request.Header.Set("Authorization", "Bearer "+token)
	rejected := httptest.NewRecorder()
	a.Handler.ServeHTTP(rejected, request)
	if rejected.Code != 409 {
		t.Fatal("generic InstallMedia bypassed owned book scope", rejected.Code, rejected.Body.String())
	}
	if f.posts.Load() != 0 {
		t.Fatal("book install mutated licensing")
	}
	// The same user license does not make InstallMedia available on modern macOS.
	parent := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "modern-mac"}
	seedContentEnrollment(t, a, parent, "mac-pin", "")
	parentRecord, err := a.Store.Get(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	parentRecord.Device.ProductName = "Mac14,7"
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *parentRecord}); err != nil {
		t.Fatal(err)
	}
	user := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "modern-mac:reader", ParentID: parent.ID}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: user, Enabled: true, UserShortName: "reader", EnrolledAt: time.Now(), TokenUpdatedAt: time.Now(), LastSeenAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": user, "clientUserId": "reader"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	plan.Target.Enrollment = user
	w = contentRequest(t, a, token, "PUT", "/managed-content/operations/unsupported-book", map[string]any{"mode": "install", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", "/managed-content/operations/unsupported-book/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 400 {
		t.Fatal("unsupported modern macOS book dispatched", w.Code, w.Body.String())
	}
	rows, err := a.Store.Commands(t.Context(), user, storage.CommandQuery{}, paging.Page{Limit: 10})
	if err != nil || len(rows.Items) != 0 {
		t.Fatal("unsupported media side effect", rows, err)
	}
}

type contentWakePusher func(context.Context, []push.Target) (map[mdm.EnrollmentID]push.Result, error)

// Push drives the simulator only through the real outbox/notifier APNs seam.
func (f contentWakePusher) Push(ctx context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
	return f(ctx, targets)
}
