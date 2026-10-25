package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
)

// AxMConfig connects the reference server to Apple Business Manager or
// Apple School Manager (decision record 0030). Inactive until ClientID
// and KeyID are set with a key.
type AxMConfig struct {
	ClientID string
	KeyID    string
	// KeyFile is the PEM private key downloaded from the portal; KeyPEM is
	// its content (tests set it directly).
	KeyFile string
	KeyPEM  []byte
	// Scope, BaseURL, and TokenURL override the defaults derived from the
	// client id (tests point them at a fake).
	Scope, BaseURL, TokenURL string
	HTTPClient               *http.Client
	// RootCAFile supplies private HTTPS trust instead of HTTPClient.
	RootCAFile string
}

// Enabled reports whether the client is configured.
func (c AxMConfig) Enabled() bool { return c.ClientID != "" }

// validate requires a key identifier and private key when the Apple Business Manager
// client is enabled.
func (c AxMConfig) validate() error {
	if !c.Enabled() {
		return nil
	}
	if c.KeyID == "" || (c.KeyFile == "" && len(c.KeyPEM) == 0) {
		return fmt.Errorf("%w: AxM needs a key id and a private key", ErrConfig)
	}
	return nil
}

// newAxM builds the client from the configuration.
func (a *App) newAxM(ctx context.Context) (*axm.Client, error) {
	c := a.cfg.AxM
	pemBytes := c.KeyPEM
	if len(pemBytes) == 0 {
		var err error
		if pemBytes, err = os.ReadFile(c.KeyFile); err != nil {
			return nil, fmt.Errorf("%w: AxM key file: %w", ErrConfig, err)
		}
	}
	httpClient, err := outboundClient(c.HTTPClient, c.RootCAFile)
	if err != nil {
		return nil, err
	}
	client, err := axm.New(ctx, axm.Config{
		ClientID:      c.ClientID,
		KeyID:         c.KeyID,
		PrivateKeyPEM: pemBytes,
		Scope:         c.Scope,
		BaseURL:       c.BaseURL,
		TokenURL:      c.TokenURL,
		HTTPClient:    httpClient,
		Clock:         a.cfg.Clock,
		Logger:        a.cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("AxM: %w", err)
	}
	return client, nil
}

// axmHandler is the admin API for Business Manager (mounted under the
// admin prefix as /axm/...):
//
//	GET  /axm/servers                        list device management servers
//	GET  /axm/devices?cursor=&limit=          list organisation devices
//	GET  /axm/activities/{id}
func (a *App) axmRoutes(client *axm.Client) []adminRoute {
	var routes []adminRoute
	actions := map[string]string{"GET /axm/servers": ActionReadBusinessMgr, "GET /axm/devices": ActionReadBusinessMgr, "GET /axm/activities/{id}": ActionReadBusinessMgr}
	add := func(pattern string, handler http.HandlerFunc) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: actions[pattern], Family: "axm", Handler: handler})
	}
	add("GET /axm/servers", func(w http.ResponseWriter, r *http.Request) {
		page, err := client.ListMDMServers(r.Context(), listOptions(r))
		if err != nil {
			writeError(w, axmStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	})
	add("GET /axm/devices", func(w http.ResponseWriter, r *http.Request) {
		page, err := client.ListOrgDevices(r.Context(), listOptions(r))
		if err != nil {
			writeError(w, axmStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	})
	add("GET /axm/activities/{id}", func(w http.ResponseWriter, r *http.Request) {
		act, err := client.GetOrgDeviceActivity(r.Context(), r.PathValue("id"), axm.GetOptions{})
		if err != nil {
			writeError(w, axmStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, act)
	})
	return routes
}

// listOptions reads the cursor and optional numeric page limit for an Apple Business
// Manager request.
func listOptions(r *http.Request) axm.ListOptions {
	o := axm.ListOptions{Cursor: r.URL.Query().Get("cursor")}
	if v := r.URL.Query().Get("limit"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			o.Limit = n
		}
	}
	return o
}

// axmStatus maps client errors to admin API statuses without leaking
// Apple's response bodies.
func axmStatus(err error) int {
	switch {
	case axm.IsNotFound(err):
		return http.StatusNotFound
	case axm.IsConflict(err):
		return http.StatusConflict
	case axm.IsRateLimited(err):
		return http.StatusTooManyRequests
	case errors.Is(err, axm.ErrArgument),
		errors.Is(err, axm.ErrActivityRule),
		errors.Is(err, axm.ErrLimit):
		return http.StatusBadRequest
	case errors.Is(err, axm.ErrWaitTimeout), errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}

// AxMStatusForTests exposes the error mapping to tests of the wiring.
func AxMStatusForTests(err error) int { return axmStatus(err) }
