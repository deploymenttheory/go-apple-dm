package webauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/clock"
)

// State lifetime bounds.
const (
	DefaultStateTTL = 5 * time.Minute
	MaxStateTTL     = 8 * time.Minute
	// DefaultClockSkew is tolerated on exp and iat.
	DefaultClockSkew = time.Minute
	// DefaultMaxResponseBytes bounds provider responses.
	DefaultMaxResponseBytes = 1 << 20
)

// ClientAuth selects how the client secret reaches the token endpoint.
type ClientAuth int

// Client authentication methods (RFC 6749 section 2.3.1).
const (
	// ClientAuthBasic sends the credentials in an Authorization header.
	ClientAuthBasic ClientAuth = iota
	// ClientAuthPost sends client_secret in the form body.
	ClientAuthPost
)

// Decision is the Authorizer's verdict handed to Complete. This package
// passes it on without interpreting it.
type Decision struct {
	// Profile names the enrollment profile or context the caller serves.
	Profile string
	// Attributes carries values for Complete.
	Attributes map[string]string
}

// Authorizer decides whether the authenticated person may enrol the bound
// device. Return an error wrapping ErrDenied to answer 403; any other
// error is a 500.
type Authorizer func(ctx context.Context, bound Bound, claims Claims) (Decision, error)

// Completer serves the final response, normally the signed enrollment
// profile as application/x-apple-aspen-config.
type Completer func(ctx context.Context, bound Bound, claims Claims, decision Decision, w http.ResponseWriter, r *http.Request)

// ErrorWriter renders a failure to the web view. The default writes a
// generic text message; err is for the caller's own logging or page.
type ErrorWriter func(w http.ResponseWriter, r *http.Request, status int, err error)

// Config configures a Flow.
type Config struct {
	// Issuer is the provider's issuer URL, used for discovery when
	// Endpoints is empty and always compared with the id_token iss.
	Issuer string
	// Endpoints skip discovery when set.
	Endpoints Endpoints
	// ClientID is required.
	ClientID string
	// ClientSecret is optional; a public client uses PKCE only.
	ClientSecret string
	// ClientAuth selects how ClientSecret is sent.
	ClientAuth ClientAuth
	// RedirectURL is the callback URL; https is required.
	RedirectURL string
	// Scopes to request; openid is always added.
	Scopes []string
	// StateStore defaults to an in-memory store.
	StateStore StateStore
	// StateTTL defaults to DefaultStateTTL and may not exceed MaxStateTTL.
	StateTTL time.Duration
	// ClockSkew defaults to DefaultClockSkew.
	ClockSkew time.Duration
	// Clock defaults to the real clock.
	Clock clock.Clock
	// HTTPClient defaults to a client with a timeout that refuses to be
	// redirected to plain http.
	HTTPClient *http.Client
	// Authorizer decides; nil allows everyone.
	Authorizer Authorizer
	// Complete serves the final response; required.
	Complete Completer
	// OnError renders failures; the default is a generic text message.
	OnError ErrorWriter
	// Logger defaults to slog.Default.
	Logger *slog.Logger
	// MaxResponseBytes bounds provider responses; default 1 MiB.
	MaxResponseBytes int64
	// AllowInsecureForTests accepts http URLs. Never set it in production.
	AllowInsecureForTests bool
}

// Errors the flow reports.
var (
	ErrConfig = errors.New("webauth: config")
	// ErrDenied is what an Authorizer wraps to refuse enrollment.
	ErrDenied = errors.New("webauth: denied")
	// ErrCallback is wrapped by callback request failures (bad or missing
	// parameters, unknown or expired state).
	ErrCallback = errors.New("webauth: callback")
	// ErrAccessDenied is wrapped when the provider reported access_denied.
	ErrAccessDenied = errors.New("webauth: access denied by the provider")
	// ErrStateExpired is reported for a state past its TTL.
	ErrStateExpired = errors.New("webauth: state expired")
)

// Flow is the relying party.
type Flow struct {
	cfg      Config
	store    StateStore
	clock    clock.Clock
	http     *http.Client
	log      *slog.Logger
	ttl      time.Duration
	skew     time.Duration
	maxBytes int64
	scope    string
	onError  ErrorWriter

	mu          sync.Mutex
	resolved    Endpoints
	keys        []verificationKey
	keysFetched time.Time
}

// New validates the configuration.
func New(cfg Config) (*Flow, error) {
	f := &Flow{cfg: cfg, store: cfg.StateStore, clock: cfg.Clock, http: cfg.HTTPClient, log: cfg.Logger, ttl: cfg.StateTTL, skew: cfg.ClockSkew, maxBytes: cfg.MaxResponseBytes, onError: cfg.OnError}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("%w: ClientID is required", ErrConfig)
	}
	if cfg.Complete == nil {
		return nil, fmt.Errorf("%w: Complete is required", ErrConfig)
	}
	if err := f.requireHTTPS(cfg.RedirectURL); err != nil {
		return nil, fmt.Errorf("%w: RedirectURL: %w", ErrConfig, err)
	}
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("%w: Issuer is required", ErrConfig)
	}
	if err := f.requireHTTPS(cfg.Issuer); err != nil {
		return nil, fmt.Errorf("%w: Issuer: %w", ErrConfig, err)
	}
	if cfg.Endpoints != (Endpoints{}) {
		if err := f.checkEndpoints(cfg.Endpoints); err != nil {
			return nil, fmt.Errorf("%w: Endpoints: %w", ErrConfig, err)
		}
		f.resolved = cfg.Endpoints
	}
	switch {
	case f.ttl == 0:
		f.ttl = DefaultStateTTL
	case f.ttl < 0 || f.ttl > MaxStateTTL:
		return nil, fmt.Errorf("%w: StateTTL %s outside (0, %s]", ErrConfig, f.ttl, MaxStateTTL)
	}
	if f.skew == 0 {
		f.skew = DefaultClockSkew
	}
	if f.maxBytes <= 0 {
		f.maxBytes = DefaultMaxResponseBytes
	}
	if f.clock == nil {
		f.clock = clock.Real{}
	}
	if f.store == nil {
		f.store = NewMemoryStore(WithMemoryClock(f.clock))
	}
	if f.log == nil {
		f.log = slog.Default()
	}
	if f.onError == nil {
		f.onError = defaultErrorWriter
	}
	if f.http == nil {
		f.http = &http.Client{Timeout: 15 * time.Second, CheckRedirect: f.checkRedirect}
	}
	scopes := []string{"openid"}
	for _, s := range cfg.Scopes {
		if s != "" && !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	f.scope = strings.Join(scopes, " ")
	return f, nil
}

// checkRedirect keeps provider fetches on https.
func (f *Flow) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("%w: too many redirects", ErrProvider)
	}
	return f.requireHTTPS(req.URL.String())
}

func defaultErrorWriter(w http.ResponseWriter, _ *http.Request, status int, _ error) {
	msg := "Enrollment could not continue."
	switch status {
	case http.StatusBadRequest:
		msg = "The sign-in request is invalid or has expired. Start the enrollment again."
	case http.StatusForbidden:
		msg = "You are not allowed to enroll this device."
	case http.StatusBadGateway:
		msg = "The identity provider did not complete the sign-in. Try again later."
	}
	http.Error(w, msg, status)
}

// fail logs and renders an error.
func (f *Flow) fail(w http.ResponseWriter, r *http.Request, status int, err error) {
	level := slog.LevelWarn
	if status >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	f.log.Log(r.Context(), level, "webauth: request failed", "status", status, "error", err, "remote", r.RemoteAddr)
	f.onError(w, r, status, err)
}

// random returns n random bytes base64url-encoded without padding.
func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("webauth: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Begin stores a new state bound to the device and redirects the web view
// to the provider. The response is written in every case; the returned
// error is for the caller's logging.
func (f *Flow) Begin(w http.ResponseWriter, r *http.Request, bound Bound) error {
	ctx := r.Context()
	eps, err := f.endpoints(ctx)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrConfig) {
			status = http.StatusInternalServerError
		}
		f.fail(w, r, status, err)
		return err
	}
	state, verifier, nonce := random(16), random(32), random(16)
	st := State{Bound: bound, Verifier: verifier, Nonce: nonce, ExpiresAt: f.clock.Now().Add(f.ttl)}
	if err := f.store.Put(ctx, state, st); err != nil {
		f.fail(w, r, http.StatusInternalServerError, err)
		return err
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {f.cfg.ClientID},
		"redirect_uri":          {f.cfg.RedirectURL},
		"scope":                 {f.scope},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	if bound.LoginHint != "" {
		q.Set("login_hint", bound.LoginHint)
	}
	target, err := url.Parse(eps.Authorization)
	if err != nil {
		f.fail(w, r, http.StatusInternalServerError, fmt.Errorf("%w: authorization endpoint: %w", ErrConfig, err))
		return err
	}
	existing := target.Query()
	for k, vs := range q {
		existing[k] = vs
	}
	target.RawQuery = existing.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target.String(), http.StatusFound)
	return nil
}

// Callback returns the handler for RedirectURL.
func (f *Flow) Callback() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		f.callback(w, r)
	})
}

func (f *Flow) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	stateKey := q.Get("state")
	if stateKey == "" {
		f.fail(w, r, http.StatusBadRequest, fmt.Errorf("%w: state missing", ErrCallback))
		return
	}
	st, err := f.store.Take(ctx, stateKey)
	if err != nil {
		status := http.StatusBadRequest
		if !errors.Is(err, ErrStateNotFound) {
			status = http.StatusInternalServerError
		}
		f.fail(w, r, status, fmt.Errorf("%w: state: %w", ErrCallback, err))
		return
	}
	if !f.clock.Now().Before(st.ExpiresAt) {
		f.fail(w, r, http.StatusBadRequest, fmt.Errorf("%w: %w", ErrCallback, ErrStateExpired))
		return
	}
	if code := q.Get("error"); code != "" {
		f.providerError(w, r, code, q.Get("error_description"), q.Get("error_uri"))
		return
	}
	code := q.Get("code")
	if code == "" {
		f.fail(w, r, http.StatusBadRequest, fmt.Errorf("%w: code missing", ErrCallback))
		return
	}
	eps, err := f.endpoints(ctx)
	if err != nil {
		f.fail(w, r, http.StatusBadGateway, err)
		return
	}
	rawIDToken, err := f.exchange(ctx, eps.Token, code, st.Verifier)
	if err != nil {
		f.fail(w, r, http.StatusBadGateway, err)
		return
	}
	lookup := func(kid, alg string) []verificationKey { return f.keysFor(ctx, eps.JWKS, kid, alg) }
	claims, err := verifyIDToken(rawIDToken, lookup, idTokenChecks{issuer: f.cfg.Issuer, clientID: f.cfg.ClientID, nonce: st.Nonce, now: f.clock.Now(), skew: f.skew})
	if err != nil {
		f.fail(w, r, http.StatusBadRequest, err)
		return
	}
	var decision Decision
	if f.cfg.Authorizer != nil {
		decision, err = f.cfg.Authorizer(ctx, st.Bound, claims)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrDenied) {
				status = http.StatusForbidden
			}
			f.fail(w, r, status, err)
			return
		}
	}
	f.log.InfoContext(ctx, "webauth: authenticated", "subject", claims.Subject, "serial", st.Bound.Serial, "udid", st.Bound.UDID)
	f.cfg.Complete(ctx, st.Bound, claims, decision, w, r)
}

// providerError maps an OAuth error response (RFC 6749 section 4.1.2.1).
func (f *Flow) providerError(w http.ResponseWriter, r *http.Request, code, description, uri string) {
	err := fmt.Errorf("%w: %s", ErrProvider, code)
	status := http.StatusBadGateway
	if code == "access_denied" {
		err = fmt.Errorf("%w: %s", ErrAccessDenied, code)
		status = http.StatusForbidden
	}
	if description != "" {
		err = fmt.Errorf("%w: %s", err, description)
	}
	if uri != "" {
		err = fmt.Errorf("%w (see %s)", err, uri)
	}
	f.fail(w, r, status, err)
}
