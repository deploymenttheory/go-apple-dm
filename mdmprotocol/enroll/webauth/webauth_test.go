package webauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/webauth/webauthtest"
)

var errBoom = errors.New("boom")

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type completion struct {
	bound    webauth.Bound
	claims   webauth.Claims
	decision webauth.Decision
}

// harness is a relying party in front of the fake provider.
type harness struct {
	t     *testing.T
	idp   *webauthtest.Provider
	rp    *httptest.Server
	flow  *webauth.Flow
	clock *clock.Fake
	view  *webauthtest.WebView

	mu        sync.Mutex
	completed []completion
	failures  []failure
}

type failure struct {
	status int
	err    error
}

func newHarness(t *testing.T, mutate func(cfg *webauth.Config)) *harness {
	t.Helper()
	return newHarnessWith(t, webauthtest.New(t), mutate)
}

// newHarnessWith builds a relying party in front of an existing provider.
func newHarnessWith(t *testing.T, idp *webauthtest.Provider, mutate func(cfg *webauth.Config)) *harness {
	t.Helper()
	h := &harness{t: t, idp: idp, clock: clock.NewFake(time.Now())}
	h.idp.Set(func(o *webauthtest.Options) { o.Now = h.clock.Now })
	mux := http.NewServeMux()
	h.rp = httptest.NewTLSServer(mux)
	t.Cleanup(h.rp.Close)
	cfg := webauth.Config{
		Issuer:      h.idp.Issuer(),
		ClientID:    "enroll-client",
		RedirectURL: h.rp.URL + "/callback",
		Scopes:      []string{"email", "openid", "", "email"},
		Clock:       h.clock,
		HTTPClient:  h.idp.HTTPClient(),
		Logger:      quiet(),
		Complete: func(_ context.Context, bound webauth.Bound, claims webauth.Claims, decision webauth.Decision, w http.ResponseWriter, _ *http.Request) {
			h.mu.Lock()
			h.completed = append(h.completed, completion{bound, claims, decision})
			h.mu.Unlock()
			w.Header().Set("Content-Type", "application/x-apple-aspen-config")
			fmt.Fprintf(w, "profile for %s as %s", bound.Serial, claims.Subject)
		},
		OnError: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			h.mu.Lock()
			h.failures = append(h.failures, failure{status, err})
			h.mu.Unlock()
			http.Error(w, "failed", status)
		},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	flow, err := webauth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.flow = flow
	mux.HandleFunc("/begin", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		bound := webauth.Bound{Serial: q.Get("serial"), UDID: q.Get("udid"), LoginHint: q.Get("hint")}
		if q.Get("extra") != "" {
			bound.Extra = map[string]string{"k": q.Get("extra")}
		}
		_ = flow.Begin(w, r, bound)
	})
	mux.Handle("/callback", flow.Callback())
	h.view = h.idp.Client(h.rp.Certificate())
	return h
}

// get follows the redirects like the web view and returns the final
// status and body.
func (h *harness) get(path string) (int, string) {
	h.t.Helper()
	resp, err := h.view.Get(context.Background(), h.rp.URL+path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// callbackURL runs Begin and the provider hop by hand and returns the
// callback URL the provider redirected to, without fetching it.
func (h *harness) callbackURL(path string) string {
	h.t.Helper()
	v := h.idp.Client(h.rp.Certificate())
	v.MaxHops = 1
	resp, err := v.Get(context.Background(), h.rp.URL+path)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		h.t.Fatalf("provider hop: status %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func (h *harness) lastFailure() failure {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.failures) == 0 {
		h.t.Fatal("no failure recorded")
	}
	return h.failures[len(h.failures)-1]
}

func (h *harness) completions() []completion {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]completion(nil), h.completed...)
}

func s256(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestFlow(t *testing.T) {
	t.Parallel()

	t.Run("PKCEAndNonce", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		status, body := h.get("/begin?serial=C02XYZ&udid=UDID-1&hint=user01%40example.com&extra=v")
		if status != http.StatusOK || body != "profile for C02XYZ as user-1" {
			t.Fatalf("status %d body %q", status, body)
		}
		if len(h.view.Hops) != 3 || !strings.Contains(h.view.Hops[1], "/authorize?") || !strings.Contains(h.view.Hops[2], "/callback?") {
			t.Fatalf("hops %v", h.view.Hops)
		}
		auths, tokens := h.idp.Authorizes(), h.idp.Tokens()
		if len(auths) != 1 || len(tokens) != 1 {
			t.Fatalf("authorizes %d tokens %d", len(auths), len(tokens))
		}
		a, tk := auths[0], tokens[0]
		if a.ResponseType != "code" || a.ClientID != "enroll-client" || a.RedirectURI != h.rp.URL+"/callback" || a.Scope != "openid email" || a.LoginHint != "user01@example.com" {
			t.Fatalf("authorize %+v", a)
		}
		if len(a.State) != 22 || len(a.Nonce) != 22 || a.CodeChallengeMethod != "S256" || len(tk.CodeVerifier) != 43 || s256(tk.CodeVerifier) != a.CodeChallenge {
			t.Fatalf("pkce: authorize %+v token %+v", a, tk)
		}
		if tk.GrantType != "authorization_code" || tk.RedirectURI != a.RedirectURI || tk.ClientID != "enroll-client" || tk.ClientSecret != "" || tk.BasicAuth {
			t.Fatalf("token %+v", tk)
		}
		c := h.completions()
		if len(c) != 1 {
			t.Fatalf("completions %d", len(c))
		}
		got := c[0]
		if got.bound.Serial != "C02XYZ" || got.bound.UDID != "UDID-1" || got.bound.Extra["k"] != "v" || got.bound.LoginHint != "user01@example.com" {
			t.Fatalf("bound %+v", got.bound)
		}
		if got.claims.Subject != "user-1" || got.claims.Email != "user-1@example.com" || !got.claims.EmailVerified || got.claims.Name != "User One" || got.claims.PreferredUsername != "user1" || len(got.claims.Groups) != 1 || got.claims.Groups[0] != "staff" {
			t.Fatalf("claims %+v", got.claims)
		}
		if got.claims.Raw["nonce"] != a.Nonce || got.claims.Raw["iss"] != h.idp.Issuer() {
			t.Fatalf("raw %v", got.claims.Raw)
		}
		if got.decision.Profile != "" || got.decision.Attributes != nil {
			t.Fatalf("decision %+v", got.decision)
		}
		// The second sign-in gets fresh state, nonce, and verifier.
		if status, _ := h.get("/begin?serial=C02XYZ"); status != http.StatusOK {
			t.Fatalf("second: %d", status)
		}
		auths, tokens = h.idp.Authorizes(), h.idp.Tokens()
		if auths[1].State == a.State || auths[1].Nonce == a.Nonce || tokens[1].CodeVerifier == tk.CodeVerifier {
			t.Fatal("state, nonce, or verifier reused")
		}
	})

	t.Run("StateSingleUse", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		cb := h.callbackURL("/begin?serial=S1")
		first, err := h.view.Get(context.Background(), cb)
		if err != nil {
			t.Fatal(err)
		}
		first.Body.Close()
		if first.StatusCode != http.StatusOK {
			t.Fatalf("first: %d", first.StatusCode)
		}
		second, err := h.view.Get(context.Background(), cb)
		if err != nil {
			t.Fatal(err)
		}
		second.Body.Close()
		if second.StatusCode != http.StatusBadRequest {
			t.Fatalf("replay: %d", second.StatusCode)
		}
		if f := h.lastFailure(); !errors.Is(f.err, webauth.ErrStateNotFound) || !errors.Is(f.err, webauth.ErrCallback) {
			t.Fatalf("replay error %v", f.err)
		}
		// A state the provider never saw is rejected the same way.
		resp, err := h.view.Get(context.Background(), h.rp.URL+"/callback?state=forged&code=x")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("forged: %d", resp.StatusCode)
		}
		if len(h.completions()) != 1 {
			t.Fatalf("completions %d", len(h.completions()))
		}
	})

	t.Run("StateExpired", func(t *testing.T) {
		t.Parallel()
		store := webauth.NewMemoryStore(webauth.WithMemoryClock(clock.NewFake(time.Now())))
		h := newHarness(t, func(cfg *webauth.Config) { cfg.StateStore = store; cfg.StateTTL = 2 * time.Minute })
		cb := h.callbackURL("/begin?serial=S1")
		h.clock.Advance(2*time.Minute + time.Second)
		resp, err := h.view.Get(context.Background(), cb)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expired: %d", resp.StatusCode)
		}
		if f := h.lastFailure(); !errors.Is(f.err, webauth.ErrStateExpired) {
			t.Fatalf("error %v", f.err)
		}
		if store.Len() != 0 {
			t.Fatalf("expired state kept: %d", store.Len())
		}
		// Within the TTL the same flow succeeds.
		cb = h.callbackURL("/begin?serial=S2")
		h.clock.Advance(time.Minute)
		resp, err = h.view.Get(context.Background(), cb)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("in time: %d", resp.StatusCode)
		}
	})

	t.Run("StateBoundToSerial", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(cfg *webauth.Config) {
			cfg.Authorizer = func(_ context.Context, bound webauth.Bound, claims webauth.Claims) (webauth.Decision, error) {
				return webauth.Decision{Profile: "p-" + bound.Serial, Attributes: map[string]string{"sub": claims.Subject}}, nil
			}
		})
		cbA := h.callbackURL("/begin?serial=A&udid=UA")
		cbB := h.callbackURL("/begin?serial=B&udid=UB")
		for _, cb := range []string{cbB, cbA} {
			resp, err := h.view.Get(context.Background(), cb)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: %d", cb, resp.StatusCode)
			}
		}
		c := h.completions()
		if len(c) != 2 || c[0].bound.Serial != "B" || c[0].bound.UDID != "UB" || c[1].bound.Serial != "A" || c[1].bound.UDID != "UA" {
			t.Fatalf("completions %+v", c)
		}
		if c[0].decision.Profile != "p-B" || c[1].decision.Profile != "p-A" || c[0].decision.Attributes["sub"] != "user-1" {
			t.Fatalf("decisions %+v %+v", c[0].decision, c[1].decision)
		}
		// Swapping the state between two callbacks binds the wrong code to
		// the state's verifier, so the exchange fails at the provider.
		cbC := h.callbackURL("/begin?serial=C")
		cbD := h.callbackURL("/begin?serial=D")
		uc, _ := url.Parse(cbC)
		ud, _ := url.Parse(cbD)
		q := uc.Query()
		q.Set("code", ud.Query().Get("code"))
		uc.RawQuery = q.Encode()
		resp, err := h.view.Get(context.Background(), uc.String())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("swapped code: %d", resp.StatusCode)
		}
		if len(h.completions()) != 2 {
			t.Fatal("swapped code completed")
		}
	})

	t.Run("WrongNonce", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.idp.Set(func(o *webauthtest.Options) { o.BadNonce = true })
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusBadRequest {
			t.Fatalf("status %d", status)
		}
		if f := h.lastFailure(); !errors.Is(f.err, webauth.ErrIDToken) || !strings.Contains(f.err.Error(), "nonce") {
			t.Fatalf("error %v", f.err)
		}
		if len(h.completions()) != 0 {
			t.Fatal("completed with a bad nonce")
		}
	})

	t.Run("AccessDeniedIs403", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.idp.Set(func(o *webauthtest.Options) { o.AuthorizeError = "access_denied" })
		status, _ := h.get("/begin?serial=S1")
		if status != http.StatusForbidden {
			t.Fatalf("status %d", status)
		}
		if f := h.lastFailure(); !errors.Is(f.err, webauth.ErrAccessDenied) || f.status != http.StatusForbidden {
			t.Fatalf("failure %+v", f)
		}
		// The state was consumed by the denial.
		last := h.view.Hops[len(h.view.Hops)-1]
		resp, err := h.view.Get(context.Background(), last)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("replayed denial: %d", resp.StatusCode)
		}
	})

	t.Run("IdPErrorIs502", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.idp.Set(func(o *webauthtest.Options) { o.AuthorizeError = "server_error" })
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("authorize error: %d", status)
		}
		if f := h.lastFailure(); !errors.Is(f.err, webauth.ErrProvider) || errors.Is(f.err, webauth.ErrAccessDenied) {
			t.Fatalf("failure %+v", f)
		}
		h.idp.Set(func(o *webauthtest.Options) { o.AuthorizeError = ""; o.TokenStatus = http.StatusInternalServerError })
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("token error: %d", status)
		}
		if f := h.lastFailure(); !strings.Contains(f.err.Error(), "server_error") {
			t.Fatalf("token failure %v", f.err)
		}
		h.idp.Set(func(o *webauthtest.Options) { o.TokenStatus = 0; o.OmitIDToken = true })
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("no id_token: %d", status)
		}
		if len(h.completions()) != 0 {
			t.Fatal("completed after provider errors")
		}
	})

	t.Run("HTTPRedirectRejected", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.idp.Set(func(o *webauthtest.Options) { o.InsecureEndpoints = true })
		status, _ := h.get("/begin?serial=S1")
		if status != http.StatusInternalServerError {
			t.Fatalf("discovery with http endpoints: %d", status)
		}
		if f := h.lastFailure(); !errors.Is(f.err, webauth.ErrNotHTTPS) {
			t.Fatalf("failure %v", f.err)
		}
		if len(h.idp.Authorizes()) != 0 {
			t.Fatal("the web view was sent to the http endpoint")
		}
		base := webauth.Config{Issuer: "https://idp.example.com", ClientID: "c", RedirectURL: "https://mdm.example.com/cb", Complete: func(context.Context, webauth.Bound, webauth.Claims, webauth.Decision, http.ResponseWriter, *http.Request) {
		}}
		for name, mutate := range map[string]func(*webauth.Config){
			"httpRedirect": func(c *webauth.Config) { c.RedirectURL = "http://mdm.example.com/cb" },
			"relative":     func(c *webauth.Config) { c.RedirectURL = "/cb" },
			"httpIssuer":   func(c *webauth.Config) { c.Issuer = "http://idp.example.com" },
			"httpAuthorize": func(c *webauth.Config) {
				c.Endpoints = webauth.Endpoints{Authorization: "http://idp.example.com/a", Token: "https://idp.example.com/t", JWKS: "https://idp.example.com/j"}
			},
			"httpToken": func(c *webauth.Config) {
				c.Endpoints = webauth.Endpoints{Authorization: "https://idp.example.com/a", Token: "http://idp.example.com/t", JWKS: "https://idp.example.com/j"}
			},
			"httpJWKS": func(c *webauth.Config) {
				c.Endpoints = webauth.Endpoints{Authorization: "https://idp.example.com/a", Token: "https://idp.example.com/t", JWKS: "http://idp.example.com/j"}
			},
			"badURL": func(c *webauth.Config) { c.RedirectURL = "https://exa mple.com/%zz" },
		} {
			cfg := base
			mutate(&cfg)
			if _, err := webauth.New(cfg); !errors.Is(err, webauth.ErrNotHTTPS) || !errors.Is(err, webauth.ErrConfig) {
				t.Fatalf("%s: err %v", name, err)
			}
		}
		insecure := base
		insecure.RedirectURL, insecure.Issuer, insecure.AllowInsecureForTests = "http://mdm.example.com/cb", "http://idp.example.com", true
		if _, err := webauth.New(insecure); err != nil {
			t.Fatalf("AllowInsecureForTests: %v", err)
		}
	})

	t.Run("ErrorDescriptionParsed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.idp.Set(func(o *webauthtest.Options) {
			o.AuthorizeError, o.AuthorizeErrorDescription, o.AuthorizeErrorURI = "access_denied", "User is not in the enrollment group", "https://idp.example.com/help/denied"
		})
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusForbidden {
			t.Fatalf("status %d", status)
		}
		f := h.lastFailure()
		msg := f.err.Error()
		if !strings.Contains(msg, "access_denied") || !strings.Contains(msg, "User is not in the enrollment group") || !strings.Contains(msg, "https://idp.example.com/help/denied") {
			t.Fatalf("error %q", msg)
		}
		h.idp.Set(func(o *webauthtest.Options) { o.AuthorizeError = "temporarily_unavailable"; o.AuthorizeErrorURI = "" })
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("status %d", status)
		}
		if msg := h.lastFailure().err.Error(); !strings.Contains(msg, "temporarily_unavailable: User is not in the enrollment group") {
			t.Fatalf("error %q", msg)
		}
	})

	t.Run("IDTokenChecks", func(t *testing.T) {
		t.Parallel()
		for name, set := range map[string]func(*webauthtest.Options){
			"Expired":       func(o *webauthtest.Options) { o.Expired = true },
			"WrongAudience": func(o *webauthtest.Options) { o.WrongAudience = true },
			"UnknownKID":    func(o *webauthtest.Options) { o.UnknownKID = true },
			"UnknownKIDRSA": func(o *webauthtest.Options) { o.UnknownKID = true; o.Alg = webauthtest.AlgRS256 },
		} {
			h := newHarness(t, nil)
			h.idp.Set(set)
			if status, _ := h.get("/begin?serial=S1"); status != http.StatusBadRequest {
				t.Fatalf("%s: status %d", name, status)
			}
			if f := h.lastFailure(); !errors.Is(f.err, webauth.ErrIDToken) {
				t.Fatalf("%s: error %v", name, f.err)
			}
		}
	})

	t.Run("RS256", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.idp.Set(func(o *webauthtest.Options) { o.Alg = webauthtest.AlgRS256 })
		if status, body := h.get("/begin?serial=S1"); status != http.StatusOK || body != "profile for S1 as user-1" {
			t.Fatalf("status %d body %q", status, body)
		}
	})

	t.Run("KeyRotationRefreshesJWKS", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusOK {
			t.Fatalf("before rotation: %d", status)
		}
		if err := h.idp.RotateKeys(); err != nil {
			t.Fatal(err)
		}
		// Too soon after the last fetch the unknown kid is not refetched.
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusBadRequest {
			t.Fatalf("rotated, within the refresh window: %d", status)
		}
		h.clock.Advance(2 * time.Minute)
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusOK {
			t.Fatalf("rotated, after the refresh window: %d", status)
		}
	})

	t.Run("ClientSecret", func(t *testing.T) {
		t.Parallel()
		for name, auth := range map[string]webauth.ClientAuth{"Basic": webauth.ClientAuthBasic, "Post": webauth.ClientAuthPost} {
			h := newHarness(t, func(cfg *webauth.Config) { cfg.ClientSecret = "s3cret/with=chars"; cfg.ClientAuth = auth })
			h.idp.Set(func(o *webauthtest.Options) { o.ClientSecret = "s3cret/with=chars" })
			if status, _ := h.get("/begin?serial=S1"); status != http.StatusOK {
				t.Fatalf("%s: status %d", name, status)
			}
			tk := h.idp.Tokens()[0]
			if tk.ClientID != "enroll-client" || tk.ClientSecret != "s3cret/with=chars" || tk.BasicAuth != (auth == webauth.ClientAuthBasic) {
				t.Fatalf("%s: token %+v", name, tk)
			}
		}
		// A wrong secret is refused by the provider and reported as 502.
		h := newHarness(t, func(cfg *webauth.Config) { cfg.ClientSecret = "wrong" })
		h.idp.Set(func(o *webauthtest.Options) { o.ClientSecret = "right" })
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("wrong secret: %d", status)
		}
		if msg := h.lastFailure().err.Error(); !strings.Contains(msg, "invalid_client") {
			t.Fatalf("error %q", msg)
		}
	})

	t.Run("ExplicitEndpoints", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(cfg *webauth.Config) { cfg.Endpoints = webauth.Endpoints{} })
		h.idp.Set(func(o *webauthtest.Options) { o.InsecureEndpoints = true })
		explicit := newHarnessWith(t, h.idp, func(cfg *webauth.Config) { cfg.Endpoints = h.idp.Endpoints() })
		if status, _ := explicit.get("/begin?serial=S1"); status != http.StatusOK {
			t.Fatalf("explicit endpoints skip discovery: %d", status)
		}
	})

	t.Run("Authorizer", func(t *testing.T) {
		t.Parallel()
		denied := newHarness(t, func(cfg *webauth.Config) {
			cfg.Authorizer = func(_ context.Context, _ webauth.Bound, claims webauth.Claims) (webauth.Decision, error) {
				return webauth.Decision{}, fmt.Errorf("%w: %s not in group", webauth.ErrDenied, claims.Subject)
			}
		})
		if status, _ := denied.get("/begin?serial=S1"); status != http.StatusForbidden {
			t.Fatalf("denied: %d", status)
		}
		if f := denied.lastFailure(); !errors.Is(f.err, webauth.ErrDenied) {
			t.Fatalf("error %v", f.err)
		}
		failing := newHarness(t, func(cfg *webauth.Config) {
			cfg.Authorizer = func(context.Context, webauth.Bound, webauth.Claims) (webauth.Decision, error) {
				return webauth.Decision{}, errBoom
			}
		})
		if status, _ := failing.get("/begin?serial=S1"); status != http.StatusInternalServerError {
			t.Fatalf("failing: %d", status)
		}
	})

	t.Run("CallbackRequestErrors", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		for name, path := range map[string]string{"noState": "/callback?code=x", "empty": "/callback"} {
			if status, _ := h.get(path); status != http.StatusBadRequest {
				t.Fatalf("%s: %d", name, status)
			}
		}
		cb := h.callbackURL("/begin?serial=S1")
		u, _ := url.Parse(cb)
		q := u.Query()
		q.Del("code")
		u.RawQuery = q.Encode()
		resp, err := h.view.Get(context.Background(), u.String())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("no code: %d", resp.StatusCode)
		}
		if msg := h.lastFailure().err.Error(); !strings.Contains(msg, "code missing") {
			t.Fatalf("error %q", msg)
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, h.rp.URL+"/callback", nil)
		post, err := h.rp.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		post.Body.Close()
		if post.StatusCode != http.StatusMethodNotAllowed || post.Header.Get("Allow") != "GET, HEAD" {
			t.Fatalf("POST: %d %q", post.StatusCode, post.Header.Get("Allow"))
		}
	})

	t.Run("StoreFailure", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(cfg *webauth.Config) { cfg.StateStore = failingStore{} })
		if status, _ := h.get("/begin?serial=S1"); status != http.StatusInternalServerError {
			t.Fatalf("Put failure: %d", status)
		}
		if f := h.lastFailure(); !errors.Is(f.err, errBoom) {
			t.Fatalf("error %v", f.err)
		}
		if status, _ := h.get("/callback?state=x&code=y"); status != http.StatusInternalServerError {
			t.Fatalf("Take failure: %d", status)
		}
	})

	t.Run("DiscoveryFailures", func(t *testing.T) {
		t.Parallel()
		mismatch := newHarness(t, func(cfg *webauth.Config) { cfg.Issuer += "/" })
		if status, _ := mismatch.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("issuer mismatch: %d", status)
		}
		if msg := mismatch.lastFailure().err.Error(); !strings.Contains(msg, "issuer") {
			t.Fatalf("error %q", msg)
		}
		missing := newHarness(t, func(cfg *webauth.Config) { cfg.Issuer += "/tenant" })
		if status, _ := missing.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("404 discovery: %d", status)
		}
		small := newHarness(t, func(cfg *webauth.Config) { cfg.MaxResponseBytes = 64 })
		if status, _ := small.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("oversized discovery: %d", status)
		}
		if msg := small.lastFailure().err.Error(); !strings.Contains(msg, "larger than 64") {
			t.Fatalf("error %q", msg)
		}
		unreachable := newHarness(t, func(cfg *webauth.Config) { cfg.Issuer = "https://127.0.0.1:1" })
		if status, _ := unreachable.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("unreachable: %d", status)
		}
		garbage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{not json")) }))
		t.Cleanup(garbage.Close)
		bad := newHarness(t, func(cfg *webauth.Config) { cfg.Issuer = garbage.URL; cfg.HTTPClient = garbage.Client() })
		if status, _ := bad.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("garbage: %d", status)
		}
	})

	t.Run("JWKSFailures", func(t *testing.T) {
		t.Parallel()
		// A JWKS endpoint that is down after discovery worked.
		h := newHarness(t, nil)
		eps := h.idp.Endpoints()
		eps.JWKS = h.idp.Issuer() + "/missing"
		broken := newHarnessWith(t, h.idp, func(cfg *webauth.Config) { cfg.Endpoints = eps })
		if status, _ := broken.get("/begin?serial=S1"); status != http.StatusBadRequest {
			t.Fatalf("jwks 404: %d", status)
		}
		if msg := broken.lastFailure().err.Error(); !strings.Contains(msg, "no key for kid") {
			t.Fatalf("error %q", msg)
		}
		// Malformed keys after rotation: refresh fails and the token is
		// rejected rather than trusted.
		bad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"keys":[{"kty":"EC","crv":"P-256","x":"!!","y":"!!"}]}`))
		}))
		t.Cleanup(bad.Close)
		eps.JWKS = bad.URL
		client := &http.Client{Transport: trustBoth(h.idp, bad)}
		malformed := newHarnessWith(t, h.idp, func(cfg *webauth.Config) { cfg.Endpoints = eps; cfg.HTTPClient = client })
		if status, _ := malformed.get("/begin?serial=S1"); status != http.StatusBadRequest {
			t.Fatalf("malformed jwks: %d", status)
		}
	})

	t.Run("TokenEndpointFailures", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		garbage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }))
		t.Cleanup(garbage.Close)
		eps := h.idp.Endpoints()
		eps.Token = garbage.URL
		client := &http.Client{Transport: trustBoth(h.idp, garbage)}
		bad := newHarnessWith(t, h.idp, func(cfg *webauth.Config) { cfg.Endpoints = eps; cfg.HTTPClient = client })
		if status, _ := bad.get("/begin?serial=S1"); status != http.StatusBadGateway {
			t.Fatalf("garbage token response: %d", status)
		}
		if msg := bad.lastFailure().err.Error(); !strings.Contains(msg, "token response") {
			t.Fatalf("error %q", msg)
		}
	})

	t.Run("DefaultClient", func(t *testing.T) {
		t.Parallel()
		// Without an HTTPClient the flow builds one that refuses redirects
		// off https (or http when insecure is allowed) and stops after five.
		loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/ftp/.well-known/openid-configuration":
				http.Redirect(w, r, "ftp://example.com/x", http.StatusFound)
			default:
				http.Redirect(w, r, r.URL.Path, http.StatusFound)
			}
		}))
		t.Cleanup(loop.Close)
		for name, issuer := range map[string]string{"scheme": loop.URL + "/ftp", "loop": loop.URL + "/loop"} {
			flow, err := webauth.New(webauth.Config{
				Issuer: issuer, ClientID: "c", RedirectURL: "http://rp.example.com/cb", AllowInsecureForTests: true, Logger: quiet(),
				Complete: func(context.Context, webauth.Bound, webauth.Claims, webauth.Decision, http.ResponseWriter, *http.Request) {
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			err = flow.Begin(rec, httptest.NewRequest(http.MethodGet, "/begin", nil), webauth.Bound{})
			if rec.Code != http.StatusBadGateway || !errors.Is(err, webauth.ErrProvider) {
				t.Fatalf("%s: status %d err %v", name, rec.Code, err)
			}
		}
	})

	t.Run("DefaultErrorWriter", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(cfg *webauth.Config) { cfg.OnError = nil; cfg.Logger = nil })
		h.idp.Set(func(o *webauthtest.Options) { o.AuthorizeError = "access_denied" })
		status, body := h.get("/begin?serial=S1")
		if status != http.StatusForbidden || !strings.Contains(body, "not allowed") {
			t.Fatalf("403: %d %q", status, body)
		}
		h.idp.Set(func(o *webauthtest.Options) { o.AuthorizeError = "server_error" })
		if status, body := h.get("/begin?serial=S1"); status != http.StatusBadGateway || !strings.Contains(body, "identity provider") {
			t.Fatalf("502: %d %q", status, body)
		}
		if status, body := h.get("/callback?state=nope"); status != http.StatusBadRequest || !strings.Contains(body, "expired") {
			t.Fatalf("400: %d %q", status, body)
		}
		rec := httptest.NewRecorder()
		_ = h.flow.Begin(rec, httptest.NewRequest(http.MethodGet, "/begin", nil), webauth.Bound{})
		if rec.Code != http.StatusFound {
			t.Fatalf("Begin: %d", rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control %q", got)
		}
		failing := newHarness(t, func(cfg *webauth.Config) { cfg.OnError = nil; cfg.StateStore = failingStore{} })
		if status, body := failing.get("/begin?serial=S1"); status != http.StatusInternalServerError || !strings.Contains(body, "could not continue") {
			t.Fatalf("500: %d %q", status, body)
		}
	})
}

func TestNew(t *testing.T) {
	t.Parallel()
	complete := func(context.Context, webauth.Bound, webauth.Claims, webauth.Decision, http.ResponseWriter, *http.Request) {
	}
	base := webauth.Config{Issuer: "https://idp.example.com", ClientID: "c", RedirectURL: "https://mdm.example.com/cb", Complete: complete}
	if _, err := webauth.New(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*webauth.Config){
		"noClientID":  func(c *webauth.Config) { c.ClientID = "" },
		"noComplete":  func(c *webauth.Config) { c.Complete = nil },
		"noIssuer":    func(c *webauth.Config) { c.Issuer = "" },
		"ttlTooLong":  func(c *webauth.Config) { c.StateTTL = 9 * time.Minute },
		"ttlNegative": func(c *webauth.Config) { c.StateTTL = -time.Second },
	} {
		cfg := base
		mutate(&cfg)
		if _, err := webauth.New(cfg); !errors.Is(err, webauth.ErrConfig) {
			t.Fatalf("%s: err %v", name, err)
		}
	}
	cfg := base
	cfg.StateTTL = webauth.MaxStateTTL
	if _, err := webauth.New(cfg); err != nil {
		t.Fatalf("max ttl: %v", err)
	}
}

func TestMemoryStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := clock.NewFake(time.Unix(1_700_000_000, 0))
	store := webauth.NewMemoryStore(webauth.WithMemoryClock(fake), webauth.WithMemoryMaxEntries(2), webauth.WithMemorySweepInterval(time.Minute))
	if err := store.Put(ctx, "", webauth.State{}); !errors.Is(err, webauth.ErrStateKey) {
		t.Fatalf("empty key: %v", err)
	}
	st := webauth.State{Bound: webauth.Bound{Serial: "S"}, Verifier: "v", Nonce: "n", ExpiresAt: fake.Now().Add(time.Minute)}
	if err := store.Put(ctx, "a", st); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "a", st); !errors.Is(err, webauth.ErrStateExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := store.Put(ctx, "b", st); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "c", st); !errors.Is(err, webauth.ErrStoreFull) {
		t.Fatalf("full: %v", err)
	}
	got, err := store.Take(ctx, "a")
	if err != nil || got.Bound.Serial != "S" || got.Verifier != "v" || got.Nonce != "n" {
		t.Fatalf("take: %+v %v", got, err)
	}
	if _, err := store.Take(ctx, "a"); !errors.Is(err, webauth.ErrStateNotFound) {
		t.Fatalf("second take: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("len %d", store.Len())
	}
	// Once b has expired the cap no longer blocks c.
	fake.Advance(2 * time.Minute)
	if err := store.Put(ctx, "c", webauth.State{ExpiresAt: fake.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if err := store.Put(ctx, "d", webauth.State{ExpiresAt: fake.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("d: %v", err)
	}
	if _, err := store.Take(ctx, "b"); !errors.Is(err, webauth.ErrStateNotFound) {
		t.Fatalf("expired b: %v", err)
	}
	fake.Advance(2 * time.Minute)
	if n := store.Sweep(); n != 2 || store.Len() != 0 {
		t.Fatalf("sweep %d len %d", n, store.Len())
	}
	defaults := webauth.NewMemoryStore()
	if err := defaults.Put(ctx, "x", webauth.State{ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
}

// failingStore fails every operation.
type failingStore struct{}

func (failingStore) Put(context.Context, string, webauth.State) error { return errBoom }
func (failingStore) Take(context.Context, string) (webauth.State, error) {
	return webauth.State{}, errBoom
}

// trustBoth returns a transport trusting the provider and another test
// server.
func trustBoth(p *webauthtest.Provider, other *httptest.Server) http.RoundTripper {
	tr := other.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.RootCAs.AddCert(p.Certificate())
	return tr
}
