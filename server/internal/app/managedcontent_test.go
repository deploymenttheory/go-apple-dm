package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

type contentFixture struct {
	uid                string
	callbackClient     *http.Client
	notificationSecret atomic.Value
	config             AppsBooksConfig
	posts              atomic.Int32
	assets             atomic.Bool
	assetReads         atomic.Int32
	assignments        atomic.Bool
	fail               atomic.Bool
	owner              atomic.Value
}

// contentFixtureService hosts controlled real TLS Apple-service responses and counts mutations.
func contentFixtureService(t *testing.T) *contentFixture {
	t.Helper()
	f := &contentFixture{uid: rand.Text()}
	f.owner.Store("ours")
	f.notificationSecret.Store("private-callback")
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/service/config" {
			urls := map[string]string{}
			for k, p := range map[string]string{"clientConfig": "/config", "getAssets": "/assets", "getAssignments": "/assignments", "getUsers": "/users", "associateAssets": "/manage", "disassociateAssets": "/manage", "eventStatus": "/status"} {
				urls[k] = srv.URL + p
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"urls": urls, "limits": map[string]int{"maxAssets": 100, "maxClientUserIds": 100, "maxSerialNumbers": 100, "maxRequestPerSecond": 100000, "maxMdmIdLength": 100, "maxMdmNameLength": 100, "maxMdmMetadataLength": 100, "maxNotificationLength": 100}})
			return
		}
		if r.Method == http.MethodPost {
			f.posts.Add(1)
			if f.fail.Load() {
				http.Error(w, "unavailable", 503)
				return
			}
			if r.URL.Path == "/manage" {
				_, _ = fmt.Fprintf(w, `{"uId":%q,"mdmInfo":{"id":%q},"eventId":"fixture-event"}`, f.uid, f.owner.Load().(string))
				return
			}
			var in appsbooks.ClientConfigurationRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
				return
			}
			if in.NotificationURL != "" {
				if f.callbackClient == nil {
					http.Error(w, "notification receiver not available", 503)
					return
				}
				body := fmt.Sprintf(`{"notificationId":"synchronous-%d","notificationType":"TEST_NOTIFICATION","uId":%q,"notification":{}}`, f.posts.Load(), f.uid)
				request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, in.NotificationURL, strings.NewReader(body))
				if err != nil {
					t.Error(err)
					http.Error(w, "invalid receiver", 503)
					return
				}
				request.Header.Set("Authorization", "Bearer "+in.NotificationAuthToken)
				response, err := f.callbackClient.Do(request)
				if err != nil {
					t.Error(err)
					http.Error(w, "notification receiver failed", 503)
					return
				}
				if err := response.Body.Close(); err != nil {
					t.Error(err)
				}
				if response.StatusCode/100 != 2 {
					http.Error(w, "notification rejected", 503)
					return
				}
			}
			f.notificationSecret.Store(in.NotificationAuthToken)
			f.owner.Store(in.MDMInfo.ID)
		}
		meta := fmt.Sprintf(`"uId":%q`, f.uid)
		if owner := f.owner.Load().(string); owner != "" {
			meta += fmt.Sprintf(`,"mdmInfo":{"id":%q}`, owner)
		}
		if r.URL.Path == "/config" {
			_, _ = fmt.Fprintf(w, `{%s,"locationName":"London","notificationAuthToken":%q}`, meta, f.notificationSecret.Load().(string))
			return
		}
		if r.URL.Path == "/assets" {
			f.assetReads.Add(1)
		}
		if r.URL.Path == "/assets" && f.assets.Load() {
			_, _ = fmt.Fprintf(w, `{%s,"currentPageIndex":0,"totalPages":1,"versionId":"assets-v1","assets":[{"adamId":"1","pricingParam":"STDQ","productType":"App","deviceAssignable":true,"revocable":true,"supportedPlatforms":["iOS","macOS"],"totalCount":100,"availableCount":100}]}`, meta)
			return
		}
		if r.URL.Path == "/assignments" && f.assignments.Load() {
			assignment := map[string]string{"adamId": "1", "pricingParam": "STDQ"}
			if user := r.URL.Query().Get("clientUserId"); user != "" {
				assignment["clientUserId"] = user
				assignment["userStatus"] = "Associated"
			} else {
				assignment["serialNumber"] = r.URL.Query().Get("serialNumber")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"uId": f.uid, "mdmInfo": map[string]string{"id": f.owner.Load().(string)}, "currentPageIndex": 0, "totalPages": 1, "versionId": "assignments-v1", "assignments": []any{assignment}})
			return
		}
		kind := strings.TrimPrefix(r.URL.Path, "/")
		_, _ = fmt.Fprintf(w, `{%s,"currentPageIndex":0,"totalPages":1,"size":0,"versionId":"v1",%q:[]}`, meta, kind)
	}))
	t.Cleanup(srv.Close)
	f.config = AppsBooksConfig{MDMID: "ours", BaseURL: srv.URL, HTTPClient: srv.Client()}
	return f
}

// contentConfig selects an isolated strict-sealed SQLite runtime for the service fixture.
func contentConfig(t *testing.T, f *contentFixture) Config {
	t.Helper()
	return Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "content.db"), Listen: "127.0.0.1:0", StorageKeys: []string{"content"}, StorageKeysStrict: true, Secrets: secrets.Static{"content": bytes.Repeat([]byte{7}, 32)}, AppsBooks: f.config, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Sinks: SinkConfig{Persist: true}}
}

// contentBuild constructs the real runtime and grants its test principal explicit operations.
func contentBuild(t *testing.T, cfg Config) (*App, string) {
	t.Helper()
	a, err := Build(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	token, err := FixtureAdminCredential(a)
	if err != nil {
		t.Fatal(err)
	}
	return a, string(token)
}

// contentRequest submits bounded JSON through the real authenticated admin handler.
func contentRequest(t *testing.T, a *App, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), method, "/admin/v1"+path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	return w
}

// contentToken creates a private fixture content token with a stable future expiry.
func contentToken(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`{"token":%q,"expDate":"2035-01-01T00:00:00Z"}`, value)))
}

// registerContent registers one explicitly named location through authenticated HTTP.
func registerContent(t *testing.T, a *App, token string) appsbooks.Location {
	t.Helper()
	w := contentRequest(t, a, token, "PUT", "/appsbooks/locations/london", map[string]any{"revision": 0, "sToken": contentToken("initial-secret")})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var l appsbooks.Location
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// TestManagedContentHTTPDurability verifies real sealed SQL, explicit ownership,
// safe/evidence separation, callback auth/rotation/replays and process restart.
func TestManagedContentHTTPDurability(t *testing.T) {
	f := contentFixtureService(t)
	cfg := contentConfig(t, f)
	a, token := contentBuild(t, cfg)
	l := registerContent(t, a, token)
	for _, path := range []string{"/appsbooks/locations/london", "/appsbooks/locations/london/jobs", "/managed-content/operations"} {
		w := contentRequest(t, a, token, "GET", path, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "private-callback") || strings.Contains(w.Body.String(), "initial-secret") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := contentRequest(t, a, token, "GET", "/appsbooks/locations/london/evidence", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "configuration") {
		t.Fatal(w.Code, w.Body.String())
	}
	// Registration and refresh never claim the remote location.
	if f.posts.Load() != 0 {
		t.Fatal("implicit ownership mutation")
	}
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/refresh", map[string]any{"revision": l.Revision, "maxPages": 2})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, receiver := range []string{"old-receiver", "new-receiver"} {
		w = contentRequest(t, a, token, "PUT", "/appsbooks/locations/london/receiver", map[string]any{"token": receiver})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		r := httptest.NewRequestWithContext(t.Context(), "POST", "/appsbooks/notifications/london", strings.NewReader(fmt.Sprintf(`{"notificationId":"callback-1","notificationType":"TEST_NOTIFICATION","uId":%q,"notification":{}}`, f.uid)))
		r.Header.Set("Authorization", "Bearer "+receiver)
		reply := httptest.NewRecorder()
		a.Handler.ServeHTTP(reply, r)
		if reply.Code != 204 {
			t.Fatal(reply.Code, reply.Body.String())
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/appsbooks/notifications/london", strings.NewReader(fmt.Sprintf(`{"notificationId":"callback-2","notificationType":"TEST_NOTIFICATION","uId":%q,"notification":{}}`, f.uid)))
	r.Header.Set("Authorization", "Bearer old-receiver")
	w = httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("old receiver accepted", w.Code)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, token = contentBuild(t, cfg)
	w = contentRequest(t, a, token, "GET", "/appsbooks/locations/london/notifications/callback-1/evidence", nil)
	if w.Code != 200 {
		t.Fatal("receipt lost on restart", w.Code, w.Body.String())
	}
	// Durable intent uses no network and survives a second process composition.
	seedContentEnrollment(t, a, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "selected"}, "selected-pin", "S1")
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "selected"}, Serial: "S1"}, MaxPages: 2}
	w = contentRequest(t, a, token, "PUT", "/managed-content/operations/assign-1", map[string]any{"mode": "assign", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if f.posts.Load() != 0 {
		t.Fatal("queue caused remote mutation")
	}
	w = contentRequest(t, a, token, "POST", "/managed-content/operations/assign-1/cancel", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := a.ManagedContent.Load(t.Context(), "assign-1"); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := a.db.QueryRowContext(t.Context(), "SELECT value FROM protocol_state WHERE record_key = ?", receiverKey("london")).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("new-receiver")) {
		t.Fatal("unsealed callback credential")
	}
}

// TestManagedContentDeniedAndCaptureRollback checks actual HTTP authorization
// before Apple calls and actual SDK writes joined to required SQL event capture.
func TestManagedContentDeniedAndCaptureRollback(t *testing.T) {
	f := contentFixtureService(t)
	a, token := contentBuild(t, contentConfig(t, f))
	registerContent(t, a, token)
	_, deniedToken, err := a.admin.CreatePrincipal(t.Context(), adminauth.Root, adminauth.Principal{Name: "denied"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	w := contentRequest(t, a, string(deniedToken), "POST", "/appsbooks/locations/london/configure", map[string]any{"revision": 1, "configuration": map[string]any{"mdmInfo": map[string]string{"id": "ours"}, "notificationTypes": []string{}}})
	if w.Code != 403 || f.posts.Load() != 0 {
		t.Fatal("denied side effect", w.Code, f.posts.Load())
	}
	for _, action := range managedContentActions() {
		for _, registered := range AdminActions() {
			if registered.ID == action.ID && len(registered.Groups) != 0 {
				t.Fatal("implicit routine grant", registered)
			}
		}
	}
	// Fault at the production capture seam, after actual state Put, rolls it back.
	publisher := a.eventPublisher
	publisher.CaptureAdditional = func(context.Context, event.Event) error { return errors.New("required capture unavailable") }
	old, err := a.AppsBooks.Load(t.Context(), "london")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.AppsBooks.Refresh(t.Context(), "london", old.Revision, 2)
	if !errors.Is(err, event.ErrCapture) {
		t.Fatal("refresh accepted failed capture", err)
	}
	current, err := a.AppsBooks.Load(t.Context(), "london")
	if err != nil || current.Revision != old.Revision {
		t.Fatal("revision escaped rollback", current, err)
	}
	w = contentRequest(t, a, token, "PUT", "/appsbooks/locations/london/receiver", map[string]any{"token": "must-rollback"})
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := a.protocol.Get(t.Context(), receiverKey("london")); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("receiver escaped capture rollback", err)
	}
	publisher.CaptureAdditional = nil
}

// TestContentNativeUserBinding uses native privacy-preserving enrollment state;
// no ABM connection, serial or universal inventory record is needed.
func TestContentNativeUserBinding(t *testing.T) {
	f := contentFixtureService(t)
	a, token := contentBuild(t, contentConfig(t, f))
	id := mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "native-private"}
	seedContentEnrollment(t, a, id, "native-authenticated-pin", "")
	w := contentRequest(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": id, "clientUserId": "apple-user"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	targets, err := (contentResolver{a}).Resolve(t.Context(), managedcontent.Selection{Enrollment: id, ClientUserID: "apple-user"})
	if err != nil || len(targets) != 1 || targets[0].Serial != "" || targets[0].ClientUserID != "apple-user" {
		t.Fatal(targets, err)
	}
	_, err = (contentResolver{a}).Resolve(t.Context(), managedcontent.Selection{Enrollment: id, ClientUserID: "other-user"})
	if !errors.Is(err, managedcontent.ErrTarget) {
		t.Fatal("unbound Apple user accepted", err)
	}
	if err := a.Store.AssociateCert(t.Context(), id, "replacement-pin", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err = (contentResolver{a}).Resolve(t.Context(), managedcontent.Selection{Enrollment: id, ClientUserID: "apple-user"})
	if !errors.Is(err, managedcontent.ErrTarget) {
		t.Fatal("binding survived reenrollment", err)
	}
}

// seedContentEnrollment seeds an authenticated native fixture without Apple inventory enrichment.
func seedContentEnrollment(t *testing.T, a *App, id mdm.EnrollmentID, cert, serial string) {
	t.Helper()
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, CertHash: cert, EnrolledAt: time.Now(), TokenUpdatedAt: time.Now(), LastSeenAt: time.Now(), CertHashAt: time.Now(), Device: storage.DeviceInfo{SerialNumber: serial, ProductName: "iPhone18,1", OSVersion: "27.0"}, Capabilities: storage.Capabilities{Supervised: storage.CapabilityTrue}}}); err != nil {
		t.Fatal(err)
	}
}

// TestManagedContentWorkerLifecycle proves persisted local intent is observed
// after runtime reconstruction and the supervised worker joins cancellation.
func TestManagedContentWorkerLifecycle(t *testing.T) {
	f := contentFixtureService(t)
	cfg := contentConfig(t, f)
	a, token := contentBuild(t, cfg)
	registerContent(t, a, token)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "worker-target"}
	seedContentEnrollment(t, a, id, "worker-pin", "S-WORKER")
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "S-WORKER"}, MaxPages: 2}
	w := contentRequest(t, a, token, "PUT", "/managed-content/operations/worker-operation", map[string]any{"mode": "assign", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, _ = contentBuild(t, cfg)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		op, err := a.ManagedContent.Load(t.Context(), "worker-operation")
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if op.Blocked != "" {
			break
		}
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("persisted intent not resumed")
		case <-poll.C:
		}
	}
	for f.assetReads.Load() < 2 {
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("worker did not finish a populated page and resume its next iteration")
		case <-poll.C:
		}
	}
	running := false
	for _, worker := range a.Workers() {
		if worker.Name == "managed-content" {
			running = worker.Running
		}
	}
	if !running {
		t.Fatal("content worker absent from supervision")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, worker := range a.Workers() {
		if worker.Name == "managed-content" && worker.Running {
			t.Fatal("content worker outlived cancellation")
		}
	}
	if f.posts.Load() != 0 {
		t.Fatal("failed native asset preflight submitted a mutation")
	}
}

// TestManagedContentExplicitConfigurationAndJobRecovery exercises the HTTP SDK
// composition through ownership, uncertain remote writes, token recovery and
// stopped licensing reconciliation without replaying a mutation.
func TestManagedContentExplicitConfigurationAndJobRecovery(t *testing.T) {
	f := contentFixtureService(t)
	f.owner.Store("")
	a, token := contentBuild(t, contentConfig(t, f))
	location := registerContent(t, a, token)
	configuration := appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Example MDM", Metadata: "operator-owned"}, NotificationTypes: []string{}}
	f.fail.Store(true)
	w := contentRequest(t, a, token, "POST", "/appsbooks/locations/london/configure", map[string]any{"revision": location.Revision, "configuration": configuration})
	if w.Code != 503 || f.posts.Load() != 1 {
		t.Fatal(w.Code, w.Body.String(), f.posts.Load())
	}
	pending, err := a.AppsBooks.Load(t.Context(), "london")
	if err != nil || !pending.ConfigurationPending {
		t.Fatal(pending, err)
	}
	f.fail.Store(false)
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/recover-configuration", map[string]any{"revision": pending.Revision, "sToken": contentToken("recovery-secret")})
	if w.Code != 200 || f.posts.Load() != 1 {
		t.Fatal("recovery replayed configuration", w.Code, w.Body.String(), f.posts.Load())
	}
	location, err = a.AppsBooks.Load(t.Context(), "london")
	if err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/configure", map[string]any{"revision": location.Revision, "configuration": configuration})
	if w.Code != 200 || f.owner.Load().(string) != "ours" {
		t.Fatal(w.Code, w.Body.String())
	}
	location, err = a.AppsBooks.Load(t.Context(), "london")
	if err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/probe", map[string]any{"revision": location.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	location, err = a.AppsBooks.Load(t.Context(), "london")
	if err != nil {
		t.Fatal(err)
	}
	f.assets.Store(true)
	request := appsbooks.JobRequest{Operation: appsbooks.JobAssociate, Assets: &appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{{AdamID: "1", PricingParam: "STDQ"}}, SerialNumbers: []string{"chosen-serial"}}, MaxPages: 2}
	before := f.posts.Load()
	w = contentRequest(t, a, token, "PUT", "/appsbooks/locations/london/jobs/chosen", map[string]any{"revision": location.Revision, "request": request})
	if w.Code != 200 || f.posts.Load() != before {
		t.Fatal("job admission called Apple mutation", w.Code, w.Body.String())
	}
	for _, suffix := range []string{"", "/evidence"} {
		w = contentRequest(t, a, token, "GET", "/appsbooks/locations/london/jobs/chosen"+suffix, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	f.fail.Store(true)
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/jobs/chosen/submit", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 503 || f.posts.Load() != before+1 {
		t.Fatal(w.Code, w.Body.String(), f.posts.Load())
	}
	job, err := a.AppsBooks.LoadJob(t.Context(), "london", "chosen")
	if err != nil || job.State != appsbooks.JobUncertain {
		t.Fatal(job.State, err)
	}
	f.fail.Store(false)
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/recover-licensing", map[string]any{"revision": location.Revision, "jobId": "chosen", "sToken": contentToken("licensing-recovery")})
	if w.Code != 200 || f.posts.Load() != before+1 {
		t.Fatal("recovery replayed licensing", w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/jobs/chosen/reconcile", map[string]any{"note": "original worker stopped; operator abandons uncertain fixture operation", "abandon": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	location, err = a.AppsBooks.Load(t.Context(), "london")
	if err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, token, "PUT", "/appsbooks/locations/london/jobs/accepted", map[string]any{"revision": location.Revision, "request": request})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/jobs/accepted/submit", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/jobs/accepted/poll", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 409 {
		t.Fatal("early poll not fenced", w.Code, w.Body.String())
	}
}

// TestManagedContentAdmissionRejectsRebinding proves a queued native user intent
// cannot follow a later alias/certificate binding into a different principal.
func TestManagedContentAdmissionRejectsRebinding(t *testing.T) {
	f := contentFixtureService(t)
	a, token := contentBuild(t, contentConfig(t, f))
	registerContent(t, a, token)
	id := mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "bound-user"}
	seedContentEnrollment(t, a, id, "initial-pin", "")
	bind := func(alias string) {
		t.Helper()
		w := contentRequest(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": id, "clientUserId": alias})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	bind("chosen-user")
	plan := managedcontent.Plan{LocationID: "london", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, ClientUserID: "chosen-user"}, MaxPages: 2}
	w := contentRequest(t, a, token, "PUT", "/managed-content/operations/pinned", map[string]any{"mode": "assign", "plan": plan})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	bind("other-user")
	w = contentRequest(t, a, token, "POST", "/managed-content/operations/pinned/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 503 || f.posts.Load() != 0 {
		t.Fatal("revoked binding submitted remote licensing", w.Code, f.posts.Load())
	}
	if err := a.Store.AssociateCert(t.Context(), id, "replacement-pin", time.Now()); err != nil {
		t.Fatal(err)
	}
	bind("chosen-user")
	w = contentRequest(t, a, token, "POST", "/managed-content/operations/pinned/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
	if w.Code != 503 || f.posts.Load() != 0 {
		t.Fatal("certificate rotation redirected intent", w.Code, f.posts.Load())
	}
	op, err := a.ManagedContent.Load(t.Context(), "pinned")
	if err != nil || op.Blocked != "target" || op.License != nil || op.Dispatch != nil {
		t.Fatal("rebound intent gained effects", op, err)
	}
}

// TestManagedContentSynchronousNotificationBootstrap models Apple's actual
// configuration handshake: the service requires a committed test callback before
// responding to the configuration POST, including capture failure and recovery.
func TestManagedContentSynchronousNotificationBootstrap(t *testing.T) {
	f := contentFixtureService(t)
	a, token := contentBuild(t, contentConfig(t, f))
	callbackServer := httptest.NewTLSServer(a.Handler)
	t.Cleanup(callbackServer.Close)
	f.callbackClient = callbackServer.Client()
	f.callbackClient.Timeout = 2 * time.Second
	location := registerContent(t, a, token)
	configuration := appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Reference MDM", Metadata: "fixture"}, NotificationURL: callbackServer.URL + "/appsbooks/notifications/london", NotificationAuthToken: "bootstrap-secret", NotificationTypes: []string{"TEST_NOTIFICATION"}}
	w := contentRequest(t, a, token, "POST", "/appsbooks/locations/london/configure", map[string]any{"revision": location.Revision, "configuration": configuration})
	if w.Code < 400 || f.posts.Load() != 0 {
		t.Fatal("configuration without durable receiver contacted Apple", w.Code, f.posts.Load())
	}
	w = contentRequest(t, a, token, "PUT", "/appsbooks/locations/london/receiver", map[string]any{"token": "bootstrap-secret"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.eventPublisher.CaptureAdditional = func(_ context.Context, e event.Event) error {
		if e.Type == "appsbooks-notification" {
			return errors.New("required test notification capture failed")
		}
		return nil
	}
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/configure", map[string]any{"revision": location.Revision, "configuration": configuration})
	if w.Code != 503 || f.posts.Load() != 1 {
		t.Fatal("failed synchronous callback accepted", w.Code, w.Body.String(), f.posts.Load())
	}
	pending, err := a.AppsBooks.Load(t.Context(), "london")
	if err != nil || !pending.ConfigurationPending {
		t.Fatal("uncertain configuration lost reservation", pending, err)
	}
	if _, err := a.AppsBooks.LoadNotification(t.Context(), "london", "synchronous-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed callback capture escaped rollback", err)
	}
	a.eventPublisher.CaptureAdditional = nil
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/probe", map[string]any{"revision": pending.Revision})
	if w.Code != 200 || f.posts.Load() != 1 {
		t.Fatal("probe replayed POST", w.Code, w.Body.String())
	}
	location, err = a.AppsBooks.Load(t.Context(), "london")
	if err != nil {
		t.Fatal(err)
	}
	w = contentRequest(t, a, token, "POST", "/appsbooks/locations/london/configure", map[string]any{"revision": location.Revision, "configuration": configuration})
	if w.Code != 200 || f.posts.Load() != 2 {
		t.Fatal("synchronous notification handshake failed", w.Code, w.Body.String(), f.posts.Load())
	}
	if _, err := a.AppsBooks.LoadNotification(t.Context(), "london", "synchronous-2"); err != nil {
		t.Fatal("successful configuration lacks durable test receipt", err)
	}
	location, err = a.AppsBooks.Load(t.Context(), "london")
	if err != nil || location.ConfigurationPending {
		t.Fatal("successful configuration remains pending", location, err)
	}
	var captured int
	if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_records WHERE type = ?", "appsbooks-notification").Scan(&captured); err != nil || captured != 1 {
		t.Fatal("test callback audit did not commit exactly once", captured, err)
	}
}
