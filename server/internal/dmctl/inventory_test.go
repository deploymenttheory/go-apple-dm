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
	}{
		{[]string{"axm", "accounts", "diagnostics", "first"}, "GET", "/admin/v1/axm/accounts/first/diagnostics", ""},
		{[]string{"axm", "accounts", "services", "first"}, "GET", "/admin/v1/axm/accounts/first/services", ""},
		{[]string{"axm", "accounts", "devices", "first"}, "GET", "/admin/v1/axm/accounts/first/devices", ""},
		{[]string{"axm", "accounts", "service", "first", "bound"}, "GET", "/admin/v1/axm/accounts/first/services/bound", ""},
		{[]string{"axm", "accounts", "device", "first", "opaque"}, "GET", "/admin/v1/axm/accounts/first/devices/opaque", ""},
		{[]string{"axm", "accounts", "coverage", "first", "opaque"}, "GET", "/admin/v1/axm/accounts/first/devices/opaque/coverage", ""},
		{[]string{"axm", "accounts", "assigned-service", "first", "opaque"}, "GET", "/admin/v1/axm/accounts/first/devices/opaque/assigned-service", ""},
		{[]string{"axm", "accounts", "assignment-service", "first"}, "GET", "/admin/v1/axm/accounts/first/assignment-service", ""},
		{[]string{"axm", "accounts", "configure-service", "first"}, "PUT", "/admin/v1/axm/accounts/first/assignment-service", ""},
		{[]string{"axm", "accounts", "assign", "first"}, "POST", "/admin/v1/axm/accounts/first/assignments", ""},
		{[]string{"axm", "accounts", "unassign", "first"}, "POST", "/admin/v1/axm/accounts/first/unassignments", ""},
		{[]string{"axm", "accounts", "operation", "first", "request"}, "GET", "/admin/v1/axm/accounts/first/assignments/request", ""},
		{[]string{"axm", "accounts", "operation-log", "first", "request"}, "GET", "/admin/v1/axm/accounts/first/assignments/request/log", ""},
		{[]string{"axm", "accounts", "jobs", "first"}, "GET", "/admin/v1/axm/accounts/first/jobs", ""},
		{[]string{"axm", "accounts", "job", "first", "job"}, "GET", "/admin/v1/axm/accounts/first/jobs/job", ""},
		{[]string{"devices", "inspect", "SER"}, "GET", "/admin/v1/inventory/serials/SER", ""},
		{[]string{"devices", "inspect", "SER", "--raw"}, "GET", "/admin/v1/inventory/raw/serials/SER", ""},
		{[]string{"devices", "evidence", "record", "--raw"}, "GET", "/admin/v1/inventory/raw/devices/record/evidence", ""},
		{[]string{"devices", "resolve", "apple", "--raw"}, "POST", "/admin/v1/inventory/raw/devices/apple/resolve", ""},
		{[]string{"inventory", "correlate"}, "POST", "/admin/v1/inventory/correlations", ""},
		{[]string{"inventory", "presets", "--raw"}, "GET", "/admin/v1/inventory/raw/presets", ""},
		{[]string{"devices", "list", "--where", `[{"field":"data.attributes.imei","operator":"contains","value":"one"}]`}, "GET", "/admin/v1/devices", "where"},
		{[]string{"devices", "get", "opaque", "--raw"}, "GET", "/admin/v1/inventory/raw/devices/opaque", ""},
		{[]string{"inventory", "export", "--format", "csv", "--columns", "meta.resource_id,meta.id"}, "GET", "/admin/v1/inventory/exports", "columns"},
		{[]string{"axm", "accounts", "sync", "source", "--force"}, "POST", "/admin/v1/axm/accounts/source/sync", "force"},
		{[]string{"inventory", "jobs", "pause", "job"}, "POST", "/admin/v1/inventory/jobs/job/pause", ""},
	} {
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
