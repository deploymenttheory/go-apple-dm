package webauthtest

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/enroll/webauth"
)

// Signing algorithms the provider can use.
const (
	AlgES256 = "ES256"
	AlgRS256 = "RS256"
)

// AuthorizeRequest is what the relying party sent to /authorize.
type AuthorizeRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod string
	LoginHint           string
}

// TokenRequest is what the relying party sent to /token.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	CodeVerifier string
	ClientID     string
	ClientSecret string
	// BasicAuth reports whether the credentials came in an Authorization
	// header rather than the form.
	BasicAuth bool
}

// Options script the provider. Change them through Provider.Set.
type Options struct {
	// ClientID, ClientSecret, RedirectURI are what /authorize and /token
	// expect. An empty ClientSecret makes the client public.
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// Identity claims in the id_token.
	Subject           string
	Email             string
	EmailVerified     bool
	Name              string
	PreferredUsername string
	Groups            []string
	// Alg selects the signing key: AlgES256 (default) or AlgRS256.
	Alg string
	// Switches.
	BadNonce      bool
	Expired       bool
	WrongAudience bool
	UnknownKID    bool
	// AuthorizeError, when set, makes /authorize redirect back with this
	// error code and the description and URI.
	AuthorizeError            string
	AuthorizeErrorDescription string
	AuthorizeErrorURI         string
	// TokenStatus, when non-zero, makes /token answer that status with
	// {"error":"server_error"} after the checks pass.
	TokenStatus int
	// OmitIDToken makes /token answer 200 without an id_token.
	OmitIDToken bool
	// InsecureEndpoints rewrites the discovery document's endpoints to
	// http so a relying party can be shown to refuse them.
	InsecureEndpoints bool
	// Now is the provider's clock; default time.Now.
	Now func() time.Time
}

type codeRecord struct {
	challenge string
	method    string
	nonce     string
	redirect  string
}

// Provider is the fake OpenID provider.
type Provider struct {
	// Server is the TLS server; its URL is the issuer.
	Server *httptest.Server

	mu         sync.Mutex
	opts       Options
	authorizes []AuthorizeRequest
	tokens     []TokenRequest
	codes      map[string]codeRecord
	es         *ecdsa.PrivateKey
	rs         *rsa.PrivateKey
	esKID      string
	rsKID      string
	generation int
}

var (
	rsaOnce sync.Once
	rsaKey  *rsa.PrivateKey
)

// sharedRSAKey avoids a 2048-bit key generation per provider; RotateKeys
// generates a fresh one.
func sharedRSAKey() *rsa.PrivateKey {
	rsaOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		rsaKey = k
	})
	return rsaKey
}

// New starts a provider with sensible defaults: client "enroll-client",
// public, subject "user-1", ES256. It is closed when the test ends.
func New(tb testing.TB) *Provider {
	tb.Helper()
	es, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	p := &Provider{
		opts: Options{
			ClientID: "enroll-client", Subject: "user-1", Email: "user-1@example.com", EmailVerified: true,
			Name: "User One", PreferredUsername: "user1", Groups: []string{"staff"}, Alg: AlgES256, Now: time.Now,
		},
		codes: map[string]codeRecord{},
		es:    es, rs: sharedRSAKey(), esKID: "es256-1", rsKID: "rs256-1", generation: 1,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	p.Server = httptest.NewTLSServer(mux)
	tb.Cleanup(p.Server.Close)
	return p
}

// Set changes the options under the provider's lock.
func (p *Provider) Set(fn func(o *Options)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.opts)
}

func (p *Provider) options() Options {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opts
}

// Issuer is the provider's issuer URL.
func (p *Provider) Issuer() string { return p.Server.URL }

// Endpoints returns the explicit endpoints for a relying party that skips
// discovery.
func (p *Provider) Endpoints() webauth.Endpoints {
	return webauth.Endpoints{Authorization: p.Server.URL + "/authorize", Token: p.Server.URL + "/token", JWKS: p.Server.URL + "/jwks"}
}

// Certificate is the server's TLS certificate for clients that need to
// trust it.
func (p *Provider) Certificate() *x509.Certificate { return p.Server.Certificate() }

// HTTPClient returns a client that trusts the provider's certificate, for
// the relying party's back-channel requests.
func (p *Provider) HTTPClient() *http.Client { return p.Server.Client() }

// Authorizes returns every recorded /authorize request.
func (p *Provider) Authorizes() []AuthorizeRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]AuthorizeRequest(nil), p.authorizes...)
}

// Tokens returns every recorded /token request.
func (p *Provider) Tokens() []TokenRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]TokenRequest(nil), p.tokens...)
}

// RotateKeys replaces both signing keys and their key ids, as a provider
// does when it rolls keys.
func (p *Provider) RotateKeys() error {
	es, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("webauthtest: rotate: %w", err)
	}
	rs, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("webauthtest: rotate: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	p.es, p.rs = es, rs
	p.esKID, p.rsKID = "es256-"+strconv.Itoa(p.generation), "rs256-"+strconv.Itoa(p.generation)
	return nil
}

// ErrProvider is returned by the provider's helpers.
var ErrProvider = errors.New("webauthtest")

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	o := p.options()
	base := p.Server.URL
	if o.InsecureEndpoints {
		u, _ := url.Parse(base)
		u.Scheme = "http"
		base = u.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.Server.URL,
		"authorization_endpoint":                base + "/authorize",
		"token_endpoint":                        base + "/token",
		"jwks_uri":                              base + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{AlgES256, AlgRS256},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	es, rs, esKID, rsKID := p.es, p.rs, p.esKID, p.rsKID
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]any{
		{
			"kty": "EC", "crv": "P-256", "kid": esKID, "alg": AlgES256, "use": "sig",
			"x": b64(es.PublicKey.X.FillBytes(make([]byte, 32))),
			"y": b64(es.PublicKey.Y.FillBytes(make([]byte, 32))),
		},
		{
			"kty": "RSA", "kid": rsKID, "alg": AlgRS256, "use": "sig",
			"n": b64(rs.PublicKey.N.Bytes()),
			"e": b64(big.NewInt(int64(rs.PublicKey.E)).Bytes()),
		},
	}})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	o := p.options()
	q := r.URL.Query()
	ar := AuthorizeRequest{
		ResponseType: q.Get("response_type"), ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"),
		Scope: q.Get("scope"), State: q.Get("state"), Nonce: q.Get("nonce"), CodeChallenge: q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"), LoginHint: q.Get("login_hint"),
	}
	p.mu.Lock()
	p.authorizes = append(p.authorizes, ar)
	p.mu.Unlock()
	if ar.ClientID != o.ClientID {
		http.Error(w, "unknown client", http.StatusBadRequest)
		return
	}
	if o.RedirectURI != "" && ar.RedirectURI != o.RedirectURI {
		http.Error(w, "redirect_uri not registered", http.StatusBadRequest)
		return
	}
	back, err := url.Parse(ar.RedirectURI)
	if err != nil {
		http.Error(w, "redirect_uri unparseable", http.StatusBadRequest)
		return
	}
	bq := back.Query()
	bq.Set("state", ar.State)
	switch {
	case o.AuthorizeError != "":
		bq.Set("error", o.AuthorizeError)
		if o.AuthorizeErrorDescription != "" {
			bq.Set("error_description", o.AuthorizeErrorDescription)
		}
		if o.AuthorizeErrorURI != "" {
			bq.Set("error_uri", o.AuthorizeErrorURI)
		}
	case ar.ResponseType != "code":
		bq.Set("error", "unsupported_response_type")
	case ar.CodeChallenge == "" || ar.CodeChallengeMethod != "S256":
		bq.Set("error", "invalid_request")
		bq.Set("error_description", "PKCE S256 is required")
	default:
		code := randomString()
		p.mu.Lock()
		p.codes[code] = codeRecord{challenge: ar.CodeChallenge, method: ar.CodeChallengeMethod, nonce: ar.Nonce, redirect: ar.RedirectURI}
		p.mu.Unlock()
		bq.Set("code", code)
	}
	back.RawQuery = bq.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	o := p.options()
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "form")
		return
	}
	tr := TokenRequest{
		GrantType: r.PostForm.Get("grant_type"), Code: r.PostForm.Get("code"), RedirectURI: r.PostForm.Get("redirect_uri"),
		CodeVerifier: r.PostForm.Get("code_verifier"), ClientID: r.PostForm.Get("client_id"), ClientSecret: r.PostForm.Get("client_secret"),
	}
	if user, pass, ok := r.BasicAuth(); ok {
		tr.BasicAuth = true
		tr.ClientID, _ = url.QueryUnescape(user)
		tr.ClientSecret, _ = url.QueryUnescape(pass)
	}
	p.mu.Lock()
	p.tokens = append(p.tokens, tr)
	rec, known := p.codes[tr.Code]
	delete(p.codes, tr.Code)
	p.mu.Unlock()
	switch {
	case tr.ClientID != o.ClientID || tr.ClientSecret != o.ClientSecret:
		tokenError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
	case tr.GrantType != "authorization_code":
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type", tr.GrantType)
	case !known:
		tokenError(w, http.StatusBadRequest, "invalid_grant", "unknown or used code")
	case tr.RedirectURI != rec.redirect:
		tokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
	case rec.method != "S256" || s256(tr.CodeVerifier) != rec.challenge:
		tokenError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match")
	case o.TokenStatus != 0:
		tokenError(w, o.TokenStatus, "server_error", "scripted")
	default:
		resp := map[string]any{"access_token": randomString(), "token_type": "Bearer", "expires_in": 3600}
		if !o.OmitIDToken {
			idToken, err := p.SignIDToken(p.claims(o, rec.nonce))
			if err != nil {
				tokenError(w, http.StatusInternalServerError, "server_error", err.Error())
				return
			}
			resp["id_token"] = idToken
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// claims builds the id_token payload from the options and switches.
func (p *Provider) claims(o Options, nonce string) map[string]any {
	now := o.Now()
	aud := o.ClientID
	if o.WrongAudience {
		aud = "someone-else"
	}
	if o.BadNonce {
		nonce = "not-the-nonce"
	}
	iat, exp := now, now.Add(time.Hour)
	if o.Expired {
		iat, exp = now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	}
	return map[string]any{
		"iss": p.Server.URL, "sub": o.Subject, "aud": aud, "exp": exp.Unix(), "iat": iat.Unix(), "nonce": nonce,
		"email": o.Email, "email_verified": o.EmailVerified, "name": o.Name, "preferred_username": o.PreferredUsername,
		"groups": o.Groups,
	}
}

// SignIDToken signs claims as a compact JWS with the current key for the
// configured algorithm, using the unknown key id when that switch is on.
func (p *Provider) SignIDToken(claims map[string]any) (string, error) {
	o := p.options()
	p.mu.Lock()
	es, rs, kid := p.es, p.rs, p.esKID
	if o.Alg == AlgRS256 {
		kid = p.rsKID
	}
	p.mu.Unlock()
	if o.UnknownKID {
		kid = "unknown-kid"
	}
	if o.Alg != AlgES256 && o.Alg != AlgRS256 {
		return "", fmt.Errorf("%w: alg %q", ErrProvider, o.Alg)
	}
	header, err := json.Marshal(map[string]string{"alg": o.Alg, "kid": kid, "typ": "JWT"})
	if err != nil {
		return "", fmt.Errorf("%w: header: %w", ErrProvider, err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("%w: claims: %w", ErrProvider, err)
	}
	signingInput := b64(header) + "." + b64(payload)
	digest := sha256.Sum256([]byte(signingInput))
	var sig []byte
	if o.Alg == AlgES256 {
		r, s, err := ecdsa.Sign(rand.Reader, es, digest[:])
		if err != nil {
			return "", fmt.Errorf("%w: sign: %w", ErrProvider, err)
		}
		sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	} else {
		sig, err = rsa.SignPKCS1v15(rand.Reader, rs, crypto.SHA256, digest[:])
		if err != nil {
			return "", fmt.Errorf("%w: sign: %w", ErrProvider, err)
		}
	}
	return signingInput + "." + b64(sig), nil
}

func tokenError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return b64(sum[:])
}

func randomString() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b64(b)
}

// WebView drives requests the way the device's web view does: it follows
// HTTP redirects itself, records the hops, and stops at the first
// non-redirect response, at a Location with a non-HTTP scheme such as
// apple-remotemanagement-user-login, or after MaxHops redirects,
// returning the last response unread.
type WebView struct {
	client *http.Client
	// MaxHops bounds the redirects followed; default 10. A test that wants
	// the provider's redirect back to the callback without fetching it
	// sets 1.
	MaxHops int
	// Hops lists every URL requested during the last Get.
	Hops []string
}

// Client returns a WebView that trusts the provider's certificate and any
// extra certificates (typically the relying party's test server's).
func (p *Provider) Client(trust ...*x509.Certificate) *WebView {
	pool := x509.NewCertPool()
	pool.AddCert(p.Certificate())
	for _, c := range trust {
		pool.AddCert(c)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &WebView{client: client, MaxHops: 10}
}

// Get fetches rawURL and follows redirects. The caller closes the body.
func (v *WebView) Get(ctx context.Context, rawURL string) (*http.Response, error) {
	v.Hops = v.Hops[:0]
	current := rawURL
	for {
		v.Hops = append(v.Hops, current)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return nil, fmt.Errorf("webauthtest: %w", err)
		}
		resp, err := v.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("webauthtest: %w", err)
		}
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" || len(v.Hops) > v.MaxHops {
			return resp, nil
		}
		next, err := req.URL.Parse(loc)
		if err != nil || (next.Scheme != "http" && next.Scheme != "https") {
			return resp, nil
		}
		_ = resp.Body.Close()
		current = next.String()
	}
}
