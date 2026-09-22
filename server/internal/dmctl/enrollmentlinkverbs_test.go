package dmctl_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

// TestEnrollmentLinkCommands checks enrollment-link requests, human output and usage errors.
func TestEnrollmentLinkCommands(t *testing.T) {
	f := &fakeAdmin{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serve(httptest.NewRecorder(), r)
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ID":"abc","URL":"https://mdm.example/enroll/links/token","ExpiresAt":"2026-09-22T12:00:00Z"}`))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"ID":"abc","State":"revoked"}`))
		default:
			_, _ = w.Write([]byte(`{"Items":[{"ID":"abc","State":"active","DeviceID":"UDID","Identity":"scep","ExpiresAt":"2026-09-22T12:00:00Z"}]}`))
		}
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = srv.URL, "test-token"
	for _, tc := range []struct {
		args                   []string
		method, path, contains string
		output                 []string
	}{
		{
			[]string{"create", "-device-id", "UDID", "-serial", "SERIAL", "-identity", "scep", "-access-rights", "19", "-ttl", "30m"},
			"POST", "/enrollment-links", `"AccessRights":19`,
			[]string{"https://mdm.example/enroll/links/token", "abc"},
		},
		{[]string{"list"}, "GET", "/enrollment-links", "", []string{"STATE", "active", "UDID"}},
		{[]string{"revoke", "-id", "abc"}, "DELETE", "/enrollment-links/abc", "", []string{"revoked"}},
	} {
		out, _, err := run(t, env, append([]string{"enrollment-links"}, tc.args...)...)
		if err != nil {
			t.Fatal(tc.args, err)
		}
		got := f.last()
		if got.method != tc.method || got.path != "/admin/v1"+tc.path || !strings.Contains(got.body, tc.contains) {
			t.Fatal(tc.args, got)
		}
		for _, want := range tc.output {
			if !strings.Contains(out, want) {
				t.Fatalf("%v output lacks %q:\n%s", tc.args, want, out)
			}
		}
	}
	if out, _, err := run(t, env, "-output", "json", "enrollment-links", "create", "-device-id", "UDID"); err != nil || !strings.Contains(out, `"URL"`) {
		t.Fatal(out, err)
	}
	for _, args := range [][]string{nil, {"bogus"}, {"create"}, {"revoke"}, {"list", "trailing"}, {"list", "-invalid"}} {
		if _, _, err := run(t, env, append([]string{"enrollment-links"}, args...)...); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatal(args, err)
		}
	}
}

// TestEnrollmentLinkCommandsReportServerErrors checks that request failures are returned.
func TestEnrollmentLinkCommandsReportServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"Error":"denied"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = srv.URL, "test-token"
	for _, args := range [][]string{{"create", "-device-id", "UDID"}, {"revoke", "-id", "abc"}, {"list"}} {
		if _, _, err := run(t, env, append([]string{"enrollment-links"}, args...)...); err == nil {
			t.Fatal(args, "server error accepted")
		}
	}
	delete(env, "DMCTL_SERVER")
	if _, _, err := run(t, env, "enrollment-links", "list"); err == nil {
		t.Fatal("missing server accepted")
	}
}
