package app_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/ddmsync"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func build(t *testing.T, cfg app.Config) *app.App {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = quiet
	}
	// A persistent store refuses to run without a keyring, so a test that names
	// one gets a key even when it is about something else.
	if cfg.Secrets == nil && cfg.SecretsDir == "" {
		if cfg.Storage != "inmem" && len(cfg.StorageKeys) == 0 {
			cfg.StorageKeys = []string{"test"}
		}
		if len(cfg.StorageKeys) > 0 {
			material := secrets.Static{}
			for _, name := range cfg.StorageKeys {
				material[name] = []byte("0123456789abcdef0123456789abcdef")
			}
			cfg.Secrets = material
		}
	}
	a, err := app.Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	fixtureAdmin(t, a, cfg.BootstrapToken, cfg.DSN)
	return a
}

func serve(t *testing.T, a *app.App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestParseEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string {
			// A persistent store needs a keyring, and sqlite is the default, so
			// every ParseEnv case supplies one unless it sets its own.
			if k == app.EnvStorageKeys && m[k] == "" {
				return "test"
			}
			return m[k]
		}
	}
	t.Run("Defaults", func(t *testing.T) {
		cfg, err := app.ParseEnv(env(nil))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Listen != app.DefaultListen || cfg.Storage != "sqlite" ||
			cfg.DSN != "dm.db" ||
			!cfg.Subscriptions {
			t.Fatalf("cfg = %+v", cfg)
		}
	})
	t.Run("Overrides", func(t *testing.T) {
		cfg, err := app.ParseEnv(env(map[string]string{
			app.EnvListen:         ":9",
			app.EnvStorage:        "postgres",
			app.EnvDSN:            "postgres://x",
			app.EnvBootstrapToken: "t",
			app.EnvSubscriptions:  "false",
			app.EnvCAFile:         "ca.pem",
			app.EnvCertHeader:     "X-Cert",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BootstrapToken != "t" || cfg.Subscriptions ||
			cfg.CAFile != "ca.pem" ||
			cfg.CertHeader != "X-Cert" {
			t.Fatalf("cfg = %+v", cfg)
		}
	})

	t.Run("Inmem", func(t *testing.T) {
		cfg, err := app.ParseEnv(env(map[string]string{app.EnvStorage: "inmem"}))
		if err != nil || cfg.DSN != "" {
			t.Fatalf("cfg = %+v, err = %v", cfg, err)
		}
	})
	t.Run("BadBool", func(t *testing.T) {
		if _, err := app.ParseEnv(
			env(map[string]string{app.EnvSubscriptions: "maybe"}),
		); !errors.Is(
			err,
			app.ErrConfig,
		) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("Invalid", func(t *testing.T) {
		if _, err := app.ParseEnv(
			env(map[string]string{"DM_ROLE": "proxy"}),
		); !errors.Is(
			err,
			app.ErrConfig,
		) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestBuild(t *testing.T) {
	ctx := context.Background()

	t.Run("AllRole", func(t *testing.T) {
		dir := t.TempDir()
		a := build(t, app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "all.db"), BootstrapToken: "t"})
		if a.Core == nil || a.Engine == nil {
			t.Fatal("all role wires everything")
		}
		srv := serve(t, a)
		if got := get(t, srv.URL+"/healthz", ""); got != http.StatusOK {
			t.Fatalf("healthz = %d", got)
		}
		// The engine store lives in the same SQLite file: a declaration survives a rebuild.
		res := do(t, srv, "PUT", "/admin/v1/declarations", "t", propsDecl("com.example.persist"))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("put = %d", res.StatusCode)
		}
		_ = a.Close()
		b := build(t, app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "all.db"), BootstrapToken: "t"})
		if _, err := b.Engine.GetDeclaration(ctx, "com.example.persist"); err != nil {
			t.Fatalf("after reopen: %v", err)
		}
	})
	t.Run("CAFile", func(t *testing.T) {
		ca, err := testpki.NewCA("file CA")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw}), 0o600); err != nil {
			t.Fatal(err)
		}
		a := build(t, app.Config{Storage: "inmem", CAFile: path})
		srv := serve(t, a)
		// Signature verification is active: an unsigned check-in is refused.
		if got := put(t, srv.URL+"/mdm", "application/x-apple-aspen-mdm-checkin", []byte("<plist/>")); got != http.StatusBadRequest {
			t.Fatalf("unsigned = %d", got)
		}
	})
	t.Run("BadConfig", func(t *testing.T) {
		noCerts := filepath.Join(t.TempDir(), "empty.pem")
		if err := os.WriteFile(noCerts, []byte("not pem"), 0o600); err != nil {
			t.Fatal(err)
		}
		cases := map[string]app.Config{
			"storage":         {Storage: "redis"},
			"dsn":             {Storage: "sqlite"},
			"ca file missing": {Storage: "inmem", CAFile: filepath.Join(t.TempDir(), "nope.pem")},
			"ca file empty":   {Storage: "inmem", CAFile: noCerts},
		}
		for name, cfg := range cases {
			cfg.Logger = quiet
			if _, err := app.Build(ctx, cfg); !errors.Is(err, app.ErrConfig) {
				t.Errorf("%s: err = %v, want ErrConfig", name, err)
			}
		}
		if _, err := app.Build(ctx, app.Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "missing", "x.db"), StorageKeys: []string{"k"}, Secrets: secrets.Static{"k": []byte("0123456789abcdef0123456789abcdef")}, Logger: quiet}); err == nil {
			t.Error("sqlite in a missing directory must fail")
		}
		if _, err := app.Build(ctx, app.Config{Storage: "postgres", DSN: "postgres://127.0.0.1:1/x?sslmode=disable&connect_timeout=1", StorageKeys: []string{"k"}, Secrets: secrets.Static{"k": []byte("0123456789abcdef0123456789abcdef")}, Logger: quiet}); err == nil {
			t.Error("unreachable postgres must fail")
		}
		if _, err := app.Build(ctx, app.Config{Storage: "mysql", DSN: "dm:dm@tcp(127.0.0.1:1)/x?timeout=1s", StorageKeys: []string{"k"}, Secrets: secrets.Static{"k": []byte("0123456789abcdef0123456789abcdef")}, Logger: quiet}); err == nil {
			t.Error("unreachable mysql must fail")
		}
	})
}

func TestHealthz(t *testing.T) {
	a := build(t, app.Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "h.db")})
	srv := serve(t, a)
	if got := get(t, srv.URL+"/healthz", ""); got != http.StatusOK {
		t.Fatalf("healthz = %d", got)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if got := get(t, srv.URL+"/healthz", ""); got != http.StatusServiceUnavailable {
		t.Fatalf("healthz after close = %d, want 503", got)
	}
	if got := get(t, srv.URL+"/nope", ""); got != http.StatusServiceUnavailable {
		t.Fatalf("unknown path = %d", got)
	}
}

func TestRun(t *testing.T) {
	a := build(t, app.Config{Storage: "inmem"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// TestAdminInternalErrors closes the database under a running app: every
// admin route fails closed at the maintenance gate without leaking the cause.
func TestAdminInternalErrors(t *testing.T) {
	a := build(t, app.Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "i.db"), BootstrapToken: "t"})
	srv := serve(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	calls := []struct {
		method, path string
		status       int
	}{
		{"PUT", "/admin/v1/declarations", 503},
		{"GET", "/admin/v1/declarations/x", 500},
		{"DELETE", "/admin/v1/declarations/x", 503},
		{"PUT", "/admin/v1/sets/s/declarations/x", 503},
		{"DELETE", "/admin/v1/sets/s/declarations/x", 503},
		{"PUT", "/admin/v1/enrollments/device/D/sets/s", 500},
		{"DELETE", "/admin/v1/enrollments/device/D/sets/s", 500},
		{"GET", "/admin/v1/enrollments/device/D/declarations", 500},
		{"GET", "/admin/v1/enrollments/device/D/status", 500},
		{"GET", "/admin/v1/enrollments/device/D/status/values", 500},
		{"GET", "/admin/v1/enrollments/device/D/tokens", 500},
		{"POST", "/admin/v1/notify", 503},
	}
	for _, c := range calls {
		body := propsDecl("com.example.closed")
		res := do(t, srv, c.method, c.path, "t", body)
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(data), "server maintenance") || strings.Contains(string(data), "sql") {
			t.Errorf("%s %s = %d %s", c.method, c.path, res.StatusCode, data)
		}
	}
}

func TestAdminRequiresAuthentication(t *testing.T) {
	a := build(t, app.Config{Storage: "inmem"})
	srv := serve(t, a)
	if got := get(t, srv.URL+"/admin/v1/declarations/x", "anything"); got != http.StatusUnauthorized {
		t.Fatalf("admin without token = %d, want 401", got)
	}
}

func TestAdminAPI(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	a := build(t, app.Config{Storage: "inmem", BootstrapToken: "secret", Clock: fake})
	srv := serve(t, a)
	const dev = "/admin/v1/enrollments/device/DEV-1"
	t.Run("Auth", func(t *testing.T) {
		for _, tok := range []string{"", "wrong", "secre"} {
			res := do(t, srv, "PUT", "/admin/v1/declarations", tok, propsDecl("com.example.auth"))
			if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") == "" {
				t.Fatalf("token %q: %d", tok, res.StatusCode)
			}
		}
		if _, err := a.Engine.GetDeclaration(context.Background(), "com.example.auth"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatal("unauthenticated write reached the engine")
		}
	})
	t.Run("PutDeclaration", func(t *testing.T) {
		res := do(t, srv, "PUT", "/admin/v1/declarations", "secret", propsDecl("com.example.a"))
		var body struct {
			Identifier, Type, ServerToken string
			Changed                       bool
		}
		decode(t, res, http.StatusOK, &body)
		if body.Identifier != "com.example.a" || len(body.ServerToken) != 64 || !body.Changed {
			t.Fatalf("body = %+v", body)
		}
		decode(t, do(t, srv, "PUT", "/admin/v1/declarations", "secret", propsDecl("com.example.a")), http.StatusOK, &body)
		if body.Changed {
			t.Fatal("equivalent re-upload reported as changed")
		}
		if res := do(t, srv, "PUT", "/admin/v1/declarations", "secret", []byte(`{"Type":"nope","Identifier":"x","Payload":{}}`)); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("unknown type = %d", res.StatusCode)
		}
		if res := do(t, srv, "PUT", "/admin/v1/declarations", "secret", []byte(`{`)); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad json = %d", res.StatusCode)
		}
		if res := do(t, srv, "PUT", "/admin/v1/declarations", "secret", bytes.Repeat([]byte("x"), app.MaxAdminBody+1)); res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("too large = %d", res.StatusCode)
		}
		// Same identifier, different kind: conflict.
		conflict := []byte(`{"Type":"com.apple.activation.simple","Identifier":"com.example.a","Payload":{"StandardConfigurations":["c"]}}`)
		if res := do(t, srv, "PUT", "/admin/v1/declarations", "secret", conflict); res.StatusCode != http.StatusConflict {
			t.Fatalf("kind change = %d", res.StatusCode)
		}
	})
	t.Run("GetDelete", func(t *testing.T) {
		res := do(t, srv, "GET", "/admin/v1/declarations/com.example.a", "secret", nil)
		var d map[string]any
		decode(t, res, http.StatusOK, &d)
		if d["Identifier"] != "com.example.a" {
			t.Fatalf("get = %v", d)
		}
		if res := do(t, srv, "GET", "/admin/v1/declarations/com.example.missing", "secret", nil); res.StatusCode != http.StatusNotFound {
			t.Fatalf("missing = %d", res.StatusCode)
		}
		do(t, srv, "PUT", "/admin/v1/declarations", "secret", propsDecl("com.example.gone"))
		if res := do(t, srv, "DELETE", "/admin/v1/declarations/com.example.gone", "secret", nil); res.StatusCode != http.StatusNoContent {
			t.Fatalf("delete = %d", res.StatusCode)
		}
		if res := do(t, srv, "DELETE", "/admin/v1/declarations/com.example.gone", "secret", nil); res.StatusCode != http.StatusNotFound {
			t.Fatalf("delete again = %d", res.StatusCode)
		}
	})
	t.Run("Assign", func(t *testing.T) {
		var ch struct{ Changed bool }
		decode(t, do(t, srv, "PUT", "/admin/v1/sets/s1/declarations/com.example.a", "secret", nil), http.StatusOK, &ch)
		if !ch.Changed {
			t.Fatal("add to set not changed")
		}
		if res := do(t, srv, "PUT", "/admin/v1/sets/s1/declarations/com.example.missing", "secret", nil); res.StatusCode != http.StatusNotFound {
			t.Fatalf("add missing = %d", res.StatusCode)
		}
		decode(t, do(t, srv, "PUT", dev+"/sets/s1", "secret", nil), http.StatusOK, &ch)
		if !ch.Changed {
			t.Fatal("assign not changed")
		}
		var ids []string
		decode(t, do(t, srv, "GET", dev+"/declarations", "secret", nil), http.StatusOK, &ids)
		if len(ids) != 1 || ids[0] != "com.example.a" {
			t.Fatalf("declarations = %v", ids)
		}
		if res := do(t, srv, "PUT", "/admin/v1/enrollments/group/DEV-1/sets/s1", "secret", nil); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad channel = %d", res.StatusCode)
		}
		if res := do(t, srv, "PUT", "/admin/v1/enrollments/user/U-1/sets/s1", "secret", nil); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("user without parent = %d", res.StatusCode)
		}
		decode(t, do(t, srv, "PUT", "/admin/v1/enrollments/user/U-1/sets/s1?parent=DEV-1", "secret", nil), http.StatusOK, &ch)
		decode(t, do(t, srv, "DELETE", "/admin/v1/enrollments/user/U-1/sets/s1?parent=DEV-1", "secret", nil), http.StatusOK, &ch)
		if !ch.Changed {
			t.Fatal("unassign user not changed")
		}
		if res := do(t, srv, "DELETE", "/admin/v1/enrollments/group/U-1/sets/s1", "secret", nil); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad channel unassign = %d", res.StatusCode)
		}
		decode(t, do(t, srv, "DELETE", "/admin/v1/sets/s1/declarations/com.example.a", "secret", nil), http.StatusOK, &ch)
		if !ch.Changed {
			t.Fatal("remove from set not changed")
		}
		decode(t, do(t, srv, "PUT", "/admin/v1/sets/s1/declarations/com.example.a", "secret", nil), http.StatusOK, &ch)
	})
	t.Run("Status", func(t *testing.T) {
		var tokens struct {
			SyncTokens struct{ DeclarationsToken, Timestamp string }
		}
		decode(t, do(t, srv, "GET", dev+"/tokens", "secret", nil), http.StatusOK, &tokens)
		if len(tokens.SyncTokens.DeclarationsToken) != 64 {
			t.Fatalf("tokens = %+v", tokens)
		}
		report := fmt.Sprintf(`{"StatusItems":{"device":{"model":{"family":"Mac"}},"management":{"declarations":{"activations":[],"configurations":[],"assets":[],"management":[{"identifier":"com.example.a","server-token":%q,"active":false,"valid":"valid","reasons":[]}]}}},"Errors":[]}`, serverToken(t, a, "com.example.a"))
		if _, err := a.Engine.Status(context.Background(), enrollment("DEV-1"), []byte(report)); err != nil {
			t.Fatal(err)
		}
		var rows []struct{ Identifier, Valid string }
		decode(t, do(t, srv, "GET", dev+"/status", "secret", nil), http.StatusOK, &rows)
		if len(rows) != 1 || rows[0].Identifier != "com.example.a" || rows[0].Valid != "valid" {
			t.Fatalf("status = %+v", rows)
		}
		var values struct{ Items []struct{ Path string } }
		decode(t, do(t, srv, "GET", dev+"/status/values", "secret", nil), http.StatusOK, &values)
		if len(values.Items) == 0 {
			t.Fatal("no status values")
		}
		for _, p := range []string{dev + "/status", dev + "/status/values", dev + "/tokens", dev + "/declarations"} {
			bad := strings.Replace(p, "/device/", "/group/", 1)
			if res := do(t, srv, "GET", bad, "secret", nil); res.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s = %d", bad, res.StatusCode)
			}
		}
	})
	t.Run("Notify", func(t *testing.T) {
		fake.Advance(ddmsync.DefaultNotifyWindow)
		var res ddmsync.DrainResult
		decode(t, do(t, srv, "POST", "/admin/v1/notify", "secret", nil), http.StatusOK, &res)
		// DEV-1 and U-1 are unknown to the enrollment store, so their changes are dropped.
		if res.Skipped != 2 || res.Queued != 0 {
			t.Fatalf("drain = %+v, want the unknown enrollments skipped", res)
		}
	})
}

// TestSplitRoundTrip runs the ddm role and the mdm role as two processes'
// worth of handlers and drives Apple's DDM endpoints through the hop.

func TestCertSources(t *testing.T) {
	t.Run("Header", func(t *testing.T) {
		ca, err := testpki.NewCA("proxy roots")
		if err != nil {
			t.Fatal(err)
		}
		a := build(
			t,
			app.Config{
				Storage:        "inmem",
				CertHeader:     "X-Client-Cert",
				CARoots:        ca.Pool(),
				TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			},
		)
		srv := serve(t, a)
		if got := put(
			t,
			srv.URL+"/mdm",
			"application/x-apple-aspen-mdm-checkin",
			[]byte("<plist/>"),
		); got != http.StatusBadRequest {
			t.Fatalf("no header = %d", got)
		}
	})
	t.Run("TLSDefault", func(t *testing.T) {
		a := build(t, app.Config{Storage: "inmem"})
		srv := serve(t, a)
		if got := put(
			t,
			srv.URL+"/mdm",
			"application/x-apple-aspen-mdm-checkin",
			[]byte("<plist/>"),
		); got != http.StatusBadRequest {
			t.Fatalf("no TLS cert = %d", got)
		}
	})
}

// helpers

func propsDecl(id string) []byte {
	return fmt.Appendf(nil, `{"Type":"com.apple.management.properties","Identifier":%q,"Payload":{"shard":7}}`, id)
}

func enrollment(id string) mdm.EnrollmentID {
	return mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: id}
}

func serverToken(t *testing.T, a *app.App, id string) string {
	t.Helper()
	d, err := a.Engine.GetDeclaration(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d.ServerToken
}

func get(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

func put(t *testing.T, url, contentType string, body []byte) int {
	t.Helper()
	return send(t, http.MethodPut, url, contentType, body)
}

func send(t *testing.T, method, url, contentType string, body []byte) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// testResponse belongs to the test: do registers its body for cleanup.
type testResponse struct{ *http.Response }

func do(t *testing.T, srv *httptest.Server, method, path, token string, body []byte) testResponse {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, srv.URL+path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return testResponse{res}
}

func decode(t *testing.T, res testResponse, want int, v any) {
	t.Helper()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("status = %d, want %d: %s", res.StatusCode, want, data)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}
