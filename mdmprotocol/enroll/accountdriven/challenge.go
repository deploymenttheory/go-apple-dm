package accountdriven

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Methods in the WWW-Authenticate challenge.
const (
	MethodAppleAsWeb  = "apple-as-web"
	MethodAppleOAuth2 = "apple-oauth2"
	// CallbackScheme is the ASWebAuthenticationSession callback scheme.
	CallbackScheme = "apple-remotemanagement-user-login"
	// ResultsHost is the network location of the apple-as-web result URL.
	ResultsHost = "authentication-results"
	// ResultsParam carries the access token in that URL.
	ResultsParam = "access-token"
)

// ErrChallenge reports an invalid challenge parameter.
var ErrChallenge = errors.New("accountdriven: invalid challenge")

// Challenge is the Bearer challenge Apple documents for each flow.
type Challenge struct {
	Method string
	// URL is the apple-as-web web-auth URL.
	URL string
	// OAuth 2 parameters (apple-oauth2).
	AuthorizationURL string
	TokenURL         string
	RedirectURL      string
	ClientID         string
	Scope            string
}

// Validate checks the parameters Apple requires for the method.
func (c Challenge) Validate() error {
	switch c.Method {
	case MethodAppleAsWeb:
		return requireHTTPS("url", c.URL)
	case MethodAppleOAuth2:
		for name, v := range map[string]string{"authorization-url": c.AuthorizationURL, "token-url": c.TokenURL} {
			if err := requireHTTPS(name, v); err != nil {
				return err
			}
		}
		u, err := url.Parse(c.RedirectURL)
		if err != nil || u.Scheme != CallbackScheme || (u.Path == "" && u.Opaque == "") {
			return fmt.Errorf("%w: redirect-url must use the %s scheme with a path", ErrChallenge, CallbackScheme)
		}
		if c.ClientID == "" || c.Scope == "" {
			return fmt.Errorf("%w: client-id and scope are required", ErrChallenge)
		}
		return nil
	}
	return fmt.Errorf("%w: method %q", ErrChallenge, c.Method)
}

func requireHTTPS(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: %s must be an absolute https URL", ErrChallenge, name)
	}
	return nil
}

// Header renders the WWW-Authenticate value.
func (c Challenge) Header() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	var parts []string
	add := func(k, v string) { parts = append(parts, fmt.Sprintf("%s=%q", k, v)) }
	add("method", c.Method)
	switch c.Method {
	case MethodAppleAsWeb:
		add("url", c.URL)
	case MethodAppleOAuth2:
		add("authorization-url", c.AuthorizationURL)
		add("token-url", c.TokenURL)
		add("redirect-url", c.RedirectURL)
		add("client-id", c.ClientID)
		add("scope", c.Scope)
	}
	return "Bearer " + strings.Join(parts, ", "), nil
}

// ParseChallenge reads a WWW-Authenticate value (the simulator uses it).
func ParseChallenge(header string) (Challenge, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(header), "Bearer ")
	if !ok {
		return Challenge{}, fmt.Errorf("%w: not a Bearer challenge", ErrChallenge)
	}
	var c Challenge
	for part := range strings.SplitSeq(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return Challenge{}, fmt.Errorf("%w: malformed parameter %q", ErrChallenge, part)
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
	return c, c.Validate()
}

// ResultURL is the apple-as-web completion redirect target.
func ResultURL(accessToken string) string {
	return CallbackScheme + "://" + ResultsHost + "?" + ResultsParam + "=" + url.QueryEscape(accessToken)
}
