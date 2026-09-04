package webauth

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrProvider is wrapped by failures talking to the identity provider:
// discovery, JWKS, and the token endpoint.
var ErrProvider = errors.New("webauth: provider")

// Endpoints are the provider URLs. Set them to skip discovery.
type Endpoints struct {
	Authorization string
	Token         string
	JWKS          string
}

// discoveryDocument is the subset of the OpenID Provider Metadata used.
//
//nolint:tagliatelle // keys are the OpenID Connect Discovery names
type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// tokenResponse is the token endpoint's JSON body.
//
//nolint:tagliatelle // keys are RFC 6749 names
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// jwksMinRefresh bounds how often an unknown key id triggers a refetch.
const jwksMinRefresh = time.Minute

// discoveryPath is appended to the issuer.
const discoveryPath = "/.well-known/openid-configuration"

// fetch performs a bounded GET or POST and returns the body and status.
func (f *Flow) fetch(req *http.Request) (int, []byte, error) {
	resp, err := f.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s: %w", ErrProvider, req.URL.Host, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s: read: %w", ErrProvider, req.URL.Host, err)
	}
	if int64(len(body)) > f.maxBytes {
		return 0, nil, fmt.Errorf("%w: %s: response larger than %d bytes", ErrProvider, req.URL.Host, f.maxBytes)
	}
	return resp.StatusCode, body, nil
}

func (f *Flow) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProvider, err)
	}
	req.Header.Set("Accept", "application/json")
	status, body, err := f.fetch(req)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: GET %s: status %d", ErrProvider, req.URL.Host+req.URL.Path, status)
	}
	return body, nil
}

// endpoints returns the resolved provider endpoints, fetching the
// discovery document once when they were not configured.
func (f *Flow) endpoints(ctx context.Context) (Endpoints, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resolved.Authorization != "" {
		return f.resolved, nil
	}
	body, err := f.get(ctx, strings.TrimSuffix(f.cfg.Issuer, "/")+discoveryPath)
	if err != nil {
		return Endpoints{}, err
	}
	var doc discoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return Endpoints{}, fmt.Errorf("%w: discovery document: %w", ErrProvider, err)
	}
	if doc.Issuer != f.cfg.Issuer {
		return Endpoints{}, fmt.Errorf("%w: discovery document issuer %q, configured %q", ErrProvider, doc.Issuer, f.cfg.Issuer)
	}
	eps := Endpoints{Authorization: doc.AuthorizationEndpoint, Token: doc.TokenEndpoint, JWKS: doc.JWKSURI}
	if err := f.checkEndpoints(eps); err != nil {
		return Endpoints{}, fmt.Errorf("%w: discovery document: %w", ErrConfig, err)
	}
	f.resolved = eps
	return eps, nil
}

func (f *Flow) checkEndpoints(eps Endpoints) error {
	for name, u := range map[string]string{"authorization_endpoint": eps.Authorization, "token_endpoint": eps.Token, "jwks_uri": eps.JWKS} {
		if err := f.requireHTTPS(u); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// ErrNotHTTPS reports a URL that is not absolute https.
var ErrNotHTTPS = errors.New("webauth: URL must be absolute https")

func (f *Flow) requireHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotHTTPS, err)
	}
	if u.Host == "" || (u.Scheme != "https" && !(f.cfg.AllowInsecureForTests && u.Scheme == "http")) {
		return fmt.Errorf("%w: %q", ErrNotHTTPS, raw)
	}
	return nil
}

// keysFor returns the cached keys for kid and alg, refreshing the JWKS
// when the kid is unknown and the last fetch is old enough.
func (f *Flow) keysFor(ctx context.Context, jwksURL, kid, alg string) []verificationKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keys == nil {
		if err := f.refreshKeysLocked(ctx, jwksURL); err != nil {
			f.log.WarnContext(ctx, "webauth: jwks fetch failed", "error", err)
			return nil
		}
	}
	if keys := selectKeys(f.keys, kid, alg); len(keys) > 0 {
		return keys
	}
	if f.clock.Now().Sub(f.keysFetched) < jwksMinRefresh {
		return nil
	}
	if err := f.refreshKeysLocked(ctx, jwksURL); err != nil {
		f.log.WarnContext(ctx, "webauth: jwks refresh failed", "error", err)
		return nil
	}
	return selectKeys(f.keys, kid, alg)
}

func (f *Flow) refreshKeysLocked(ctx context.Context, jwksURL string) error {
	body, err := f.get(ctx, jwksURL)
	if err != nil {
		return err
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return err
	}
	f.keys, f.keysFetched = keys, f.clock.Now()
	return nil
}

// selectKeys picks keys by kid and algorithm; a token without a kid may
// match any key of its algorithm.
func selectKeys(keys []verificationKey, kid, alg string) []verificationKey {
	var out []verificationKey
	for _, k := range keys {
		if k.alg == alg && (kid == "" || k.kid == kid) {
			out = append(out, k)
		}
	}
	return out
}

// exchange redeems the authorization code with the PKCE verifier.
func (f *Flow) exchange(ctx context.Context, tokenURL, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {f.cfg.RedirectURL},
		"code_verifier": {verifier},
		"client_id":     {f.cfg.ClientID},
	}
	if f.cfg.ClientSecret != "" && f.cfg.ClientAuth == ClientAuthPost {
		form.Set("client_secret", f.cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrProvider, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if f.cfg.ClientSecret != "" && f.cfg.ClientAuth == ClientAuthBasic {
		// RFC 6749 section 2.3.1: the credentials are form-encoded before
		// they go into the Basic header.
		req.SetBasicAuth(url.QueryEscape(f.cfg.ClientID), url.QueryEscape(f.cfg.ClientSecret))
	}
	status, body, err := f.fetch(req)
	if err != nil {
		return "", err
	}
	var tr tokenResponse
	if jerr := json.Unmarshal(body, &tr); jerr != nil && status == http.StatusOK {
		return "", fmt.Errorf("%w: token response: %w", ErrProvider, jerr)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("%w: token endpoint: status %d error %q: %s", ErrProvider, status, tr.Error, tr.ErrorDescription)
	}
	if tr.IDToken == "" {
		return "", fmt.Errorf("%w: token response has no id_token", ErrProvider)
	}
	return tr.IDToken, nil
}
