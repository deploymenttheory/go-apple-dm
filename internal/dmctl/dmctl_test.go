package dmctl_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/dmctl"
	"github.com/deploymenttheory/go-apple-dm/internal/dmctl/adminclient"
)

// run executes the CLI and returns stdout, stderr, and the error.
func run(t *testing.T, env map[string]string, args ...string) (string, string, error) {
	t.Helper()
	var out, errBuf strings.Builder
	getenv := func(k string) string { return env[k] }
	err := dmctl.Run(context.Background(), args, getenv, strings.NewReader(""), &out, &errBuf)
	return out.String(), errBuf.String(), err
}

// runWithStdin is run with a body on stdin, for the verbs that read one.
func runWithStdin(t *testing.T, env map[string]string, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var out, errBuf strings.Builder
	getenv := func(k string) string { return env[k] }
	err := dmctl.Run(context.Background(), args, getenv, strings.NewReader(stdin), &out, &errBuf)
	return out.String(), errBuf.String(), err
}

// noConfig points the CLI at an empty directory so a developer's real config
// never influences a test.
func noConfig(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"DMCTL_CONFIG": filepath.Join(t.TempDir(), "absent.json")}
}

func TestUsage(t *testing.T) {
	t.Run("NoCommand", func(t *testing.T) {
		_, errOut, err := run(t, noConfig(t))
		if !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
		if !strings.Contains(errOut, "Commands:") {
			t.Fatalf("no command list printed:\n%s", errOut)
		}
	})

	t.Run("UnknownCommand", func(t *testing.T) {
		_, _, err := run(t, noConfig(t), "nope")
		if !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	t.Run("BadFlag", func(t *testing.T) {
		_, _, err := run(t, noConfig(t), "-nosuchflag", "version")
		if !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	// Every verb appears in the help, so a command cannot be added without
	// being discoverable.
	t.Run("EveryVerbIsListed", func(t *testing.T) {
		_, errOut, _ := run(t, noConfig(t))
		for _, verb := range dmctl.Verbs() {
			if !strings.Contains(errOut, verb) {
				t.Errorf("verb %q is missing from the help", verb)
			}
		}
	})
}

// Documented, distinct exit codes: a script can tell a usage mistake from a
// refusal. Both reference CLIs exit 1 from arbitrary depth.
func TestExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"ok":         {nil, dmctl.ExitOK},
		"usage":      {dmctl.ErrUsage, dmctl.ExitUsage},
		"partial":    {dmctl.ErrPartial, dmctl.ExitPartial},
		"unauth":     {adminclient.ErrUnauthorized, dmctl.ExitAuth},
		"forbidden":  {adminclient.ErrForbidden, dmctl.ExitAuth},
		"notfound":   {adminclient.ErrNotFound, dmctl.ExitFailed},
		"transport":  {errors.New("boom"), dmctl.ExitFailed},
		"wrapped401": {errors.Join(errors.New("x"), adminclient.ErrUnauthorized), dmctl.ExitAuth},
	} {
		if got := dmctl.ExitCode(tc.err); got != tc.want {
			t.Errorf("%s: ExitCode = %d, want %d", name, got, tc.want)
		}
	}
}

// explain reads no server and no token, so it works on a laptop with no
// deployment.
func TestExplainNeedsNoServer(t *testing.T) {
	env := noConfig(t)
	out, _, err := run(t, env, "-server", "http://127.0.0.1:1", "explain", "DeviceLock")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !strings.Contains(out, "DeviceLock") {
		t.Fatalf("output:\n%s", out)
	}
}

// The flag package stops at the first positional argument, so a flag written
// after one would be silently ignored: the worst kind of wrong, because it
// answers a different question without saying so.
func TestGlobalFlagsAfterVerb(t *testing.T) {
	env := noConfig(t)
	before, _, err := run(t, env, "explain", "-target", "macos:15.0", "DeviceLock")
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := run(t, env, "explain", "DeviceLock", "-target", "macos:15.0")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("flag order changed the output:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if !strings.Contains(after, "Target") {
		t.Fatalf("the target was ignored:\n%s", after)
	}
}

func TestExplainVerb(t *testing.T) {
	env := noConfig(t)

	t.Run("NeedsAnArgument", func(t *testing.T) {
		if _, _, err := run(t, env, "explain"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	t.Run("SuggestsOnANearMiss", func(t *testing.T) {
		_, errOut, err := run(t, env, "explain", "DeviceLok")
		if !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
		if !strings.Contains(errOut, "did you mean") || !strings.Contains(errOut, "DeviceLock") {
			t.Fatalf("no useful suggestion:\n%s", errOut)
		}
	})

	t.Run("ListsFamiliesAndIDs", func(t *testing.T) {
		out, _, err := run(t, env, "explain", "-list")
		if err != nil || !strings.Contains(out, "commands") {
			t.Fatalf("families = %q, %v", out, err)
		}
		out, _, err = run(t, env, "explain", "-list", "-family", "commands")
		if err != nil || !strings.Contains(out, "DeviceLock") {
			t.Fatalf("ids = %q, %v", out, err)
		}
		out, _, err = run(t, env, "explain", "-list", "-paths", "-family", "commands")
		if err != nil || !strings.Contains(out, "DeviceLock.") {
			t.Fatalf("paths = %q, %v", out, err)
		}
		if _, _, err := run(t, env, "explain", "-list", "-family", "nope"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("unknown family: %v", err)
		}
	})

	// An ambiguous identifier answers with every match; -first narrows it.
	t.Run("AmbiguousAndFirst", func(t *testing.T) {
		all, _, err := run(t, env, "explain", "com.apple.MCX")
		if err != nil {
			t.Fatal(err)
		}
		one, _, err := run(t, env, "explain", "-first", "com.apple.MCX")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(all, "(profiles)") < 2 {
			t.Fatalf("expected several matches:\n%s", all)
		}
		if strings.Count(one, "(profiles)") != 1 {
			t.Fatalf("-first returned more than one:\n%s", one)
		}
	})

	t.Run("BadTarget", func(t *testing.T) {
		if _, _, err := run(t, env, "explain", "DeviceLock", "-target", "linux:1"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})
}

func TestVersionVerb(t *testing.T) {
	out, _, err := run(t, noConfig(t), "version")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("version = %q, %v", out, err)
	}
}

// A server-backed verb against a fake admin API.
func TestServerVerbs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"Error":"app: unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/admin/v1/config":
			_, _ = w.Write([]byte(`{"Role":"all","Version":"devel","Families":["ddm"],"Policy":true}`))
		case "/admin/v1/routes":
			_, _ = w.Write([]byte(`{"Routes":[{"Method":"GET","Pattern":"/config","Action":"readConfig","Family":"introspection"}]}`))
		case "/admin/v1/actions":
			_, _ = w.Write([]byte(`{"Items":[{"ID":"notify","Help":"Wake devices."}]}`))
		case "/admin/v1/principals":
			_, _ = w.Write([]byte(`{"Items":[{"Name":"ci","Roles":["reader"],"Root":false,"TokenID":"abcd1234"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"Error":"not found"}`))
		}
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"] = srv.URL
	env["DMCTL_TOKEN"] = "tok"

	t.Run("Status", func(t *testing.T) {
		out, _, err := run(t, env, "status")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Role:", "all", "policy"} {
			if !strings.Contains(out, want) {
				t.Fatalf("status output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("Routes", func(t *testing.T) {
		out, _, err := run(t, env, "routes")
		if err != nil || !strings.Contains(out, "readConfig") {
			t.Fatalf("routes = %q, %v", out, err)
		}
	})

	t.Run("Actions", func(t *testing.T) {
		out, _, err := run(t, env, "actions")
		if err != nil || !strings.Contains(out, "Wake devices.") {
			t.Fatalf("actions = %q, %v", out, err)
		}
	})

	t.Run("PrincipalsList", func(t *testing.T) {
		out, _, err := run(t, env, "principals", "list")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "ci") || !strings.Contains(out, "reader") {
			t.Fatalf("principals:\n%s", out)
		}
	})

	// json mode writes the server's bytes unchanged, so canonical JSON and
	// key order survive to whatever consumes them.
	t.Run("JSONIsVerbatim", func(t *testing.T) {
		out, _, err := run(t, env, "-output", "json", "status")
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"Role":"all","Version":"devel","Families":["ddm"],"Policy":true}`
		if strings.TrimSpace(out) != want {
			t.Fatalf("json output = %q, want the body byte for byte", out)
		}
	})

	t.Run("UnknownOutputMode", func(t *testing.T) {
		if _, _, err := run(t, env, "-output", "yaml", "status"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	t.Run("BadCredentialIsExitAuth", func(t *testing.T) {
		bad := noConfig(t)
		bad["DMCTL_SERVER"] = srv.URL
		bad["DMCTL_TOKEN"] = "wrong"
		_, _, err := run(t, bad, "status")
		if dmctl.ExitCode(err) != dmctl.ExitAuth {
			t.Fatalf("exit = %d, want ExitAuth for %v", dmctl.ExitCode(err), err)
		}
	})

	t.Run("SubcommandRequired", func(t *testing.T) {
		for _, verb := range []string{"principals", "policies", "declarations"} {
			if _, _, err := run(t, env, verb); !errors.Is(err, dmctl.ErrUsage) {
				t.Errorf("%s with no subcommand: %v", verb, err)
			}
			if _, _, err := run(t, env, verb, "nope"); !errors.Is(err, dmctl.ErrUsage) {
				t.Errorf("%s nope: %v", verb, err)
			}
		}
	})

	t.Run("BadServerURL", func(t *testing.T) {
		bad := noConfig(t)
		bad["DMCTL_SERVER"] = "not a url"
		if _, _, err := run(t, bad, "status"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	t.Run("InsecureWarns", func(t *testing.T) {
		_, errOut, _ := run(t, env, "-insecure", "status")
		if !strings.Contains(errOut, "insecure") {
			t.Fatalf("no warning printed:\n%s", errOut)
		}
	})

	t.Run("VerboseTracesWithoutTheToken", func(t *testing.T) {
		_, errOut, err := run(t, env, "-v", "status")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(errOut, "GET") {
			t.Fatalf("nothing traced:\n%s", errOut)
		}
		if strings.Contains(errOut, "tok") && strings.Contains(errOut, "Bearer") {
			t.Fatalf("the trace carries the credential:\n%s", errOut)
		}
	})
}

// The config file names where a credential lives; it never holds one by
// default. micromdm's mdmctl writes the live token under a 0777 directory
// and its `config print` echoes it.
func TestConfig(t *testing.T) {
	t.Run("TokenByReference", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "dmctl.json")
		if err := os.WriteFile(path, []byte(`{"current":"lab","contexts":{"lab":{"server":"http://x","token_env":"LAB_TOKEN"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer from-env" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"Role":"all","Version":"devel"}`))
		}))
		defer srv.Close()
		env := map[string]string{
			"DMCTL_CONFIG": path,
			"LAB_TOKEN":    "from-env",
			"DMCTL_SERVER": srv.URL,
		}
		if _, _, err := run(t, env, "status"); err != nil {
			t.Fatalf("status with a referenced token: %v", err)
		}
	})

	// A file other users can read is refused rather than used: reading it is
	// what makes the leak matter.
	t.Run("RefusesWorldReadable", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "dmctl.json")
		if err := os.WriteFile(path, []byte(`{"current":"lab","contexts":{"lab":{"server":"http://x"}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := run(t, map[string]string{"DMCTL_CONFIG": path}, "status")
		if !errors.Is(err, dmctl.ErrConfigPermissions) {
			t.Fatalf("err = %v, want ErrConfigPermissions", err)
		}
	})

	t.Run("UnknownContext", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "dmctl.json")
		if err := os.WriteFile(path, []byte(`{"current":"lab","contexts":{"lab":{"server":"http://x"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := run(t, map[string]string{"DMCTL_CONFIG": path}, "-context", "nope", "status")
		if !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	t.Run("MalformedConfig", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "dmctl.json")
		if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := run(t, map[string]string{"DMCTL_CONFIG": path}, "status"); err == nil {
			t.Fatal("a malformed config was accepted")
		}
	})

	t.Run("DefaultPath", func(t *testing.T) {
		got := dmctl.DefaultConfigPath(func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return "/tmp/cfg"
			}
			return ""
		})
		if got != filepath.Join("/tmp/cfg", "go-apple-dm", "dmctl.json") {
			t.Fatalf("DefaultConfigPath = %q", got)
		}
		if dmctl.DefaultConfigPath(func(string) string { return "" }) != "" {
			t.Fatal("DefaultConfigPath with no HOME should be empty")
		}
	})
}

// A token may be given inline, from a file, or from a named environment
// variable, but never as a positional argument, because argv is readable by
// every process on the machine.
func TestTokenSpecs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-value" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"Role":"all"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, spec := range map[string]string{
		"inline": "secret-value",
		"file":   "@" + tokenFile,
		"env":    "env:SOME_TOKEN",
	} {
		env := noConfig(t)
		env["DMCTL_SERVER"] = srv.URL
		env["SOME_TOKEN"] = "secret-value"
		if _, _, err := run(t, env, "-token", spec, "status"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	env := noConfig(t)
	env["DMCTL_SERVER"] = srv.URL
	if _, _, err := run(t, env, "-token", "@"+filepath.Join(dir, "absent"), "status"); err == nil {
		t.Fatal("a missing token file was accepted")
	}
	if _, _, err := run(t, env, "-token", "env:UNSET_TOKEN", "status"); err == nil {
		t.Fatal("an empty environment variable was accepted")
	}
}

// The break-glass token is root, bypasses policy, has no expiry and cannot be
// revoked without a restart, so status says so plainly and says what to do
// about it. Reading logs should not be the only way to find out it is still
// set.
func TestStatusReportsBreakGlass(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
		gone string
	}{
		"ActiveBesideAStore": {
			body: `{"Role":"all","Version":"devel","Families":["ddm"],"Policy":true,"BreakGlass":true}`,
			want: "unset DM_ADMIN_TOKEN",
		},
		"TheOnlyCredential": {
			body: `{"Role":"all","Version":"devel","Families":["ddm"],"Policy":false,"BreakGlass":true}`,
			want: "the only credential",
		},
		"Removed": {
			body: `{"Role":"all","Version":"devel","Families":["ddm"],"Policy":true,"BreakGlass":false}`,
			want: "not configured",
			gone: "unset DM_ADMIN_TOKEN",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			env := noConfig(t)
			env["DMCTL_SERVER"] = srv.URL
			env["DMCTL_TOKEN"] = "tok"
			out, _, err := run(t, env, "status")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("status output missing %q:\n%s", tc.want, out)
			}
			if tc.gone != "" && strings.Contains(out, tc.gone) {
				t.Fatalf("status output should not contain %q:\n%s", tc.gone, out)
			}
		})
	}
}

// The trail is only useful if the questions an investigation starts from are
// the ones the CLI can ask: what happened, by whom, to which device, and
// when.
func TestAuditVerb(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/audit/7"):
			_, _ = w.Write([]byte(`{"ID":7,"Type":"command-queued","Actor":"ops"}`))
		case strings.HasSuffix(r.URL.Path, "/audit"):
			_, _ = w.Write([]byte(`{"Items":[{"ID":2,"At":"2026-09-03T12:00:00Z","Type":"admin-action","Actor":"break-glass","Enrollment":"","Fields":{"Action":"erase"}}],"NextCursor":""}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"Error":"not found"}`))
		}
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"] = srv.URL
	env["DMCTL_TOKEN"] = "tok"

	t.Run("List", func(t *testing.T) {
		out, _, err := run(t, env, "audit", "list")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"ID", "ACTOR", "admin-action", "break-glass"} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q:\n%s", want, out)
			}
		}
	})

	// -since takes an age because that is how the question is asked; the
	// wire carries an absolute instant so the server never guesses whose
	// clock a relative window belongs to.
	t.Run("SinceBecomesAnInstant", func(t *testing.T) {
		seen = nil
		if _, _, err := run(t, env, "audit", "list", "-since", "1h"); err != nil {
			t.Fatal(err)
		}
		if len(seen) == 0 || !strings.Contains(seen[0], "since=") {
			t.Fatalf("request = %v", seen)
		}
		if strings.Contains(seen[0], "since=1h") {
			t.Fatalf("the age was sent verbatim: %s", seen[0])
		}
	})

	t.Run("Filters", func(t *testing.T) {
		seen = nil
		_, _, err := run(t, env, "audit", "list", "-type", "admin-action", "-actor", "ops", "-enrollment", "UDID-1")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"type=admin-action", "actor=ops", "enrollment=UDID-1"} {
			if !strings.Contains(seen[0], want) {
				t.Fatalf("request %q missing %q", seen[0], want)
			}
		}
	})

	t.Run("Get", func(t *testing.T) {
		out, _, err := run(t, env, "audit", "get", "7")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "command-queued") {
			t.Fatalf("output = %s", out)
		}
	})

	t.Run("BadAge", func(t *testing.T) {
		if _, _, err := run(t, env, "audit", "list", "-since", "yesterday"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	t.Run("NoSubcommand", func(t *testing.T) {
		if _, _, err := run(t, env, "audit"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	t.Run("UnknownSubcommand", func(t *testing.T) {
		if _, _, err := run(t, env, "audit", "purge"); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})
}

// A config context names a server, which is most of the point of having
// contexts. The fallback that read it was guarded by `e.opts.server == ""`,
// which defaultsFromEnv made impossible, so the field was dead: every
// invocation went to the built-in default unless -server or DMCTL_SERVER
// said otherwise.
func TestServerPrecedence(t *testing.T) {
	newServer := func(t *testing.T, name string, hit *string) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*hit = name
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Role":"all","Version":"devel"}`))
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("ContextServerIsUsed", func(t *testing.T) {
		var hit string
		srv := newServer(t, "context", &hit)
		dir := t.TempDir()
		path := filepath.Join(dir, "dmctl.json")
		body := `{"current":"lab","contexts":{"lab":{"server":"` + srv.URL + `","token":"t"}}}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := run(t, map[string]string{"DMCTL_CONFIG": path}, "status"); err != nil {
			t.Fatalf("status: %v", err)
		}
		if hit != "context" {
			t.Fatal("the context's server was ignored")
		}
	})

	t.Run("FlagBeatsContext", func(t *testing.T) {
		var ctxHit, flagHit string
		ctxSrv := newServer(t, "context", &ctxHit)
		flagSrv := newServer(t, "flag", &flagHit)
		dir := t.TempDir()
		path := filepath.Join(dir, "dmctl.json")
		body := `{"current":"lab","contexts":{"lab":{"server":"` + ctxSrv.URL + `","token":"t"}}}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"DMCTL_CONFIG": path}
		if _, _, err := run(t, env, "-server", flagSrv.URL, "status"); err != nil {
			t.Fatalf("status: %v", err)
		}
		if flagHit != "flag" || ctxHit != "" {
			t.Fatalf("flag=%q context=%q; the flag must win", flagHit, ctxHit)
		}
	})

	t.Run("EnvBeatsContext", func(t *testing.T) {
		var ctxHit, envHit string
		ctxSrv := newServer(t, "context", &ctxHit)
		envSrv := newServer(t, "env", &envHit)
		dir := t.TempDir()
		path := filepath.Join(dir, "dmctl.json")
		body := `{"current":"lab","contexts":{"lab":{"server":"` + ctxSrv.URL + `","token":"t"}}}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"DMCTL_CONFIG": path, "DMCTL_SERVER": envSrv.URL, "DMCTL_TOKEN": "t"}
		if _, _, err := run(t, env, "status"); err != nil {
			t.Fatalf("status: %v", err)
		}
		if envHit != "env" || ctxHit != "" {
			t.Fatalf("env=%q context=%q; the environment must win", envHit, ctxHit)
		}
	})

	// With nothing configured the built-in default still applies, so the
	// common local case needs no flags.
	t.Run("FallsBackToTheDefault", func(t *testing.T) {
		_, _, err := run(t, noConfig(t), "status")
		// Nothing is listening there in a test, so this is a transport
		// failure rather than a usage error: the point is that a server was
		// chosen at all.
		if errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("no default server was applied: %v", err)
		}
	})
}
