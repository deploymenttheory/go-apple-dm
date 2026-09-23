//go:build !windows

package lab

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// TestIsolatedServerFailureFailsItsModule checks that a module whose own server
// configuration cannot start is failed with the reason, and does not run its steps.
func TestIsolatedServerFailureFailsItsModule(t *testing.T) {
	ran := false
	m := Module{
		ID: "TEST-ISOLATED-FAIL", Modes: []string{"simulated"},
		Settings: map[string]string{"DM_STORAGE": "unsupported-engine"},
		Steps: []Step{{Name: "step", Run: func(context.Context, *Session) error {
			ran = true
			return nil
		}}},
	}
	r := Run(t.Context(), &Environment{Instance: Instance{Mode: "simulated"}}, target.Simulator{}, m, Options{})
	if r.Status != StatusFailed || r.Detail == "" {
		t.Fatalf("%s: %q", r.Status, r.Detail)
	}
	if ran {
		t.Fatal("a module ran without its isolated server")
	}
}

// TestStartContainersReportsUnusableWorkspacePaths checks the adapter when the fixtures
// directory or the generated environment file cannot be written.
func TestStartContainersReportsUnusableWorkspacePaths(t *testing.T) {
	fakeDocker(t, 0)

	blockedFixtures := dockerWorkspace(t, nil)
	writeFixture(t, blockedFixtures.path("fixtures"), nil)
	e := &Environment{Workspace: blockedFixtures}
	if err := e.startContainers(t.Context(), map[string]string{}, os.Stderr); err == nil {
		t.Fatal("a fixtures path that is a file was accepted")
	}

	blockedEnv := dockerWorkspace(t, nil)
	if err := os.Mkdir(blockedEnv.path("mdm", EnvFile), 0o700); err != nil {
		t.Fatal(err)
	}
	e = &Environment{Workspace: blockedEnv}
	if err := e.startContainers(t.Context(), map[string]string{}, os.Stderr); err == nil {
		t.Fatal("an unwritable container environment was accepted")
	}
}

// TestStopContainersReportsComposeFailure checks that a failed teardown is reported rather
// than leaving the adapter believing the stack is still running.
func TestStopContainersReportsComposeFailure(t *testing.T) {
	fakeDocker(t, 1)
	e := &Environment{Workspace: dockerWorkspace(t, nil), containers: true}
	e.stopContainers()
	if e.containers {
		t.Fatal("a failed teardown left the stack recorded as running")
	}
}

// TestRepoRootFallsBackToTheWorkspace checks compose-file resolution when neither the
// workspace nor the working directory is inside a checkout.
func TestRepoRootFallsBackToTheWorkspace(t *testing.T) {
	w := dockerWorkspace(t, nil)
	t.Chdir(t.TempDir())
	outside := filepath.Join(t.TempDir(), "deep", "workspace")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	w.Directory = outside
	if got := w.repoRoot(); got != outside {
		t.Fatalf("root = %s, want the workspace itself", got)
	}
}

// TestContainerPrerequisitesReportsAnUnreachableDaemon checks the doctor report when the
// docker command exists but its daemon does not answer.
func TestContainerPrerequisitesReportsAnUnreachableDaemon(t *testing.T) {
	fakeDocker(t, 1)
	w := dockerWorkspace(t, nil)
	blocked, _ := w.containerPrerequisites()["Blocked"].([]string)
	if !strings.Contains(strings.Join(blocked, " "), "daemon is not reachable") {
		t.Fatalf("blocked = %v", blocked)
	}
}

// TestReissueTLSReportsUnwritableIdentity checks that a leaf that cannot be written is an
// error rather than a silently unchanged identity.
func TestReissueTLSReportsUnwritableIdentity(t *testing.T) {
	w := dockerWorkspace(t, nil)
	if err := os.Remove(w.path("mdm", "tls.pem")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(w.path("mdm", "tls.pem"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ReissueTLS(w, []string{"mdm.lab.test"}); err == nil {
		t.Fatal("an unwritable HTTPS leaf was accepted")
	}
}

// TestInitRejectsAWorkspacePathThatIsAFile checks workspace creation against a file.
func TestInitRejectsAWorkspacePathThatIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, file, nil)
	if err := Init(file, "simulated", "inmem", "127.0.0.1:0", AdapterProcess, nil); err == nil {
		t.Fatal("a workspace path that is a file was accepted")
	}
}

// TestLoadDefaultsTheAdapterAndReportsUnreadableCredentials checks a workspace document
// written without an adapter, and a credential file that cannot be read.
func TestLoadDefaultsTheAdapterAndReportsUnreadableCredentials(t *testing.T) {
	dir := t.TempDir()
	document := `{"Version":2,"Mode":"simulated","Storage":"inmem","Listen":"127.0.0.1:0"}`
	if err := os.WriteFile(filepath.Join(dir, WorkspaceFile), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := Load(dir)
	if err != nil || w.Adapter != AdapterProcess {
		t.Fatalf("adapter = %q: %v", w.Adapter, err)
	}

	live := dockerWorkspace(t, nil)
	if err := os.Remove(live.path("mdm", "admin-token")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(live.path("mdm", "admin-token"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := live.token(); err == nil {
		t.Fatal("an unreadable administrator credential was accepted")
	}
}

// TestContainerPrerequisitesWithoutAWorkspaceDirectory checks the free-space probe when
// the workspace has been removed.
func TestContainerPrerequisitesWithoutAWorkspaceDirectory(t *testing.T) {
	w := dockerWorkspace(t, nil)
	if err := os.RemoveAll(w.Directory); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.containerPrerequisites()["FreeBytes"]; ok {
		t.Fatal("free space was reported for a removed workspace")
	}
}

// TestStartWithTheContainerAdapterUsesThePublishedPort checks that the container adapter
// reaches the server through the published port rather than a listener of its own.
func TestStartWithTheContainerAdapterUsesThePublishedPort(t *testing.T) {
	fakeDocker(t, 0)
	w := dockerWorkspace(t, []string{"mdm.lab.test"})
	ctx, cancel := context.WithCancel(t.Context())
	// The fake stack never serves, so readiness is cut short rather than waited out.
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	_, err := Start(ctx, w, "", io.Discard)
	if err == nil {
		t.Fatal("a stack that never becomes ready was accepted")
	}
	// #nosec G304 -- test-controlled workspace path
	env, readErr := os.ReadFile(w.path("mdm", EnvFile))
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, want := range []string{"DM_LISTEN=" + containerListen, "DM_PUBLIC_URL=https://mdm.lab.test:18443"} {
		if !strings.Contains(string(env), want) {
			t.Fatalf("container environment lacks %q:\n%s", want, env)
		}
	}
}
