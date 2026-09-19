package dmctl_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

func TestAppIdentityCommands(t *testing.T) {
	const response = `{"items":[{"id":123,"bundleID":"com.example.selected"}],"complete":false,"issues":[{"reason":"review this"}]}`
	for _, test := range []struct {
		name, path, query string
		args              []string
	}{
		{"store-search", "/public-app-store", "country=GB&developer=Example+%26+Co&entity=iPadSoftware&limit=10&term=Example+App", []string{"public-app-store", "search", "-term", "Example App", "-country", "gb", "-entity", "iPadSoftware", "-developer", "Example & Co", "-limit", "10"}},
		{"store-default-limit", "/public-app-store", "country=US&entity=software&term=Example", []string{"public-app-store", "search", "-term", "Example", "-country", "US", "-entity", "software"}},
		{"store-lookup", "/public-app-store/123", "country=GB&entity=macSoftware", []string{"public-app-store", "lookup", "123", "-country", "GB", "-entity", "macSoftware"}},
		{"apple-search", "/apple", "term=Safari", []string{"apple", "search", "-term", "Safari"}},
		{"apple-catalogue", "/apple", "term=", []string{"apple", "search"}},
		{"apple-lookup", "/apple/com.apple.mobilesafari", "", []string{"apple", "lookup", "com.apple.mobilesafari"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/admin/v1/authoring/app-identities"+test.path || r.URL.RawQuery != test.query || r.Header.Get("Authorization") != "Bearer operator" {
					t.Errorf("wrong request: %s %s", r.Method, r.URL)
				}
				_, _ = io.WriteString(w, response)
			}))
			t.Cleanup(srv.Close)
			env := noConfig(t)
			env["DMCTL_SERVER"], env["DMCTL_TOKEN"], env["IDENTITY_TEST_TOKEN"] = srv.URL, "env:IDENTITY_TEST_TOKEN", "operator"
			for _, mode := range []string{"human", "json", "ndjson"} {
				args := append([]string{"-output", mode, "app-identities"}, test.args...)
				out, stderr, err := run(t, env, args...)
				if err != nil || strings.TrimSpace(out) != response {
					t.Fatalf("discovery response changed: %q %q %v", out, stderr, err)
				}
			}
		})
	}
}

func TestAppIdentityArtifactUpload(t *testing.T) {
	const artifact = "PK\x03\x04\x00\xff\x80artifact\nbytes"
	const report = `{"complete":false,"applications":[],"issues":[{"reason":"unsupported candidate"}]}`
	var uploads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != artifact || r.Header.Get("Content-Type") != "application/octet-stream" || r.Header.Get("Authorization") != "Bearer operator" || r.Method != "POST" || r.URL.Path != "/admin/v1/authoring/app-identities/artifacts" {
			t.Errorf("binary upload changed: %q, %s, %s, %v", body, r.Method, r.URL, err)
		}
		uploads.Add(1)
		_, _ = io.WriteString(w, report)
	}))
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "application.dmg")
	if err := os.WriteFile(file, []byte(artifact), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{file, "-"} {
		out, stderr, err := runWithStdin(t, noConfig(t), artifact, "-server", srv.URL, "-token", "operator", "app-identities", "inspect", "-file", source, "-output", "json", "-timeout", "3m")
		if err != nil || strings.TrimSpace(out) != report {
			t.Fatalf("inspection: %q %q %v", out, stderr, err)
		}
	}
	if uploads.Load() != 2 {
		t.Fatal("missing upload", uploads.Load())
	}
}

func TestAppIdentityInputAndHelp(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(srv.Close)
	store := []string{"public-app-store", "-country", "GB", "-entity", "software"}
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"-invalid"},
		{"inspect"},
		{"inspect", "-file", "-", "extra"},
		{"inspect", "-invalid"},
		{"apple"},
		{"apple", "unknown"},
		{"apple", "search", "extra"},
		{"apple", "lookup"},
		{"apple", "lookup", ""},
		{"apple", "lookup", "com.apple.x", "-term", "x"},
		{"apple", "search", "-all"},
		{"apple", "search", "-limit", "10"},
		{"apple", "search", "-invalid"},
		{"public-app-store", "search", "-term", "x"},
		{"public-app-store", "search", "-country", "UK!", "-entity", "software", "-term", "x"},
		{"public-app-store", "search", "-country", "GB", "-entity", "invalid", "-term", "x"},
		append(append([]string{}, store...), "search"),
		append(append([]string{}, store...), "search", "-term", " "),
		append(append([]string{}, store...), "search", "-term", "x", "-limit", "-1"),
		append(append([]string{}, store...), "search", "-term", "x", "-limit", "201"),
		append(append([]string{}, store...), "lookup", "0"),
		append(append([]string{}, store...), "lookup", "invalid"),
		append(append([]string{}, store...), "lookup", "123", "-developer", "ignored"),
		append(append([]string{}, store...), "lookup", "123", "-limit", "10"),
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, err := run(t, noConfig(t), append([]string{"-server", srv.URL, "app-identities"}, args...)...)
			if !errors.Is(err, dmctl.ErrUsage) {
				t.Fatalf("invalid input accepted: %v", err)
			}
		})
	}
	for _, args := range [][]string{{"-h"}, {"apple", "-h"}, {"public-app-store", "search", "-h"}, {"inspect", "-file", "missing", "-h"}} {
		out, help, err := run(t, noConfig(t), append([]string{"-server", srv.URL, "app-identities"}, args...)...)
		if err != nil || out != "" || !strings.Contains(help, "Usage of") {
			t.Fatalf("help performed an operation: %q %q %v", out, help, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid input or help contacted the server")
	}
}

func TestAppIdentityFailures(t *testing.T) {
	for _, operation := range [][]string{{"apple", "search"}, {"inspect", "-file", "-"}} {
		args := append([]string{"app-identities"}, operation...)
		if _, _, err := run(t, noConfig(t), append([]string{"-server", ":bad"}, args...)...); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatal("invalid connection accepted", err)
		}
		for _, code := range []int{401, 403, 413, 415, 429, 503, 504} {
			t.Run(strings.Join(operation, " ")+http.StatusText(code), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(code)
					_, _ = io.WriteString(w, `{"Error":"inspection refused"}`)
				}))
				t.Cleanup(srv.Close)
				_, _, err := run(t, noConfig(t), append([]string{"-server", srv.URL}, args...)...)
				wantExit := dmctl.ExitFailed
				if code == 401 || code == 403 {
					wantExit = dmctl.ExitAuth
				}
				if err == nil || dmctl.ExitCode(err) != wantExit || !strings.Contains(err.Error(), "inspection refused") {
					t.Fatal("server error lost", err)
				}
			})
		}
	}
	if _, _, err := run(t, noConfig(t), "app-identities", "inspect", "-file", filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file error lost", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	var stdout, stderr strings.Builder
	err := dmctl.Run(t.Context(), []string{"-server", srv.URL, "app-identities", "inspect", "-file", "-"}, func(string) string { return "" }, failReader{}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "pipe broken") {
		t.Fatal("input stream error lost", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = dmctl.Run(ctx, []string{"-server", srv.URL, "app-identities", "inspect", "-file", "-"}, func(string) string { return "" }, strings.NewReader("artifact"), &stdout, &stderr)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	for _, args := range [][]string{{"apple", "search"}, {"inspect", "-file", "-"}} {
		err := dmctl.Run(t.Context(), append([]string{"-server", srv.URL, "app-identities"}, args...), func(string) string { return "" }, strings.NewReader("artifact"), &failWriter{}, &stderr)
		if !errors.Is(err, errWrite) {
			t.Fatal("output failure lost", err)
		}
	}
}
