package app_test

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func TestContentCacheIngestion(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			cfg := app.Config{Role: app.RoleAll, Storage: backend, AdminToken: "test-admin", ContentCache: app.ContentCacheConfig{PublicURL: "https://cache.example.test"}}
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

func TestContentCacheConfig(t *testing.T) {
	for _, endpoint := range []string{"http://cache.example.test", "https://cache.example.test/path", "https://cache.example.test?token=secret", "https://user:secret@cache.example.test"} {
		_, err := app.Build(t.Context(), app.Config{Role: app.RoleAll, Storage: "inmem", ContentCache: app.ContentCacheConfig{PublicURL: endpoint}})
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe endpoint: %v", err)
		}
	}
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem"})
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "https://cache.example.test/content-cache/metrics/unused", nil))
	if w.Code != 404 {
		t.Fatal("collector enabled by default", w.Code)
	}
}
