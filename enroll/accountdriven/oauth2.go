package accountdriven

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth2 is the apple-oauth2 flow: we are the OAuth 2 authorization server
// for a public client. The device builds the authorization request from
// the 401 parameters (adding login_hint), the person authenticates on the
// authorization page, Grant issues a code and redirects (308) to the
// redirect-url with code and state, and the token endpoint exchanges the
// code (or a refresh token) for the bearer used on the second POST.
type OAuth2 struct {
	AuthorizationURL string
	TokenURL         string
	// RedirectURL uses the apple-remotemanagement-user-login scheme.
	RedirectURL string
	ClientID    string
	Scope       string
	Tokens      *Tokens
	// AccessTTL is reported as expires_in; zero uses the token default.
	AccessTTL time.Duration
}

// OAuth2 errors.
var (
	ErrOAuth2Request = errors.New("accountdriven: invalid oauth2 request")
	ErrOAuth2Grant   = errors.New("accountdriven: invalid grant")
)

// Challenge implements Authenticator.
func (o *OAuth2) Challenge(context.Context, *http.Request, *DeviceInfo) (Challenge, error) {
	return Challenge{Method: MethodAppleOAuth2, AuthorizationURL: o.AuthorizationURL, TokenURL: o.TokenURL,
		RedirectURL: o.RedirectURL, ClientID: o.ClientID, Scope: o.Scope}, nil
}

// AuthorizationRequest is the validated authorization request.
type AuthorizationRequest struct {
	State     string
	LoginHint string
	Scope     string
}

// ParseAuthorization validates the device's authorization request:
// response_type=code, our client id and redirect URI, a state.
func (o *OAuth2) ParseAuthorization(r *http.Request) (*AuthorizationRequest, error) {
	q := r.URL.Query()
	switch {
	case q.Get("response_type") != "code":
		return nil, fmt.Errorf("%w: response_type must be code", ErrOAuth2Request)
	case q.Get("client_id") != o.ClientID:
		return nil, fmt.Errorf("%w: unknown client_id", ErrOAuth2Request)
	case q.Get("redirect_uri") != o.RedirectURL:
		return nil, fmt.Errorf("%w: redirect_uri mismatch", ErrOAuth2Request)
	case q.Get("state") == "":
		return nil, fmt.Errorf("%w: state is required", ErrOAuth2Request)
	}
	return &AuthorizationRequest{State: q.Get("state"), LoginHint: q.Get("login_hint"), Scope: q.Get("scope")}, nil
}

// Grant completes authorization for id: it issues a single-use code bound
// to the request and redirects (308) to the redirect URL with code and
// the echoed state.
func (o *OAuth2) Grant(w http.ResponseWriter, r *http.Request, req *AuthorizationRequest, id Identity) error {
	if id.ManagedAppleID == "" {
		return ErrManagedAppleID
	}
	code, err := o.Tokens.Issue(r.Context(), KindCode, id, map[string]string{"redirect_uri": o.RedirectURL, "client_id": o.ClientID, "state": req.State})
	if err != nil {
		return err
	}
	u, err := url.Parse(o.RedirectURL)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOAuth2Request, err)
	}
	q := u.Query()
	q.Set("code", code)
	q.Set("state", req.State)
	u.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusPermanentRedirect)
	return nil
}

// TokenResponse is the token endpoint's JSON body.
type TokenResponse struct {
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

type tokenError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// TokenHandler serves the token endpoint: authorization_code and
// refresh_token grants for the public client.
func (o *OAuth2) TokenHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			writeTokenError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
			return
		}
		if r.PostForm.Get("client_id") != o.ClientID {
			writeTokenError(w, http.StatusUnauthorized, "invalid_client", "unknown client_id")
			return
		}
		var id Identity
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			if r.PostForm.Get("redirect_uri") != o.RedirectURL {
				writeTokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
				return
			}
			rec, err := o.Tokens.Consume(r.Context(), KindCode, r.PostForm.Get("code"))
			if err != nil {
				writeTokenError(w, http.StatusBadRequest, "invalid_grant", "code rejected")
				return
			}
			id = rec.Identity
		case "refresh_token":
			rec, err := o.Tokens.Consume(r.Context(), KindRefresh, r.PostForm.Get("refresh_token"))
			if err != nil {
				writeTokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token rejected")
				return
			}
			id = rec.Identity
		default:
			writeTokenError(w, http.StatusBadRequest, "unsupported_grant_type", "")
			return
		}
		access, err := o.Tokens.Issue(r.Context(), KindAccess, id, nil)
		if err != nil {
			writeTokenError(w, http.StatusInternalServerError, "server_error", "")
			return
		}
		refresh, err := o.Tokens.Issue(r.Context(), KindRefresh, id, nil)
		if err != nil {
			writeTokenError(w, http.StatusInternalServerError, "server_error", "")
			return
		}
		ttl := o.AccessTTL
		if ttl <= 0 {
			ttl = o.Tokens.ttl(KindAccess)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		_ = json.MarshalWrite(w, TokenResponse{TokenType: "Bearer", Scope: o.Scope, AccessToken: access, ExpiresIn: int64(ttl / time.Second), RefreshToken: refresh})
	})
}

func writeTokenError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, tokenError{Error: code, Description: desc})
}

// LoginHint reads the login_hint of an authorization request.
func LoginHint(r *http.Request) string { return strings.TrimSpace(r.URL.Query().Get("login_hint")) }
