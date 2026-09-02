package dep

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
)

// Defaults for ClientConfig.
const (
	DefaultBaseURL               = "https://mdmenrollment.apple.com"
	DefaultUserAgent             = "go-apple-mdm-dep/1"
	DefaultExpiryWarning         = 30 * 24 * time.Hour
	DefaultExpiryWarningInterval = time.Hour
	DefaultMaxBodyBytes          = 8 << 20
	// maxResponseBytes bounds what Do reads from a response body.
	maxResponseBytes = 64 << 20
	// contentType is what Apple's service sends and expects.
	contentType = "application/json;charset=UTF8"
	// HeaderSession is the session header of every authenticated call.
	HeaderSession = "X-ADM-Auth-Session"
	// HeaderProtocolVersion selects the response schema version.
	HeaderProtocolVersion = "X-Server-Protocol-Version"
)

// ClientConfig configures NewClient. Store is required.
type ClientConfig struct {
	// Store supplies accounts, tokens, and sessions; a rotated session is
	// written back so every process shares it.
	Store Store
	// HTTPClient defaults to one with a 60s timeout.
	HTTPClient *http.Client
	// BaseURL defaults to Apple's production service.
	BaseURL string
	// UserAgent is sent on every request; Apple rejects a missing one with
	// USER_AGENT_MISSING.
	UserAgent string
	Clock     clock.Clock
	// Nonce supplies the OAuth nonce; the default is 16 random bytes.
	Nonce  func() (string, error)
	Bus    *event.Bus
	Logger *slog.Logger
	// ExpiryWarning is the window before access_token_expiry inside which
	// EventTokenExpiring is published, at most once per
	// ExpiryWarningInterval per account.
	ExpiryWarning         time.Duration
	ExpiryWarningInterval time.Duration
	// MaxBodyBytes bounds the replay buffer for requests without GetBody.
	MaxBodyBytes int64
}

// Client talks to the DEP service for any number of accounts. Every call
// names the account; tokens and sessions come from the store, and a
// /session re-authentication runs at most once at a time per account.
type Client struct {
	cfg  ClientConfig
	base *url.URL

	mu       sync.Mutex
	sessions map[string]*accountSession
	warned   map[string]time.Time
}

// accountSession serialises session refreshes for one account.
type accountSession struct {
	mu    sync.Mutex
	token string
}

// NewClient validates the configuration and applies defaults.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: client needs a Store", ErrConfig)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("%w: base URL %q", ErrConfig, cfg.BaseURL)
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Nonce == nil {
		cfg.Nonce = randomNonce
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ExpiryWarning <= 0 {
		cfg.ExpiryWarning = DefaultExpiryWarning
	}
	if cfg.ExpiryWarningInterval <= 0 {
		cfg.ExpiryWarningInterval = DefaultExpiryWarningInterval
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}
	return &Client{cfg: cfg, base: base, sessions: map[string]*accountSession{}, warned: map[string]time.Time{}}, nil
}

// Store returns the store the client was built with.
func (c *Client) Store() Store { return c.cfg.Store }

// URL returns the absolute URL of a service path.
func (c *Client) URL(path string, query url.Values) string {
	u := *c.base
	u.Path = c.base.Path + path
	u.RawQuery = query.Encode()
	return u.String()
}

// NewRequest builds a request for path with body encoded as JSON (nil
// sends no body). The body is a bytes.Reader, so GetBody is set and Do can
// replay it after a re-authentication.
func (c *Client) NewRequest(ctx context.Context, method, path string, query url.Values, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		raw, err := Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL(path, query), r)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %w", ErrInvalid, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

// Do sends req on behalf of account and decodes a 2xx JSON body into out
// (nil discards it). It sets the session, protocol version, User-Agent,
// and Accept headers; adopts and persists a rotated X-ADM-Auth-Session;
// re-authenticates exactly once on 401, 403 FORBIDDEN, or EXPIRED_TOKEN,
// replaying the body from GetBody or a bounded buffer; and returns *Error
// for any other non-2xx answer. ErrTokenExpired is returned before any
// HTTP call when the account's token has expired.
func (c *Client) Do(ctx context.Context, account string, req *http.Request, out any) error {
	acct, err := c.account(ctx, account)
	if err != nil {
		return err
	}
	replay, length, err := replayable(req, c.cfg.MaxBodyBytes)
	if err != nil {
		return err
	}
	token, err := c.sessionToken(ctx, acct, "")
	if err != nil {
		return err
	}
	for attempt := range 2 {
		status, header, body, err := c.send(ctx, acct, req, replay, length, token)
		if err != nil {
			return err
		}
		if rotated := header.Get(HeaderSession); rotated != "" && rotated != token {
			if err := c.adoptSession(ctx, acct.Name, rotated); err != nil {
				return err
			}
			token = rotated
		}
		if status >= 200 && status < 300 {
			if acct.State != (AccountState{}) {
				// A definitive success clears TermsExpired and TokenInvalid;
				// a store failure is reported, not swallowed.
				if err := c.cfg.Store.SetAccountState(ctx, acct.Name, AccountState{}); err != nil {
					return fmt.Errorf("dep: clear account state: %w", err)
				}
				acct.State = AccountState{}
			}
			if out == nil || len(bytes.TrimSpace(body)) == 0 {
				return nil
			}
			return Unmarshal(body, out)
		}
		derr := newError(status, body, header.Get("Retry-After"), c.cfg.Clock.Now())
		if attempt == 0 && needsReauth(derr) {
			c.cfg.Logger.DebugContext(ctx, "dep: session rejected, re-authenticating", "account", acct.Name, "status", status, "code", derr.Code)
			if token, err = c.sessionToken(ctx, acct, token); err != nil {
				return err
			}
			continue
		}
		return derr
	}
	return fmt.Errorf("%w: unreachable", ErrInvalid)
}

// needsReauth reports whether the answer means the session is gone.
func needsReauth(e *Error) bool {
	return e.Status == http.StatusUnauthorized || (e.Status == http.StatusForbidden && e.Code == CodeForbidden) || e.Code == CodeExpiredToken
}

// send performs one attempt and returns the status, headers, and body.
func (c *Client) send(ctx context.Context, acct *Account, req *http.Request, replay func() (io.ReadCloser, error), length int64, token string) (int, http.Header, []byte, error) {
	r := req.Clone(ctx)
	body, err := replay()
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: replay body: %w", ErrInvalid, err)
	}
	r.Body = body
	r.ContentLength = length
	r.Header.Set("User-Agent", c.cfg.UserAgent)
	r.Header.Set("Accept", contentType)
	r.Header.Set(HeaderSession, token)
	r.Header.Set(HeaderProtocolVersion, strconv.Itoa(acct.Protocol()))
	if length != 0 && r.Header.Get("Content-Type") == "" {
		r.Header.Set("Content-Type", contentType)
	}
	resp, err := c.cfg.HTTPClient.Do(r)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("dep: %s %s: %w", r.Method, r.URL.Path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("dep: read %s %s: %w", r.Method, r.URL.Path, err)
	}
	return resp.StatusCode, resp.Header, data, nil
}

// replayable returns a function producing a fresh body for each attempt:
// GetBody when the request has one, else a copy of the body bounded by
// maxBytes, which does not need ContentLength (chunked requests report -1).
func replayable(req *http.Request, maxBytes int64) (func() (io.ReadCloser, error), int64, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return func() (io.ReadCloser, error) { return http.NoBody, nil }, 0, nil
	}
	if req.GetBody != nil {
		return req.GetBody, req.ContentLength, nil
	}
	defer req.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(req.Body, maxBytes+1))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: read body: %w", ErrInvalid, err)
	}
	if int64(len(buf)) > maxBytes {
		return nil, 0, fmt.Errorf("%w: %d bytes over %d", ErrBodyTooLarge, len(buf), maxBytes)
	}
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf)), nil }, int64(len(buf)), nil
}

// account loads the account and fails fast on missing or expired tokens.
func (c *Client) account(ctx context.Context, name string) (*Account, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty account name", ErrInvalid)
	}
	acct, err := c.cfg.Store.GetAccount(ctx, name)
	if err != nil {
		return nil, err
	}
	if !acct.HasTokens() {
		return nil, fmt.Errorf("%w: %s", ErrNoTokens, name)
	}
	if err := c.checkExpiry(ctx, name, acct.AccessTokenExpiry); err != nil {
		return nil, err
	}
	return acct, nil
}

// checkExpiry returns ErrTokenExpired for a past expiry and publishes
// EventTokenExpiring inside the warning window.
func (c *Client) checkExpiry(ctx context.Context, name string, expiry *time.Time) error {
	if expiry == nil {
		return nil
	}
	now := c.cfg.Clock.Now()
	if !now.Before(*expiry) {
		return fmt.Errorf("%w: %s expired at %s", ErrTokenExpired, name, expiry.UTC().Format(time.RFC3339))
	}
	if c.cfg.Bus == nil || expiry.Sub(now) > c.cfg.ExpiryWarning {
		return nil
	}
	c.mu.Lock()
	last, ok := c.warned[name]
	if ok && now.Sub(last) < c.cfg.ExpiryWarningInterval {
		c.mu.Unlock()
		return nil
	}
	c.warned[name] = now
	c.mu.Unlock()
	ev := event.Event{Type: EventTokenExpiring, At: now, Actor: Actor, Data: TokenExpiringEvent{Account: name, Expiry: *expiry}}
	if err := c.cfg.Bus.Publish(ctx, ev); err != nil {
		c.cfg.Logger.WarnContext(ctx, "dep: publish", "type", string(EventTokenExpiring), "error", err)
	}
	return nil
}

// sessionFor returns the per-account refresh lock.
func (c *Client) sessionFor(name string) *accountSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sessions[name]
	if !ok {
		s = &accountSession{}
		c.sessions[name] = s
	}
	return s
}

// sessionToken returns a session token for the account, authenticating
// when none is cached or stored, or when the cached and stored tokens are
// the stale one the caller was just refused with. Concurrent callers wait
// on the account lock and reuse the token the first one obtained.
func (c *Client) sessionToken(ctx context.Context, acct *Account, stale string) (string, error) {
	s := c.sessionFor(acct.Name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.token != stale {
		return s.token, nil
	}
	stored, err := c.cfg.Store.Session(ctx, acct.Name)
	if err != nil {
		return "", err
	}
	if stored != "" && stored != stale {
		s.token = stored
		return stored, nil
	}
	token, err := c.authenticate(ctx, acct)
	if err != nil {
		return "", err
	}
	if err := c.cfg.Store.SetSession(ctx, acct.Name, token); err != nil {
		return "", err
	}
	s.token = token
	return token, nil
}

// adoptSession persists a session the service rotated in a response.
func (c *Client) adoptSession(ctx context.Context, name, token string) error {
	s := c.sessionFor(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == token {
		return nil
	}
	if err := c.cfg.Store.SetSession(ctx, name, token); err != nil {
		return err
	}
	s.token = token
	return nil
}

// authenticate calls /session for the account and records the outcome in
// the account state: TokenInvalid on 401, TermsExpired on T_C_NOT_SIGNED,
// both cleared on a definitive success.
func (c *Client) authenticate(ctx context.Context, acct *Account) (string, error) {
	token, err := c.session(ctx, acct.Tokens(), acct.Protocol())
	switch {
	case err == nil:
		if acct.State != (AccountState{}) {
			if serr := c.cfg.Store.SetAccountState(ctx, acct.Name, AccountState{}); serr != nil {
				return "", serr
			}
			acct.State = AccountState{}
		}
		return token, nil
	case errors.Is(err, ErrTokenInvalid):
		return "", c.markState(ctx, acct, err, AccountState{TermsExpired: acct.State.TermsExpired, TokenInvalid: true})
	case errors.Is(err, ErrTermsNotSigned):
		return "", c.markState(ctx, acct, err, AccountState{TermsExpired: true, TokenInvalid: acct.State.TokenInvalid})
	default:
		return "", err
	}
}

func (c *Client) markState(ctx context.Context, acct *Account, cause error, s AccountState) error {
	if acct.State == s {
		return cause
	}
	if err := c.cfg.Store.SetAccountState(ctx, acct.Name, s); err != nil {
		return errors.Join(cause, err)
	}
	acct.State = s
	return cause
}

// session performs the OAuth 1.0a GET /session with the tokens given and
// returns the auth_session_token. 401 is ErrTokenInvalid, 403
// T_C_NOT_SIGNED is ErrTermsNotSigned, anything else non-200 is *Error.
func (c *Client) session(ctx context.Context, t Tokens, protocol int) (string, error) {
	nonce, err := c.cfg.Nonce()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL("/session", nil), http.NoBody)
	if err != nil {
		return "", fmt.Errorf("%w: session request: %w", ErrInvalid, err)
	}
	o := OAuth1{ConsumerKey: t.ConsumerKey, ConsumerSecret: t.ConsumerSecret, Token: t.AccessToken, TokenSecret: t.AccessSecret, Timestamp: c.cfg.Clock.Now().Unix(), Nonce: nonce, Realm: OAuth1Realm, Version: true}
	req.Header.Set("Authorization", o.Header(req.Method, req.URL))
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", contentType)
	req.Header.Set(HeaderProtocolVersion, strconv.Itoa(protocol))
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dep: GET /session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("dep: read /session: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		derr := newError(resp.StatusCode, body, resp.Header.Get("Retry-After"), c.cfg.Clock.Now())
		switch {
		case derr.Status == http.StatusUnauthorized:
			return "", fmt.Errorf("%w: %w", ErrTokenInvalid, derr)
		case derr.Status == http.StatusForbidden && derr.Code == CodeTermsNotSigned:
			return "", fmt.Errorf("%w: %w", ErrTermsNotSigned, derr)
		}
		return "", derr
	}
	var sr sessionResponse
	if err := Unmarshal(body, &sr); err != nil {
		return "", err
	}
	if sr.AuthSessionToken == "" {
		return "", fmt.Errorf("%w: /session answered without auth_session_token", ErrInvalid)
	}
	return sr.AuthSessionToken, nil
}
