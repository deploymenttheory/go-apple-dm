package app

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
)

// TestPackageByteRanges verifies complete, bounded, suffix and invalid requests.
func TestPackageByteRanges(t *testing.T) {
	for _, tc := range []struct {
		raw                  string
		size, offset, length int64
		bad                  bool
	}{
		{"", 10, 0, 10, false},
		{"bytes=0-0", 10, 0, 1, false},
		{"bytes=2-", 10, 2, 8, false},
		{"bytes=3-100", 10, 3, 7, false},
		{"bytes=-3", 10, 7, 3, false},
		{"bytes=-100", 10, 0, 10, false},
		{"items=1-2", 10, 0, 0, true},
		{"bytes=0-1,3-4", 10, 0, 0, true},
		{"bytes=1", 10, 0, 0, true},
		{"bytes=0-1", 0, 0, 0, true},
		{"bytes=-0", 10, 0, 0, true},
		{"bytes=-x", 10, 0, 0, true},
		{"bytes=x-", 10, 0, 0, true},
		{"bytes=10-", 10, 0, 0, true},
		{"bytes=4-3", 10, 0, 0, true},
		{"bytes=0-x", 10, 0, 0, true},
		{"bytes=0-9223372036854775808", 10, 0, 0, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			offset, length, err := packageRange(tc.raw, tc.size)
			if (err != nil) != tc.bad || offset != tc.offset && !tc.bad || length != tc.length && !tc.bad {
				t.Fatalf("range = %d,%d,%v", offset, length, err)
			}
		})
	}
}

// TestServePackageHTTP verifies range headers, conditional requests and HEAD avoid
// unnecessary backend reads while backend faults cannot report successful content.
func TestServePackageHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, method, rangeHeader, ifRange, ifNone, body string
		status                                           int
		opens                                            bool
	}{
		{"full", "GET", "", "", "", "0123456789", 200, true},
		{"bounded", "GET", "bytes=2-4", "", "", "234", 206, true},
		{"suffix", "GET", "bytes=-2", "", "", "89", 206, true},
		{"head", "HEAD", "", "", "", "", 200, false},
		{"head range", "HEAD", "bytes=2-4", "", "", "", 206, false},
		{"unsatisfied", "GET", "bytes=20-", "", "", "", 416, false},
		{"etag", "GET", "", "", `"digest"`, "", 304, false},
		{"wildcard", "GET", "", "", "*", "", 304, false},
		{"if range match", "GET", "bytes=2-4", `"digest"`, "", "234", 206, true},
		{"if range changed", "GET", "bytes=2-4", `"other"`, "", "0123456789", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), tc.method, "https://example.test/pkg", nil)
			r.Header.Set("Range", tc.rangeHeader)
			r.Header.Set("If-Range", tc.ifRange)
			r.Header.Set("If-None-Match", tc.ifNone)
			w := httptest.NewRecorder()
			opened := false
			servePackage(w, r, applications.Content{Size: 10, Digests: applications.Digests{SHA256: "digest"}}, func(offset, length int64) (io.ReadCloser, error) {
				opened = true
				return io.NopCloser(strings.NewReader("0123456789"[offset : offset+length])), nil
			})
			if w.Code != tc.status || w.Body.String() != tc.body || opened != tc.opens {
				t.Fatalf("status=%d body=%q opened=%t", w.Code, w.Body.String(), opened)
			}
			if w.Header().Get("ETag") != `"digest"` || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing integrity headers")
			}
			if tc.status == 416 && w.Header().Get("Content-Range") != "bytes */10" {
				t.Fatal("missing unsatisfied size")
			}
			if tc.status == 206 && w.Header().Get("Content-Range") == "" {
				t.Fatal("missing range")
			}
		})
	}
	t.Run("unavailable", func(t *testing.T) {
		w := httptest.NewRecorder()
		servePackage(w, httptest.NewRequestWithContext(t.Context(), "GET", "https://example.test/pkg", nil), applications.Content{Size: 10}, func(int64, int64) (io.ReadCloser, error) { return nil, io.ErrUnexpectedEOF })
		if w.Code != 503 {
			t.Fatal(w.Code)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		defer func() {
			if err := recover(); !errors.Is(asPackageError(err), http.ErrAbortHandler) {
				t.Fatal("truncated transfer not aborted", err)
			}
		}()
		servePackage(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", "https://example.test/pkg", nil), applications.Content{Size: 10}, func(int64, int64) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("short")), nil })
	})
}

// asPackageError converts a recovered handler panic for the truncation assertion.
func asPackageError(v any) error { err, _ := v.(error); return err }

// TestPackageCapabilityRedaction prevents bearer material reaching downstream
// logs and rejects ambiguous download routes before any observer runs.
func TestPackageCapabilityRedaction(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/applications/download/private-token/package.pkg", 204},
		{"/applications/download/private-token/manifest.plist", 204},
		{"/applications/download/private-token/package.pkg?query=1", 404},
		{"/applications/download/private-token/extra/package.pkg", 404},
		{"/applications/download//package.pkg", 404},
		{"/applications/download/private-token", 404},
		{"/healthz", 204},
	} {
		t.Run(tc.path, func(t *testing.T) {
			observed := false
			handler := redactPackageURL(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observed = true
				if strings.Contains(r.URL.String()+r.RequestURI, "private-token") {
					t.Fatal("download token reached observer")
				}
				if tc.path != "/healthz" && r.Context().Value(packageTokenKey{}) != "private-token" {
					t.Fatal("lost capability")
				}
				w.WriteHeader(204)
			}))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "https://example.test"+tc.path, nil))
			if w.Code != tc.status || observed != (tc.status == 204) {
				t.Fatal(w.Code, observed)
			}
		})
	}
}
