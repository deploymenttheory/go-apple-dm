package dmctl_test

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

const depTestCertificate = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"

// depRequest is one captured DEP administration request.
type depRequest struct {
	method, path, query, contentType, body string
}

// depAdmin answers like the reference server's DEP routes and records requests.
type depAdmin struct {
	mu       sync.Mutex
	requests []depRequest
	status   int
}

// seen returns a copy of the captured requests.
func (f *depAdmin) seen() []depRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]depRequest(nil), f.requests...)
}

// serve records a request and returns the route's fixture response.
func (f *depAdmin) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	escaped := r.URL.EscapedPath()
	f.requests = append(f.requests, depRequest{r.Method, escaped, r.URL.RawQuery, r.Header.Get("Content-Type"), string(body)})
	status := f.status
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"Error":"internal error"}`))
		return
	}
	path := strings.TrimPrefix(escaped, "/admin/v1/dep/accounts")
	switch r.Method + " " + path {
	case "GET ":
		_, _ = w.Write([]byte(`{"Items":[` +
			`{"Name":"abm-lab","OrgName":"Example Org","ServerUUID":"SERVER-1","HasTokens":true,"State":{"TermsExpired":false,"TokenInvalid":false},"AccessTokenExpiry":"2027-10-26T00:00:00Z"},` +
			`{"Name":"renewed","HasTokens":true,"State":{"TermsExpired":true}},` +
			`{"Name":"revoked","HasTokens":false,"State":{"TokenInvalid":true}},` +
			`"unexpected"],"NextCursor":""}`))
	case "PUT /abm-lab/keypair":
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write([]byte(depTestCertificate))
	case "PUT /abm-lab/token":
		_, _ = w.Write([]byte(`{"server_name":"Lab server","server_uuid":"SERVER-1","org_name":"Example Org","org_id":"ORG-1","org_type":"edu","admin_id":"admin@example.test"}`))
	case "GET /abm-lab/devices":
		_, _ = w.Write([]byte(`{"Items":[{"serial_number":"SERIAL-A","model":"Mac","device_family":"Mac","os":"OSX","profile_status":"assigned","profile_uuid":"PROFILE-1"}]}`))
	case "GET /abm-lab/profiles":
		_, _ = w.Write([]byte(`{"Items":[{"profile_uuid":"PROFILE-1","profile_name":"Lab","url":"https://mdm.example.test/enroll/ade"}]}`))
	case "GET /abm-lab/targets":
		_, _ = w.Write([]byte(`{"Default":"PROFILE-1","Serials":{"SERIAL-B":"PROFILE-2","SERIAL-A":"PROFILE-1"},"Revision":3}`))
	case "GET /empty/targets":
		_, _ = w.Write([]byte(`{"Default":"","Serials":null,"Revision":0}`))
	case "GET /odd/targets":
		_, _ = w.Write([]byte(`[]`))
	case "PUT /abm-lab/profile":
		_, _ = w.Write([]byte(`{"profile_uuid":"PROFILE-3"}`))
	case "POST /abm-lab/sync":
		_, _ = w.Write([]byte(`{"Sync":{"Pages":1},"Assign":{"Candidates":0}}`))
	case "PUT /abm-lab/default-profile", "DELETE /abm-lab/default-profile",
		"PUT /abm-lab/targets/SERIAL%20A", "DELETE /abm-lab/targets/SERIAL%20A":
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"Error":"not found"}`))
	}
}

// depServer starts a fake DEP administration API and its CLI environment.
func depServer(t *testing.T) (*depAdmin, map[string]string) {
	t.Helper()
	f := &depAdmin{}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	env := noConfig(t)
	env["DMCTL_SERVER"] = srv.URL
	env["DMCTL_TOKEN"] = "tok"
	return f, env
}

// TestDEPAccountCommands checks each typed operation's request and rendered result.
func TestDEPAccountCommands(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile.json")
	if err := os.WriteFile(profile, []byte(`{"profile_name":"Lab"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		want  depRequest
		shown []string
	}{
		{
			"list",
			[]string{"list"},
			depRequest{method: "GET", path: "/admin/v1/dep/accounts"},
			[]string{"NAME", "abm-lab  Example Org", "SERVER-1", "ok", "terms-expired", "token-invalid", "2027-10-26"},
		},
		{
			"devices",
			[]string{"devices", "abm-lab"},
			depRequest{method: "GET", path: "/admin/v1/dep/accounts/abm-lab/devices"},
			[]string{"SERIAL-A", "assigned", "PROFILE-1"},
		},
		{
			"profiles",
			[]string{"profiles", "abm-lab"},
			depRequest{method: "GET", path: "/admin/v1/dep/accounts/abm-lab/profiles"},
			[]string{"PROFILE-1", "https://mdm.example.test/enroll/ade"},
		},
		{
			"targets",
			[]string{"targets", "abm-lab"},
			depRequest{method: "GET", path: "/admin/v1/dep/accounts/abm-lab/targets"},
			[]string{"Default   PROFILE-1", "Revision  3", "SERIAL-A  PROFILE-1\nSERIAL-B  PROFILE-2"},
		},
		{
			"targets without choices",
			[]string{"targets", "empty"},
			depRequest{method: "GET", path: "/admin/v1/dep/accounts/empty/targets"},
			[]string{"Default   -"},
		},
		{
			"unexpected targets",
			[]string{"targets", "odd"},
			depRequest{method: "GET", path: "/admin/v1/dep/accounts/odd/targets"},
			[]string{"[]"},
		},
		{
			"keypair",
			[]string{"keypair", "abm-lab"},
			depRequest{method: "PUT", path: "/admin/v1/dep/accounts/abm-lab/keypair"},
			[]string{depTestCertificate},
		},
		{
			"define profile",
			[]string{"define-profile", "abm-lab", "-file", profile, "-default"},
			depRequest{method: "PUT", path: "/admin/v1/dep/accounts/abm-lab/profile", query: "default=true", contentType: "application/json", body: `{"profile_name":"Lab"}`},
			[]string{"PROFILE-3"},
		},
		{
			"default profile",
			[]string{"default-profile", "abm-lab", "PROFILE-1"},
			depRequest{method: "PUT", path: "/admin/v1/dep/accounts/abm-lab/default-profile", contentType: "application/json", body: `{"profile_uuid":"PROFILE-1"}`},
			nil,
		},
		{
			"clear default",
			[]string{"clear-default", "abm-lab"},
			depRequest{method: "DELETE", path: "/admin/v1/dep/accounts/abm-lab/default-profile"},
			nil,
		},
		{
			"target",
			[]string{"target", "abm-lab", "SERIAL A", "PROFILE-2"},
			depRequest{method: "PUT", path: "/admin/v1/dep/accounts/abm-lab/targets/SERIAL%20A", contentType: "application/json", body: `{"profile_uuid":"PROFILE-2"}`},
			nil,
		},
		{
			"clear target",
			[]string{"clear-target", "abm-lab", "SERIAL A"},
			depRequest{method: "DELETE", path: "/admin/v1/dep/accounts/abm-lab/targets/SERIAL%20A"},
			nil,
		},
		{
			"sync",
			[]string{"sync", "abm-lab"},
			depRequest{method: "POST", path: "/admin/v1/dep/accounts/abm-lab/sync"},
			[]string{`"Pages":1`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, env := depServer(t)
			out, _, err := run(t, env, append([]string{"dep", "accounts"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			seen := f.seen()
			if len(seen) != 1 || seen[0] != tc.want {
				t.Fatalf("requests = %+v, want %+v", seen, tc.want)
			}
			for _, s := range tc.shown {
				if !strings.Contains(out, s) {
					t.Errorf("output lacks %q:\n%s", s, out)
				}
			}
		})
	}
}

// TestDEPImportToken checks that the portal token reaches the server byte for
// byte, from a file or stdin, with the explicit identity-replacement choice.
func TestDEPImportToken(t *testing.T) {
	// #nosec G101 -- An opaque fixture body, not a server token.
	token := "Content-Type: application/pkcs7-mime\r\n\r\nMIAG\x00\xff\r\n"
	file := filepath.Join(t.TempDir(), "token.p7m")
	if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	want := depRequest{method: "PUT", path: "/admin/v1/dep/accounts/abm-lab/token", contentType: "application/pkcs7-mime", body: token}

	f, env := depServer(t)
	out, _, err := run(t, env, "dep", "accounts", "import-token", "abm-lab", "-file", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Server                Lab server\n", "Server UUID           SERVER-1\n", "Organization ID       ORG-1\n", "Organization version  -\n"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "admin@example.test") {
		t.Error("human output included the administrator contact")
	}

	out, _, err = runWithStdin(t, env, token, "-output", "json", "dep", "accounts", "import-token", "abm-lab", "-file", "-", "-force")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"admin_id":"admin@example.test"`) {
		t.Errorf("JSON output was not the server's response: %s", out)
	}
	forced := want
	forced.query = "force=true"
	if seen := f.seen(); len(seen) != 2 || seen[0] != want || seen[1] != forced {
		t.Fatalf("requests = %+v", seen)
	}
}

// TestDEPKeypairOutput checks that -out receives the certificate, never replaces
// a file, and is cleaned up when the keypair is not staged.
func TestDEPKeypairOutput(t *testing.T) {
	dir := t.TempDir()
	f, env := depServer(t)
	cert := filepath.Join(dir, "public.pem")
	out, _, err := run(t, env, "dep", "accounts", "keypair", "abm-lab", "-out", cert)
	if err != nil || out != "" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	// #nosec G304 -- The test owns its temporary certificate path.
	got, err := os.ReadFile(cert)
	if err != nil || string(got) != depTestCertificate {
		t.Fatalf("certificate file = %q, %v", got, err)
	}
	if info, err := os.Stat(cert); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("certificate file mode = %v, %v", info.Mode(), err)
	}
	if _, _, err := run(t, env, "dep", "accounts", "keypair", "abm-lab", "-out", cert); err == nil {
		t.Fatal("an existing certificate file was replaced")
	}
	if len(f.seen()) != 1 {
		t.Fatal("a refused output path still staged another keypair")
	}

	for _, tc := range []struct {
		name  string
		setup func(map[string]string)
	}{
		{"credential error", func(env map[string]string) { env["DMCTL_TOKEN"] = "@" + filepath.Join(dir, "absent") }},
		{"server failure", func(map[string]string) { f.mu.Lock(); f.status = http.StatusInternalServerError; f.mu.Unlock() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := maps.Clone(env)
			tc.setup(env)
			failed := filepath.Join(t.TempDir(), "public.pem")
			if _, _, err := run(t, env, "dep", "accounts", "keypair", "abm-lab", "-out", failed); err == nil {
				t.Fatal("failure reported success")
			}
			if _, err := os.Stat(failed); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed keypair left an output file: %v", err)
			}
		})
	}
}

// TestDEPInvalidInvocationsStayLocal checks that argument and input mistakes
// never reach the server, and that help needs no server.
func TestDEPInvalidInvocationsStayLocal(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	empty := write("empty.p7m", nil)
	large := write("large.p7m", make([]byte, 1<<20+1))
	invalid := write("profile.json", []byte(`{`))
	f, env := depServer(t)
	for _, args := range [][]string{
		{},
		{"accounts"},
		{"profiles", "abm-lab"},
		{"accounts", "nonsense", "abm-lab"},
		{"accounts", "keypair"},
		{"accounts", "keypair", ""},
		{"accounts", "keypair", "a", "b"},
		{"accounts", "target", "abm-lab", "SERIAL"},
		{"accounts", "import-token", "abm-lab"},
		{"accounts", "import-token", "abm-lab", "-file", empty},
		{"accounts", "import-token", "abm-lab", "-file", large},
		{"accounts", "define-profile", "abm-lab", "-file", invalid},
		{"accounts", "define-profile", "abm-lab"},
		{"accounts", "list", "-out", filepath.Join(dir, "x.pem")},
		{"accounts", "sync", "abm-lab", "-force"},
		{"accounts", "import-token", "abm-lab", "-file", empty, "-default"},
		{"accounts", "devices", "abm-lab", "-file", invalid},
		{"accounts", "list", "-unknown"},
	} {
		if _, _, err := run(t, env, append([]string{"dep"}, args...)...); !errors.Is(err, dmctl.ErrUsage) {
			t.Errorf("%v: err = %v, want ErrUsage", args, err)
		}
	}
	if _, _, err := run(t, env, "dep", "accounts", "import-token", "abm-lab", "-file", filepath.Join(dir, "absent.p7m")); err == nil {
		t.Error("a missing token file was accepted")
	}
	_, stderr, err := run(t, env, "dep", "-h")
	if err != nil || !strings.Contains(stderr, "import-token ACCOUNT -file token.p7m") {
		t.Fatalf("help = %q, %v", stderr, err)
	}
	if seen := f.seen(); len(seen) != 0 {
		t.Fatalf("invalid invocations reached the server: %+v", seen)
	}
}

// TestDEPServerFailures checks that every operation reports a server failure.
func TestDEPServerFailures(t *testing.T) {
	dir := t.TempDir()
	token, profile := filepath.Join(dir, "token.p7m"), filepath.Join(dir, "profile.json")
	for path, data := range map[string]string{token: "token", profile: "{}"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, env := depServer(t)
	f.status = http.StatusBadGateway
	for _, args := range [][]string{
		{"list"},
		{"keypair", "abm-lab"},
		{"import-token", "abm-lab", "-file", token},
		{"devices", "abm-lab"},
		{"profiles", "abm-lab"},
		{"targets", "abm-lab"},
		{"sync", "abm-lab"},
		{"define-profile", "abm-lab", "-file", profile},
		{"default-profile", "abm-lab", "PROFILE-1"},
		{"clear-default", "abm-lab"},
		{"target", "abm-lab", "SERIAL", "PROFILE-1"},
		{"clear-target", "abm-lab", "SERIAL"},
	} {
		if _, _, err := run(t, env, append([]string{"dep", "accounts"}, args...)...); dmctl.ExitCode(err) != dmctl.ExitFailed {
			t.Errorf("%v: err = %v, want a failed exit", args, err)
		}
	}
}
