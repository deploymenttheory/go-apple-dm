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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/internal/app"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/internal/testpki"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/simulator"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func build(t *testing.T, cfg app.Config) *app.App {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = quiet
	}
	// A durable store refuses to run without a keyring, so a test that names
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
	// The hop refuses to run unauthenticated, so a role that serves or calls
	// it needs a credential even when the test is about something else.
	if cfg.Role == app.RoleDDM || cfg.DDMURL != "" {
		if len(cfg.DDMSendKey) == 0 {
			cfg.DDMSendKey = []byte("hop-send-key")
		}
		if len(cfg.DDMRecvKey) == 0 {
			cfg.DDMRecvKey = []byte("hop-recv-key")
		}
	}
	a, err := app.Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
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
			// A durable store needs a keyring, and sqlite is the default, so
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
		if cfg.Role != app.RoleAll || cfg.Listen != ":8080" || cfg.Storage != "sqlite" || cfg.DSN != "mdm.db" || !cfg.Subscriptions {
			t.Fatalf("cfg = %+v", cfg)
		}
	})
	t.Run("Overrides", func(t *testing.T) {
		cfg, err := app.ParseEnv(env(map[string]string{
			app.EnvRole: "mdm", app.EnvListen: ":9", app.EnvStorage: "postgres", app.EnvDSN: "postgres://x",
			app.EnvDDMURL: "http://ddm", app.EnvDDMSendKey: "s", app.EnvDDMRecvKey: "r", app.EnvAdminToken: "t",
			app.EnvSubscriptions: "false", app.EnvCAFile: "ca.pem", app.EnvCertHeader: "X-Cert",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Role != app.RoleMDM || cfg.DDMURL != "http://ddm" || string(cfg.DDMSendKey) != "s" || string(cfg.DDMRecvKey) != "r" ||
			cfg.AdminToken != "t" || cfg.Subscriptions || cfg.CAFile != "ca.pem" || cfg.CertHeader != "X-Cert" {
			t.Fatalf("cfg = %+v", cfg)
		}
	})
	t.Run("AdminStore", func(t *testing.T) {
		cfg, err := app.ParseEnv(env(nil))
		if err != nil || cfg.AdminStoreEnabled {
			t.Fatalf("the principal store must be off unless asked for: %+v, %v", cfg, err)
		}
		cfg, err = app.ParseEnv(env(map[string]string{app.EnvAdminStore: "true"}))
		if err != nil || !cfg.AdminStoreEnabled {
			t.Fatalf("cfg = %+v, err = %v", cfg, err)
		}
		if _, err := app.ParseEnv(env(map[string]string{app.EnvAdminStore: "maybe"})); !errors.Is(err, app.ErrConfig) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("Inmem", func(t *testing.T) {
		cfg, err := app.ParseEnv(env(map[string]string{app.EnvStorage: "inmem"}))
		if err != nil || cfg.DSN != "" {
			t.Fatalf("cfg = %+v, err = %v", cfg, err)
		}
	})
	t.Run("BadBool", func(t *testing.T) {
		if _, err := app.ParseEnv(env(map[string]string{app.EnvSubscriptions: "maybe"})); !errors.Is(err, app.ErrConfig) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("Invalid", func(t *testing.T) {
		if _, err := app.ParseEnv(env(map[string]string{app.EnvRole: "proxy"})); !errors.Is(err, app.ErrConfig) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestBuild(t *testing.T) {
	ctx := context.Background()
	t.Run("MDMRole", func(t *testing.T) {
		a := build(t, app.Config{Role: app.RoleMDM, Storage: "inmem", AdminToken: "t"})
		if a.Core == nil || a.Engine == nil || a.Notifier == nil {
			t.Fatal("mdm role must wire the core, the engine (for cleanup), and the notifier")
		}
		srv := serve(t, a)
		// No admin API on the mdm role even with a token.
		if got := get(t, srv.URL+"/admin/v1/declarations/x", "t"); got != http.StatusNotFound {
			t.Fatalf("admin on mdm role = %d", got)
		}
		if got := get(t, srv.URL+"/ddm/v1/declarative-management", ""); got != http.StatusNotFound {
			t.Fatalf("ddm hop on mdm role = %d", got)
		}
	})
	t.Run("DDMRole", func(t *testing.T) {
		a := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem", AdminToken: "t"})
		// The ddm role builds a core so its notifier can enqueue
		// DeclarativeManagement through the MDM command path, but it mounts
		// no device route: the /mdm 404 below is what "does not serve
		// check-in" actually means.
		if a.Core == nil {
			t.Fatal("ddm role needs a core to enqueue through")
		}
		srv := serve(t, a)
		if got := post(t, srv.URL+"/mdm", "application/x-apple-aspen-mdm-checkin", nil); got != http.StatusNotFound {
			t.Fatalf("/mdm on ddm role = %d", got)
		}
		if got := post(t, srv.URL+"/ddm/v1/declarative-management", "text/plain", nil); got != http.StatusUnsupportedMediaType {
			t.Fatalf("hop content type check = %d", got)
		}
	})
	t.Run("AllRole", func(t *testing.T) {
		dir := t.TempDir()
		a := build(t, app.Config{Role: app.RoleAll, Storage: "sqlite", DSN: filepath.Join(dir, "all.db"), AdminToken: "t"})
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
		b := build(t, app.Config{Role: app.RoleAll, Storage: "sqlite", DSN: filepath.Join(dir, "all.db"), AdminToken: "t"})
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
		a := build(t, app.Config{Role: app.RoleMDM, Storage: "inmem", CAFile: path})
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
			"role":            {Role: "proxy", Storage: "inmem"},
			"storage":         {Role: app.RoleAll, Storage: "redis"},
			"dsn":             {Role: app.RoleAll, Storage: "sqlite"},
			"ddm url on ddm":  {Role: app.RoleDDM, Storage: "inmem", DDMURL: "http://x"},
			"ca file missing": {Role: app.RoleMDM, Storage: "inmem", CAFile: filepath.Join(t.TempDir(), "nope.pem")},
			"ca file empty":   {Role: app.RoleMDM, Storage: "inmem", CAFile: noCerts},
		}
		for name, cfg := range cases {
			cfg.Logger = quiet
			if _, err := app.Build(ctx, cfg); !errors.Is(err, app.ErrConfig) {
				t.Errorf("%s: err = %v, want ErrConfig", name, err)
			}
		}
		if _, err := app.Build(ctx, app.Config{Role: app.RoleAll, Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "missing", "x.db"), StorageKeys: []string{"k"}, Secrets: secrets.Static{"k": []byte("0123456789abcdef0123456789abcdef")}, Logger: quiet}); err == nil {
			t.Error("sqlite in a missing directory must fail")
		}
		if _, err := app.Build(ctx, app.Config{Role: app.RoleAll, Storage: "postgres", DSN: "postgres://127.0.0.1:1/x?sslmode=disable&connect_timeout=1", StorageKeys: []string{"k"}, Secrets: secrets.Static{"k": []byte("0123456789abcdef0123456789abcdef")}, Logger: quiet}); err == nil {
			t.Error("unreachable postgres must fail")
		}
		if _, err := app.Build(ctx, app.Config{Role: app.RoleAll, Storage: "mysql", DSN: "mdm:mdm@tcp(127.0.0.1:1)/x?timeout=1s", StorageKeys: []string{"k"}, Secrets: secrets.Static{"k": []byte("0123456789abcdef0123456789abcdef")}, Logger: quiet}); err == nil {
			t.Error("unreachable mysql must fail")
		}
		if _, err := app.Build(ctx, app.Config{Role: app.RoleMDM, Storage: "inmem", DDMURL: "::bad", Logger: quiet}); err == nil {
			t.Error("bad DDM URL must fail")
		}
	})
}

func TestHealthz(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleDDM, Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "h.db")})
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
	if got := get(t, srv.URL+"/nope", ""); got != http.StatusNotFound {
		t.Fatalf("unknown path = %d", got)
	}
}

func TestRun(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem"})
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
// admin route answers 500 without leaking the cause.
func TestAdminInternalErrors(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleDDM, Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "i.db"), AdminToken: "t"})
	srv := serve(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	calls := []struct{ method, path string }{
		{"PUT", "/admin/v1/declarations"},
		{"GET", "/admin/v1/declarations/x"},
		{"DELETE", "/admin/v1/declarations/x"},
		{"PUT", "/admin/v1/sets/s/declarations/x"},
		{"DELETE", "/admin/v1/sets/s/declarations/x"},
		{"PUT", "/admin/v1/enrollments/device/D/sets/s"},
		{"DELETE", "/admin/v1/enrollments/device/D/sets/s"},
		{"GET", "/admin/v1/enrollments/device/D/declarations"},
		{"GET", "/admin/v1/enrollments/device/D/status"},
		{"GET", "/admin/v1/enrollments/device/D/status/values"},
		{"GET", "/admin/v1/enrollments/device/D/tokens"},
		{"POST", "/admin/v1/notify"},
	}
	for _, c := range calls {
		body := propsDecl("com.example.closed")
		res := do(t, srv, c.method, c.path, "t", body)
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusInternalServerError || !strings.Contains(string(data), "internal error") || strings.Contains(string(data), "sql") {
			t.Errorf("%s %s = %d %s", c.method, c.path, res.StatusCode, data)
		}
	}
}

func TestAdminDisabledWithoutToken(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem"})
	srv := serve(t, a)
	if got := get(t, srv.URL+"/admin/v1/declarations/x", "anything"); got != http.StatusNotFound {
		t.Fatalf("admin without token = %d, want 404", got)
	}
}

func TestAdminAPI(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	a := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem", AdminToken: "secret", Clock: fake})
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
		fake.Advance(ddm.DefaultNotifyWindow)
		var res ddm.DrainResult
		decode(t, do(t, srv, "POST", "/admin/v1/notify", "secret", nil), http.StatusOK, &res)
		// DEV-1 and U-1 are unknown to the enrollment store, so their changes are dropped.
		if res.Skipped != 2 || res.Queued != 0 {
			t.Fatalf("drain = %+v, want the unknown enrollments skipped", res)
		}
	})
}

// TestSplitRoundTrip runs the ddm role and the mdm role as two processes'
// worth of handlers and drives Apple's DDM endpoints through the hop.
func TestSplitRoundTrip(t *testing.T) {
	ctx := context.Background()
	ca, err := testpki.NewCA("app test CA")
	if err != nil {
		t.Fatal(err)
	}
	send, recv := []byte("mdm-to-ddm"), []byte("ddm-to-mdm")
	ddmApp := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem", AdminToken: "t", DDMRecvKey: send, DDMSendKey: recv})
	ddmSrv := serve(t, ddmApp)
	mdmApp := build(t, app.Config{Role: app.RoleMDM, Storage: "inmem", DDMURL: ddmSrv.URL + "/ddm", DDMSendKey: send, DDMRecvKey: recv, CARoots: ca.Pool()})
	mdmSrv := serve(t, mdmApp)

	do(t, ddmSrv, "PUT", "/admin/v1/declarations", "t", propsDecl("com.example.split"))
	do(t, ddmSrv, "PUT", "/admin/v1/enrollments/device/UDID-1/declarations/com.example.split", "t", nil)
	do(t, ddmSrv, "PUT", "/admin/v1/sets/set1/declarations/com.example.split", "t", nil)
	do(t, ddmSrv, "PUT", "/admin/v1/enrollments/device/UDID-1/sets/set1", "t", nil)

	id, err := ca.Issue("UDID-1", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	dev := simulator.New("UDID-1",
		simulator.WithURLs(mdmSrv.URL+"/mdm", mdmSrv.URL+"/mdm"),
		simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}))
	if err := dev.Enroll(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	body, err := dev.DeclarativeManagement(ctx, "tokens", nil)
	if err != nil {
		t.Fatalf("tokens: %v", err)
	}
	var tokens struct {
		SyncTokens struct{ DeclarationsToken string }
	}
	if err := json.Unmarshal(body, &tokens); err != nil || len(tokens.SyncTokens.DeclarationsToken) != 64 {
		t.Fatalf("tokens body %s: %v", body, err)
	}
	body, err = dev.DeclarativeManagement(ctx, "declaration-items", nil)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	var items struct {
		Declarations struct {
			Management []struct{ Identifier, ServerToken string }
		}
		DeclarationsToken string
	}
	if err := json.Unmarshal(body, &items); err != nil || len(items.Declarations.Management) != 1 || items.DeclarationsToken != tokens.SyncTokens.DeclarationsToken {
		t.Fatalf("items body %s: %v", body, err)
	}
	body, err = dev.DeclarativeManagement(ctx, "declaration/management/com.example.split", nil)
	if err != nil {
		t.Fatalf("declaration: %v", err)
	}
	var decl struct{ Identifier, ServerToken string }
	if err := json.Unmarshal(body, &decl); err != nil || decl.ServerToken != items.Declarations.Management[0].ServerToken {
		t.Fatalf("declaration body %s: %v", body, err)
	}
	var herr *simulator.HTTPError
	if _, err := dev.DeclarativeManagement(ctx, "declaration/management/com.example.absent", nil); !errors.As(err, &herr) || herr.Status != http.StatusNotFound {
		t.Fatalf("absent declaration: %v, want 404 relayed", err)
	}
	if _, err := dev.DeclarativeManagement(ctx, "declaration/../x", nil); !errors.As(err, &herr) || herr.Status != http.StatusBadRequest {
		t.Fatalf("bad endpoint: %v, want 400 relayed", err)
	}
	report := fmt.Sprintf(`{"StatusItems":{"management":{"declarations":{"activations":[],"configurations":[],"assets":[],"management":[{"identifier":"com.example.split","server-token":%q,"active":false,"valid":"valid","reasons":[]}]}}},"Errors":[],"FullReport":true}`, decl.ServerToken)
	if body, err := dev.DeclarativeManagement(ctx, "status", []byte(report)); err != nil || len(body) != 0 {
		t.Fatalf("status: body %q err %v", body, err)
	}
	var rows []struct{ Identifier string }
	decode(t, do(t, ddmSrv, "GET", "/admin/v1/enrollments/device/UDID-1/status", "t", nil), http.StatusOK, &rows)
	if len(rows) != 1 || rows[0].Identifier != "com.example.split" {
		t.Fatalf("status rows on the ddm role = %+v", rows)
	}

	// Wrong send key: the ddm role answers 401 and the device sees an internal error, never a 404.
	badApp := build(t, app.Config{Role: app.RoleMDM, Storage: "inmem", DDMURL: ddmSrv.URL + "/ddm", DDMSendKey: []byte("wrong"), DDMRecvKey: recv, CARoots: ca.Pool()})
	badSrv := serve(t, badApp)
	bad := simulator.New("UDID-1",
		simulator.WithURLs(badSrv.URL+"/mdm", badSrv.URL+"/mdm"),
		simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}))
	if err := bad.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.DeclarativeManagement(ctx, "tokens", nil); !errors.As(err, &herr) || herr.Status != http.StatusInternalServerError {
		t.Fatalf("wrong key: %v, want 500", err)
	}
}

func TestCertSources(t *testing.T) {
	t.Run("Header", func(t *testing.T) {
		a := build(t, app.Config{Role: app.RoleMDM, Storage: "inmem", CertHeader: "X-Client-Cert"})
		srv := serve(t, a)
		if got := put(t, srv.URL+"/mdm", "application/x-apple-aspen-mdm-checkin", []byte("<plist/>")); got != http.StatusBadRequest {
			t.Fatalf("no header = %d", got)
		}
	})
	t.Run("TLSDefault", func(t *testing.T) {
		a := build(t, app.Config{Role: app.RoleMDM, Storage: "inmem"})
		srv := serve(t, a)
		if got := put(t, srv.URL+"/mdm", "application/x-apple-aspen-mdm-checkin", []byte("<plist/>")); got != http.StatusBadRequest {
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
	res.Body.Close()
	return res.StatusCode
}

func post(t *testing.T, url, contentType string, body []byte) int {
	return send(t, http.MethodPost, url, contentType, body)
}

func put(t *testing.T, url, contentType string, body []byte) int {
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
	res.Body.Close()
	return res.StatusCode
}

func do(t *testing.T, srv *httptest.Server, method, path, token string, body []byte) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, srv.URL+path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func decode(t *testing.T, res *http.Response, want int, v any) {
	t.Helper()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("status = %d, want %d: %s", res.StatusCode, want, data)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}
