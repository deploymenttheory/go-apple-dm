package app_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appartifact"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

type identityTransport func(*http.Request) (*http.Response, error)

func (f identityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func identityRequest(t *testing.T, a *app.App, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "https://mdm.example/admin/v1"+path, body)
	r.Header.Set("Authorization", "Bearer admin")
	r.Header.Set("Content-Type", "application/octet-stream")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	return w
}

func TestApplicationIdentityAuthorization(t *testing.T) {
	a, manager, _ := policyApp(t, nil)
	token := mintPrincipal(t, manager, adminauth.Principal{Name: "author", Roles: []string{"author"}})
	prefix := "/authoring/app-identities"
	for _, route := range []string{"/apple", "/public-app-store", "/artifacts"} {
		method := "GET"
		if route == "/artifacts" {
			method = "POST"
		}
		for _, auth := range []string{"", "Bearer " + token} {
			w := identityRequest(t, a, method, prefix+route, nil, map[string]string{"Authorization": auth})
			if w.Code != 401 && w.Code != 403 {
				t.Fatal("discovery bypassed authorization", w.Code)
			}
		}
	}
	_, err := manager.PutPolicy(t.Context(), adminauth.Root, adminauth.Policy{Name: "identity-authors", Source: `permit(principal in MDM::Role::"author", action == MDM::Action::"discoverApplicationIdentities", resource);`})
	if err != nil {
		t.Fatal(err)
	}
	if w := identityRequest(t, a, "GET", prefix+"/apple", nil, map[string]string{"Authorization": "Bearer " + token}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := identityRequest(t, a, "PUT", "/blueprints/restricted", strings.NewReader(`{"Identifier":"restricted"}`), map[string]string{"Authorization": "Bearer " + token}); w.Code != 403 {
		t.Fatal("discovery grants publication", w.Code)
	}
}

type heldIdentityBody struct {
	entered chan struct{}
	release chan struct{}
}

func (b heldIdentityBody) Read([]byte) (int, error) { close(b.entered); <-b.release; return 0, io.EOF }

func TestApplicationIdentityConcurrentUpload(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", AdminToken: "admin"})
	body := heldIdentityBody{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- identityRequest(t, a, "POST", "/authoring/app-identities/artifacts", body, nil) }()
	select {
	case <-body.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not start")
	}
	w := identityRequest(t, a, "POST", "/authoring/app-identities/artifacts", strings.NewReader("another"), nil)
	close(body.release)
	if first := <-done; first.Code != 400 {
		t.Fatal(first.Code)
	}
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatal(w.Code, w.Header())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequestWithContext(ctx, "POST", "https://mdm.example/admin/v1/authoring/app-identities/artifacts", strings.NewReader("test"))
	r.Header.Set("Authorization", "Bearer admin")
	r.Header.Set("Content-Type", "application/octet-stream")
	w = httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	if w.Code != 408 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestApplicationIdentityInvalidConfig(t *testing.T) {
	if _, err := app.Build(t.Context(), app.Config{Role: app.RoleAll, Storage: "inmem", ApplicationIdentities: app.ApplicationIdentityConfig{Artifacts: appartifact.Options{MaxBytes: -1}}}); !errors.Is(err, app.ErrConfig) {
		t.Fatal(err)
	}
}

func TestApplicationIdentityAuthoring(t *testing.T) {
	var unavailable atomic.Bool
	client := &publicappstoreidentity.Client{HTTPClient: &http.Client{Transport: identityTransport(func(r *http.Request) (*http.Response, error) {
		if unavailable.Load() {
			return nil, errors.New("store offline")
		}
		if r.URL.Query().Get("country") != "GB" {
			t.Errorf("storefront lost: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"resultCount":1,"results":[{"trackId":123,"bundleId":"com.example.app","trackName":"Example","artistName":"Example Inc","kind":"software"}]}`))}, nil
	})}}
	temp := t.TempDir()
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", AdminToken: "admin", ApplicationIdentities: app.ApplicationIdentityConfig{PublicAppStore: client, Artifacts: appartifact.Options{TempDir: temp}}})
	prefix := "/authoring/app-identities"
	for _, route := range []string{"/public-app-store?term=Example&developer=Example&country=GB&entity=software&limit=10", "/public-app-store/123?country=GB&entity=software", "/apple?term=Safari", "/apple/com.apple.mobilesafari"} {
		w := identityRequest(t, a, "GET", prefix+route, nil, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.HasPrefix(route, "/apple") && !strings.Contains(w.Body.String(), "reviewedOn") {
			t.Fatal("missing catalogue provenance")
		}
	}
	fixture, err := os.ReadFile("../../../devicemanagement/utility/appidentity/testdata/fixture.macho")
	if err != nil {
		t.Fatal(err)
	}
	w := identityRequest(t, a, "POST", prefix+"/artifacts", bytes.NewReader(fixture), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var report appartifact.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Complete || len(report.Applications) != 1 {
		t.Fatal(report)
	}
	payload := &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{}}
	for _, architecture := range report.Applications[0].Identity.Architectures {
		payload.Allowed.DeniedBinaries = append(payload.Allowed.DeniedBinaries, ddm.AppSettingsAllowedDeniedBinaries{CDHash: new(architecture.CDHash)})
	}
	declaration, err := blueprint.NewDeclaration("applications", payload)
	if err != nil {
		t.Fatal(err)
	}
	spec := blueprint.Spec{Identifier: "identity-authoring", Declarations: []blueprint.Declaration{declaration}}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query  string
		status int
	}{{"", 200}, {"?os=macOS&version=27&channel=device&supervised=true", 200}, {"?os=macOS&version=26&channel=device&supervised=true", 400}} {
		w = identityRequest(t, a, "POST", "/blueprints/validate"+check.query, bytes.NewReader(raw), nil)
		if w.Code != check.status {
			t.Fatal(check, w.Code, w.Body.String())
		}
	}
	unavailable.Store(true)
	if w := identityRequest(t, a, "GET", prefix+"/public-app-store?term=Example&country=GB&entity=software", nil, nil); w.Code != 502 {
		t.Fatal(w.Code)
	}
	w = identityRequest(t, a, "PUT", "/blueprints/identity-authoring", bytes.NewReader(raw), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var record blueprints.Record
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	w = identityRequest(t, a, "PUT", "/blueprints/identity-authoring", bytes.NewReader(raw), map[string]string{"If-Match": `"` + record.Revision + `"`})
	if w.Code != 200 || w.Header().Get("ETag") != `"`+record.Revision+`"` {
		t.Fatal("publication depends on discovery", w.Code, w.Body.String())
	}
	w = identityRequest(t, a, "GET", "/blueprints/identity-authoring", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "CDHash") {
		t.Fatal(w.Code, w.Body.String())
	}
	files, err := os.ReadDir(temp)
	if err != nil || len(files) != 0 {
		t.Fatal("uploaded bytes retained", files, err)
	}
}

func TestIdentityInputsAndUpstreamFailures(t *testing.T) {
	var mode atomic.Int32
	client := &publicappstoreidentity.Client{HTTPClient: &http.Client{Transport: identityTransport(func(r *http.Request) (*http.Response, error) {
		status, body := 200, `{"resultCount":0,"results":[]}`
		headers := make(http.Header)
		switch mode.Load() {
		case 1:
			status = 429
			headers.Set("Retry-After", "30")
		case 2:
			body = "bad"
		case 3:
			return nil, context.DeadlineExceeded
		case 4:
			return nil, context.Canceled
		case 5:
			status = 503
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", AdminToken: "admin", ApplicationIdentities: app.ApplicationIdentityConfig{PublicAppStore: client}})
	prefix := "/authoring/app-identities"
	for _, test := range []struct {
		path string
		want int
	}{{"/public-app-store", 400}, {"/public-app-store?term=x&country=GB&entity=software&limit=invalid", 400}, {"/public-app-store?limit=0", 400}, {"/public-app-store/nope", 400}, {"/public-app-store/-1?country=GB&entity=software", 400}, {"/public-app-store/123?country=GB&entity=software", 404}, {"/public-app-store?term=x&country=GB&entity=software", 200}, {"/apple/missing", 404}, {"/apple?term=nonexistentapp", 200}, {"/public-app-store?term=" + strings.Repeat("a", 4097), 400}, {"/apple?term=" + strings.Repeat("a", 4097), 400}} {
		if w := identityRequest(t, a, "GET", prefix+test.path, nil, nil); w.Code != test.want {
			t.Fatal(test, w.Code, w.Body.String())
		}
	}
	for n, want := range []int{200, 429, 502, 504, 408, 502} {
		mode.Store(int32(n))
		w := identityRequest(t, a, "GET", prefix+"/public-app-store?term=x&country=GB&entity=software", nil, nil)
		if w.Code != want {
			t.Fatal(n, w.Code, w.Body.String())
		}
		if n == 1 && w.Header().Get("Retry-After") != "30" {
			t.Fatal("lost backoff")
		}
	}
	for _, q := range []string{"?os=invalid&version=27&channel=device", "?os=macOS&channel=device", "?os=macOS&version=0&channel=device", "?os=macOS&version=27&channel=bad", "?os=macOS&version=27&channel=device&supervised=bad", "?os=macOS&version=27&channel=device&other=true", "?os=macOS&os=iOS&version=27&channel=device"} {
		if w := identityRequest(t, a, "POST", "/blueprints/validate"+q, strings.NewReader(`{"Identifier":"test"}`), nil); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
}

type identityBodyFailure struct{}

func (identityBodyFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestArtifactUploadFailures(t *testing.T) {
	fixture, err := os.ReadFile("../../../devicemanagement/utility/appidentity/testdata/fixture.macho")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		opts   appartifact.Options
		body   io.Reader
		media  string
		status int
	}{
		{"media", appartifact.Options{}, bytes.NewReader(fixture), "application/json", 415},
		{"large", appartifact.Options{MaxBytes: 5}, bytes.NewReader(fixture), "application/octet-stream", 413},
		{"stream-large", appartifact.Options{MaxBytes: 5}, io.LimitReader(bytes.NewReader(fixture), 10), "application/octet-stream", 413},
		{"scratch", appartifact.Options{TempDir: filepath.Join(t.TempDir(), "missing")}, bytes.NewReader(fixture), "application/octet-stream", 500},
		{"read", appartifact.Options{}, identityBodyFailure{}, "application/octet-stream", 400},
		{"unsupported", appartifact.Options{}, strings.NewReader("unsupported file"), "application/octet-stream", 415},
		{"invalid", appartifact.Options{}, strings.NewReader("xar!bad"), "application/octet-stream", 400},
		{"deadline", appartifact.Options{Timeout: time.Nanosecond}, bytes.NewReader(fixture), "application/octet-stream", 504},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", AdminToken: "admin", ApplicationIdentities: app.ApplicationIdentityConfig{Artifacts: test.opts}})
			w := identityRequest(t, a, "POST", "/authoring/app-identities/artifacts", test.body, map[string]string{"Content-Type": test.media})
			if w.Code != test.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "dm-upload-") || strings.Contains(w.Body.String(), test.opts.TempDir) && test.opts.TempDir != "" {
				t.Fatal("private path exposed")
			}
		})
	}
}
