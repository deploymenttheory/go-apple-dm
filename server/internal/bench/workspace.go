// Package bench drives the reference server through maintained scenarios.
package bench

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Workspace contains non-secret settings. Existing local identities stay in mdm/.
//
//nolint:tagliatelle // Preserve the private workspace document schema.
type Workspace struct {
	Settings  map[string]string `json:"Settings,omitempty"`
	Version   int               `json:"Version"`
	Mode      string            `json:"Mode"`
	Storage   string            `json:"Storage"`
	Topology  string            `json:"Topology"`
	Listen    string            `json:"Listen"`
	DSN       string            `json:"DSN,omitempty"`
	Directory string            `json:"-"`
}

func Init(dir, mode, storage, topology, listen string) error {
	if mode != "simulated" && mode != "live" {
		return fmt.Errorf("%w: mode must be simulated or live", errOperation)
	}
	if topology != "all" && topology != "split" {
		return fmt.Errorf("%w: topology must be all or split", errOperation)
	}
	if storage != "sqlite" && storage != "inmem" && storage != "postgres" && storage != "mysql" {
		return fmt.Errorf("%w: unknown storage", errOperation)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return wrapError(err)
	}
	if err = os.MkdirAll(root, 0o700); err != nil {
		return wrapError(err)
	}
	if _, err = os.Stat(filepath.Join(root, "bench.json")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: workspace already exists; identities were preserved", errOperation)
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
		if err = initialize(mdm); err != nil {
			return wrapError(err)
		}
	} else if found != len(names) {
		return fmt.Errorf(
			"%w: partial identity directory; restore missing files before initialization",
			errOperation,
		)
	}
	b, err := json.MarshalIndent(
		Workspace{Version: 1, Mode: mode, Storage: storage, Topology: topology, Listen: listen},
		"",
		"  ",
	)
	if err != nil {
		return wrapError(err)
	}
	return privateFile(filepath.Join(root, "bench.json"), append(b, '\n'))
}

func Load(dir string) (*Workspace, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, wrapError(err)
	}
	b, err := os.ReadFile(
		filepath.Join(root, "bench.json"),
	) // #nosec G304 -- Operator-selected local CLI workspace, not HTTP input.
	if err != nil {
		return nil, wrapError(err)
	}
	var w Workspace
	if err = json.Unmarshal(b, &w); err != nil {
		return nil, wrapError(err)
	}
	if w.Version != 1 || (w.Mode != "simulated" && w.Mode != "live") ||
		(w.Topology != "all" && w.Topology != "split") {
		return nil, fmt.Errorf("%w: invalid workspace configuration", errOperation)
	}
	w.Directory = root
	return &w, nil
}

func (w *Workspace) path(parts ...string) string {
	return filepath.Join(append([]string{w.Directory}, parts...)...)
}

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

func (w *Workspace) token() (string, error) {
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
	return map[string]any{
		"Mode":              w.Mode,
		"Storage":           w.Storage,
		"Topology":          w.Topology,
		"Missing":           missing,
		"LivePrerequisites": live,
	}
}

// HTTP performs a bounded admin request. Errors contain status, never bodies or tokens.
func HTTP(
	ctx context.Context,
	c *http.Client,
	base, token, method, path string,
	body io.Reader,
) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+"/admin/v1"+path, body)
	if err != nil {
		return nil, 0, wrapError(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: bench: server request failed", errOperation)
	}
	defer resp.Body.Close()
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
