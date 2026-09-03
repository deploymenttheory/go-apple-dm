package mdmctl_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/mdmctl"
)

// apiRecorder answers every admin route with a plausible body and remembers
// what was asked, so a test can assert the request rather than the plumbing.
type apiRecorder struct {
	mu       sync.Mutex
	requests []string
	bodies   []string
}

func (a *apiRecorder) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.Method+" "+r.URL.RequestURI())
	a.bodies = append(a.bodies, string(body))
}

func (a *apiRecorder) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.requests...)
}

func (a *apiRecorder) sawPrefix(prefix string) bool {
	for _, r := range a.seen() {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func mdmServer(t *testing.T) (*apiRecorder, map[string]string) {
	t.Helper()
	rec := &apiRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/admin/v1")
		switch {
		case path == "/enrollments" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"Items":[{"Channel":"device","ID":"UDID-1","Enabled":true,"SerialNumber":"S1","OSVersion":"26.0","LastSeenAt":"2026-09-03T12:00:00Z"}],"NextCursor":""}`))
		case strings.HasSuffix(path, "/commands") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"Items":[{"CommandUUID":"C1","RequestType":"DeviceInformation","State":"queued","Attempts":0,"Status":""}],"NextCursor":""}`))
		case path == "/pushcerts" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"Items":[{"Topic":"com.apple.mgmt.External.t","NotAfter":"2027-01-01T00:00:00Z","Version":2}],"NextCursor":""}`))
		case path == "/export":
			_, _ = w.Write([]byte(`{"Items":[{"Channel":"device","ID":"UDID-1","Enabled":true}],"NextCursor":""}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	env := noConfig(t)
	env["MDMCTL_SERVER"] = srv.URL
	env["MDMCTL_TOKEN"] = "tok"
	return rec, env
}

// The MDM half of the admin API is now driveable, which it was not: the CLI
// covered declarations and principals and nothing that touches an enrollment
// or its command queue.
func TestMDMVerbs(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		stdin  string
		expect string
		output string
	}{
		{
			name: "EnrollmentsList", args: []string{"enrollments", "list"},
			expect: "GET /admin/v1/enrollments", output: "UDID-1",
		},
		{
			name:   "EnrollmentsListFilters",
			args:   []string{"enrollments", "list", "-channel", "device", "-serial", "S1", "-enabled", "true"},
			expect: "channel=device",
		},
		{
			name: "EnrollmentsGet", args: []string{"enrollments", "get", "device", "UDID-1"},
			expect: "GET /admin/v1/enrollments/device/UDID-1",
		},
		{
			name:   "EnrollmentsGetUserChannelCarriesParent",
			args:   []string{"enrollments", "get", "-parent", "UDID-1", "user", "U1"},
			expect: "parent=UDID-1",
		},
		{
			name: "EnrollmentsDisable", args: []string{"enrollments", "disable", "device", "UDID-1"},
			expect: "DELETE /admin/v1/enrollments/device/UDID-1",
		},
		{
			name: "CommandsSend", args: []string{"commands", "send", "device", "UDID-1"},
			stdin: "<plist/>", expect: "POST /admin/v1/enrollments/device/UDID-1/commands",
		},
		{
			name: "CommandsList", args: []string{"commands", "list", "device", "UDID-1", "-type", "DeviceInformation"},
			expect: "type=DeviceInformation", output: "C1",
		},
		{
			name: "CommandsClear", args: []string{"commands", "clear", "device", "UDID-1"},
			expect: "DELETE /admin/v1/enrollments/device/UDID-1/commands",
		},
		{
			name: "Push", args: []string{"push", "device", "UDID-1"},
			expect: "POST /admin/v1/enrollments/device/UDID-1/push",
		},
		{
			name: "PushCertsList", args: []string{"pushcerts", "list"},
			expect: "GET /admin/v1/pushcerts", output: "com.apple.mgmt.External.t",
		},
		{
			name: "PushCertsPut", args: []string{"pushcerts", "put"},
			stdin: `{"Topic":"t"}`, expect: "PUT /admin/v1/pushcerts",
		},
		{
			name: "SetsAdd", args: []string{"sets", "add", "lab", "com.example.a"},
			expect: "PUT /admin/v1/sets/lab/declarations/com.example.a",
		},
		{
			name: "SetsRemove", args: []string{"sets", "remove", "lab", "com.example.a"},
			expect: "DELETE /admin/v1/sets/lab/declarations/com.example.a",
		},
		{
			name: "SetsAssign", args: []string{"sets", "assign", "device", "UDID-1", "lab"},
			expect: "PUT /admin/v1/enrollments/device/UDID-1/sets/lab",
		},
		{
			name: "SetsUnassign", args: []string{"sets", "unassign", "device", "UDID-1", "lab"},
			expect: "DELETE /admin/v1/enrollments/device/UDID-1/sets/lab",
		},
		{
			name: "Notify", args: []string{"notify"}, expect: "POST /admin/v1/notify",
		},
		{
			name: "Export", args: []string{"export"}, expect: "GET /admin/v1/export",
		},
		{
			name: "Import", args: []string{"import"}, stdin: `{"ID":{"ID":"X"}}`,
			expect: "POST /admin/v1/import",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, env := mdmServer(t)
			out, _, err := runWithStdin(t, env, tc.stdin, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if !rec.sawPrefix(tc.expect) && !containsAny(rec.seen(), tc.expect) {
				t.Fatalf("requests = %v, want one containing %q", rec.seen(), tc.expect)
			}
			if tc.output != "" && !strings.Contains(out, tc.output) {
				t.Fatalf("output missing %q:\n%s", tc.output, out)
			}
		})
	}
}

func containsAny(reqs []string, want string) bool {
	for _, r := range reqs {
		if strings.Contains(r, want) {
			return true
		}
	}
	return false
}

// The dep, axm and acme families proxy Apple-shaped APIs whose surface is not
// ours to model, so the escape hatch keeps them reachable rather than
// pretending "every admin route" excludes them.
func TestAPIVerb(t *testing.T) {
	t.Run("ReachesAProxiedFamily", func(t *testing.T) {
		rec, env := mdmServer(t)
		if _, _, err := run(t, env, "api", "GET", "/dep/accounts"); err != nil {
			t.Fatal(err)
		}
		if !rec.sawPrefix("GET /admin/v1/dep/accounts") {
			t.Fatalf("requests = %v", rec.seen())
		}
	})

	t.Run("NormalisesMethodAndPath", func(t *testing.T) {
		rec, env := mdmServer(t)
		if _, _, err := run(t, env, "api", "get", "acme/certificates"); err != nil {
			t.Fatal(err)
		}
		if !rec.sawPrefix("GET /admin/v1/acme/certificates") {
			t.Fatalf("requests = %v", rec.seen())
		}
	})

	t.Run("CarriesAQueryString", func(t *testing.T) {
		rec, env := mdmServer(t)
		if _, _, err := run(t, env, "api", "GET", "/audit?actor=break-glass"); err != nil {
			t.Fatal(err)
		}
		if !containsAny(rec.seen(), "actor=break-glass") {
			t.Fatalf("requests = %v", rec.seen())
		}
	})

	t.Run("SendsABody", func(t *testing.T) {
		rec, env := mdmServer(t)
		if _, _, err := runWithStdin(t, env, `{"x":1}`, "api", "POST", "/dep/accounts"); err != nil {
			t.Fatal(err)
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		found := false
		for _, b := range rec.bodies {
			if strings.Contains(b, `"x":1`) {
				found = true
			}
		}
		if !found {
			t.Fatalf("bodies = %v", rec.bodies)
		}
	})

	t.Run("NeedsAMethodAndPath", func(t *testing.T) {
		_, env := mdmServer(t)
		if _, _, err := run(t, env, "api", "GET"); !errors.Is(err, mdmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})
}

// Every new verb reports a usage error rather than a confusing HTTP failure
// when the operator gets the arguments wrong.
func TestMDMVerbUsageErrors(t *testing.T) {
	_, env := mdmServer(t)
	for _, args := range [][]string{
		{"enrollments"},
		{"enrollments", "nonsense"},
		{"enrollments", "get"},
		{"enrollments", "get", "device"},
		{"commands"},
		{"commands", "nonsense"},
		{"commands", "list", "device"},
		{"commands", "send", "device"},
		{"commands", "clear", "device"},
		{"push"},
		{"push", "device"},
		{"pushcerts"},
		{"pushcerts", "nonsense"},
		{"sets"},
		{"sets", "nonsense"},
		{"sets", "add", "lab"},
		{"sets", "assign", "device", "UDID-1"},
	} {
		if _, _, err := run(t, env, args...); !errors.Is(err, mdmctl.ErrUsage) {
			t.Errorf("%v: err = %v, want ErrUsage", args, err)
		}
	}
}

// verbInvocations is every new verb with arguments that would otherwise
// succeed, so a table can drive them all through one failure mode.
func verbInvocations() [][]string {
	return [][]string{
		{"enrollments", "list"},
		{"enrollments", "get", "device", "UDID-1"},
		{"enrollments", "disable", "device", "UDID-1"},
		{"commands", "send", "device", "UDID-1"},
		{"commands", "list", "device", "UDID-1"},
		{"commands", "clear", "device", "UDID-1"},
		{"push", "device", "UDID-1"},
		{"pushcerts", "list"},
		{"pushcerts", "put"},
		{"sets", "add", "lab", "com.example.a"},
		{"sets", "assign", "device", "UDID-1", "lab"},
		{"notify"},
		{"export"},
		{"import"},
		{"api", "GET", "/dep/accounts"},
	}
}

// A server URL the client cannot use is the operator's mistake, reported as
// usage rather than as a transport failure halfway through.
func TestMDMVerbsRejectABadServer(t *testing.T) {
	env := noConfig(t)
	env["MDMCTL_SERVER"] = "ftp://example.com"
	env["MDMCTL_TOKEN"] = "tok"
	for _, args := range verbInvocations() {
		full := append([]string{}, args...)
		if _, _, err := runWithStdin(t, env, "{}", full...); !errors.Is(err, mdmctl.ErrUsage) {
			t.Errorf("%v: err = %v, want ErrUsage", args, err)
		}
	}
}

// A 404 on a route this server does not serve becomes a sentence naming the
// role, rather than a bare status the operator has to interpret.
func TestMDMVerbsExplainAMissingFamily(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/config") {
			_, _ = w.Write([]byte(`{"Role":"ddm","Version":"devel","Families":["ddm"]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"Error":"not found"}`))
	}))
	defer srv.Close()
	env := noConfig(t)
	env["MDMCTL_SERVER"] = srv.URL
	env["MDMCTL_TOKEN"] = "tok"

	_, _, err := run(t, env, "enrollments", "list")
	if err == nil {
		t.Fatal("a missing family reported success")
	}
	if !strings.Contains(err.Error(), "role=ddm") {
		t.Fatalf("err = %v, want the role named", err)
	}
}

// A file that cannot be read is reported before any request goes out.
func TestMDMVerbsReportAnUnreadableFile(t *testing.T) {
	_, env := mdmServer(t)
	for _, args := range [][]string{
		{"commands", "send", "-file", "/no/such/file", "device", "UDID-1"},
		{"pushcerts", "put", "-file", "/no/such/file"},
		{"import", "-file", "/no/such/file"},
		{"api", "POST", "/dep/accounts", "-file", "/no/such/file"},
	} {
		if _, _, err := run(t, env, args...); err == nil {
			t.Errorf("%v: an unreadable file reported success", args)
		}
	}
}

// An unknown flag is a usage error on every verb, not a silently ignored
// argument that makes the operator think something ran.
func TestMDMVerbsRejectAnUnknownFlag(t *testing.T) {
	_, env := mdmServer(t)
	for _, args := range verbInvocations() {
		full := append(append([]string{}, args...), "-nonsense")
		if _, _, err := runWithStdin(t, env, "{}", full...); !errors.Is(err, mdmctl.ErrUsage) {
			t.Errorf("%v: err = %v, want ErrUsage", full, err)
		}
	}
}
