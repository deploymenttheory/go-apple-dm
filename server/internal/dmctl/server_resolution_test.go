package dmctl

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/adminclient"
)

// TestClientServerResolution checks server precedence: the -server flag or
// DMCTL_SERVER, then the selected configuration context, then DefaultServer.
// Each request uses a cancelled context, so the resolved target is read from
// the request trace without contacting any listener.
func TestClientServerResolution(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(Config{Current: "lab", Contexts: map[string]Context{"lab": {Server: "https://config.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(config, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "absent.json")
	for _, tc := range []struct {
		name, flag, server, config, want string
	}{
		{"built-in default", "", "", absent, DefaultServer},
		{"configuration context", "", "", config, "https://config.example"},
		{"environment over context", "", "https://env.example", config, "https://env.example"},
		{"flag over environment", "https://flag.example", "https://env.example", config, "https://flag.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{EnvServer: tc.server, EnvConfig: tc.config}
			getenv := func(name string) string { return vars[name] }
			var trace bytes.Buffer
			e := &env{opts: defaultsFromEnv(getenv), stderr: &trace, getenv: getenv}
			if tc.flag != "" {
				e.opts.server = tc.flag
			}
			e.opts.verbose = true
			c, err := e.client()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err = c.Do(ctx, http.MethodGet, "/devices", nil, nil); err == nil {
				t.Fatal("cancelled request succeeded")
			}
			if want := "GET " + tc.want + adminclient.Prefix + "/devices"; !strings.Contains(trace.String(), want) {
				t.Fatalf("request target = %q, want %q", trace.String(), want)
			}
		})
	}
}
