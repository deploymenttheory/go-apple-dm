package dmctl_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

// TestPersistentEventCommands checks persistent-event commands and rejects unavailable or
// unconfigured servers.
func TestPersistentEventCommands(t *testing.T) {
	f := &fakeAdmin{}
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serve(httptest.NewRecorder(), r)
		w.WriteHeader(status)
		if status != 204 {
			_, _ = w.Write([]byte(`{"records":1}`))
		}
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = srv.URL, "test-token"
	for _, tc := range []struct {
		args               []string
		method, path, body string
	}{
		{[]string{"status"}, "GET", "/admin/v1/events/status", ""},
		{[]string{"list", "-type", "enrolled", "-after-event", "previous", "-limit", "5"}, "GET", "/admin/v1/events", ""},
		{[]string{"list", "-deliveries"}, "GET", "/admin/v1/events/deliveries", ""},
		{[]string{"list", "-state", "blocked"}, "GET", "/admin/v1/events/deliveries", ""},
		{[]string{"list", "-event-id", "one"}, "GET", "/admin/v1/events/one", ""},
		{[]string{"retry", "-event-id", "one", "-destination", "audit"}, "POST", "/admin/v1/events/one/retry", `"destination":"audit"`},
	} {
		if tc.method == "POST" {
			status = 204
		} else {
			status = 200
		}
		out, _, err := run(t, env, append([]string{"events"}, tc.args...)...)
		if err != nil {
			t.Fatal(tc.args, err)
		}
		got := f.last()
		if got.method != tc.method || got.path != tc.path || !strings.Contains(got.body, tc.body) {
			t.Fatal(tc.args, got)
		}
		if status == 200 && !strings.Contains(out, "records") {
			t.Fatal(out)
		}
	}
	for _, args := range [][]string{nil, {"bogus"}, {"retry"}, {"retry", "-event-id", "one"}, {"list", "unexpected"}, {"status", "-invalid"}} {
		if _, _, err := run(t, env, append([]string{"events"}, args...)...); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatal(args, err)
		}
	}
	status = 503
	if _, _, err := run(t, env, "events", "status"); err == nil {
		t.Fatal("unavailable server reported success")
	}
	delete(env, "DMCTL_SERVER")
	if _, _, err := run(t, env, "events", "list"); err == nil {
		t.Fatal("unconfigured server accepted")
	}
}
