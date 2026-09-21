package dmctl_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInventoryCommands checks command routing and shared filter and export options.
func TestInventoryCommands(t *testing.T) {
	for _, tc := range []struct {
		args                []string
		method, path, query string
	}{{[]string{"devices", "list", "--where", `[{"field":"imei","operator":"contains","value":"one"}]`}, "GET", "/admin/v1/devices", "where"}, {[]string{"devices", "get", "opaque", "--raw"}, "GET", "/admin/v1/inventory/raw/devices/opaque", ""}, {[]string{"inventory", "export", "--format", "csv", "--columns", "imei,serial_number"}, "GET", "/admin/v1/inventory/exports", "columns"}, {[]string{"axm", "accounts", "sync", "source", "--force"}, "POST", "/admin/v1/axm/accounts/source/sync", "force"}, {[]string{"inventory", "jobs", "pause", "job"}, "POST", "/admin/v1/inventory/jobs/job/pause", ""}} {
		t.Run(strings.Join(tc.args[:2], "-"), func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("%s %s", r.Method, r.URL.Path)
				}
				if tc.query != "" && r.URL.Query().Get(tc.query) == "" {
					t.Errorf("missing %s", tc.query)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			env := noConfig(t)
			env["DMCTL_SERVER"] = server.URL
			env["DMCTL_TOKEN"] = "test-token"
			if _, _, e := run(t, env, tc.args...); e != nil {
				t.Fatal(e)
			}
			if !called {
				t.Fatal("no request")
			}
		})
	}
}
