package lab

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
		Client:    c,
		Token:     token,
		Workspace: w,
	}, nil
}
