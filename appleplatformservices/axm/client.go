package axm

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/secrets"
)

// API hosts and defaults.
const (
	// BusinessBaseURL and SchoolBaseURL are the two API hosts; paths start
	// at /v1.
	BusinessBaseURL = "https://api-business.apple.com"
	SchoolBaseURL   = "https://api-school.apple.com"
	// DefaultTimeout bounds one HTTP request when Config.HTTPClient is nil.
	DefaultTimeout = 30 * time.Second
	// DefaultPageCap bounds the pages the iterators follow.
	DefaultPageCap = 100
	// MaxLimit is the largest page size Apple accepts.
	MaxLimit = 1000
	// DefaultUserAgent is sent when Config.UserAgent is empty.
	DefaultUserAgent = "go-apple-dm/axm"
	// maxBody bounds a JSON response body.
	maxBody = 32 << 20
)

// Retry bounds the retry policy: Max attempts after the first, Base the
// first backoff, Cap the largest backoff. A 429 waits for Retry-After
// instead of backing off.
type Retry struct {
	Max  int
	Base time.Duration
	Cap  time.Duration
}

// DefaultRetry is used when Config.Retry is zero.
var DefaultRetry = Retry{Max: 3, Base: 500 * time.Millisecond, Cap: 30 * time.Second}

// Config builds a Client.
type Config struct {
	// ClientID is the API account's client id (BUSINESSAPI.… or
	// SCHOOLAPI.…); it is also the assertion's iss and sub.
	ClientID string
	// KeyID is the kid of the uploaded public key.
	KeyID string
	// PrivateKey is the P-256 key matching KeyID. When nil, PrivateKeyPEM
	// is parsed; when that is empty too, KeyName is read from Keys.
	PrivateKey    *ecdsa.PrivateKey
	PrivateKeyPEM []byte
	Keys          secrets.Provider
	KeyName       string
	// Scope defaults to the scope derived from the ClientID prefix.
	Scope string
	// BaseURL defaults to the host matching Scope. A path prefix is kept.
	BaseURL string
	// TokenURL defaults to DefaultTokenURL.
	TokenURL string
	// HTTPClient defaults to an http.Client with DefaultTimeout.
	HTTPClient *http.Client
	// Clock defaults to the wall clock.
	Clock clock.Clock
	// AssertionTTL defaults to DefaultAssertionTTL and is capped at
	// MaxAssertionTTL.
	AssertionTTL time.Duration
	// ClockSkew back-dates the assertion's iat; default DefaultClockSkew.
	ClockSkew time.Duration
	// RefreshMargin renews the token this long before expires_in; default
	// DefaultRefreshMargin.
	RefreshMargin time.Duration
	// Retry defaults to DefaultRetry.
	Retry Retry
	// PageCap defaults to DefaultPageCap.
	PageCap int
	// Logger receives debug lines; nil discards them.
	Logger *slog.Logger
	// UserAgent defaults to DefaultUserAgent.
	UserAgent string
}

// Client calls the Apple Business Manager or Apple School Manager API.
// It is safe for concurrent use.
type Client struct {
	cfg   Config
	key   *ecdsa.PrivateKey
	base  *url.URL
	http  *http.Client
	clock clock.Clock
	log   *slog.Logger

	mu       sync.Mutex
	tok      cachedToken
	inflight chan struct{}
	exchErr  error
}

// New validates cfg, resolves the private key, and returns a Client. No
// request is made until the first call.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("%w: ClientID is required", ErrConfig)
	}
	if cfg.KeyID == "" {
		return nil, fmt.Errorf("%w: KeyID is required", ErrConfig)
	}
	key, err := resolveKey(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Scope == "" {
		cfg.Scope = ScopeFor(cfg.ClientID)
	}
	if cfg.Scope == "" {
		return nil, fmt.Errorf("%w: Scope cannot be derived from ClientID %q", ErrConfig, cfg.ClientID)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = BusinessBaseURL
		if cfg.Scope == ScopeSchool {
			cfg.BaseURL = SchoolBaseURL
		}
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("%w: BaseURL %q needs an http or https scheme and a host", ErrConfig, cfg.BaseURL)
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	base.RawQuery, base.Fragment = "", ""
	if cfg.TokenURL == "" {
		cfg.TokenURL = DefaultTokenURL
	}
	if _, err := url.Parse(cfg.TokenURL); err != nil {
		return nil, fmt.Errorf("%w: TokenURL: %w", ErrConfig, err)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: DefaultTimeout}
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.AssertionTTL <= 0 {
		cfg.AssertionTTL = DefaultAssertionTTL
	}
	if cfg.AssertionTTL > MaxAssertionTTL {
		cfg.AssertionTTL = MaxAssertionTTL
	}
	if cfg.ClockSkew < 0 {
		return nil, fmt.Errorf("%w: ClockSkew must not be negative", ErrConfig)
	}
	if cfg.ClockSkew == 0 {
		cfg.ClockSkew = DefaultClockSkew
	}
	if cfg.RefreshMargin <= 0 {
		cfg.RefreshMargin = DefaultRefreshMargin
	}
	if cfg.Retry == (Retry{}) {
		cfg.Retry = DefaultRetry
	}
	if cfg.Retry.Max < 0 {
		return nil, fmt.Errorf("%w: Retry.Max must not be negative", ErrConfig)
	}
	if cfg.Retry.Base <= 0 {
		cfg.Retry.Base = DefaultRetry.Base
	}
	if cfg.Retry.Cap < cfg.Retry.Base {
		cfg.Retry.Cap = cfg.Retry.Base
	}
	if cfg.PageCap <= 0 {
		cfg.PageCap = DefaultPageCap
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	cfg.PrivateKey, cfg.PrivateKeyPEM = nil, nil
	return &Client{cfg: cfg, key: key, base: base, http: cfg.HTTPClient, clock: cfg.Clock, log: cfg.Logger}, nil
}

// resolveKey picks the configured key source.
func resolveKey(ctx context.Context, cfg Config) (*ecdsa.PrivateKey, error) {
	switch {
	case cfg.PrivateKey != nil:
		if cfg.PrivateKey.Curve == nil || cfg.PrivateKey.Curve.Params().Name != "P-256" {
			return nil, fmt.Errorf("%w: PrivateKey", ErrKeyType)
		}
		return cfg.PrivateKey, nil
	case len(cfg.PrivateKeyPEM) > 0:
		return ParseKey(cfg.PrivateKeyPEM)
	case cfg.Keys != nil && cfg.KeyName != "":
		return LoadKey(ctx, cfg.Keys, cfg.KeyName)
	}
	return nil, fmt.Errorf("%w: one of PrivateKey, PrivateKeyPEM, or Keys with KeyName is required", ErrConfig)
}

// Scope returns the scope in use.
func (c *Client) Scope() string { return c.cfg.Scope }

// BaseURL returns the API base URL in use.
func (c *Client) BaseURL() string { return c.base.String() }

// RequestOption adjusts one request.
type RequestOption func(*requestOptions)

type requestOptions struct {
	idempotent bool
}

// WithIdempotent marks a POST as safe to retry after a 429, a 5xx, or a
// transport failure. Other methods are always retried.
func WithIdempotent() RequestOption {
	return func(o *requestOptions) { o.idempotent = true }
}

// request is one API call before execution.
type request struct {
	method string
	// path is joined to the base URL; rawURL, when set, is used as is
	// (links.next, download URLs).
	path   string
	rawURL string
	query  url.Values
	body   any
	opts   requestOptions
	// accept overrides Accept: application/json.
	accept string
}

// url resolves the request's URL.
func (c *Client) url(r request) (string, error) {
	if r.rawURL != "" {
		return r.rawURL, nil
	}
	u := *c.base
	u.Path += r.path
	if len(r.query) > 0 {
		u.RawQuery = r.query.Encode()
	}
	return u.String(), nil
}

// do executes r and decodes a JSON body into out (nil for no body).
func (c *Client) do(ctx context.Context, r request, out any) error {
	resp, err := c.roundTrip(ctx, r)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("%w: reading body: %w", ErrTransport, err)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrDecode, r.method, resp.Request.URL, err)
	}
	return nil
}

// roundTrip executes r with authentication, one replay on 401, and the
// retry policy, returning a response with a status below 400 whose body
// the caller closes.
func (c *Client) roundTrip(ctx context.Context, r request) (*http.Response, error) {
	target, err := c.url(r)
	if err != nil {
		return nil, err
	}
	var payload []byte
	if r.body != nil {
		if payload, err = json.Marshal(r.body); err != nil {
			return nil, fmt.Errorf("%w: encoding body: %w", ErrArgument, err)
		}
	}
	retryable := r.opts.idempotent || r.method != http.MethodPost
	attempt, replayed := 0, false
	for {
		tok, err := c.token(ctx, "")
		if err != nil {
			return nil, err
		}
		resp, err := c.send(ctx, r, target, payload, tok)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%w: %w", ErrTransport, ctx.Err())
			}
			if !retryable || attempt >= c.cfg.Retry.Max {
				return nil, fmt.Errorf("%w: %s %s: %w", ErrTransport, r.method, target, err)
			}
			attempt++
			if err := c.wait(ctx, c.backoff(attempt), "transport", err); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode < http.StatusBadRequest {
			return resp, nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		apiErr := newError(resp, body)
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			if !replayed {
				replayed = true
				c.invalidate(tok)
				c.log.DebugContext(ctx, "axm: 401, replaying with a fresh token", "method", r.method, "url", target)
				continue
			}
			return nil, &AuthError{Status: resp.StatusCode, Body: body, Err: apiErr}
		case resp.StatusCode == http.StatusTooManyRequests:
			if !retryable || attempt >= c.cfg.Retry.Max {
				return nil, apiErr
			}
			attempt++
			delay := parseRetryAfter(resp.Header.Get("Retry-After"), c.clock.Now())
			if delay == 0 {
				delay = c.backoff(attempt)
			}
			if err := c.wait(ctx, delay, "429", apiErr); err != nil {
				return nil, err
			}
		case resp.StatusCode >= http.StatusInternalServerError:
			if !retryable || attempt >= c.cfg.Retry.Max {
				return nil, apiErr
			}
			attempt++
			if err := c.wait(ctx, c.backoff(attempt), "5xx", apiErr); err != nil {
				return nil, err
			}
		default:
			return nil, apiErr
		}
	}
}

// send performs one HTTP exchange.
func (c *Client) send(ctx context.Context, r request, target string, payload []byte, tok string) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, target, body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrArgument, err)
	}
	accept := r.accept
	if accept == "" {
		accept = "application/json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return resp, nil
}

// invalidate drops tok from the cache if it is still the cached token.
func (c *Client) invalidate(tok string) {
	c.mu.Lock()
	if c.tok.value == tok {
		c.tok = cachedToken{}
	}
	c.mu.Unlock()
}

// backoff is the jittered exponential delay for the nth retry (1-based):
// Base doubled per attempt, capped at Cap, scaled by a factor in [0.5, 1.5).
func (c *Client) backoff(attempt int) time.Duration {
	d := c.cfg.Retry.Base
	for i := 1; i < attempt && d < c.cfg.Retry.Cap; i++ {
		d *= 2
	}
	if d > c.cfg.Retry.Cap {
		d = c.cfg.Retry.Cap
	}
	jitter := 0.5 + rand.Float64() // #nosec G404 -- jitter is not security-sensitive
	return time.Duration(float64(d) * jitter)
}

// wait sleeps d on the clock, honouring the context.
func (c *Client) wait(ctx context.Context, d time.Duration, reason string, cause error) error {
	c.log.DebugContext(ctx, "axm: retrying", "reason", reason, "after", d, "error", cause)
	select {
	case <-c.clock.After(d):
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrTransport, ctx.Err())
	}
}
