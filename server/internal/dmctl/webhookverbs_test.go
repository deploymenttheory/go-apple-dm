package dmctl_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

// TestWebhookCommands checks native webhook CLI operations.
func TestWebhookCommands(t *testing.T) {
	f := &fakeAdmin{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serve(httptest.NewRecorder(), r)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = srv.URL, "test-token"
	spec := `{"name":"workflow","url":"https://receiver.example.test/events","events":["protocol.mdm.exchange"]}`
	for _, tc := range []struct {
		args                          []string
		method, path, stdin, contains string
	}{
		{[]string{"list", "-after", "previous", "-limit", "5"}, "GET", "/webhooks", "", ""},
		{[]string{"get", "-id", "one"}, "GET", "/webhooks/one", "", ""},
		{[]string{"create"}, "POST", "/webhooks", spec, `"name":"workflow"`},
		{[]string{"update", "-id", "one", "-revision", "2"}, "PUT", "/webhooks/one", spec, `"revision":2`},
		{[]string{"delete", "-id", "one"}, "DELETE", "/webhooks/one", "", ""},
		{[]string{"pause", "-id", "one"}, "POST", "/webhooks/one/pause", "", ""},
		{[]string{"resume", "-id", "one"}, "POST", "/webhooks/one/resume", "", ""},
		{[]string{"enable", "-id", "one"}, "POST", "/webhooks/one/enable", "", ""},
		{[]string{"disable", "-id", "one"}, "POST", "/webhooks/one/disable", "", ""},
		{[]string{"test", "-id", "one"}, "POST", "/webhooks/one/test", "", ""},
		{[]string{"rotate", "-id", "one", "-overlap", "0s"}, "POST", "/webhooks/one/credentials", "", `"overlap_seconds":0`},
		{[]string{"catalogue"}, "GET", "/webhooks/catalogue", "", ""},
		{[]string{"status"}, "GET", "/webhooks/status", "", ""},
		{[]string{"deliveries", "-subscription", "one"}, "GET", "/webhooks/deliveries", "", ""},
		{[]string{"deliveries", "-id", "delivery"}, "GET", "/webhooks/deliveries/delivery", "", ""},
		{[]string{"retry", "-id", "delivery"}, "POST", "/webhooks/deliveries/delivery/retry", "", ""},
		{[]string{"replay", "-key", "operator-request", "-dry-run"}, "POST", "/webhooks/replays", `{"subscription_id":"one"}`, `"key":"operator-request"`},
		{[]string{"replay"}, "POST", "/webhooks/replays", `{"subscription_id":"one"}`, `"key":`},
	} {
		out, _, err := runWithStdin(t, env, tc.stdin, append([]string{"webhooks"}, tc.args...)...)
		if err != nil {
			t.Fatal(tc.args, err)
		}
		got := f.last()
		if got.method != tc.method || got.path != "/admin/v1"+tc.path || !strings.Contains(got.body, tc.contains) || !strings.Contains(out, "ok") {
			t.Fatal(tc.args, got, out)
		}
	}
	for _, args := range [][]string{nil, {"bogus"}, {"get"}, {"get", "trailing"}, {"get", "-invalid"}, {"update", "-id", "one"}, {"create"}, {"replay"}} {
		if _, _, err := runWithStdin(t, env, `{"unknown":true}`, append([]string{"webhooks"}, args...)...); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatal(args, err)
		}
	}
}
