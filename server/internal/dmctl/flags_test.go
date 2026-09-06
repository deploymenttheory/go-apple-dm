package dmctl_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

// Everything after "--" is positional, so an argument that looks like a flag
// can still be passed.
func TestDoubleDashEndsFlags(t *testing.T) {
	env := noConfig(t)
	_, errOut, err := run(t, env, "explain", "--", "-not-a-flag")
	if !errors.Is(err, dmctl.ErrUsage) {
		t.Fatalf("err = %v, want a not-found usage error", err)
	}
	// It was treated as an identifier to look up, not as an unknown flag.
	if strings.Contains(errOut, "flag provided but not defined") {
		t.Fatalf("the argument after -- was parsed as a flag:\n%s", errOut)
	}
}

// An unknown flag after a positional argument is still reported by name
// rather than silently ignored.
func TestUnknownFlagAfterPositional(t *testing.T) {
	_, errOut, err := run(t, noConfig(t), "explain", "DeviceLock", "-nope")
	if !errors.Is(err, dmctl.ErrUsage) {
		t.Fatalf("err = %v, want ErrUsage", err)
	}
	if !strings.Contains(errOut, "nope") {
		t.Fatalf("the unknown flag was not named:\n%s", errOut)
	}
}

// -h prints help and succeeds rather than being an error.
func TestHelpFlags(t *testing.T) {
	if _, _, err := run(t, noConfig(t), "-h"); err != nil {
		t.Fatalf("top-level -h: %v", err)
	}
	if _, _, err := run(t, noConfig(t), "explain", "-h"); err != nil {
		t.Fatalf("verb -h: %v", err)
	}
}

// A flag written as -flag=value keeps its value when reordered.
func TestFlagWithEqualsAfterPositional(t *testing.T) {
	env := noConfig(t)
	out, _, err := run(t, env, "explain", "DeviceLock", "-target=macos:15.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Target") {
		t.Fatalf("-flag=value after a positional was ignored:\n%s", out)
	}
}

func TestConfigTokenSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer from-config" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"Role":"all"}`))
	}))
	defer srv.Close()

	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "dmctl.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("TokenFile", func(t *testing.T) {
		dir := t.TempDir()
		tok := filepath.Join(dir, "tok")
		if err := os.WriteFile(tok, []byte("from-config\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := write(t, `{"current":"lab","contexts":{"lab":{"server":"`+srv.URL+`","token_file":"`+tok+`"}}}`)
		if _, _, err := run(t, map[string]string{"DMCTL_CONFIG": path, "DMCTL_SERVER": srv.URL}, "status"); err != nil {
			t.Fatalf("token_file: %v", err)
		}
	})

	// An inlined token still works; it is writing one that takes a flag.
	t.Run("InlineToken", func(t *testing.T) {
		path := write(t, `{"current":"lab","contexts":{"lab":{"server":"`+srv.URL+`","token":"from-config"}}}`)
		if _, _, err := run(t, map[string]string{"DMCTL_CONFIG": path, "DMCTL_SERVER": srv.URL}, "status"); err != nil {
			t.Fatalf("inline token: %v", err)
		}
	})

	t.Run("MissingTokenFile", func(t *testing.T) {
		path := write(t, `{"current":"lab","contexts":{"lab":{"server":"x","token_file":"/no/such/file"}}}`)
		if _, _, err := run(t, map[string]string{"DMCTL_CONFIG": path}, "status"); err == nil {
			t.Fatal("a missing token file was accepted")
		}
	})

	t.Run("EmptyTokenEnv", func(t *testing.T) {
		path := write(t, `{"current":"lab","contexts":{"lab":{"server":"x","token_env":"UNSET"}}}`)
		if _, _, err := run(t, map[string]string{"DMCTL_CONFIG": path}, "status"); err == nil {
			t.Fatal("an unset token variable was accepted")
		}
	})

	// No "current" and no -context means no credential rather than a crash.
	t.Run("NoCurrentContext", func(t *testing.T) {
		path := write(t, `{"contexts":{"lab":{"server":"x"}}}`)
		env := map[string]string{"DMCTL_CONFIG": path, "DMCTL_SERVER": srv.URL}
		if _, _, err := run(t, env, "status"); err == nil {
			t.Fatal("expected the request to be refused with no credential")
		}
	})
}

func TestDefaultConfigPathHomeFallback(t *testing.T) {
	got := dmctl.DefaultConfigPath(func(k string) string {
		if k == "HOME" {
			return "/home/op"
		}
		return ""
	})
	want := filepath.Join("/home/op", ".config", "go-apple-dm", "dmctl.json")
	if got != want {
		t.Fatalf("DefaultConfigPath = %q, want %q", got, want)
	}
	// DMCTL_CONFIG wins over both.
	if got := dmctl.DefaultConfigPath(func(k string) string {
		if k == "DMCTL_CONFIG" {
			return "/explicit.json"
		}
		return "/home/op"
	}); got != "/explicit.json" {
		t.Fatalf("explicit path = %q", got)
	}
}

// -all means the same thing in every output mode: json streams rather than
// printing one page.
func TestJSONWithAllStreams(t *testing.T) {
	f, env := fakeServer(t)
	out, _, err := run(t, env, "-output", "json", "-all", "principals", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want one per item across both pages:\n%s", len(lines), out)
	}
	if f.last().query == "" {
		t.Fatal("the second page was never requested")
	}
}

// A bad flag on a subcommand is a usage error rather than being taken as the
// name of the thing to act on.
func TestBadFlagOnSubcommands(t *testing.T) {
	_, env := fakeServer(t)
	for _, args := range [][]string{
		{"principals", "get", "-bogus", "x"},
		{"principals", "create", "-bogus", "x"},
		{"principals", "set-roles", "-bogus", "x"},
		{"policies", "get", "-bogus", "x"},
		{"policies", "put", "-bogus", "x"},
		{"declarations", "get", "-bogus", "x"},
		{"declarations", "put", "-bogus"},
		{"status", "-bogus"},
		{"routes", "-bogus"},
		{"actions", "-bogus"},
	} {
		if _, _, err := run(t, env, args...); !errors.Is(err, dmctl.ErrUsage) {
			t.Errorf("%v: err = %v, want ErrUsage", args, err)
		}
	}
}

// A config path that cannot be read is an error rather than a silent fallback
// to no credential, which would turn a permissions problem into a confusing
// 401 from the server.
func TestUnreadableConfig(t *testing.T) {
	dir := t.TempDir()
	// A directory where a file belongs: stat succeeds, reading does not.
	if err := os.Mkdir(filepath.Join(dir, "dmctl.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, map[string]string{"DMCTL_CONFIG": filepath.Join(dir, "dmctl.json")}, "status"); err == nil {
		t.Fatal("an unreadable config was accepted")
	}
}

// When the server cannot describe itself, the original error survives rather
// than being replaced by a failure to explain it.
func TestNotFoundWithUnreachableConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"Error":"not found"}`))
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"] = srv.URL
	env["DMCTL_TOKEN"] = "tok"
	_, _, err := run(t, env, "principals", "list")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want the original not-found", err)
	}
	if strings.Contains(err.Error(), "role=") {
		t.Fatalf("err = %v, want no role explanation when /config is unavailable", err)
	}
}
