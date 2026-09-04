package simulator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"uuid"

	json "encoding/json/v2"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/profile"
)

// Account-driven enrollment constants from Apple's flow description.
const (
	WellKnownPath          = "/.well-known/com.apple.remotemanagement"
	ContentTypeDeviceInfo  = "application/pkcs7-signature"
	AuthCallbackScheme     = "apple-remotemanagement-user-login"
	AuthResultsHost        = "authentication-results"
	AuthResultsTokenParam  = "access-token"
	accountDrivenMaxBody   = 1 << 20
	ContentTypeAspenConfig = "application/x-apple-aspen-config"
)

// ErrAccountDriven reports a failure in the account-driven flow.
var ErrAccountDriven = errors.New("simulator: account-driven enrollment")

// DiscoveryServer is one entry of the well-known document.
type DiscoveryServer struct {
	Version string
	BaseURL string
}

// AuthChallenge is the parsed WWW-Authenticate Bearer challenge.
type AuthChallenge struct {
	Method           string
	URL              string
	AuthorizationURL string
	TokenURL         string
	RedirectURL      string
	ClientID         string
	Scope            string
}

// ParseAuthChallenge reads the 401 header the device receives.
func ParseAuthChallenge(header string) (AuthChallenge, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(header), "Bearer ")
	if !ok {
		return AuthChallenge{}, fmt.Errorf("%w: not a Bearer challenge: %q", ErrAccountDriven, header)
	}
	var c AuthChallenge
	for part := range strings.SplitSeq(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "method":
			c.Method = v
		case "url":
			c.URL = v
		case "authorization-url":
			c.AuthorizationURL = v
		case "token-url":
			c.TokenURL = v
		case "redirect-url":
			c.RedirectURL = v
		case "client-id":
			c.ClientID = v
		case "scope":
			c.Scope = v
		}
	}
	if c.Method == "" {
		return AuthChallenge{}, fmt.Errorf("%w: challenge without method", ErrAccountDriven)
	}
	return c, nil
}

// AccountDrivenOptions drive AccountDrivenEnroll.
type AccountDrivenOptions struct {
	// UserIdentifier is what the person types (user@domain); the domain
	// hosts the well-known document unless DiscoveryURL overrides it.
	UserIdentifier string
	// DiscoveryURL, when set, replaces https://<domain> as the well-known
	// host (tests point it at a local server).
	DiscoveryURL string
	// ModelFamily is sent as model-family (default from the device model).
	ModelFamily string
	// Language is the LANGUAGE the body carries.
	Language string
	// Authenticate turns the 401 challenge into the bearer token the
	// device presents on the second attempt. Tests drive the web view or
	// the OAuth 2 flow here.
	Authenticate func(ctx context.Context, c AuthChallenge) (string, error)
	// Signer signs the enrollment body; nil uses the device Identity.
	Signer *Identity
	// Parse configures profile parsing (signature requirements).
	Parse profile.ParseOptions
}

// AccountDrivenResult reports what the flow saw.
type AccountDrivenResult struct {
	Servers   []DiscoveryServer
	Chosen    DiscoveryServer
	Challenge AuthChallenge
	// Profile is the enrollment profile the device installed.
	Profile []byte
}

// AccountDrivenEnroll runs Apple's account-driven enrollment: service
// discovery, the first signed POST, the 401 challenge, authentication
// through opts.Authenticate, the second POST with the bearer, then
// ApplyProfile and Enroll as a User Enrollment identified by a fresh
// EnrollmentID.
func (d *Device) AccountDrivenEnroll(ctx context.Context, opts AccountDrivenOptions) (*AccountDrivenResult, error) {
	if opts.Authenticate == nil {
		return nil, fmt.Errorf("%w: Authenticate is required", ErrAccountDriven)
	}
	_, domain, ok := strings.Cut(opts.UserIdentifier, "@")
	if !ok || domain == "" {
		return nil, fmt.Errorf("%w: user identifier %q is not user@domain", ErrAccountDriven, opts.UserIdentifier)
	}
	res := &AccountDrivenResult{}
	var err error
	if res.Servers, err = d.discover(ctx, opts, domain); err != nil {
		return res, err
	}
	if len(res.Servers) == 0 {
		return res, fmt.Errorf("%w: no servers in the well-known document", ErrAccountDriven)
	}
	res.Chosen = res.Servers[0]
	body, err := d.enrollmentBody(opts)
	if err != nil {
		return res, err
	}
	status, header, data, err := d.attempt(ctx, res.Chosen.BaseURL, body, "")
	if err != nil {
		return res, err
	}
	if status != http.StatusUnauthorized {
		return res, &HTTPError{Status: status, Body: data}
	}
	if res.Challenge, err = ParseAuthChallenge(header); err != nil {
		return res, err
	}
	bearer, err := opts.Authenticate(ctx, res.Challenge)
	if err != nil {
		return res, fmt.Errorf("%w: authenticate: %w", ErrAccountDriven, err)
	}
	status, _, data, err = d.attempt(ctx, res.Chosen.BaseURL, body, bearer)
	if err != nil {
		return res, err
	}
	if status != http.StatusOK {
		return res, &HTTPError{Status: status, Body: data}
	}
	res.Profile = data
	if err := d.ApplyProfile(ctx, data, opts.Parse); err != nil {
		return res, err
	}
	d.EnrollmentID = strings.ToUpper(uuid.NewV7().String())
	if err := d.Enroll(ctx); err != nil {
		return res, err
	}
	return res, nil
}

func (d *Device) discover(ctx context.Context, opts AccountDrivenOptions, domain string) ([]DiscoveryServer, error) {
	base := "https://" + domain
	if opts.DiscoveryURL != "" {
		base = strings.TrimSuffix(opts.DiscoveryURL, "/")
	}
	family := opts.ModelFamily
	if family == "" {
		family = modelFamily(d.ProductName)
	}
	q := url.Values{"user-identifier": {opts.UserIdentifier}, "model-family": {family}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+WellKnownPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccountDriven, err)
	}
	req.Header.Set("User-Agent", "MDM/1.0 go-apple-dm-simulator")
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery: %w", ErrAccountDriven, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, accountDrivenMaxBody))
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Status: resp.StatusCode, Body: data}
	}
	var doc struct {
		Servers []DiscoveryServer `json:"Servers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: well-known document: %w", ErrAccountDriven, err)
	}
	return doc.Servers, nil
}

// modelFamily maps a product type to Apple's model-family values.
func modelFamily(product string) string {
	switch {
	case strings.HasPrefix(product, "iPhone"):
		return "iPhone"
	case strings.HasPrefix(product, "iPad"):
		return "iPad"
	case strings.HasPrefix(product, "AppleTV"):
		return "AppleTV"
	case strings.HasPrefix(product, "RealityDevice"):
		return "RealityDevice"
	case strings.HasPrefix(product, "Watch"):
		return "Watch"
	}
	return "Mac"
}

// enrollmentBody is the CMS-signed plist with LANGUAGE, PRODUCT, VERSION
// (and the OS version), signed with the device's built-in identity.
func (d *Device) enrollmentBody(opts AccountDrivenOptions) ([]byte, error) {
	lang := opts.Language
	if lang == "" {
		lang = "en"
	}
	raw, err := plist.Marshal(map[string]any{"LANGUAGE": lang, "PRODUCT": d.ProductName, "VERSION": d.BuildVersion, "OS_VERSION": d.OSVersion})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccountDriven, err)
	}
	signer := opts.Signer
	if signer == nil {
		signer = d.Identity
	}
	if signer == nil {
		return nil, fmt.Errorf("%w: no identity to sign the enrollment body", ErrAccountDriven)
	}
	signed, err := cms.SignAttached(raw, signer.Cert, signer.Key)
	if err != nil {
		return nil, fmt.Errorf("%w: sign body: %w", ErrAccountDriven, err)
	}
	return signed, nil
}

// attempt posts the enrollment body, optionally with a bearer, and returns
// the status, WWW-Authenticate header, and body.
func (d *Device) attempt(ctx context.Context, baseURL string, body []byte, bearer string) (int, string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, fmt.Errorf("%w: %w", ErrAccountDriven, err)
	}
	req.Header.Set("Content-Type", ContentTypeDeviceInfo)
	req.Header.Set("User-Agent", "MDM/1.0 go-apple-dm-simulator")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return 0, "", nil, fmt.Errorf("%w: enroll: %w", ErrAccountDriven, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, accountDrivenMaxBody))
	return resp.StatusCode, resp.Header.Get("WWW-Authenticate"), data, nil
}

// AccessTokenFromRedirect extracts the access token from the
// apple-remotemanagement-user-login://authentication-results redirect an
// apple-as-web page ends with.
func AccessTokenFromRedirect(location string) (string, error) {
	u, err := url.Parse(location)
	if err != nil || u.Scheme != AuthCallbackScheme || u.Host != AuthResultsHost {
		return "", fmt.Errorf("%w: unexpected redirect %q", ErrAccountDriven, location)
	}
	tok := u.Query().Get(AuthResultsTokenParam)
	if tok == "" {
		return "", fmt.Errorf("%w: redirect without access-token", ErrAccountDriven)
	}
	return tok, nil
}

// OAuth2CodeFlow performs the apple-oauth2 authorization code grant the
// way the device does: it builds the authorization request with state and
// login_hint, hands it to authorize (which plays the person signing in and
// returns the 308 Location), checks the echoed state, and exchanges the
// code at the token endpoint. It returns the access token.
func (d *Device) OAuth2CodeFlow(ctx context.Context, c AuthChallenge, loginHint string, authorize func(ctx context.Context, authorizationURL string) (location string, err error)) (string, error) {
	state := strings.ToUpper(uuid.NewV7().String())
	q := url.Values{"response_type": {"code"}, "client_id": {c.ClientID}, "redirect_uri": {c.RedirectURL}, "state": {state}, "login_hint": {loginHint}}
	if c.Scope != "" {
		q.Set("scope", c.Scope)
	}
	sep := "?"
	if strings.Contains(c.AuthorizationURL, "?") {
		sep = "&"
	}
	location, err := authorize(ctx, c.AuthorizationURL+sep+q.Encode())
	if err != nil {
		return "", fmt.Errorf("%w: authorize: %w", ErrAccountDriven, err)
	}
	loc, err := url.Parse(location)
	if err != nil || loc.Scheme != AuthCallbackScheme {
		return "", fmt.Errorf("%w: unexpected authorization redirect %q", ErrAccountDriven, location)
	}
	if loc.Query().Get("state") != state {
		return "", fmt.Errorf("%w: state mismatch", ErrAccountDriven)
	}
	code := loc.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("%w: redirect without code", ErrAccountDriven)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.RedirectURL}, "client_id": {c.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrAccountDriven, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := d.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: token request: %w", ErrAccountDriven, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, accountDrivenMaxBody))
	if resp.StatusCode != http.StatusOK {
		return "", &HTTPError{Status: resp.StatusCode, Body: data}
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(data, &tr); err != nil || tr.AccessToken == "" {
		return "", fmt.Errorf("%w: token response: %v", ErrAccountDriven, err)
	}
	return tr.AccessToken, nil
}
