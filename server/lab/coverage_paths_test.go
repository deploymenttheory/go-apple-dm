package lab

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// TestModuleWithSettingsRunsAnIsolatedServer checks that a module needing its own server
// configuration runs against a nested instance and that the instance is torn down.
func TestModuleWithSettingsRunsAnIsolatedServer(t *testing.T) {
	w := testWorkspace(t, "simulated")
	e, err := Start(t.Context(), w, "", os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	var nested string
	m := Module{
		ID: "TEST-ISOLATED", Modes: []string{"simulated"},
		Settings: map[string]string{"DM_ALLOW_REENROLL": "true"},
		Steps: []Step{{Name: "observe", Run: func(_ context.Context, s *Session) error {
			nested = s.Env.URL
			return nil
		}}},
	}
	r := Run(t.Context(), e, target.Simulator{}, m, Options{})
	if r.Status != StatusPassed {
		t.Fatalf("%s: %s", r.Status, r.Detail)
	}
	if nested == "" || nested == e.URL {
		t.Fatalf("the module ran against %q, not an isolated server", nested)
	}
}

// TestExecuteReportsAnUnavailableTarget checks the direct module entry point.
func TestExecuteReportsAnUnavailableTarget(t *testing.T) {
	m := Module{Steps: []Step{{Name: "unreached", Run: func(context.Context, *Session) error {
		t.Error("a step ran against an unavailable target")
		return nil
	}}}}
	if err := m.Execute(t.Context(), nil, fakeTarget{fails: errors.New("vm is gone")}); err == nil {
		t.Fatal("an unavailable target was accepted")
	}
	failing := Module{Steps: []Step{{Name: "step", Run: func(context.Context, *Session) error {
		return errors.New("refused")
	}}}}
	if err := failing.Execute(t.Context(), nil, fakeTarget{}); err == nil {
		t.Fatal("a failed step was accepted")
	}
}

// TestWorkspaceDocumentFailures checks workspace loading, saving and credential reads
// when the private files are missing or malformed.
func TestWorkspaceDocumentFailures(t *testing.T) {
	w := dockerWorkspace(t, nil)
	if w.Path("mdm", "tls.pem") != filepath.Join(w.Directory, "mdm", "tls.pem") {
		t.Fatal(w.Path("mdm", "tls.pem"))
	}
	if _, err := w.token(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(w.path("mdm", "admin-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.token(); err == nil {
		t.Fatal("a missing administrator credential was accepted")
	}

	// A workspace document that is not JSON, and one that is JSON but not a workspace.
	for _, document := range []string{"{", `{"Version":1,"Mode":"simulated"}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, WorkspaceFile), []byte(document), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Fatalf("invalid workspace document accepted: %s", document)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("a missing workspace was accepted")
	}

	// Saving into a directory that has been removed reports the failure.
	gone := dockerWorkspace(t, nil)
	if err := os.RemoveAll(gone.Directory); err != nil {
		t.Fatal(err)
	}
	if err := gone.save(); err == nil {
		t.Fatal("saving into a removed workspace succeeded")
	}
}

// TestContainerPrerequisitesWithoutDocker checks the doctor report when the host has no
// docker command.
func TestContainerPrerequisitesWithoutDocker(t *testing.T) {
	w := dockerWorkspace(t, nil)
	t.Setenv("PATH", t.TempDir())
	blocked, _ := w.containerPrerequisites()["Blocked"].([]string)
	if len(blocked) == 0 || !strings.Contains(strings.Join(blocked, " "), "docker is not on PATH") {
		t.Fatalf("blocked = %v", blocked)
	}
}

// TestIdentityFailures checks identity generation and reissue against unusable paths.
func TestIdentityFailures(t *testing.T) {
	// initialize refuses a directory it cannot create.
	file := filepath.Join(t.TempDir(), "file")
	writeFixture(t, file, nil)
	if err := initialize(filepath.Join(file, "mdm"), nil); err == nil {
		t.Fatal("initialization into a file succeeded")
	}

	w := dockerWorkspace(t, nil)
	// A CA certificate that is not PEM.
	if err := os.WriteFile(w.path("mdm", "ca.pem"), []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReissueTLS(w, []string{"mdm.lab.test"}); err == nil {
		t.Fatal("a malformed CA certificate was accepted")
	}
	// A CA certificate that is PEM but not a certificate.
	if err := os.WriteFile(w.path("mdm", "ca.pem"), []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReissueTLS(w, []string{"mdm.lab.test"}); err == nil {
		t.Fatal("a malformed CA certificate was accepted")
	}
	// A missing CA certificate.
	if err := os.Remove(w.path("mdm", "ca.pem")); err != nil {
		t.Fatal(err)
	}
	if err := ReissueTLS(w, []string{"mdm.lab.test"}); err == nil {
		t.Fatal("a missing CA certificate was accepted")
	}
}
