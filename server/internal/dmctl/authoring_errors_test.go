package dmctl_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/adminclient"
)

func TestAuthoringCommandsRejectInvalidInputBeforeSending(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(srv.Close)
	for _, test := range []struct {
		args  []string
		input string
	}{
		{args: []string{"blueprints"}},
		{args: []string{"blueprints", "unknown"}},
		{args: []string{"blueprints", "get", "-unknown"}},
		{args: []string{"blueprints", "get"}},
		{args: []string{"blueprints", "delete", "apps"}},
		{args: []string{"blueprints", "assign", "apps"}},
		{args: []string{"blueprints", "validate", "extra"}},
		{args: []string{"blueprints", "list", "extra"}},
		{args: []string{"blueprints", "validate"}, input: "{"},
		{args: []string{"blueprints", "validate"}, input: `{"Identifier":"apps","Unknown":true}`},
		{args: []string{"blueprints", "publish"}, input: `{"Identifier":"invalid/name"}`},
		{args: []string{"configuration-profiles"}},
		{args: []string{"configuration-profiles", "unknown"}},
		{args: []string{"configuration-profiles", "get", "-unknown"}},
		{args: []string{"configuration-profiles", "get"}},
		{args: []string{"configuration-profiles", "download", "invalid"}},
		{args: []string{"configuration-profiles", "upload", "extra"}},
		{args: []string{"configuration-profiles", "list", "extra"}},
		{args: []string{"configuration-profiles", "upload"}, input: strings.Repeat("x", configurationprofile.MaxBytes+1)},
	} {
		t.Run(strings.Join(test.args, " ")+test.input[:min(len(test.input), 10)], func(t *testing.T) {
			args := append([]string{"-server", srv.URL, "-token", "operator"}, test.args...)
			_, _, err := runWithStdin(t, noConfig(t), test.input, args...)
			if !errors.Is(err, dmctl.ErrUsage) {
				t.Fatalf("expected input rejection, got %v", err)
			}
		})
	}
	for _, command := range []string{"blueprints", "configuration-profiles"} {
		if _, _, err := run(t, noConfig(t), "-server", ":bad", "-token", "operator", command, "list"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("%s accepted invalid server: %v", command, err)
		}
		operation := "upload"
		if command == "blueprints" {
			operation = "publish"
		}
		if _, _, err := run(t, noConfig(t), "-server", srv.URL, "-token", "operator", command, operation, "-file", filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s lost file error: %v", command, err)
		}
		var out, stderr strings.Builder
		env := noConfig(t)
		err := dmctl.Run(t.Context(), []string{"-server", srv.URL, "-token", "operator", command, operation}, func(k string) string { return env[k] }, failReader{}, &out, &stderr)
		if err == nil || !strings.Contains(err.Error(), "pipe broken") {
			t.Fatalf("%s lost stdin error: %v", command, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid input made %d server requests", calls.Load())
	}
}

func TestAuthoringListingsAndProfileFile(t *testing.T) {
	const profile = "profile\x00bytes"
	var uploaded atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != profile || r.Header.Get("Content-Type") != "application/x-apple-aspen-config" {
				t.Errorf("profile file changed: %q, %v", body, err)
			}
			uploaded.Store(true)
			_, _ = io.WriteString(w, `{"Revision":"uploaded"}`)
		case r.URL.Path == "/admin/v1/blueprints":
			_, _ = io.WriteString(w, `{"Items":[{"Spec":{"Identifier":"engineering"},"Revision":"current"}]}`)
		default:
			_, _ = io.WriteString(w, `{"Items":[{"Revision":"profile-revision","PayloadIdentifier":"com.example.settings"}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	for _, test := range []struct{ command, first, second string }{
		{"blueprints", "engineering", "current"},
		{"configuration-profiles", "profile-revision", "com.example.settings"},
	} {
		out, _, err := run(t, noConfig(t), "-server", srv.URL, "-token", "operator", test.command, "list")
		if err != nil || !strings.Contains(out, test.first) || !strings.Contains(out, test.second) {
			t.Fatalf("%s listing: %q, %v", test.command, out, err)
		}
	}
	file := filepath.Join(t.TempDir(), "app.mobileconfig")
	if err := os.WriteFile(file, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, noConfig(t), "-server", srv.URL, "-token", "operator", "configuration-profiles", "upload", "-file", file); err != nil || !uploaded.Load() {
		t.Fatalf("file upload: %v", err)
	}
}

func TestAuthoringCommandsPropagateServerAndOutputFailures(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
		if status.Load() == http.StatusOK {
			_, _ = io.WriteString(w, "profile bytes")
		}
	}))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{{"blueprints", "get", "apps"}, {"configuration-profiles", "get", strings.Repeat("a", 64)}} {
		_, _, err := run(t, noConfig(t), append([]string{"-server", srv.URL, "-token", "operator"}, args...)...)
		if !errors.Is(err, adminclient.ErrStatus) || !strings.Contains(err.Error(), "503") {
			t.Fatalf("server failure lost: %v", err)
		}
	}
	status.Store(http.StatusNoContent)
	if out, _, err := run(t, noConfig(t), "-server", srv.URL, "-token", "operator", "blueprints", "delete", "-revision", "current", "apps"); err != nil || out != "" {
		t.Fatalf("empty deletion response: %q, %v", out, err)
	}
	status.Store(http.StatusOK)
	env := noConfig(t)
	var stderr strings.Builder
	err := dmctl.Run(t.Context(), []string{"-server", srv.URL, "-token", "operator", "configuration-profiles", "download", strings.Repeat("a", 64)}, func(k string) string { return env[k] }, strings.NewReader(""), &failWriter{}, &stderr)
	if !errors.Is(err, errWrite) {
		t.Fatalf("download output failure lost: %v", err)
	}
}
