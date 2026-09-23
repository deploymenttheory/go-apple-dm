package lab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WorkspaceFile is the private workspace document. LegacyWorkspaceFile is the name used
// before the lab replaced the bench; Load still reads it so existing live workspaces keep
// their identities, database and storage-key names.
const (
	WorkspaceFile       = "lab.json"
	LegacyWorkspaceFile = "bench.json"
)

// Workspace contains non-secret settings. Existing local identities stay in mdm/.
//
// Preserve the private workspace document schema.
type Workspace struct {
	Settings map[string]string `json:"Settings,omitempty"`
	Version  int               `json:"Version"`
	Mode     string            `json:"Mode"`
	Storage  string            `json:"Storage"`
	Listen   string            `json:"Listen"`
	DSN      string            `json:"DSN,omitempty"`
	// Adapter is how dmserver runs: AdapterProcess (default) or AdapterDocker.
	Adapter string `json:"Adapter,omitempty"`
	// Hosts are the DNS names and IP addresses in the HTTPS leaf beyond loopback. The
	// first entry names the server in device-facing URLs.
	Hosts []string `json:"Hosts,omitempty"`
	// Bind is the host address the container adapter publishes its ports on. Empty
	// publishes on loopback only.
	Bind      string `json:"Bind,omitempty"`
	Directory string `json:"-"`
}

// Server adapters.
const (
	AdapterProcess = "process"
	AdapterDocker  = "docker"
)

// PublicBase returns the device-facing origin: the first configured host, or the
// loopback listener when no host is configured.
func (w *Workspace) PublicBase() string {
	_, port, err := net.SplitHostPort(w.Listen)
	if err != nil || len(w.Hosts) == 0 {
		return ""
	}
	return "https://" + net.JoinHostPort(w.Hosts[0], port)
}

// save rewrites the workspace document after a setting changes.
func (w *Workspace) save() error {
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return wrapError(err)
	}
	return wrapError(os.WriteFile(filepath.Join(w.Directory, WorkspaceFile), append(b, '\n'), 0o600))
}

// Init creates the persistent lab workspace with its selected mode, storage, listener and
// server adapter. hosts adds device-facing names to the generated HTTPS leaf.
func Init(dir, mode, storage, listen, adapter string, hosts []string) error {
	if mode != "simulated" && mode != "live" {
		return fmt.Errorf("%w: mode must be simulated or live", errOperation)
	}
	if storage != "sqlite" && storage != "inmem" && storage != "postgres" && storage != "mysql" {
		return fmt.Errorf("%w: unknown storage", errOperation)
	}
	if adapter == "" {
		adapter = AdapterProcess
	}
	if adapter != AdapterProcess && adapter != AdapterDocker {
		return fmt.Errorf("%w: adapter must be process or docker", errOperation)
	}
	if adapter == AdapterDocker && mode != "live" {
		return fmt.Errorf(
			"%w: the docker adapter runs live workspaces; simulated fixtures are local to the lab process",
			errOperation,
		)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return wrapError(err)
	}
	if err = os.MkdirAll(root, 0o700); err != nil {
		return wrapError(err)
	}
	for _, name := range []string{WorkspaceFile, LegacyWorkspaceFile} {
		if _, err = os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: workspace already exists; identities were preserved", errOperation)
		}
	}
	mdm := filepath.Join(root, "mdm")
	names := []string{
		"ca.pem",
		"ca.key",
		"tls.pem",
		"tls.key",
		"admin-token",
		"storage-key",
		"scep-challenge",
	}
	found := 0
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(mdm, name)); err == nil {
			found++
		}
	}
	if found == 0 {
		if err = initialize(mdm, hosts); err != nil {
			return wrapError(err)
		}
	} else if found != len(names) {
		return fmt.Errorf(
			"%w: partial identity directory; restore missing files before initialization",
			errOperation,
		)
	}
	b, err := json.MarshalIndent(
		Workspace{
			Version: 2, Mode: mode, Storage: storage, Listen: listen,
			Adapter: adapter, Hosts: hosts,
		},
		"",
		"  ",
	)
	if err != nil {
		return wrapError(err)
	}
	return privateFile(filepath.Join(root, WorkspaceFile), append(b, '\n'))
}

// Load reads and validates an existing persistent lab workspace.
func Load(dir string) (*Workspace, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, wrapError(err)
	}
	// #nosec G304 -- Operator-selected local CLI workspace, not HTTP input.
	b, err := os.ReadFile(filepath.Join(root, WorkspaceFile))
	if errors.Is(err, os.ErrNotExist) {
		// #nosec G304 -- Operator-selected local CLI workspace, not HTTP input.
		b, err = os.ReadFile(filepath.Join(root, LegacyWorkspaceFile))
	}
	if err != nil {
		return nil, wrapError(err)
	}
	var w Workspace
	if err = json.Unmarshal(b, &w); err != nil {
		return nil, wrapError(err)
	}
	if w.Version != 2 || (w.Mode != "simulated" && w.Mode != "live") {
		return nil, fmt.Errorf("%w: invalid workspace configuration", errOperation)
	}
	if w.Adapter == "" {
		w.Adapter = AdapterProcess
	}
	w.Directory = root
	return &w, nil
}

// Path resolves a workspace-owned artifact path for operator output.
func (w *Workspace) Path(parts ...string) string { return w.path(parts...) }

// path resolves a workspace-owned artifact path.
func (w *Workspace) path(parts ...string) string {
	return filepath.Join(append([]string{w.Directory}, parts...)...)
}

// client constructs the HTTP client using the workspace's server trust settings.
func (w *Workspace) client() (*http.Client, error) {
	b, err := os.ReadFile(w.path("mdm", "ca.pem"))
	if err != nil {
		return nil, wrapError(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("%w: invalid workspace CA", errOperation)
	}
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		},
	}, nil
}

// token loads the stored admin credential, falling back to the bootstrap token when the
// credential file is absent.
func (w *Workspace) token() (string, error) {
	b, err := os.ReadFile(w.path("mdm", "admin-credential"))
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return w.bootstrapToken()
}

// bootstrapToken loads the one-time bootstrap credential used to initialize lab
// administration.
func (w *Workspace) bootstrapToken() (string, error) {
	b, err := os.ReadFile(w.path("mdm", "admin-token"))
	return strings.TrimSpace(string(b)), wrapError(err)
}

// Doctor checks prerequisites without enrolling a device or altering trust.
func (w *Workspace) Doctor() map[string]any {
	missing := []string{}
	for _, p := range []string{"mdm/ca.pem", "mdm/ca.key", "mdm/tls.pem", "mdm/tls.key", "mdm/admin-token", "mdm/storage-key", "mdm/scep-challenge"} {
		if _, err := os.Stat(w.path(p)); err != nil {
			missing = append(missing, p)
		}
	}
	live := map[string][]string{}
	for lane, files := range map[string][]string{"mdm": {"mdm/push.pem", "mdm/push.key"}, "app": {"app/certificate.pem", "app/push.key", "app/registration.json"}, "vendor": {"vendor/chain.pem", "vendor/signing.key", "vendor/apple-roots.pem"}} {
		for _, f := range files {
			if _, err := os.Stat(w.path(f)); err != nil {
				live[lane] = append(live[lane], f)
			}
		}
	}
	report := map[string]any{
		"Mode":              w.Mode,
		"Storage":           w.Storage,
		"Adapter":           w.Adapter,
		"Missing":           missing,
		"LivePrerequisites": live,
	}
	if len(w.Hosts) > 0 {
		report["Hosts"] = w.Hosts
		report["PublicURL"] = w.PublicBase()
	}
	if w.Adapter == AdapterDocker {
		report["Containers"] = w.containerPrerequisites()
	}
	notes := []string{}
	if w.Mode == "live" && len(live["mdm"]) > 0 {
		// Enrollment needs a push topic, which comes from the MDM push certificate.
		notes = append(notes, "enrollment routes stay unmounted until mdm/push.pem and mdm/push.key are installed")
	}
	if w.Adapter == AdapterDocker && len(w.Hosts) == 0 {
		notes = append(notes, "no device-facing host is configured; run lab tls -hosts to add one")
	}
	if len(notes) > 0 {
		report["Notes"] = notes
	}
	return report
}

// containerPrerequisites reports what the container adapter needs from the host: the
// docker command, a reachable daemon, the compose file and free space for images.
func (w *Workspace) containerPrerequisites() map[string]any {
	out := map[string]any{"Bind": w.bind(), "Port": w.port(), "FixturesPort": w.fixturesPort()}
	blocked := []string{}
	path, err := exec.LookPath("docker")
	switch {
	case err != nil:
		blocked = append(blocked, "docker is not on PATH")
	default:
		out["Docker"] = path
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		version, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
		if err != nil {
			blocked = append(blocked, "the docker daemon is not reachable")
		} else {
			out["DaemonVersion"] = strings.TrimSpace(string(version))
		}
	}
	compose := filepath.Join(w.repoRoot(), filepath.FromSlash(ComposeFile))
	if _, err := os.Stat(compose); err != nil {
		blocked = append(blocked, ComposeFile+" is not in the checkout")
	} else {
		out["ComposeFile"] = compose
	}
	if free, err := freeBytes(w.Directory); err == nil {
		out["FreeBytes"] = free
		if free < 8<<30 {
			blocked = append(blocked, "less than 8 GiB free for images and state")
		}
	}
	out["Blocked"] = blocked
	return out
}

// HTTP performs a bounded admin request. Errors contain status, never bodies or tokens.
func HTTP(
	ctx context.Context,
	c *http.Client,
	base, token, method, path string,
	body io.Reader,
	headers ...http.Header,
) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+"/admin/v1"+path, body)
	if err != nil {
		return nil, 0, wrapError(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	for _, header := range headers {
		for key, values := range header {
			req.Header[key] = append([]string(nil), values...)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: lab: server request failed", errOperation)
	}
	defer func(body io.Closer) { _ = body.Close() }(resp.Body)
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, resp.StatusCode, wrapError(err)
	}
	if resp.StatusCode >= 400 {
		return b, resp.StatusCode, fmt.Errorf(
			"%w: %s %s returned HTTP %d",
			errOperation,
			method,
			path,
			resp.StatusCode,
		)
	}
	return b, resp.StatusCode, nil
}
