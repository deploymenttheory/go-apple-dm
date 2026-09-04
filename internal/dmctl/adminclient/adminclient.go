package adminclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Errors callers distinguish.
var (
	// ErrUnauthorized is a 401: no credential, or one the server does not know.
	ErrUnauthorized = errors.New("adminclient: unauthorized")
	// ErrForbidden is a 403: authenticated, but no policy permits it.
	ErrForbidden = errors.New("adminclient: forbidden")
	// ErrNotFound is a 404, which on this API may also mean the route is not
	// served by the role the process is running.
	ErrNotFound = errors.New("adminclient: not found")
	// ErrStatus is any other unsuccessful status.
	ErrStatus = errors.New("adminclient: request failed")
	// ErrConfig is a malformed server URL or missing credential.
	ErrConfig = errors.New("adminclient: bad configuration")
)

// DefaultTimeout bounds one request.
const DefaultTimeout = 30 * time.Second

// MaxBody bounds a response body, so a hostile or broken server cannot make
// the CLI buffer without limit.
const MaxBody = 32 << 20

// Config builds a Client.
type Config struct {
	// BaseURL is the server root, without the admin prefix.
	BaseURL string
	// Token is the bearer credential.
	Token string
	// Timeout bounds one request; zero uses DefaultTimeout.
	Timeout time.Duration
	// Insecure skips TLS verification. The CLI warns on every use.
	Insecure bool
	// HTTPClient overrides the transport, for tests.
	HTTPClient *http.Client
	// Trace receives one line per request, with the token never included.
	Trace func(string)
}

// Client talks to one server.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
	trace func(string)
}

// Prefix is the admin API root every path is relative to.
const Prefix = "/admin/v1"

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("%w: no server URL", ErrConfig)
	}
	u, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("%w: server URL: %w", ErrConfig, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: server URL scheme %q (want http or https)", ErrConfig, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: server URL has no host", ErrConfig)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	// Insecure was declared but never acted on, so -insecure silently did
	// nothing and an operator testing against a self-signed lab certificate
	// got a verification failure they had already tried to opt out of. It
	// applies only to a client this package built: overriding the transport
	// of one the caller supplied would be surprising.
	if cfg.Insecure && cfg.HTTPClient == nil {
		hc.Transport = &http.Transport{
			// #nosec G402 -- the operator asked for this explicitly with
			// -insecure, and it is refused for anything but a lab above.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		}
	}
	if cfg.Timeout > 0 {
		hc.Timeout = cfg.Timeout
	} else if hc.Timeout == 0 {
		hc.Timeout = DefaultTimeout
	}
	// A redirect would replay the bearer token to a host the operator did not
	// name, so the client refuses to follow one.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{base: u, token: cfg.Token, http: hc, trace: cfg.Trace}, nil
}

// Response is one admin API answer. Body is the server's bytes, unmodified,
// so canonical JSON and key order survive to whatever consumes them.
type Response struct {
	Status int
	Body   []byte
}

// Do issues one request. path is relative to the admin prefix, for example
// "/principals" or "/declarations/com.example.a".
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any) (*Response, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + Prefix + path
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rdr = bytes.NewReader(b)
		case string:
			rdr = strings.NewReader(b)
		default:
			raw, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("adminclient: encode body: %w", err)
			}
			rdr = bytes.NewReader(raw)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, fmt.Errorf("adminclient: request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	if c.trace != nil {
		// The token is never traced; only the request line and the target.
		c.trace(fmt.Sprintf("%s %s", method, u.Redacted()))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adminclient: %s %s: %w", method, u.Redacted(), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, fmt.Errorf("adminclient: read body: %w", err)
	}
	if c.trace != nil {
		c.trace(fmt.Sprintf("  -> %d (%d bytes)", resp.StatusCode, len(raw)))
	}
	out := &Response{Status: resp.StatusCode, Body: raw}
	if resp.StatusCode >= 400 {
		return out, statusError(resp.StatusCode, raw)
	}
	return out, nil
}

// statusError turns a failure into a typed error carrying the server's own
// message when it sent one.
func statusError(status int, body []byte) error {
	msg := serverMessage(body)
	base := ErrStatus
	switch status {
	case http.StatusUnauthorized:
		base = ErrUnauthorized
	case http.StatusForbidden:
		base = ErrForbidden
	case http.StatusNotFound:
		base = ErrNotFound
	}
	if msg == "" {
		return fmt.Errorf("%w: %d", base, status)
	}
	return fmt.Errorf("%w: %d: %s", base, status, msg)
}

// serverMessage reads the {"Error":"..."} body the admin API returns, falling
// back to a trimmed excerpt when the body is not that shape.
func serverMessage(body []byte) string {
	var e struct{ Error string }
	if err := json.Unmarshal(body, &e); err == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// page is the shape every paged listing returns.
type page struct {
	Items      []jsontext.Value
	NextCursor string
}

// Each calls fn for every item of a paged listing, following NextCursor until
// the server stops returning one. None of the reference admin CLIs paginate,
// so a large fleet silently truncates for them.
//
// fn receives each item's raw JSON, unmodified.
func (c *Client) Each(ctx context.Context, path string, query url.Values, fn func(jsontext.Value) error) error {
	if query == nil {
		query = url.Values{}
	}
	seen := make(map[string]bool)
	for {
		resp, err := c.Do(ctx, http.MethodGet, path, query, nil)
		if err != nil {
			return err
		}
		var p page
		if err := json.Unmarshal(resp.Body, &p); err != nil {
			return fmt.Errorf("adminclient: decode page: %w", err)
		}
		for _, item := range p.Items {
			if err := fn(item); err != nil {
				return err
			}
		}
		if p.NextCursor == "" {
			return nil
		}
		// A server that returned the same cursor forever would spin here.
		if seen[p.NextCursor] {
			return fmt.Errorf("%w: repeated cursor %q", ErrStatus, p.NextCursor)
		}
		seen[p.NextCursor] = true
		query.Set("cursor", p.NextCursor)
	}
}

// Page fetches one page and returns its items and the next cursor.
func (c *Client) Page(ctx context.Context, path string, query url.Values) ([]jsontext.Value, string, error) {
	resp, err := c.Do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, "", err
	}
	var p page
	if err := json.Unmarshal(resp.Body, &p); err != nil {
		return nil, "", fmt.Errorf("adminclient: decode page: %w", err)
	}
	return p.Items, p.NextCursor, nil
}

// ServerConfig describes the server: its role, the route families it serves,
// the version, and which admin credentials it accepts. The CLI reads it to
// explain a 404 that is really a role split, and to report a break-glass
// token that outlived its bootstrap.
type ServerConfig struct {
	Role     string
	Version  string
	Families []string
	Routes   []Route
	// Policy reports a principal and Cedar policy store.
	Policy bool
	// BreakGlass reports that the static DM_ADMIN_TOKEN is still accepted.
	BreakGlass bool
}

// Route is one entry of the server's route table.
type Route struct {
	Method, Pattern, Action, Family string
}

// ServerConfig fetches GET /config.
func (c *Client) ServerConfig(ctx context.Context) (*ServerConfig, error) {
	resp, err := c.Do(ctx, http.MethodGet, "/config", nil, nil)
	if err != nil {
		return nil, err
	}
	var out ServerConfig
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("adminclient: decode config: %w", err)
	}
	return &out, nil
}
