package dmctl_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
