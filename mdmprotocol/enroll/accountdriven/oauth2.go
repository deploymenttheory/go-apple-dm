package accountdriven

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth2 serves the apple-oauth2 authorization-code flow for a public client.
// The device builds an authorization request from the challenge parameters.
// After user authentication, Grant issues a code and redirects to the configured
// redirect URL with code and state. The token endpoint exchanges a code or
// refresh token for the reusable access bearer.
type OAuth2 struct {
	AuthorizationURL string
	TokenURL         string
	// RedirectURL uses the apple-remotemanagement-user-login scheme.
	RedirectURL string
	ClientID    string
	Scope       string
	Tokens      *Tokens
	// AccessTTL controls both issuance and expires_in; zero uses Tokens.AccessTTL.
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

// ParseAuthorization validates response_type=code, the configured client ID and
// redirect URI, and a nonempty state.
func (o *OAuth2) ParseAuthorization(r *http.Request) (*AuthorizationRequest, error) {
	q := r.URL.Query()
	switch {
	case q.Get("response_type") != "code":
		return nil, fmt.Errorf("%w: response_type must be code", ErrOAuth2Request)
	case q.Get("client_id") != o.ClientID:
		return nil, fmt.Errorf("%w: unknown client_id", ErrOAuth2Request)
	case q.Get("redirect_uri") != o.RedirectURL:
		return nil, fmt.Errorf("%w: redirect_uri mismatch", ErrOAuth2Request)
	case q.Get("scope") != "" && q.Get("scope") != o.Scope:
		return nil, fmt.Errorf("%w: scope mismatch", ErrOAuth2Request)
	case q.Get("state") == "":
		return nil, fmt.Errorf("%w: state is required", ErrOAuth2Request)
	}
	return &AuthorizationRequest{State: q.Get("state"), LoginHint: q.Get("login_hint"), Scope: o.Scope}, nil
}

// Grant completes authorization for id: it issues a single-use code bound
// to the request and redirects (308) to the redirect URL with code and
// the echoed state.
func (o *OAuth2) Grant(w http.ResponseWriter, r *http.Request, req *AuthorizationRequest, id Identity) error {
	if req == nil || req.State == "" || req.Scope != o.Scope {
		return ErrOAuth2Request
	}
	if id.ManagedAppleAccount == "" {
		return ErrManagedAppleAccount
	}
	code, err := o.Tokens.Issue(r.Context(), KindCode, id, map[string]string{"redirect_uri": o.RedirectURL, "client_id": o.ClientID, "state": req.State, "scope": req.Scope})
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
		var kind Kind
		var value string
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			kind, value = KindCode, r.PostForm.Get("code")
		case "refresh_token":
			kind, value = KindRefresh, r.PostForm.Get("refresh_token")
		default:
			writeTokenError(w, http.StatusBadRequest, "unsupported_grant_type", "")
			return
		}
		result, err := o.exchange(r.Context(), kind, value, r.PostForm)
		if err != nil {
			if errors.Is(err, ErrOAuth2Grant) || errors.Is(err, ErrTokenNotFound) || errors.Is(err, ErrTokenExpired) || errors.Is(err, ErrTokenUsed) {
				writeTokenError(w, http.StatusBadRequest, "invalid_grant", "grant rejected")
			} else {
				writeTokenError(w, http.StatusInternalServerError, "server_error", "")
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		_ = json.MarshalWrite(w, result)
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

// exchange validates bound metadata again under the consuming transaction. A
// malformed request cannot burn a valid code or refresh token.
func (o *OAuth2) exchange(ctx context.Context, kind Kind, value string, form url.Values) (TokenResponse, error) {
	st, ok := o.Tokens.Store.(AtomicTokenStore)
	if !ok {
		return TokenResponse{}, fmt.Errorf("%w: atomic token store required", ErrConfig)
	}
	rec, err := o.Tokens.Check(ctx, kind, value)
	if err != nil {
		return TokenResponse{}, err
	}
	validate := func(r Record) error {
		if r.Kind != kind || r.Meta["client_id"] != o.ClientID || r.Meta["scope"] != o.Scope {
			return ErrOAuth2Grant
		}
		if kind == KindCode && (r.Meta["redirect_uri"] != o.RedirectURL || form.Get("redirect_uri") != r.Meta["redirect_uri"]) {
			return ErrOAuth2Grant
		}
		if scope := form.Get("scope"); scope != "" && scope != r.Meta["scope"] {
			return ErrOAuth2Grant
		}
		return nil
	}
	if err := validate(rec); err != nil {
		return TokenResponse{}, err
	}
	access, err := NewToken()
	if err != nil {
		return TokenResponse{}, err
	}
	refresh, err := NewToken()
	if err != nil {
		return TokenResponse{}, err
	}
	now := o.Tokens.now()
	ttl := or(o.AccessTTL, o.Tokens.ttl(KindAccess))
	meta := maps.Clone(rec.Meta)
	delete(meta, "access_hash")
	delete(meta, "state")
	accessRec := Record{Kind: KindAccess, Identity: rec.Identity, IssuedAt: now, ExpiresAt: now.Add(ttl), Meta: maps.Clone(meta)}
	meta["access_hash"] = Hash(access)
	refreshRec := Record{Kind: KindRefresh, Identity: rec.Identity, IssuedAt: now, ExpiresAt: now.Add(o.Tokens.ttl(KindRefresh)), Meta: meta}
	if err := st.Exchange(ctx, Hash(value), now, validate, map[string]Record{Hash(access): accessRec, Hash(refresh): refreshRec}); err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{TokenType: "Bearer", Scope: o.Scope, AccessToken: access, RefreshToken: refresh, ExpiresIn: int64(ttl / time.Second)}, nil
}
