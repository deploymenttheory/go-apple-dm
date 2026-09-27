package lab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// AttachURL connects to an existing live server using the workspace's trust and
// admin credential. It does not start a supervisor, replace state or migrate SQL.
func AttachURL(w *Workspace, address string) (*Environment, error) {
	u, err := url.Parse(address)
	if err != nil || w == nil || w.Mode != "live" || u.Scheme != "https" || u.Host == "" ||
		u.User != nil ||
		(u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" ||
		u.Fragment != "" ||
		u.ForceQuery {
		return nil, fmt.Errorf(
			"%w: direct attachment requires a live workspace and HTTPS server origin",
			errOperation,
		)
	}
	c, err := w.client()
	if err != nil {
		return nil, err
	}
	token, err := w.token()
	if err != nil {
		c.CloseIdleConnections()
		return nil, err
	}
	if token == "" {
		c.CloseIdleConnections()
		return nil, fmt.Errorf("%w: direct attachment requires an admin credential", errOperation)
	}
	// Keep credentials and requests on the operator-selected origin.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Environment{
		Instance: Instance{
			URL:  strings.TrimSuffix(address, "/"),
			Mode: w.Mode,
		},
		cancel:    func() {},
		Client:    c,
		Token:     token,
		Workspace: w,
	}, nil
}

// Connection attaches the harness to a managed deployment without creating another
// database or identity set. Paths refer to the host filesystem.
type Connection struct {
	URL, CAFile, TokenFile, EvidenceDirectory string
}

// Connect validates trust and credentials and verifies the managed principal.
// Close releases the HTTP transport; it never stops the attached server.
func Connect(ctx context.Context, cfg Connection) (*Environment, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, fmt.Errorf("%w: connection requires an HTTPS origin", errOperation)
	}
	certs, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read connection CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certs) {
		return nil, fmt.Errorf("%w: connection CA is not PEM", errOperation)
	}
	raw, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("read managed credential: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return nil, fmt.Errorf("%w: empty managed credential", errOperation)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	client := &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	e := &Environment{
		Instance: Instance{URL: strings.TrimSuffix(cfg.URL, "/"), Mode: "live"},
		Client:   client, Token: token, cancel: func() {}, Workspace: &Workspace{Mode: "live", Directory: cfg.EvidenceDirectory},
	}
	if _, status, err := HTTP(ctx, client, e.URL, token, "GET", "/auth/me", nil); err != nil || status != http.StatusOK {
		client.CloseIdleConnections()
		return nil, fmt.Errorf("%w: managed credential verification failed (HTTP %d)", errOperation, status)
	}
	return e, nil
}
