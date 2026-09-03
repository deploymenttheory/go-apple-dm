package mdmctl_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/mdmctl"
)

// failWriter fails after n successful writes, so a write error partway
// through rendering is reachable.
type failWriter struct{ remaining int }

var errWrite = errors.New("disk full")

func (f *failWriter) Write(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, errWrite
	}
	f.remaining--
	return len(p), nil
}

// jsonServer answers every path with body.
func jsonServer(t *testing.T, body string) map[string]string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	env := noConfig(t)
	env["MDMCTL_SERVER"] = srv.URL
	env["MDMCTL_TOKEN"] = "tok"
	return env
}

// A body the CLI cannot decode is printed as it arrived rather than swallowed:
// an operator seeing the raw answer can tell a server bug from a client one.
func TestMalformedBodyIsPrintedRaw(t *testing.T) {
	// Types that cannot decode into the expected shapes: a number where a
	// string belongs, a scalar where a list belongs.
	const body = `{"Role":123,"Routes":5,"Items":7}`
	env := jsonServer(t, body)
	for _, verb := range []string{"status", "routes", "actions"} {
		out, _, err := run(t, env, verb)
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if !strings.Contains(out, "123") {
			t.Fatalf("%s did not print the body it could not decode:\n%s", verb, out)
		}
	}
}

// A listing whose items are not objects still renders rather than panicking.
func TestListingWithOddItems(t *testing.T) {
	env := jsonServer(t, `{"Items":[1,"two",{"Name":"three"}]}`)
	out, _, err := run(t, env, "principals", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "three") {
		t.Fatalf("the object item did not render:\n%s", out)
	}
}

// A write failure surfaces rather than being dropped, in every output mode.
func TestWriteFailuresSurface(t *testing.T) {
	env := jsonServer(t, `{"Items":[{"Name":"a"}],"Role":"all"}`)
	getenv := func(k string) string { return env[k] }
	for name, args := range map[string][]string{
		"human table": {"principals", "list"},
		"json":        {"-output", "json", "status"},
		"ndjson":      {"-output", "ndjson", "principals", "list"},
	} {
		var errBuf strings.Builder
		err := mdmctl.Run(context.Background(), args, getenv, strings.NewReader(""),
			&failWriter{}, &errBuf)
		if err == nil {
			t.Errorf("%s: a failing stdout produced no error", name)
		}
	}
}

// An explain rendering failure is reported too.
func TestExplainWriteFailure(t *testing.T) {
	var errBuf strings.Builder
	err := mdmctl.Run(context.Background(), []string{"explain", "DeviceLock"},
		func(string) string { return "" }, strings.NewReader(""),
		&failWriter{}, &errBuf)
	if err == nil {
		t.Fatal("a failing stdout produced no error")
	}
}

// failReader fails on the first read, so a stdin failure is reachable.
type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("pipe broken") }

func TestStdinFailureSurfaces(t *testing.T) {
	env := jsonServer(t, `{"Name":"ops"}`)
	var out, errBuf strings.Builder
	err := mdmctl.Run(context.Background(), []string{"policies", "put", "ops"},
		func(k string) string { return env[k] }, failReader{}, &out, &errBuf)
	if err == nil {
		t.Fatal("a failing stdin produced no error")
	}
}

// A response the CLI cannot decode falls back to printing it, rather than
// showing an empty answer that looks like success.
func TestUndecodableResponsesFallBack(t *testing.T) {
	t.Run("PolicySource", func(t *testing.T) {
		env := jsonServer(t, `{"Source":123}`)
		out, _, err := run(t, env, "policies", "get", "ops")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "123") {
			t.Fatalf("out = %q, want the raw body", out)
		}
	})

	t.Run("CreatedToken", func(t *testing.T) {
		env := jsonServer(t, `{"Token":123}`)
		out, _, err := run(t, env, "principals", "create", "ci")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "123") {
			t.Fatalf("out = %q, want the raw body", out)
		}
	})
}
