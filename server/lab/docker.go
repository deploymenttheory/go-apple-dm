package lab

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Container layout. The workspace's mdm directory is the container's data volume, so
// identities, the database and any configured policy file keep one home on the host.
const (
	containerData   = "/data"
	containerListen = "0.0.0.0:8443"
	// ComposeFile is the lab stack, relative to the repository root.
	ComposeFile = "deploy/lab/compose.yaml"
	// EnvFile is the generated container environment, private to the workspace.
	EnvFile = "lab.env"
)

// dockerCompose runs a compose command for the workspace's project.
func (e *Environment) dockerCompose(ctx context.Context, out io.Writer, args ...string) error {
	project := "dm-lab-" + filepath.Base(e.Workspace.Directory)
	full := append([]string{"compose", "--project-name", project, "--file", e.composeFile()}, args...)
	// #nosec G204 -- Fixed compose verbs with the operator's own workspace and checkout paths.
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = e.Workspace.repoRoot()
	cmd.Env = append(os.Environ(),
		"LAB_MDM_DIR="+e.Workspace.path("mdm"),
		"LAB_FIXTURES_DIR="+e.Workspace.path("fixtures"),
		"LAB_ENV_FILE="+e.Workspace.path("mdm", EnvFile),
		"LAB_BIND="+e.Workspace.bind(),
		"LAB_PORT="+e.Workspace.port(),
		"LAB_FIXTURES_PORT="+e.Workspace.fixturesPort(),
		"LAB_CONFIG_DIR="+filepath.Join(e.Workspace.repoRoot(), "deploy", "lab"),
	)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: docker compose %s: %w", errOperation, strings.Join(args, " "), err)
	}
	return nil
}

// composeFile resolves the lab compose file inside the repository.
func (e *Environment) composeFile() string {
	return filepath.Join(e.Workspace.repoRoot(), filepath.FromSlash(ComposeFile))
}

// repoRoot locates the checkout holding the compose file: the workspace's own setting
// first, then a walk up from the workspace, then a walk up from the working directory. A
// workspace usually lives at test-lab/local inside the checkout, but it does not have to.
func (w *Workspace) repoRoot() string {
	if root := w.Settings["LAB_REPO_ROOT"]; root != "" {
		return root
	}
	starts := []string{w.Directory}
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	for _, start := range starts {
		dir := start
		for range 6 {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(ComposeFile))); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return w.Directory
}

// bind is the host address the container publishes on; loopback when unset.
func (w *Workspace) bind() string {
	if w.Bind != "" {
		return w.Bind
	}
	return "127.0.0.1"
}

// port is the published HTTPS port from the workspace listener.
func (w *Workspace) port() string {
	_, port, err := net.SplitHostPort(w.Listen)
	if err != nil || port == "0" {
		return "8443"
	}
	return port
}

// fixturesPort is the published static-fixture port, one above the server port.
func (w *Workspace) fixturesPort() string {
	if w.Settings["LAB_FIXTURES_PORT"] != "" {
		return w.Settings["LAB_FIXTURES_PORT"]
	}
	return "9443"
}

// containerEnv rewrites host paths under the workspace's mdm directory to their container
// locations and replaces the listener with the container's own.
func (w *Workspace) containerEnv(env map[string]string) map[string]string {
	host := w.path("mdm")
	out := make(map[string]string, len(env))
	for k, v := range env {
		if strings.HasPrefix(v, host) {
			v = containerData + filepath.ToSlash(strings.TrimPrefix(v, host))
		}
		out[k] = v
	}
	out["DM_LISTEN"] = containerListen
	return out
}

// writeEnvFile writes the container environment as a private compose env file.
func (w *Workspace) writeEnvFile(env map[string]string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		// Compose reads KEY=VALUE lines literally; values never contain newlines.
		fmt.Fprintf(&b, "%s=%s\n", k, strings.ReplaceAll(env[k], "\n", ""))
	}
	return privateOverwrite(w.path("mdm", EnvFile), []byte(b.String()))
}

// startContainers writes the container environment, builds the image and waits for the
// published server to become ready.
func (e *Environment) startContainers(ctx context.Context, env map[string]string, out io.Writer) error {
	w := e.Workspace
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("%w: docker is required by the container adapter", ErrBlocked)
	}
	if _, err := os.Stat(e.composeFile()); err != nil {
		return fmt.Errorf("%w: %s is not in the checkout", ErrBlocked, ComposeFile)
	}
	if err := os.MkdirAll(w.path("fixtures"), 0o700); err != nil {
		return wrapError(err)
	}
	if err := w.writeEnvFile(w.containerEnv(env)); err != nil {
		return err
	}
	if err := e.dockerCompose(ctx, out, "up", "--build", "--detach", "--wait"); err != nil {
		return err
	}
	e.containers = true
	return nil
}

// stopContainers removes the workspace's containers, leaving its bind-mounted state.
func (e *Environment) stopContainers() {
	if !e.containers {
		return
	}
	e.containers = false
	// A stop runs after the run's context is cancelled, so it uses its own.
	if err := e.dockerCompose(context.WithoutCancel(context.Background()), io.Discard, "down"); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
