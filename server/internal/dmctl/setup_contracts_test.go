package dmctl_test

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/internal/bench"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

func TestSetupCheckBuildsReadyManagedServer(t *testing.T) {
	env, dir := noConfig(t), t.TempDir()
	path := filepath.Join(dir, "setup.json")
	for _, args := range [][]string{
		{"init", "-dir", dir, "-role", "customer"},
		{"https", "lab", "-setup-file", path, "-cn", "localhost", "-hosts", "localhost"},
		{"https", "activate", "-setup-file", path, "-revision", "1"},
		{"issuer", "create", "-setup-file", path, "-cn", "issuer"},
		{"issuer", "activate", "-setup-file", path, "-revision", "1"},
	} {
		if _, _, err := run(t, env, append([]string{"setup"}, args...)...); err != nil {
			t.Fatal(args, err)
		}
	}
	cfg, err := app.LoadSetupFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.OpenSetup(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := testpki.NewCA("test Apple authority")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ca.IssuePush("com.apple.mgmt.test", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := identity.PEM()
	if err != nil {
		t.Fatal(err)
	}
	a.Certificates.Trust.Apple = []*x509.Certificate{ca.Cert}
	_, err = a.Certificates.Adopt(
		t.Context(),
		lifecycle.Request{ID: "push", Kind: lifecycle.Push},
		cert,
		key,
	)
	_ = a.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, "setup", "check", "-setup-file", path); err != nil {
		t.Fatal("ready managed server rejected", err)
	}
	env[app.EnvPushTopic] = "different-topic"
	if _, _, err := run(t, env, "setup", "check", "-setup-file", path); err == nil {
		t.Fatal("inconsistent runtime configuration accepted")
	}
	delete(env, app.EnvPushTopic)
	if _, _, err := run(
		t,
		env,
		"setup",
		"profile",
		"export",
		"-setup-file",
		path,
		"-out",
		filepath.Join(dir, "profile"),
	); err == nil {
		t.Fatal("missing device identity accepted")
	}
}

func TestSetupPropagatesBootstrapAndOpenFailures(t *testing.T) {
	for _, mode := range []string{"parse environment", "volatile storage", "open local storage"} {
		t.Run(mode, func(t *testing.T) {
			env, dir := noConfig(t), t.TempDir()
			args := []string{"setup", "init", "-dir", dir}
			if mode == "parse environment" {
				env[app.EnvStorageKeysStrict] = "invalid"
			} else {
				env[app.EnvDSN] = ":memory:"
			}
			if mode == "open local storage" {
				path, err := app.InitSetupFile(
					app.SetupInitOptions{Directory: dir, Role: "customer", Storage: "sqlite"},
				)
				if err != nil {
					t.Fatal(err)
				}
				args = []string{"setup", "status", "-setup-file", path}
			}
			if _, _, err := run(t, env, args...); err == nil {
				t.Fatal("invalid bootstrap accepted")
			}
		})
	}
}

func TestSetupRemoteCommandsAndFailures(t *testing.T) {
	for _, mode := range []string{"success", "incomplete", "unavailable", "invalid response", "invalid client"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer token" {
						t.Error("missing authorization")
					}
					if mode == "unavailable" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					if mode == "invalid response" {
						_, _ = w.Write([]byte("invalid JSON"))
						return
					}
					switch {
					case r.URL.Path == "/admin/v1/setup":
						_ = json.NewEncoder(w).Encode(app.SetupStatus{Ready: mode != "incomplete"})
					case strings.HasSuffix(r.URL.Path, "/history"):
						_, _ = w.Write([]byte(`{"history":[]}`))
					case strings.HasSuffix(r.URL.Path, "/export"),
						strings.HasSuffix(r.URL.Path, "/trust"),
						strings.HasSuffix(r.URL.Path, "/enrollment-profiles"):
						_, _ = w.Write([]byte("public artifact"))
					case r.Method == "GET":
						_ = json.NewEncoder(w).
							Encode(lifecycle.Identity{Request: lifecycle.Request{ID: "push", Kind: lifecycle.Push}, Pending: "1"})
					default:
						var req app.SetupRequest
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						if strings.HasSuffix(r.URL.Path, "/acme") &&
							(req.PublicACME == nil || !req.PublicACME.AcceptTerms) {
							t.Error("ACME policy omitted")
						}
						result := app.SetupResult{
							Identity: &lifecycle.Identity{Request: req.Request},
						}
						if strings.HasSuffix(r.URL.Path, "/sign") {
							result.Data = []byte("signed portal artifact")
						}
						_ = json.NewEncoder(w).Encode(result)
					}
				}),
			)
			defer server.Close()
			env := noConfig(t)
			env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = server.URL, "token"
			if mode == "invalid client" {
				env["DMCTL_SERVER"] = "%"
			}
			for _, args := range [][]string{
				{"status"},
				{"check"},
				{"workflow", "show", "-id", "push"},
				{"workflow", "history", "-id", "push", "-cursor", "cursor"},
				{"workflow", "cancel", "-id", "push", "-revision", "1"},
				{"workflow", "export", "-id", "push", "-out", filepath.Join(t.TempDir(), "csr")},
				{"profile", "trust", "-out", filepath.Join(t.TempDir(), "trust")},
				{"profile", "export", "-out", filepath.Join(t.TempDir(), "enrollment"), "-device-id", "device"},
				{"https", "acme", "-cn", "mdm.example", "-hosts", "mdm.example", "-accept-terms", "-contact", "operator@example.com"},
				{"vendor", "sign", "-out", filepath.Join(t.TempDir(), "signed")},
				{"push", "sign"},
			} {
				_, _, err := run(t, env, append([]string{"setup"}, args...)...)
				wantError := mode == "unavailable" || mode == "invalid client" ||
					args[0] == "push" ||
					(mode == "incomplete" && args[0] == "check")
				if mode == "invalid response" {
					wantError = args[0] == "check" || args[0] == "https" || args[0] == "vendor" ||
						args[0] == "push" ||
						(args[0] == "workflow" && args[1] == "cancel")
				}
				if (err != nil) != wantError {
					t.Fatalf("%v: got %v, want error %v", args, err, wantError)
				}
			}
		})
	}
}

func TestSetupLocalValidationAndArtifactProtection(t *testing.T) {
	env, dir := noConfig(t), t.TempDir()
	path := filepath.Join(dir, "setup.json")
	for _, args := range [][]string{
		{},
		{"vendor"},
		{"status", "extra"},
		{"status", "-bad"},
		{"status", "-from-bench", dir},
		{"vendor", "sign"},
		{"profile", "bad"},
		{"profile", "trust"},
		{"workflow", "show"},
		{"workflow", "export", "-id", "push"},
		{"workflow", "bad", "-id", "push"},
		{"init", "-dir", dir, "-storage", "inmem"},
		{"status", "-setup-file", path},
		{"push", "import", "-cert", filepath.Join(dir, "missing")},
	} {
		if _, _, err := run(t, env, append([]string{"setup"}, args...)...); err == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
	if _, _, err := run(t, env, "setup", "init", "-dir", dir, "-role", "customer"); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) error {
		_, _, err := run(t, env, append(append([]string{"setup"}, args...), "-setup-file", path)...)
		return err
	}
	if err := call("check"); !errors.Is(err, dmctl.ErrPartial) {
		t.Fatal("incomplete setup passed", err)
	}
	if err := call("push", "request", "-cn", "customer"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"workflow", "show", "-id", "push"}, {"workflow", "history", "-id", "push"}, {"workflow", "cancel", "-id", "push", "-revision", "1"}} {
		if err := call(args...); err != nil {
			t.Fatal(args, err)
		}
	}
	for _, args := range [][]string{{"workflow", "show", "-id", "missing"}, {"workflow", "history", "-id", "push", "-page-size", "0"}, {"workflow", "cancel", "-id", "missing"}, {"profile", "export", "-out", filepath.Join(dir, "profile")}, {"profile", "trust", "-out", filepath.Join(dir, "trust")}} {
		if err := call(args...); err == nil {
			t.Fatal("invalid local command accepted", args)
		}
	}
	if err := call("https", "lab", "-cn", "localhost", "-hosts", "localhost"); err != nil {
		t.Fatal(err)
	}
	if err := call("profile", "trust", "-out", filepath.Join(dir, "trust")); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	before, err := os.ReadFile(filepath.Join(dir, "trust"))
	if err != nil {
		t.Fatal(err)
	}
	if err := call("profile", "trust", "-out", filepath.Join(dir, "trust")); err == nil {
		t.Fatal("existing artifact overwritten")
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	after, err := os.ReadFile(filepath.Join(dir, "trust"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("artifact changed", err)
	}
	cfg, err := app.LoadSetupFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.OpenSetup(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	material, err := a.Certificates.LoadMaterial(t.Context(), "https-ca", "")
	_ = a.Close()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{"cert": material.Certificate, "key": material.Key, "csr": []byte("invalid CSR"), "signed": []byte("invalid signature")} {
		if err := os.WriteFile(filepath.Join(dir, name), value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := call(
		"adopt",
		"-kind",
		"issuer",
		"-id",
		"adopted",
		"-cert",
		filepath.Join(dir, "cert"),
		"-key",
		filepath.Join(dir, "key"),
	); err != nil {
		t.Fatal(err)
	}
	if err := call(
		"push",
		"sign",
		"-revision",
		"1",
		"-csr",
		filepath.Join(dir, "csr"),
		"-signed-request",
		filepath.Join(dir, "signed"),
	); err == nil {
		t.Fatal("invalid signature accepted")
	}
	// A loopback test directory fails immediately; the CLI must close its
	// HTTP-01 listener and retain the configured account for another attempt.
	ca := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }),
	)
	defer ca.Close()
	if err := call(
		"https",
		"acme",
		"-id",
		"public",
		"-cn",
		"mdm.example",
		"-hosts",
		"mdm.example",
		"-directory",
		ca.URL,
		"-contact",
		"operator@example.com",
		"-accept-terms",
		"-http01-listen",
		"127.0.0.1:0",
	); err == nil {
		t.Fatal("CA rejection ignored")
	}
	if err := call(
		"https",
		"acme",
		"-id",
		"public",
		"-directory",
		ca.URL,
		"-contact",
		"operator@example.com",
		"-accept-terms",
		"-http01-listen",
		"invalid",
	); err == nil {
		t.Fatal("invalid challenge listener accepted")
	}
}

func TestSetupBenchAdoptionPreservesDatabaseAndImportedIdentities(t *testing.T) {
	for _, mode := range []string{"missing workspace", "simulated", "missing key", "missing database", "missing challenge", "missing certificate", "missing private key", "untrusted push", "different database", "invalid environment"} {
		t.Run(mode, func(t *testing.T) {
			source, destination := t.TempDir(), t.TempDir()
			env := noConfig(t)
			if mode != "missing workspace" {
				benchMode := "live"
				if mode == "simulated" {
					benchMode = "simulated"
				}
				if err := bench.Init(
					source,
					benchMode,
					"sqlite",
					"127.0.0.1:8443",
				); err != nil {
					t.Fatal(err)
				}
				mdm := filepath.Join(source, "mdm")
				if mode != "missing database" {
					if err := os.WriteFile(
						filepath.Join(mdm, "mdm.sqlite"),
						nil,
						0o600,
					); err != nil {
						t.Fatal(err)
					}
				}
				remove := map[string]string{"missing key": "storage-key", "missing challenge": "scep-challenge", "missing certificate": "ca.pem", "missing private key": "ca.key"}[mode]
				if remove != "" {
					if err := os.Remove(filepath.Join(mdm, remove)); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "untrusted push" {
					for target, original := range map[string]string{"push.pem": "tls.pem", "push.key": "tls.key"} {
						// #nosec G304 -- The test controls this fixture path within its private workspace.
						data, err := os.ReadFile(filepath.Join(mdm, original))
						if err != nil {
							t.Fatal(err)
						}
						// #nosec G703 -- The test controls this fixture path within its private workspace.
						if err := os.WriteFile(
							filepath.Join(mdm, target),
							data,
							0o600,
						); err != nil {
							t.Fatal(err)
						}
					}
				}
				if mode == "different database" {
					env[app.EnvDSN] = filepath.Join(destination, "other.sqlite")
				}
				if mode == "invalid environment" {
					env[app.EnvStorageKeysStrict] = "invalid"
				}
			}
			_, _, err := run(
				t,
				env,
				"setup",
				"adopt",
				"-from-bench",
				source,
				"-dir",
				destination,
				"-role",
				"customer",
			)
			if err == nil {
				t.Fatal("incomplete adoption succeeded")
			}
			if mode == "untrusted push" {
				cfg, err := app.LoadSetupFile(filepath.Join(destination, "setup.json"), nil)
				if err != nil {
					t.Fatal(err)
				}
				a, err := app.OpenSetup(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer func(cleanup func() error) { _ = cleanup() }(a.Close)
				for _, name := range []string{"issuer", "https-ca", "https"} {
					v, err := a.Certificates.Get(t.Context(), name)
					if err != nil || v.Active != "1" {
						t.Fatal("verified identities lost after push failure", name, err)
					}
				}
				for _, name := range []string{"bench", "lab"} {
					// #nosec G304 -- The test controls this fixture path within its private workspace.
					got, err := os.ReadFile(filepath.Join(destination, "secrets", name))
					if err != nil {
						t.Fatal(err)
					}
					// #nosec G304 -- The test controls this fixture path within its private workspace.
					want, err := os.ReadFile(filepath.Join(source, "mdm", "storage-key"))
					if err != nil || !bytes.Equal(got, want) {
						t.Fatal("storage key changed", err)
					}
				}
			}
		})
	}
}
