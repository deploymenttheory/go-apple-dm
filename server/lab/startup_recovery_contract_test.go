package lab

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// identitySnapshot retains private workspace bytes without exposing them in output.
func identitySnapshot(t *testing.T, w *Workspace) map[string][]byte {
	t.Helper()
	snapshot := map[string][]byte{}
	for _, name := range []string{"ca.pem", "ca.key", "tls.pem", "tls.key", "admin-token", "storage-key", "scep-challenge"} {
		data, err := os.ReadFile(w.path("mdm", name))
		if err != nil {
			t.Fatal(err)
		}
		snapshot[name] = data
	}
	return snapshot
}

// assertIdentityPreserved checks setup and repair retain trust, keys and credentials.
func assertIdentityPreserved(t *testing.T, w *Workspace, want map[string][]byte) {
	t.Helper()
	for name, expected := range want {
		actual, err := os.ReadFile(w.path("mdm", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("workspace identity %s changed during fixture recovery", name)
		}
	}
}

// TestFixtureStartupFailurePreservesIdentityAndCanRecover proves failed fixture
// persistence aborts startup, releases its listener, preserves private identities
// and permits a repaired restart. It does not claim live Apple API acceptance.
func TestFixtureStartupFailurePreservesIdentityAndCanRecover(t *testing.T) {
	for _, name := range []string{"fixtures", "apns-root.pem", "app.pem", "app.key", "oidc-root.pem", "device-root.pem", "user-ha1.json", "attestation.json", "attestation-root.pem", "device-root.key", "dep-tls.pem", "dep-tokens.json", "abm-tls.pem", "abm.key", "admission.json"} {
		t.Run(name, func(t *testing.T) {
			w := testWorkspace(t, "simulated")
			snapshot := identitySnapshot(t, w)
			reservation, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			w.Listen = reservation.Addr().String()
			if err := reservation.Close(); err != nil {
				t.Fatal(err)
			}
			blocked := w.path("fixtures", name)
			if name == "fixtures" {
				blocked = w.path("fixtures")
				if err := os.RemoveAll(blocked); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(blocked, []byte("occupied"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(blocked, 0o700); err != nil {
				t.Fatal(err)
			}
			env, err := Start(t.Context(), w, "", io.Discard)
			if env != nil {
				env.Close()
				t.Fatal("startup returned an environment after fixture write failure")
			}
			var pathError *os.PathError
			if !errors.As(err, &pathError) || filepath.Clean(pathError.Path) != filepath.Clean(blocked) {
				t.Fatalf("failure did not identify the blocked artifact: %v", err)
			}
			assertIdentityPreserved(t, w, snapshot)
			released, err := net.Listen("tcp", w.Listen)
			if err != nil {
				t.Fatalf("failed startup retained its listener: %v", err)
			}
			if err := released.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(blocked); err != nil {
				t.Fatal(err)
			}
			recovered, err := Start(t.Context(), w, "", io.Discard)
			if err != nil {
				t.Fatalf("repaired fixture workspace could not restart: %v", err)
			}
			defer recovered.Close()
			if _, _, err := HTTP(t.Context(), recovered.Client, recovered.URL, recovered.Token, "GET", "/auth/me", nil); err != nil {
				t.Fatalf("repaired startup did not establish administration: %v", err)
			}
			assertIdentityPreserved(t, w, snapshot)
		})
	}
}
