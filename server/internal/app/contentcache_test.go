package app_test

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestContentCacheIngestion checks authenticated content-cache ingestion, report paging,
// credential persistence, rotation, and revocation.
func TestContentCacheIngestion(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			cfg := app.Config{Storage: backend, BootstrapToken: "test-admin", ContentCache: app.ContentCacheConfig{PublicURL: "https://cache.example.test"}}
			if backend == "sqlite" {
				cfg.DSN = filepath.Join(t.TempDir(), "cache.db")
			}
			a := build(t, cfg)
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "cache-device"}
			if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, Device: storage.DeviceInfo{ProductName: "Mac16,1", OSVersion: "27.0"}}}); err != nil {
				t.Fatal(err)
			}
			request := func(method, path, token string, body []byte) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				if token != "" {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				a.Handler.ServeHTTP(w, r)
				return w
			}
			base := "https://cache.example.test/admin/v1/enrollments/device/cache-device/content-cache/"
			if w := request("POST", base+"credential", "", nil); w.Code != 401 {
				t.Fatal("unauthed issue", w.Code)
			}
			issue := func() string {
				w := request("POST", base+"credential", "test-admin", nil)
				var response struct{ URL string }
				if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &response) != nil || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("issue: %d %s", w.Code, w.Body.String())
				}
				return response.URL
			}
			credentialURL := issue()
			body := []byte(`{"version":1,"reportDate":"2026-09-16T12:00:00Z","hostname":"untrusted","hardware":"Mac16,1","serverGUID":"13D4D110-B2B7-4F26-8E25-CD22E58C00EE"}`)
			for _, method := range []string{"PUT", "POST"} {
				if w := request(method, credentialURL, "", body); w.Code != 202 {
					t.Fatalf("ingestion: %d %s", w.Code, w.Body.String())
				}
			}
			if w := request("PUT", strings.Replace(credentialURL, "https:", "http:", 1), "", body); w.Code != 401 {
				t.Fatal("cleartext accepted", w.Code)
			}
			if w := request("PUT", credentialURL, "", []byte(`{}`)); w.Code != 400 {
				t.Fatal("invalid report accepted", w.Code)
			}
			if w := request("GET", credentialURL, "", nil); w.Code != 405 {
				t.Fatal("wrong method", w.Code)
			}
			if w := request("GET", base+"reports", "", nil); w.Code != 401 {
				t.Fatal("unauthenticated reports", w.Code)
			}
			var page paging.Result[contentcache.StoredReport]
			w := request("GET", base+"reports?limit=1", "test-admin", nil)
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Enrollment != id || page.NextCursor == "" {
				t.Fatalf("reports: %d %s", w.Code, w.Body.String())
			}
			w = request("GET", base+"reports?limit=1&cursor="+url.QueryEscape(page.NextCursor), "test-admin", nil)
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.NextCursor != "" {
				t.Fatalf("next reports: %d %s", w.Code, w.Body.String())
			}
			if backend == "sqlite" {
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				a = build(t, cfg)
				if w := request("PUT", credentialURL, "", body); w.Code != 202 {
					t.Fatal("credential did not survive restart", w.Code)
				}
			}
			newURL := issue()
			if w := request("PUT", credentialURL, "", body); w.Code != 401 {
				t.Fatal("old credential accepted", w.Code)
			}
			if w := request("DELETE", base+"credential", "test-admin", nil); w.Code != 204 {
				t.Fatal("revoke", w.Code)
			}
			if w := request("PUT", newURL, "", body); w.Code != 401 {
				t.Fatal("revoked credential accepted", w.Code)
			}
		})
	}
}

// TestContentCacheConfig checks that the content-cache collector is disabled by default and
// rejects unsafe endpoints.
func TestContentCacheConfig(t *testing.T) {
	for _, endpoint := range []string{"http://cache.example.test", "https://cache.example.test/path", "https://cache.example.test?token=secret", "https://user:secret@cache.example.test"} {
		_, err := app.Build(t.Context(), app.Config{Storage: "inmem", ContentCache: app.ContentCacheConfig{PublicURL: endpoint}})
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe endpoint: %v", err)
		}
	}
	a := build(t, app.Config{Storage: "inmem"})
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "https://cache.example.test/content-cache/metrics/unused", nil))
	if w.Code != 404 {
		t.Fatal("collector enabled by default", w.Code)
	}
}

// TestContentCacheRetentionEnv checks content cache retention env.
func TestContentCacheRetentionEnv(t *testing.T) {
	for _, value := range []string{"", "48h", "0", "-1h", "invalid"} {
		t.Run(value, func(t *testing.T) {
			cfg, err := app.ParseEnv(func(key string) string {
				switch key {
				case app.EnvStorage:
					return "inmem"
				case "DM_CONTENT_CACHE_URL":
					return "https://cache.example.test"
				case "DM_CONTENT_CACHE_RETENTION":
					return value
				default:
					return ""
				}
			})
			if value != "" && value != "48h" {
				if !errors.Is(err, app.ErrConfig) {
					t.Fatalf("unsafe retention accepted: %v", err)
				}
				return
			}
			want := time.Duration(0) // The collector applies its default retention.
			if value == "48h" {
				want = 48 * time.Hour
			}
			if err != nil || cfg.ContentCache.PublicURL != "https://cache.example.test" || cfg.ContentCache.Retention != want {
				t.Fatalf("content-cache environment: %+v, %v", cfg.ContentCache, err)
			}
		})
	}
}

// A forwarded HTTPS assertion is authoritative only when the actual socket
// peer is trusted. URL credentials must not be accepted in alternate routes.
func TestContentCacheProxyTransport(t *testing.T) {
	a := build(t, app.Config{
		Storage: "inmem", BootstrapToken: "test-admin",
		ContentCache:   app.ContentCacheConfig{PublicURL: "https://cache.example.test"},
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8::/32")},
	})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "proxy-device"}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://cache.example.test/admin/v1/enrollments/device/proxy-device/content-cache/credential", nil)
	r.Header.Set("Authorization", "Bearer test-admin")
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	var credential struct{ URL string }
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &credential) != nil || credential.URL == "" {
		t.Fatalf("issue credential: %d %s", w.Code, w.Body.String())
	}
	cleartextURL := strings.Replace(credential.URL, "https:", "http:", 1)
	for _, tc := range []struct {
		name, remote, proto, suffix string
		want                        int
	}{
		{"trusted IPv4", "192.0.2.5:1234", "https", "", http.StatusAccepted},
		{"trusted mapped IPv4", "[::ffff:192.0.2.5]:1234", "https", "", http.StatusAccepted},
		{"trusted IPv6", "[2001:db8::5]:1234", "https", "", http.StatusAccepted},
		{"untrusted peer", "198.51.100.5:1234", "https", "", http.StatusUnauthorized},
		{"unparseable peer", "not-an-address", "https", "", http.StatusUnauthorized},
		{"hostname peer", "proxy.example.test:1234", "https", "", http.StatusUnauthorized},
		{"missing assertion", "192.0.2.5:1234", "", "", http.StatusUnauthorized},
		{"forwarded HTTP", "192.0.2.5:1234", "http", "", http.StatusUnauthorized},
		{"ambiguous assertion", "192.0.2.5:1234", "https, http", "", http.StatusUnauthorized},
		{"query credential", "192.0.2.5:1234", "https", "?extra=secret", http.StatusNotFound},
		{"nested credential", "192.0.2.5:1234", "https", "/extra", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, cleartextURL+tc.suffix,
				strings.NewReader(`{"version":1,"reportDate":"2026-09-17T12:00:00Z","hostname":"untrusted","hardware":"Mac16,1","serverGUID":"13D4D110-B2B7-4F26-8E25-CD22E58C00EE"}`))
			r.RemoteAddr = tc.remote
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			// An attacker-supplied forwarded address must not confer proxy trust.
			r.Header.Set("X-Forwarded-For", "192.0.2.5")
			w := httptest.NewRecorder()
			a.Handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("ingestion status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if err := a.Store.Disable(t.Context(), id, time.Now()); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequestWithContext(t.Context(), http.MethodPost, credential.URL, strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("disabled enrollment credential accepted: %d %s", w.Code, w.Body.String())
	}
}

// TestContentCacheAdminRejectsInvalidRequests checks that content cache admin rejects invalid
// requests.
func TestContentCacheAdminRejectsInvalidRequests(t *testing.T) {
	a := build(t, app.Config{
		Storage: "inmem", BootstrapToken: "test-admin",
		ContentCache: app.ContentCacheConfig{PublicURL: "https://cache.example.test"},
	})
	for _, id := range []mdm.EnrollmentID{
		{Channel: mdm.ChannelDevice, ID: "device-id"},
		{Channel: mdm.ChannelUser, ID: "user-id", ParentID: "device-id"},
	} {
		if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "invalid/device/content-cache/credential"},
		{http.MethodDelete, "invalid/device/content-cache/credential"},
		{http.MethodGet, "invalid/device/content-cache/reports"},
		{http.MethodPost, "user/user-id/content-cache/credential?parent=device-id"},
		{http.MethodDelete, "user/user-id/content-cache/credential?parent=device-id"},
		{http.MethodGet, "user/user-id/content-cache/reports?parent=device-id"},
		{http.MethodGet, "device/device-id/content-cache/reports?limit=not-a-number"},
		{http.MethodGet, "device/device-id/content-cache/reports?limit=1001"},
		{http.MethodGet, "device/device-id/content-cache/reports?cursor=invalid"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), tc.method, "https://cache.example.test/admin/v1/enrollments/"+tc.path, nil)
			r.Header.Set("Authorization", "Bearer test-admin")
			w := httptest.NewRecorder()
			a.Handler.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid request = %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
