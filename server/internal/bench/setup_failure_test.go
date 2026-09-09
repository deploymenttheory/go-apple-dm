package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/testpki"
)

func TestFixtureSetupRefusesUnwritableOutputs(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"fixtures", "fixtures/apns-root.pem", "fixtures/app.pem", "fixtures/app.key", "fixtures/oidc-root.pem", "fixtures/device-root.pem", "fixtures/user-ha1.json", "fixtures/attestation.json", "fixtures/attestation-root.pem", "fixtures/device-root.key", "fixtures/dep-tokens.json", "fixtures/abm.key"} {
		t.Run(name, func(t *testing.T) {
			w := testWorkspace(t, "simulated")
			if name == "fixtures" {
				writeFixture(t, w.path(name), []byte("preserve"))
			} else {
				if err := os.MkdirAll(w.path(name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if e, err := Start(t.Context(), w, "", io.Discard); err == nil {
				e.Close()
				t.Fatal("unwritable fixture accepted")
			}
		})
	}
}

func TestLiveWorkspaceImportsAndPreservesMDMCredential(t *testing.T) {
	t.Parallel()
	w := testWorkspace(t, "live")
	w.Storage = "sqlite"
	w.DSN = w.path("custom.sqlite")
	ca, err := testpki.NewCA("live test issuer")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.IssuePush("com.apple.mgmt.External.bench", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, w.path("mdm", "push.pem"), cert)
	if e, err := Start(t.Context(), w, "", io.Discard); err == nil {
		e.Close()
		t.Fatal("missing MDM signing key accepted")
	}
	writeFixture(t, w.path("mdm", "push.key"), key)
	for range 2 {
		e, err := Start(t.Context(), w, "", io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		var listed struct {
			Items []struct {
				Topic   string
				Version int
			}
		}
		if err := e.api(t.Context(), "GET", "/pushcerts", nil, &listed); err != nil {
			t.Fatal(err)
		}
		if len(listed.Items) != 1 || listed.Items[0].Version != 1 {
			t.Fatal("restart replaced stored credential")
		}
		e.Close()
	}
}

func TestSeedingPropagatesImportFailures(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"malformed listing", "MDM rejected", "missing app certificate", "missing app key", "app rejected"} {
		t.Run(name, func(t *testing.T) {
			w := testWorkspace(t, "simulated")
			if err := os.Mkdir(w.path("fixtures"), 0o700); err != nil {
				t.Fatal(err)
			}
			if name != "missing app certificate" {
				writeFixture(t, w.path("fixtures", "app.pem"), []byte("certificate"))
			}
			if name != "missing app key" {
				writeFixture(t, w.path("fixtures", "app.key"), []byte("key"))
			}
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						if name == "malformed listing" {
							io.WriteString(w, "{")
						} else {
							io.WriteString(w, `{"Items":[]}`)
						}
						return
					}
					if name == "MDM rejected" || strings.Contains(r.URL.Path, "apppush") {
						w.WriteHeader(503)
					}
				}),
			)
			defer srv.Close()
			e := &Environment{Instance: Instance{URL: srv.URL}, Workspace: w, Client: srv.Client()}
			if err := e.seed(
				t.Context(),
				"topic",
				[]byte("certificate"),
				[]byte("key"),
			); err == nil {
				t.Fatal("failed import accepted")
			}
		})
	}
}

func TestScenarioPrerequisiteFailures(t *testing.T) {
	t.Parallel()
	w := testWorkspace(t, "simulated")
	e := &Environment{
		Workspace: w,
		Instance:  Instance{Mode: "simulated", URL: "https://invalid"},
		Client:    &http.Client{},
	}
	if err := splitRoundTrip(t.Context(), e, ""); !errors.Is(err, ErrBlocked) {
		t.Fatalf("missing split: %v", err)
	}
	if _, err := browser(t.Context(), e, "://"); err == nil {
		t.Fatal("invalid browser URL accepted")
	}
	if _, err := SelectMode("live", "absent"); err == nil {
		t.Fatal("unknown selector accepted")
	}
	if err := os.Remove(w.path("mdm", "ca.key")); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func(context.Context, *Environment, string) error{enrollIdle, adeEnroll, accountDriven, otaEnroll, acmeEnroll, deviceAttestation, depAssign, appRenewal} {
		if err := fn(t.Context(), e, ""); err == nil {
			t.Fatal("missing scenario prerequisite accepted")
		}
	}
	if err := os.Mkdir(w.path("fixtures"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, w.path("fixtures", "dep-tokens.json"), []byte("{"))
	if err := depAssign(t.Context(), e, ""); err == nil {
		t.Fatal("invalid DEP tokens accepted")
	}
	writeFixture(t, w.path("fixtures", "attestation.json"), []byte("{"))
	if _, err := e.attestation(); err == nil {
		t.Fatal("invalid attestation accepted")
	}
}

func TestReadinessDetectsExitedOrCancelledRuntime(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"clean exit", "failed exit", "cancel", "invalid URL"} {
		t.Run(name, func(t *testing.T) {
			e := &Environment{errs: make(chan error, 1), Client: &http.Client{}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			url := "http://127.0.0.1:1"
			switch name {
			case "clean exit":
				e.errs <- nil
			case "failed exit":
				e.errs <- io.ErrUnexpectedEOF
			case "cancel":
				cancel()
			case "invalid URL":
				url = "://"
			}
			if err := e.ready(ctx, url); err == nil {
				t.Fatal("unready server accepted")
			}
		})
	}
	// A process adapter must collect a child exit and stop a running child on cancellation.
	for _, script := range []string{"#!/bin/sh\nexit 3\n", "#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 0.1; done\n"} {
		ctx, cancel := context.WithCancel(t.Context())
		e := &Environment{errs: make(chan error, 2), Client: &http.Client{}, cancel: cancel}
		binary := filepath.Join(t.TempDir(), "server")
		writeFixture(t, binary, []byte(script))
		if err := os.Chmod(binary, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := e.launch(
			ctx,
			map[string]string{"DM_STORAGE": "inmem"},
			binary,
			io.Discard,
		); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(script, "exit 3") {
			if err := e.ready(ctx, "http://127.0.0.1:1"); err == nil {
				t.Fatal("failed child accepted")
			}
		}
		e.Close()
	}
}

func TestSupervisorAuthorizationAndScripts(t *testing.T) {
	for _, mode := range []string{"simulated", "live"} {
		t.Run(mode, func(t *testing.T) {
			w := testWorkspace(t, mode)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Up(ctx, w, "", io.Discard) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			var e *Environment
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			for e == nil {
				select {
				case <-deadline.C:
					t.Fatal("supervisor not ready")
				case <-tick.C:
					e, _ = Attach(w)
				}
			}
			defer e.Client.CloseIdleConnections()
			if err := Up(ctx, w, "", io.Discard); err == nil {
				t.Fatal("second supervisor acquired workspace")
			}
			token := e.ControlToken
			e.ControlToken = "invalid"
			if _, err := e.Control(ctx, "GET", "/status", nil); err == nil {
				t.Fatal("unauthenticated control accepted")
			}
			e.ControlToken = token
			if _, err := e.Control(
				ctx,
				"POST",
				"/apns/script",
				strings.NewReader("{"),
			); err == nil {
				t.Fatal("invalid script accepted")
			}
			data, err := json.Marshal(
				map[string]any{
					"Token":  []byte{1},
					"Script": map[string]any{"Status": 410, "Reason": "Unregistered"},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Control(ctx, "POST", "/apns/script", bytes.NewReader(data))
			if mode == "simulated" {
				if err != nil {
					t.Fatal(err)
				}
				if err := invalidToken(ctx, e, ""); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("live provider scripting accepted")
			}
		})
	}
}
