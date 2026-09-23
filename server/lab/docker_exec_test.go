//go:build !windows

package lab

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The container adapter runs on Unix hosts, and these tests stand in a shell script for
// the docker command, so they build there.

// fakeDocker puts a recording docker command first on PATH and returns the file it logs
// its arguments to. exit selects the command's exit status.
func fakeDocker(t *testing.T, exit int) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + log + "\"\n" +
		"printf 'LAB_MDM_DIR=%s\\n' \"$LAB_MDM_DIR\" >> \"" + log + "\"\n" +
		"printf 'LAB_PORT=%s\\n' \"$LAB_PORT\" >> \"" + log + "\"\n" +
		"exit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil { // #nosec G306 -- test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// calls returns the recorded invocations of the fake docker command.
func calls(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log) // #nosec G304 -- test-controlled temporary path
	if err != nil {
		return ""
	}
	return string(b)
}

// TestStartContainersWritesEnvironmentAndStartsTheStack checks the container adapter's
// compose invocation, the generated environment and the fixtures directory.
func TestStartContainersWritesEnvironmentAndStartsTheStack(t *testing.T) {
	log := fakeDocker(t, 0)
	w := dockerWorkspace(t, []string{"mdm.lab.test"})
	e := &Environment{Workspace: w}
	env := map[string]string{"DM_DSN": w.path("mdm", "mdm.sqlite"), "DM_LISTEN": "127.0.0.1:18443"}

	if err := e.startContainers(t.Context(), env, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if !e.containers {
		t.Fatal("the adapter did not record a running stack")
	}
	recorded := calls(t, log)
	for _, want := range []string{
		"compose", "--project-name dm-lab-", "up --build --detach --wait",
		"LAB_MDM_DIR=" + w.path("mdm"), "LAB_PORT=18443",
	} {
		if !strings.Contains(recorded, want) {
			t.Fatalf("compose call lacks %q:\n%s", want, recorded)
		}
	}
	// #nosec G304 -- test-controlled workspace path
	envFile, err := os.ReadFile(w.path("mdm", EnvFile))
	if err != nil || !strings.Contains(string(envFile), "DM_DSN=/data/mdm.sqlite") {
		t.Fatalf("env file: %s %v", envFile, err)
	}
	if info, err := os.Stat(w.path("fixtures")); err != nil || !info.IsDir() {
		t.Fatalf("fixtures directory: %v", err)
	}

	e.stopContainers()
	if e.containers || !strings.Contains(calls(t, log), "down") {
		t.Fatalf("stop did not remove the stack:\n%s", calls(t, log))
	}
	// Stopping again is a no-op, so a finished run cannot remove a later one's stack.
	before := calls(t, log)
	e.stopContainers()
	if calls(t, log) != before {
		t.Fatal("a second stop ran compose again")
	}
}

// TestStartContainersReportsMissingPrerequisites checks that an absent docker command or
// compose file blocks the run instead of failing it.
func TestStartContainersReportsMissingPrerequisites(t *testing.T) {
	w := dockerWorkspace(t, nil)
	e := &Environment{Workspace: w}

	t.Setenv("PATH", t.TempDir())
	if err := e.startContainers(t.Context(), map[string]string{}, os.Stderr); !errors.Is(err, ErrBlocked) {
		t.Fatalf("missing docker: %v", err)
	}

	fakeDocker(t, 0)
	w.Settings = map[string]string{"LAB_REPO_ROOT": t.TempDir()}
	if err := e.startContainers(t.Context(), map[string]string{}, os.Stderr); !errors.Is(err, ErrBlocked) {
		t.Fatalf("missing compose file: %v", err)
	}
}

// TestStartContainersReportsComposeFailure checks that a failing compose command is an
// error naming the operation, and that the adapter does not claim a running stack.
func TestStartContainersReportsComposeFailure(t *testing.T) {
	fakeDocker(t, 1)
	w := dockerWorkspace(t, nil)
	e := &Environment{Workspace: w}
	err := e.startContainers(t.Context(), map[string]string{}, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "docker compose up") {
		t.Fatalf("error = %v", err)
	}
	if e.containers {
		t.Fatal("a failed start recorded a running stack")
	}
	// A stop after a failed start must not run compose.
	e.stopContainers()
}
