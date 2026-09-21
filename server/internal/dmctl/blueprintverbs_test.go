package dmctl_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

// TestBlueprintCLI checks blueprint CLI requests and preservation of configuration-profile bytes.
func TestBlueprintCLI(t *testing.T) {
	var method, path, match, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, match = r.Method, r.URL.RequestURI(), r.Header.Get("If-Match")
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Items":[],"Revision":"current"}`)
	}))
	defer srv.Close()
	for _, test := range []struct {
		args                       []string
		input, method, path, match string
	}{
		{[]string{"blueprints", "validate"}, `{"Identifier":"test"}`, "POST", "/admin/v1/blueprints/validate", ""},
		{[]string{"blueprints", "publish", "-revision", "current"}, `{"Identifier":"test"}`, "PUT", "/admin/v1/blueprints/test", `"current"`},
		{[]string{"blueprints", "get", "test"}, "", "GET", "/admin/v1/blueprints/test", ""},
		{[]string{"blueprints", "list"}, "", "GET", "/admin/v1/blueprints", ""},
		{[]string{"blueprints", "delete", "-revision", "current", "test"}, "", "DELETE", "/admin/v1/blueprints/test", `"current"`},
		{[]string{"blueprints", "assign", "-channel", "user", "-parent", "device", "test", "user"}, "", "PUT", "/admin/v1/enrollments/user/user/blueprints/test?parent=device", ""},
		{[]string{"blueprints", "unassign", "test", "device"}, "", "DELETE", "/admin/v1/enrollments/device/device/blueprints/test", ""},
		{[]string{"configuration-profiles", "upload"}, "binary\x00profile", "POST", "/admin/v1/configuration-profiles", ""},
		{[]string{"configuration-profiles", "get", strings.Repeat("a", 64)}, "", "GET", "/admin/v1/configuration-profiles/" + strings.Repeat("a", 64), ""},
		{[]string{"configuration-profiles", "download", strings.Repeat("a", 64)}, "", "GET", "/admin/v1/configuration-profiles/" + strings.Repeat("a", 64) + "/content", ""},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			args := append([]string{"-server", srv.URL, "-token", "operator"}, test.args...)
			_, stderr, err := runWithStdin(t, noConfig(t), test.input, args...)
			if err != nil {
				t.Fatal(err, stderr)
			}
			if method != test.method || path != test.path || match != test.match {
				t.Fatal(method, path, match)
			}
			if test.method == "POST" && strings.Contains(test.path, "configuration-profiles") && body != test.input {
				t.Fatal("profile bytes changed")
			}
		})
	}
}

// TestBlueprintValidationTarget checks target validation, capability forwarding, and rejection of
// invalid targets before contacting the server.
func TestBlueprintValidationTarget(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if r.Method != "POST" || r.URL.Path != "/admin/v1/blueprints/validate" || q.Get("os") != "macOS" || q.Get("version") != "28.0" || q.Get("channel") != "user" {
			t.Errorf("target lost: %s %s", r.Method, r.URL)
		}
		for _, flag := range []string{"supervised", "sharedIPad", "dep", "userEnrollment", "userApproved"} {
			if q.Get(flag) != "true" {
				t.Errorf("capability %s lost", flag)
			}
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	for _, target := range []string{"macos", "macos:27", "macos:invalid,channel=device", "macos:27,channel=other", "unknown:27,channel=device", "macos:27,channel=device,unknown"} {
		_, _, err := runWithStdin(t, noConfig(t), `{"Identifier":"apps"}`, "-server", srv.URL, "blueprints", "validate", "-target", target)
		if !errors.Is(err, dmctl.ErrUsage) {
			t.Fatal("invalid target accepted", target, err)
		}
	}
	if _, _, err := runWithStdin(t, noConfig(t), `{"Identifier":"apps"}`, "-server", srv.URL, "blueprints", "publish", "-target", "macos:27,channel=device"); !errors.Is(err, dmctl.ErrUsage) {
		t.Fatal("publication silently ignored target", err)
	}
	if calls != 0 {
		t.Fatal("invalid target contacted server")
	}
	// A future version must be passed through to schema-backed server validation.
	_, _, err := runWithStdin(t, noConfig(t), `{"Identifier":"apps"}`, "-server", srv.URL, "blueprints", "validate", "-target", "macos:28.0,channel=user,supervised,shared-ipad,dep,user-enrollment,user-approved")
	if err != nil || calls != 1 {
		t.Fatal("validation failed", calls, err)
	}
}
