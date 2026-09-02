package app

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/axm"
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
}

// Enabled reports whether the client is configured.
func (c AxMConfig) Enabled() bool { return c.ClientID != "" }

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
	client, err := axm.New(ctx, axm.Config{
		ClientID:      c.ClientID,
		KeyID:         c.KeyID,
		PrivateKeyPEM: pemBytes,
		Scope:         c.Scope,
		BaseURL:       c.BaseURL,
		TokenURL:      c.TokenURL,
		HTTPClient:    c.HTTPClient,
		Clock:         a.cfg.Clock,
		Logger:        a.cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("app: AxM: %w", err)
	}
	return client, nil
}

// MaxAxMSerials bounds one assignment request.
const MaxAxMSerials = 1000

// axmHandler is the admin API for Business Manager (mounted under the
// admin prefix as /axm/...):
//
//	GET  /axm/servers                        list device management servers
//	GET  /axm/devices?cursor=&limit=          list organisation devices
//	POST /axm/assign   {"server":"..","serials":[..],"wait":true}
//	POST /axm/unassign {"serials":[..],"wait":true}
//	GET  /axm/activities/{id}
func (a *App) axmHandler(client *axm.Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /axm/servers", func(w http.ResponseWriter, r *http.Request) {
		page, err := client.ListMDMServers(r.Context(), listOptions(r))
		if err != nil {
			writeError(w, axmStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	})
	mux.HandleFunc("GET /axm/devices", func(w http.ResponseWriter, r *http.Request) {
		page, err := client.ListOrgDevices(r.Context(), listOptions(r))
		if err != nil {
			writeError(w, axmStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	})
	mux.HandleFunc("GET /axm/activities/{id}", func(w http.ResponseWriter, r *http.Request) {
		act, err := client.GetOrgDeviceActivity(r.Context(), r.PathValue("id"), axm.GetOptions{})
		if err != nil {
			writeError(w, axmStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, act)
	})
	type assignRequest struct {
		Server  string   `json:"server"`
		Serials []string `json:"serials"`
		Wait    bool     `json:"wait"`
	}
	activity := func(w http.ResponseWriter, r *http.Request, run func(ctx context.Context, req assignRequest) (*axm.OrgDeviceActivity, error)) {
		var req assignRequest
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
		if err != nil || len(body) > MaxAdminBody {
			writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
			return
		}
		if err := json.Unmarshal(
			body,
			&req,
		); err != nil || len(req.Serials) == 0 ||
			len(req.Serials) > MaxAxMSerials {
			writeError(
				w,
				http.StatusBadRequest,
				fmt.Errorf("%w: body needs 1 to %d serials", ErrBadAxMRequest, MaxAxMSerials),
			)
			return
		}
		act, err := run(r.Context(), req)
		if err != nil {
			writeError(w, axmStatus(err), err)
			return
		}
		if req.Wait {
			ctx, cancel := context.WithTimeout(r.Context(), AxMWaitTimeout)
			defer cancel()
			if act, err = client.WaitForActivity(
				ctx,
				act.ID,
				axm.WaitOptions{Interval: AxMWaitInterval, Timeout: AxMWaitTimeout},
			); err != nil {
				writeError(w, axmStatus(err), err)
				return
			}
		}
		writeJSON(w, http.StatusAccepted, act)
	}
	mux.HandleFunc("POST /axm/assign", func(w http.ResponseWriter, r *http.Request) {
		activity(
			w,
			r,
			func(ctx context.Context, req assignRequest) (*axm.OrgDeviceActivity, error) {
				return client.AssignDevices(ctx, req.Server, req.Serials)
			},
		)
	})
	mux.HandleFunc("POST /axm/unassign", func(w http.ResponseWriter, r *http.Request) {
		activity(
			w,
			r,
			func(ctx context.Context, req assignRequest) (*axm.OrgDeviceActivity, error) {
				return client.UnassignDevices(ctx, req.Serials)
			},
		)
	})
	return mux
}

// Waits used by the admin API when the caller asks to wait.
var (
	AxMWaitInterval = 2 * time.Second
	AxMWaitTimeout  = 5 * time.Minute
)

// ErrBadAxMRequest reports an invalid assignment body.
var ErrBadAxMRequest = errors.New("app: invalid Business Manager request")

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
	case errors.Is(err, axm.ErrWaitTimeout):
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}

// AxMStatusForTests exposes the error mapping to tests of the wiring.
func AxMStatusForTests(err error) int { return axmStatus(err) }
