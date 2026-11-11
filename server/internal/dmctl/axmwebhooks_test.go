package dmctl_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAXMWebhookCommands checks account selection, operation paths and JSON input.
func TestAXMWebhookCommands(t *testing.T) {
	f := &fakeAdmin{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serve(httptest.NewRecorder(), r)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	env := noConfig(t)
	env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = srv.URL, "test-token"
	for _, tc := range []struct{ verb, method, path string }{
		{"list", "GET", "/webhooks"},
		{"create", "POST", "/webhooks"},
		{"get", "GET", "/webhooks/local"},
		{"update", "PATCH", "/webhooks/local"},
		{"delete", "DELETE", "/webhooks/local"},
		{"bindings", "GET", "/webhook-bindings"},
		{"status", "GET", "/webhook-bindings/local"},
		{"receipts", "GET", "/webhook-bindings/local/receipts"},
		{"deliveries", "GET", "/webhooks/local/deliveries"},
		{"ping", "POST", "/webhooks/local/ping"},
		{"redeliver", "POST", "/webhooks/local/redeliver"},
		{"rotate", "POST", "/webhooks/local/rotate"},
		{"reconcile", "POST", "/webhook-bindings/local/reconcile"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			out, _, err := runWithStdin(t, env, `{"name":"lab"}`, "axm", "webhooks", tc.verb, "-account", "apple", "-binding", "local")
			if err != nil {
				t.Fatal(err)
			}
			got := f.last()
			if got.method != tc.method || got.path != "/admin/v1/axm/accounts/apple"+tc.path || !strings.Contains(out, "ok") {
				t.Fatal(got, out)
			}
		})
	}
	for _, args := range [][]string{nil, {"unknown"}, {"get", "-account", "apple"}, {"list"}} {
		if _, _, err := runWithStdin(t, env, "", append([]string{"axm", "webhooks"}, args...)...); err == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
}
