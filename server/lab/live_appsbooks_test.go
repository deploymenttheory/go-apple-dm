package lab

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
)

// appsFixture simulates Apple's service only. Reference composition acceptance
// additionally starts the shipped SQL-backed runtime; it does not use this
// fixture's preliminary negative-test administration handler as proof.
type appsFixture struct {
	mu                   sync.Mutex
	config               appsbooks.Config
	location             appsbooks.Location
	assigned             bool
	posts                int
	pageFailure          bool
	partial              bool
	statusFailure        bool
	foreign              bool
	duplicate            bool
	callback             string
	callbackClient       *http.Client
	callbackAuth         string
	assignment           appsbooks.Assignment
	dropCallback         bool
	dropCallbackEvent    int
	lateReferenceFailure bool
	beforeLicensing      func() error
	events               int
	requests             []string
	rewrite              func(*http.Request, map[string]any)
}

// appsTestClock advances private simulated delays without wall-clock minutes.
// It never changes the SQL transaction clock or invents an API outcome.
type appsTestClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

// Now returns synchronized simulated time.
func (c *appsTestClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

// Since measures elapsed simulated time.
func (c *appsTestClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// After advances only the direct-client fixture timer and returns its completion.
func (c *appsTestClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.waits = append(c.waits, d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

// postCount counts Apple mutation requests observed by the fixture.
func (f *appsFixture) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.posts }

// appsSDKFixture creates the controlled Apple endpoint with synthetic location facts.
func appsSDKFixture(t *testing.T) *appsFixture {
	t.Helper()
	f := &appsFixture{location: appsbooks.Location{ID: "lab", UID: "library", MDMID: "mdm", Revision: 1}, assignment: appsbooks.Assignment{Asset: appsbooks.Asset{AdamID: "123", PricingParam: "STDQ"}, ClientUserID: "private-user", UserStatus: "Associated"}}
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, srv.URL, w, r) }))
	t.Cleanup(srv.Close)
	f.config = appsbooks.Config{MDMID: "mdm", UID: "library", SToken: base64.StdEncoding.EncodeToString([]byte(`{"token":"private-token","expDate":"2035-01-01T00:00:00Z"}`)), BaseURL: srv.URL, HTTPClient: srv.Client(), Clock: &appsTestClock{now: time.Now()}}
	return f
}

// appsFixtureSession builds a private simulated acceptance scope and SDK fixture.
func appsFixtureSession(t *testing.T) (*Session, *appsFixture) {
	t.Helper()
	f := appsSDKFixture(t)
	var queued appsbooks.LicensingJob
	admin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/jobs/") {
			if r.Method == "PUT" {
				var in struct {
					Revision uint64               `json:"revision"`
					Request  appsbooks.JobRequest `json:"request"`
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					w.WriteHeader(400)
					return
				}
				parts := strings.Split(r.URL.Path, "/")
				queued = appsbooks.LicensingJob{ID: parts[len(parts)-1], State: appsbooks.JobQueued, LocationRevision: in.Revision, Request: in.Request}
				_ = json.NewEncoder(w).Encode(queued)
				return
			}
			if r.Method == "GET" {
				_ = json.NewEncoder(w).Encode(queued)
				return
			}
			w.WriteHeader(500)
			return
		}
		if r.Method != "GET" {
			w.WriteHeader(500)
			return
		}
		f.mu.Lock()
		location := f.location
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(location)
	}))
	t.Cleanup(admin.Close)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "appsbooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Session{Env: &Environment{Workspace: &Workspace{Directory: dir}, Client: admin.Client(), Instance: Instance{URL: admin.URL, Mode: "simulated"}, appsBooksFixture: &f.config}, Evidence: t.TempDir()}
	c := appsAcceptance{Location: "lab", UID: "library", MDMID: "mdm", MaxPages: 5, Asset: appsbooks.Asset{AdamID: "123", PricingParam: "STDQ"}, User: "private-user", FreeApp: true, Mutate: true, Quiescent: true}
	adeTestJSON(t, filepath.Join(dir, "appsbooks", "acceptance.json"), c)
	writeFixture(t, filepath.Join(dir, "appsbooks", "stoken"), []byte(f.config.SToken))
	return s, f
}

// serve implements bounded synthetic Apple responses and records requested actions.
func (f *appsFixture) serve(t *testing.T, base string, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/service/config" {
		urls := map[string]string{}
		for key, path := range map[string]string{"clientConfig": "config", "getAssets": "assets", "getAssignments": "assignments", "getUsers": "users", "associateAssets": "associate", "disassociateAssets": "disassociate", "eventStatus": "status"} {
			urls[key] = base + "/" + path
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"urls": urls, "limits": map[string]int{"maxAssets": 1, "maxClientUserIds": 1, "maxSerialNumbers": 1, "maxRequestPerSecond": 100000, "maxMdmIdLength": 100, "maxMdmNameLength": 100, "maxMdmMetadataLength": 256, "maxNotificationLength": 4096}})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.config.SToken {
		t.Error("wrong outer location sToken")
	}
	meta := map[string]any{"uId": f.location.UID, "mdmInfo": map[string]string{"id": "mdm", "name": "lab", "metadata": "lab"}}
	if f.foreign {
		meta["mdmInfo"] = map[string]string{"id": "foreign"}
	}
	switch r.URL.Path {
	case "/config":
		meta["notificationUrl"], meta["notificationAuthToken"], meta["subscribedNotificationTypes"] = base+"/callback", "private-callback", []string{"ASSET_MANAGEMENT"}
		if r.Method == "POST" {
			var in appsbooks.ClientConfigurationRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				w.WriteHeader(400)
				return
			}
			f.callback, f.callbackAuth = in.NotificationURL, in.NotificationAuthToken
			f.notify(t, r, "test-config", "TEST_NOTIFICATION", map[string]any{})
		}
		if f.callback != "" {
			meta["notificationUrl"], meta["notificationAuthToken"] = f.callback, f.callbackAuth
		}
	case "/assets", "/users", "/assignments":
		if f.pageFailure {
			http.Error(w, "private-token private-user", 500)
			return
		}
		index, _ := strconv.Atoi(r.URL.Query().Get("pageIndex"))
		meta["currentPageIndex"], meta["versionId"], meta["totalPages"] = index, "version", 1
		if r.URL.Path == "/assets" {
			meta["assets"] = []any{map[string]any{"adamId": "123", "pricingParam": "STDQ", "productType": "App", "deviceAssignable": true, "revocable": true, "availableCount": 10, "supportedPlatforms": []string{"macOS", "iOS", "tvOS", "visionOS"}, "future": nil}}
		}
		if r.URL.Path == "/users" {
			meta["users"] = []any{map[string]string{"clientUserId": "private-user", "status": "Associated"}}
		}
		if r.URL.Path == "/assignments" {
			rows := []appsbooks.Assignment{}
			if f.assigned {
				rows = append(rows, f.assignment)
				if f.duplicate {
					rows = append(rows, rows[0])
				}
			}
			meta["assignments"] = rows
		}
	case "/associate", "/disassociate":
		if r.Method != "POST" {
			t.Error("licensing mutation must POST")
		}
		if f.beforeLicensing != nil {
			if err := f.beforeLicensing(); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
		}
		f.posts++
		f.events++
		f.assigned = r.URL.Path == "/associate"
		var in appsbooks.ManageAssetsRequest
		if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Assets) != 1 || len(in.SerialNumbers)+len(in.ClientUserIDs) != 1 {
			t.Error("invalid fixture licensing request")
			w.WriteHeader(400)
			return
		}
		f.assignment = appsbooks.Assignment{Asset: in.Assets[0]}
		if len(in.SerialNumbers) == 1 {
			f.assignment.SerialNumber = in.SerialNumbers[0]
		} else {
			f.assignment.ClientUserID = in.ClientUserIDs[0]
			f.assignment.UserStatus = "Associated"
		}
		meta["eventId"] = fmt.Sprintf("event-%d", f.events)
		operation := "ASSOCIATE"
		if !f.assigned {
			operation = "DISASSOCIATE"
		}
		f.notify(t, r, fmt.Sprintf("notification-%d", f.events), "ASSET_MANAGEMENT", appsbooks.AssetManagementNotification{EventID: fmt.Sprintf("event-%d", f.events), Result: "SUCCESS", Type: operation, Assignments: []appsbooks.Assignment{f.assignment}})
		if !f.assigned && f.lateReferenceFailure {
			f.notify(t, r, "late-failure", "ASSET_MANAGEMENT", appsbooks.AssetManagementNotification{EventID: "event-2", Result: "FAILURE", Type: "ASSOCIATE", Assignments: []appsbooks.Assignment{f.assignment}})
		}
	case "/status":
		if f.statusFailure {
			http.Error(w, "private-token", 500)
			return
		}
		meta["eventStatus"], meta["eventType"], meta["numRequested"], meta["numCompleted"] = "COMPLETE", "ASSOCIATE", 1, 1
		if !f.assigned {
			meta["eventType"] = "DISASSOCIATE"
		}
		if f.partial {
			meta["numCompleted"] = 0
		}
	default:
		http.NotFound(w, r)
		return
	}
	if f.rewrite != nil {
		f.rewrite(r, meta)
	}
	_ = json.NewEncoder(w).Encode(meta)
}

// notify delivers a signed fixture notification through the configured receiver.
func (f *appsFixture) notify(t *testing.T, r *http.Request, id, kind string, payload any) {
	t.Helper()
	if f.callback == "" || f.callbackClient == nil || f.dropCallback || f.dropCallbackEvent == f.events && kind == "ASSET_MANAGEMENT" {
		return
	}
	b, err := json.Marshal(map[string]any{"notificationId": id, "notificationType": kind, "uId": f.location.UID, "notification": payload, "future": nil})
	if err != nil {
		t.Error(err)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "POST", f.callback, bytes.NewReader(b))
	if err != nil {
		t.Error(err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+f.callbackAuth)
	response, err := f.callbackClient.Do(req)
	if err != nil {
		t.Error(err)
		return
	}
	if err = response.Body.Close(); err != nil {
		t.Error(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("callback did not commit: %d", response.StatusCode)
	}
}

// TestAppsBooksReadOnlyCollectionAndPrivateEvidence collects location facts without mutations and confines raw output privately.
func TestAppsBooksReadOnlyCollectionAndPrivateEvidence(t *testing.T) {
	s, f := appsFixtureSession(t)
	if err := liveAppsBooksCollection(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if f.postCount() != 0 {
		t.Fatal("read-only collection changed licensing")
	}
	appsAssertReadOnlyRequests(t, f)
	if len(adeTestEvidence(t, s, "appsbooks-api-*/assignments-page-*.json")) != 1 {
		t.Fatal("missing assignment source")
	}
	for _, b := range adeTestEvidence(t, s, "appsbooks-api-*/assertions.json") {
		if !strings.Contains(string(b), `"api":"simulated"`) || !strings.Contains(string(b), `"installation":"unexercised"`) {
			t.Fatal("fixture invented live/device evidence", string(b))
		}
	}
	if err := liveAppsBooksCollection(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(s.Evidence, "appsbooks-api-*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("evidence file is not private", err)
		}
		parent, err := os.Stat(filepath.Dir(path))
		if err != nil || parent.Mode().Perm() != 0o700 {
			t.Fatal("evidence directory is not private", err)
		}
	}
}

// TestAppsBooksPrivatePrerequisitesAndSafeErrors rejects invalid private scope without leaking credentials or identifiers.
func TestAppsBooksPrivatePrerequisitesAndSafeErrors(t *testing.T) {
	for _, name := range []string{"missing workspace", "missing scope", "unknown field", "missing token", "invalid token", "missing evidence", "unavailable evidence", "reference missing", "reference denied", "reference malformed", "SDK read failure", "foreign ownership"} {
		t.Run(name, func(t *testing.T) {
			s, f := appsFixtureSession(t)
			switch name {
			case "missing workspace":
				s.Env.Workspace = nil
			case "missing scope":
				if err := os.Remove(s.Env.Workspace.path("appsbooks", "acceptance.json")); err != nil {
					t.Fatal(err)
				}
			case "unknown field":
				writeFixture(t, s.Env.Workspace.path("appsbooks", "acceptance.json"), []byte(`{"secret":"private-user"}`))
			case "missing token":
				if err := os.Remove(s.Env.Workspace.path("appsbooks", "stoken")); err != nil {
					t.Fatal(err)
				}
			case "invalid token":
				writeFixture(t, s.Env.Workspace.path("appsbooks", "stoken"), []byte("private-token"))
			case "missing evidence":
				s.Evidence = ""
			case "unavailable evidence":
				s.Evidence = filepath.Join(s.Env.Workspace.Directory, "appsbooks", "stoken", "child")
			case "reference missing", "reference denied", "reference malformed":
				s.Env.Client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
					code := 404
					if name == "reference denied" {
						code = 403
					}
					if name == "reference malformed" {
						code = 200
					}
					return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("private-token private-user"))}, nil
				})}
			case "SDK read failure":
				f.pageFailure = true
			case "foreign ownership":
				f.foreign = true
			}
			err := liveAppsBooksCollection(t.Context(), s)
			if err == nil || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "private-user") || strings.Contains(err.Error(), s.Env.URL) {
				t.Fatal("unsafe or missing error", err)
			}
			if f.postCount() != 0 {
				t.Fatal("failed preflight mutated Apple")
			}
		})
	}
}

// TestAppsBooksLicensingUncertaintyFenceAndReadback retains uncertain intents and requires exact assignment readback.
func TestAppsBooksLicensingUncertaintyFenceAndReadback(t *testing.T) {
	for _, name := range []string{"opt out", "ambiguous target", "unapproved app", "missing callback", "partial", "unknown outcome", "duplicate readback", "reference submission unavailable", "preexisting fence"} {
		t.Run(name, func(t *testing.T) {
			s, f := appsFixtureSession(t)
			c, err := appsConfig(s)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "opt out":
				c.Mutate = false
			case "ambiguous target":
				c.Serial = "private-serial"
			case "unapproved app":
				c.FreeApp = false
			case "missing callback":
				f.foreign = true
			case "partial":
				f.partial = true
			case "unknown outcome":
				f.statusFailure = true
			case "duplicate readback":
				f.duplicate = true
			case "preexisting fence":
				writeFixture(t, s.Env.Workspace.path("appsbooks", "pending-licensing.json"), []byte(`{"run_id":"old","evidence_directory":"/private/original"}`))
			}
			adeTestJSON(t, s.Env.Workspace.path("appsbooks", "acceptance.json"), c)
			err = liveAppsBooksLicensing(t.Context(), s)
			if err == nil {
				t.Fatal("negative licensing passed")
			}
			if strings.Contains(err.Error(), "private-user") || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "private-serial") {
				t.Fatal("private data in ordinary error", err)
			}
			_, fenceErr := os.Stat(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
			if f.postCount() > 0 && fenceErr != nil {
				t.Fatal("uncertain mutation lost fence")
			}
			if f.postCount() == 0 && name != "preexisting fence" && !errors.Is(fenceErr, os.ErrNotExist) {
				t.Fatal("preflight acquired mutation fence")
			}
			if f.postCount() > 1 {
				t.Fatal("negative run guessed cleanup inverse")
			}
			if name == "partial" || name == "unknown outcome" {
				before, err := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
				if err != nil {
					t.Fatal(err)
				}
				s.Evidence = t.TempDir()
				if err = liveAppsBooksLicensing(t.Context(), s); !errors.Is(err, ErrBlocked) {
					t.Fatal("rerun bypassed fence", err)
				}
				after, err := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
				if err != nil || string(before) != string(after) {
					t.Fatal("rerun overwrote original recovery locator", err)
				}
			}
		})
	}
}

// TestAppsBooksPauseCancellation stops the bounded polling pause on cancellation.
func TestAppsBooksPauseCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(appsPause(ctx), context.Canceled) {
		t.Fatal("wait ignored cancellation")
	}
}

// appsFixtureAsset checks fixture shape before a test changes a native capability.
func appsFixtureAsset(t *testing.T, m map[string]any) ([]any, map[string]any) {
	t.Helper()
	rows, ok := m["assets"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatal("expected one fixture asset")
	}
	asset, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatal("expected a native asset object")
	}
	return rows, asset
}

// appsAssertReadOnlyRequests classifies actions by Apple's endpoint contract;
// read endpoints can use POST, while client configuration changes cannot.
func appsAssertReadOnlyRequests(t *testing.T, f *appsFixture) {
	t.Helper()
	f.mu.Lock()
	requests := append([]string(nil), f.requests...)
	f.mu.Unlock()
	for _, request := range requests {
		method, path, ok := strings.Cut(request, " ")
		if !ok {
			t.Fatal("malformed fixture request record")
		}
		switch path {
		case "/service/config", "/assets", "/users", "/assignments":
		case "/config":
			if method != "GET" {
				t.Fatal("read-only phase changed Apple client configuration")
			}
		default:
			t.Fatal("read-only phase used an Apple mutation action", path)
		}
	}
}
