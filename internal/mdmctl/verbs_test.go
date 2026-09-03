package mdmctl_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/mdmctl"
)

// fakeAdmin records what the CLI sent and answers like the real admin API.
type fakeAdmin struct {
	mu       sync.Mutex
	requests []request
	pages    int
}

type request struct {
	method, path, query, body string
}

func (f *fakeAdmin) last() request {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return request{}
	}
	return f.requests[len(f.requests)-1]
}

func (f *fakeAdmin) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, request{r.Method, r.URL.Path, r.URL.RawQuery, string(body)})
	f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/admin/v1")
	switch {
	case path == "/config":
		_, _ = w.Write([]byte(`{"Role":"all","Version":"devel","Families":["ddm","principals"],"Policy":true}`))
	case path == "/principals" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Principal":{"Name":"ci","Roles":["reader"]},"Token":"mdmt_freshtoken"}`))
	case path == "/principals" && r.Method == http.MethodGet:
		// Two pages, so -all has something to follow.
		f.mu.Lock()
		cursor := r.URL.Query().Get("cursor")
		f.mu.Unlock()
		if cursor == "" {
			_, _ = w.Write([]byte(`{"Items":[{"Name":"a","Roles":["r"],"TokenID":"t1"}],"NextCursor":"c1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"Items":[{"Name":"b","Roles":[],"TokenID":""}]}`))
	case strings.HasSuffix(path, "/rotate"):
		_, _ = w.Write([]byte(`{"Principal":{"Name":"ci"},"Token":"mdmt_rotated"}`))
	case strings.HasSuffix(path, "/revoke"):
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(path, "/principals/") && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"Name":"ci","Roles":["reader"]}`))
	case strings.HasPrefix(path, "/principals/") && r.Method == http.MethodPatch:
		_, _ = w.Write([]byte(`{"Name":"ci","Roles":["ops"]}`))
	case strings.HasPrefix(path, "/principals/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case path == "/policies":
		_, _ = w.Write([]byte(`{"Items":[{"Name":"ops","Description":"lets ops notify"}]}`))
	case strings.HasPrefix(path, "/policies/") && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"Name":"ops","Source":"permit (principal, action, resource);\n"}`))
	case strings.HasPrefix(path, "/policies/") && r.Method == http.MethodPut:
		_, _ = w.Write([]byte(`{"Name":"ops"}`))
	case strings.HasPrefix(path, "/policies/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case path == "/declarations" && r.Method == http.MethodPut:
		_, _ = w.Write([]byte(`{"Identifier":"com.example.a","Changed":true}`))
	case strings.HasPrefix(path, "/declarations/") && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"Type":"com.apple.configuration.management.test"}`))
	case strings.HasPrefix(path, "/declarations/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"Error":"not found"}`))
	}
}

func fakeServer(t *testing.T) (*fakeAdmin, map[string]string) {
	t.Helper()
	f := &fakeAdmin{}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	env := noConfig(t)
	env["MDMCTL_SERVER"] = srv.URL
	env["MDMCTL_TOKEN"] = "tok"
	return f, env
}

func TestPrincipalVerbs(t *testing.T) {
	f, env := fakeServer(t)

	// A minted credential goes to stdout alone, so it can be captured, with
	// the explanation on stderr.
	t.Run("Create", func(t *testing.T) {
		out, errOut, err := run(t, env, "principals", "create", "ci", "-roles", "reader")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(out) != "mdmt_freshtoken" {
			t.Fatalf("stdout = %q, want the token alone", out)
		}
		if !strings.Contains(errOut, "cannot be shown again") {
			t.Fatalf("stderr = %q", errOut)
		}
		got := f.last()
		if got.method != http.MethodPost || !strings.Contains(got.body, `"Roles"`) {
			t.Fatalf("request = %+v", got)
		}
	})

	t.Run("CreateNeedsAName", func(t *testing.T) {
		if _, _, err := run(t, env, "principals", "create"); !errors.Is(err, mdmctl.ErrUsage) {
			t.Fatalf("err = %v, want ErrUsage", err)
		}
	})

	t.Run("Rotate", func(t *testing.T) {
		out, _, err := run(t, env, "principals", "rotate", "ci")
		if err != nil || strings.TrimSpace(out) != "mdmt_rotated" {
			t.Fatalf("out = %q, %v", out, err)
		}
	})

	t.Run("RevokeGetDelete", func(t *testing.T) {
		for _, args := range [][]string{
			{"principals", "revoke", "ci"},
			{"principals", "get", "ci"},
			{"principals", "delete", "ci"},
		} {
			if _, _, err := run(t, env, args...); err != nil {
				t.Errorf("%v: %v", args, err)
			}
		}
	})

	t.Run("SetRoles", func(t *testing.T) {
		if _, _, err := run(t, env, "principals", "set-roles", "ci", "-roles", "ops"); err != nil {
			t.Fatal(err)
		}
		if got := f.last(); got.method != http.MethodPatch || !strings.Contains(got.body, "ops") {
			t.Fatalf("request = %+v", got)
		}
		if _, _, err := run(t, env, "principals", "set-roles"); !errors.Is(err, mdmctl.ErrUsage) {
			t.Fatal("set-roles with no name should be a usage error")
		}
	})

	t.Run("NamesAreRequired", func(t *testing.T) {
		for _, sub := range []string{"get", "rotate", "revoke", "delete"} {
			if _, _, err := run(t, env, "principals", sub); !errors.Is(err, mdmctl.ErrUsage) {
				t.Errorf("principals %s with no name: %v", sub, err)
			}
		}
	})

	// Without -all one page prints and the cursor goes to stderr, so stdout
	// stays machine-clean for a pipe.
	t.Run("PaginationCursorGoesToStderr", func(t *testing.T) {
		out, errOut, err := run(t, env, "principals", "list")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "next cursor") {
			t.Fatalf("the cursor hint reached stdout:\n%s", out)
		}
		if !strings.Contains(errOut, "next cursor") {
			t.Fatalf("no cursor hint on stderr:\n%s", errOut)
		}
		if strings.Contains(out, "\nb\t") || strings.Contains(out, "  b ") {
			t.Fatalf("the second page printed without -all:\n%s", out)
		}
	})

	t.Run("AllFollowsTheCursor", func(t *testing.T) {
		out, _, err := run(t, env, "principals", "list", "-all")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "a") || !strings.Contains(out, "b") {
			t.Fatalf("-all did not follow the cursor:\n%s", out)
		}
	})

	t.Run("NDJSONStreamsItems", func(t *testing.T) {
		out, _, err := run(t, env, "-output", "ndjson", "principals", "list")
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("ndjson lines = %d, want one per item across both pages:\n%s", len(lines), out)
		}
		for _, l := range lines {
			if !strings.HasPrefix(l, "{") {
				t.Fatalf("line is not one JSON object: %q", l)
			}
		}
	})

	t.Run("LimitReachesTheQuery", func(t *testing.T) {
		if _, _, err := run(t, env, "principals", "list", "-limit", "7"); err != nil {
			t.Fatal(err)
		}
		if got := f.last(); !strings.Contains(got.query, "limit=7") {
			t.Fatalf("query = %q, want limit=7", got.query)
		}
	})
}

func TestPolicyVerbs(t *testing.T) {
	f, env := fakeServer(t)

	t.Run("List", func(t *testing.T) {
		out, _, err := run(t, env, "policies", "list")
		if err != nil || !strings.Contains(out, "lets ops notify") {
			t.Fatalf("out = %q, %v", out, err)
		}
	})

	// A policy is printed exactly as stored, so an operator edits what they
	// wrote rather than a reformatting of it.
	t.Run("GetPrintsTheSourceVerbatim", func(t *testing.T) {
		out, _, err := run(t, env, "policies", "get", "ops")
		if err != nil {
			t.Fatal(err)
		}
		if out != "permit (principal, action, resource);\n" {
			t.Fatalf("source = %q, want it byte for byte", out)
		}
	})

	t.Run("PutFromFile", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "p.cedar")
		const src = "permit (principal, action, resource);\n"
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := run(t, env, "policies", "put", "ops", "-file", path, "-description", "note"); err != nil {
			t.Fatal(err)
		}
		got := f.last()
		if !strings.Contains(got.body, "permit") || !strings.Contains(got.body, "note") {
			t.Fatalf("body = %q", got.body)
		}
	})

	t.Run("PutFromStdin", func(t *testing.T) {
		var out, errOut strings.Builder
		err := mdmctl.Run(context.Background(),
			[]string{"policies", "put", "ops"},
			func(k string) string { return env[k] },
			strings.NewReader("permit (principal, action, resource);"),
			&out, &errOut)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(f.last().body, "permit") {
			t.Fatalf("stdin was not sent: %q", f.last().body)
		}
	})

	t.Run("MissingFile", func(t *testing.T) {
		if _, _, err := run(t, env, "policies", "put", "ops", "-file", "/no/such/file"); err == nil {
			t.Fatal("a missing policy file was accepted")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if _, _, err := run(t, env, "policies", "delete", "ops"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("NamesAreRequired", func(t *testing.T) {
		for _, sub := range []string{"get", "put", "delete"} {
			if _, _, err := run(t, env, "policies", sub); !errors.Is(err, mdmctl.ErrUsage) {
				t.Errorf("policies %s with no name: %v", sub, err)
			}
		}
	})
}

func TestDeclarationVerbs(t *testing.T) {
	f, env := fakeServer(t)

	t.Run("Get", func(t *testing.T) {
		out, _, err := run(t, env, "declarations", "get", "com.example.a")
		if err != nil || !strings.Contains(out, "com.apple.configuration") {
			t.Fatalf("out = %q, %v", out, err)
		}
	})

	t.Run("PutFromFile", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "d.json")
		if err := os.WriteFile(path, []byte(`{"Type":"x","Identifier":"y","Payload":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := run(t, env, "declarations", "put", "-file", path); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(f.last().body, `"Identifier"`) {
			t.Fatalf("body = %q", f.last().body)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if _, _, err := run(t, env, "declarations", "delete", "com.example.a"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("NamesAreRequired", func(t *testing.T) {
		for _, sub := range []string{"get", "delete"} {
			if _, _, err := run(t, env, "declarations", sub); !errors.Is(err, mdmctl.ErrUsage) {
				t.Errorf("declarations %s with no name: %v", sub, err)
			}
		}
	})
}

// A 404 that is really a role split becomes a sentence rather than a mystery.
// No reference server has roles, so none of their CLIs needs this.
func TestNotFoundExplainsRole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	_, _, err := run(t, env, "principals", "list")
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "role=ddm") {
		t.Fatalf("err = %v, want the role named", err)
	}

	// A family the server does serve keeps the plain error.
	_, _, err = run(t, env, "declarations", "get", "nope")
	if err == nil || strings.Contains(err.Error(), "role=") {
		t.Fatalf("err = %v, want the unadorned not-found", err)
	}
}

// Every verb surfaces a server failure rather than reporting success. A CLI
// that exits 0 on a 500 is worse than one that cannot reach the server at all.
func TestEveryVerbSurfacesAServerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"Error":"internal error"}`))
	}))
	defer srv.Close()
	env := noConfig(t)
	env["MDMCTL_SERVER"] = srv.URL
	env["MDMCTL_TOKEN"] = "tok"

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte(`permit (principal, action, resource);`), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, args := range map[string][]string{
		"status":              {"status"},
		"routes":              {"routes"},
		"actions":             {"actions"},
		"principals list":     {"principals", "list"},
		"principals get":      {"principals", "get", "x"},
		"principals create":   {"principals", "create", "x"},
		"principals rotate":   {"principals", "rotate", "x"},
		"principals revoke":   {"principals", "revoke", "x"},
		"principals delete":   {"principals", "delete", "x"},
		"principals set-role": {"principals", "set-roles", "x", "-roles", "a"},
		"policies list":       {"policies", "list"},
		"policies get":        {"policies", "get", "x"},
		"policies put":        {"policies", "put", "x", "-file", src},
		"policies delete":     {"policies", "delete", "x"},
		"declarations get":    {"declarations", "get", "x"},
		"declarations put":    {"declarations", "put", "-file", src},
		"declarations delete": {"declarations", "delete", "x"},
	} {
		if _, _, err := run(t, env, args...); err == nil {
			t.Errorf("%s: a 500 produced no error", name)
		} else if mdmctl.ExitCode(err) != mdmctl.ExitFailed {
			t.Errorf("%s: exit = %d, want ExitFailed", name, mdmctl.ExitCode(err))
		}
	}
}

// "-file -" reads stdin, the same as omitting the flag.
func TestReadSourceDash(t *testing.T) {
	f, env := fakeServer(t)
	var out, errBuf strings.Builder
	err := mdmctl.Run(context.Background(),
		[]string{"policies", "put", "ops", "-file", "-"},
		func(k string) string { return env[k] },
		strings.NewReader("permit (principal, action, resource);"),
		&out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.last().body, "permit") {
		t.Fatalf("stdin was not sent: %q", f.last().body)
	}
}

// In a machine mode the whole creation response is emitted, not just the
// token, so a script can read the principal back alongside its credential.
func TestCreateJSONEmitsTheWholeBody(t *testing.T) {
	_, env := fakeServer(t)
	out, _, err := run(t, env, "-output", "json", "principals", "create", "ci")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"Principal"`) || !strings.Contains(out, "mdmt_freshtoken") {
		t.Fatalf("json create = %q", out)
	}
}
