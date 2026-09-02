package axm

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"uuid"

	"github.com/deploymenttheory/go-apple-mdm/secrets"
)

// OAuth constants from Apple's "Implementing OAuth" page.
const (
	// Audience is the aud claim of the client assertion.
	Audience = "https://account.apple.com/auth/oauth2/v2/token"
	// DefaultTokenURL is the token endpoint.
	DefaultTokenURL = "https://account.apple.com/auth/oauth2/token" // #nosec G101 -- a URL, not a credential
	// ClientAssertionType is the client_assertion_type parameter.
	ClientAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	// ScopeBusiness and ScopeSchool are the two scopes.
	ScopeBusiness = "business.api"
	ScopeSchool   = "school.api"
	// BusinessPrefix and SchoolPrefix start every client id and decide the
	// scope and host.
	BusinessPrefix = "BUSINESSAPI."
	SchoolPrefix   = "SCHOOLAPI."
	// MaxAssertionTTL is the longest life Apple allows an assertion.
	MaxAssertionTTL = 180 * 24 * time.Hour
	// DefaultAssertionTTL is the life of the assertions this package mints.
	DefaultAssertionTTL = 20 * time.Minute
	// DefaultClockSkew back-dates iat to survive a slow clock.
	DefaultClockSkew = time.Minute
	// DefaultRefreshMargin is how long before expires_in a token is renewed.
	DefaultRefreshMargin = 5 * time.Minute
)

// ParseKey reads a PEM private key in SEC1 ("EC PRIVATE KEY") or PKCS#8
// ("PRIVATE KEY") form. Only ECDSA P-256 keys are accepted.
func ParseKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("%w: no PEM block", ErrKey)
	}
	var key any
	var err error
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("%w: unsupported PEM block %q", ErrKey, block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKey, err)
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", ErrKeyType, key)
	}
	if ec.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: got %s", ErrKeyType, ec.Curve.Params().Name)
	}
	return ec, nil
}

// LoadKeyFile reads a PEM private key from path.
func LoadKeyFile(path string) (*ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the path is the operator's key file
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKey, err)
	}
	return ParseKey(b)
}

// LoadKey reads a PEM private key named name from a secrets provider.
func LoadKey(ctx context.Context, p secrets.Provider, name string) (*ecdsa.PrivateKey, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: nil secrets provider", ErrKey)
	}
	s, err := p.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrKey, name, err)
	}
	return ParseKey(s.Bytes())
}

// ScopeFor derives the scope from a client id prefix: business.api for
// BUSINESSAPI., school.api for SCHOOLAPI., "" otherwise.
func ScopeFor(clientID string) string {
	switch {
	case strings.HasPrefix(clientID, BusinessPrefix):
		return ScopeBusiness
	case strings.HasPrefix(clientID, SchoolPrefix):
		return ScopeSchool
	}
	return ""
}

// AssertionClaims are the claims of a client assertion.
type AssertionClaims struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Jti string `json:"jti"`
}

// AssertionHeader is the header of a client assertion.
type AssertionHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// Assertion mints an ES256 client assertion (RFC 7519) for clientID
// signed by key under keyID: iss and sub are the client id, aud is
// Audience, iat is now less skew, exp is now plus ttl capped at iat plus
// MaxAssertionTTL, jti is a fresh UUID.
func Assertion(key *ecdsa.PrivateKey, clientID, keyID string, now time.Time, ttl, skew time.Duration) (string, error) {
	if key == nil || key.Curve != elliptic.P256() {
		return "", ErrKeyType
	}
	if ttl <= 0 {
		ttl = DefaultAssertionTTL
	}
	if skew < 0 {
		skew = 0
	}
	iat := now.Add(-skew)
	exp := now.Add(ttl)
	if maxExp := iat.Add(MaxAssertionTTL); exp.After(maxExp) {
		exp = maxExp
	}
	header, err := json.Marshal(AssertionHeader{Alg: "ES256", Kid: keyID})
	if err != nil {
		return "", fmt.Errorf("%w: header: %w", ErrKey, err)
	}
	claims, err := json.Marshal(AssertionClaims{
		Iss: clientID, Sub: clientID, Aud: Audience,
		Iat: iat.Unix(), Exp: exp.Unix(), Jti: uuid.New().String(),
	})
	if err != nil {
		return "", fmt.Errorf("%w: claims: %w", ErrKey, err)
	}
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", fmt.Errorf("%w: sign: %w", ErrKey, err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// TokenResponse is the token endpoint's success body.
//
//nolint:tagliatelle // OAuth 2.0 member names
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

// oauthError is the token endpoint's RFC 6749 error body.
//
//nolint:tagliatelle // OAuth 2.0 member names
type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// maxErrorBody bounds how much of an error body is kept.
const maxErrorBody = 64 << 10

// exchange posts the client assertion to the token endpoint.
func (c *Client) exchange(ctx context.Context) (TokenResponse, error) {
	assertion, err := Assertion(c.key, c.cfg.ClientID, c.cfg.KeyID, c.clock.Now(), c.cfg.AssertionTTL, c.cfg.ClockSkew)
	if err != nil {
		return TokenResponse{}, &AuthError{Err: err}
	}
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {c.cfg.ClientID},
		"client_assertion_type": {ClientAssertionType},
		"client_assertion":      {assertion},
		"scope":                 {c.cfg.Scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, &AuthError{Err: fmt.Errorf("%w: %w", ErrTransport, err)}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return TokenResponse{}, &AuthError{Err: fmt.Errorf("%w: %w", ErrTransport, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return TokenResponse{}, &AuthError{Status: resp.StatusCode, Err: fmt.Errorf("%w: %w", ErrTransport, err)}
	}
	if resp.StatusCode != http.StatusOK {
		ae := &AuthError{Status: resp.StatusCode, Body: body}
		var oe oauthError
		if json.Unmarshal(body, &oe) == nil {
			ae.Code, ae.Description = oe.Error, oe.Description
		}
		return TokenResponse{}, ae
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenResponse{}, &AuthError{Status: resp.StatusCode, Body: body, Err: fmt.Errorf("%w: %w", ErrDecode, err)}
	}
	if tr.AccessToken == "" {
		return TokenResponse{}, &AuthError{Status: resp.StatusCode, Body: body, Err: fmt.Errorf("%w: empty access_token", ErrDecode)}
	}
	return tr, nil
}

// cachedToken is the access token and when it stops being used.
type cachedToken struct {
	value   string
	expires time.Time // expires_in from Apple
	renewAt time.Time // expires less the refresh margin
}

// Token returns a valid access token, exchanging a fresh client assertion
// when none is cached or the cached one is within RefreshMargin of its
// expiry. Concurrent callers share one exchange.
func (c *Client) Token(ctx context.Context) (string, error) {
	return c.token(ctx, "")
}

// token returns the cached token unless it is stale or equals invalid (the
// token a 401 just rejected), in which case one caller exchanges while the
// others wait for its result.
func (c *Client) token(ctx context.Context, invalid string) (string, error) {
	c.mu.Lock()
	if tok := c.tok; tok.value != "" && tok.value != invalid && c.clock.Now().Before(tok.renewAt) {
		c.mu.Unlock()
		return tok.value, nil
	}
	if c.inflight == nil {
		c.inflight = make(chan struct{})
		c.mu.Unlock()
		tr, err := c.exchange(ctx)
		c.mu.Lock()
		if err == nil {
			now := c.clock.Now()
			ttl := time.Duration(tr.ExpiresIn) * time.Second
			renew := ttl - c.cfg.RefreshMargin
			if renew <= 0 {
				renew = ttl / 2
			}
			c.tok = cachedToken{value: tr.AccessToken, expires: now.Add(ttl), renewAt: now.Add(renew)}
			c.log.DebugContext(ctx, "axm: token exchanged", "scope", tr.Scope, "expires_in", tr.ExpiresIn)
		}
		c.exchErr = err
		close(c.inflight)
		c.inflight = nil
		c.mu.Unlock()
		if err != nil {
			return "", err
		}
		return tr.AccessToken, nil
	}
	wait := c.inflight
	c.mu.Unlock()
	select {
	case <-wait:
	case <-ctx.Done():
		return "", fmt.Errorf("%w: waiting for token: %w", ErrTransport, ctx.Err())
	}
	c.mu.Lock()
	tok, err := c.tok, c.exchErr
	c.mu.Unlock()
	if err != nil {
		return "", err
	}
	return tok.value, nil
}

// ForceRefresh drops the cached token so the next request exchanges a new
// one.
func (c *Client) ForceRefresh() {
	c.mu.Lock()
	c.tok = cachedToken{}
	c.mu.Unlock()
}
