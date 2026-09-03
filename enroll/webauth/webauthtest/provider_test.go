package webauthtest_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/enroll/webauth/webauthtest"
)

func getJSON(t *testing.T, client *http.Client, rawURL string) map[string]any {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.UnmarshalRead(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func s256(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorize drives /authorize by hand and returns the redirect's query.
func authorize(t *testing.T, p *webauthtest.Provider, params url.Values) (int, url.Values) {
	t.Helper()
	client := p.HTTPClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, p.Server.URL+"/authorize?"+params.Encode(), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return resp.StatusCode, nil
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, loc.Query()
}

func token(t *testing.T, p *webauthtest.Provider, form url.Values, basic [2]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, p.Server.URL+"/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic[0] != "" {
		req.SetBasicAuth(url.QueryEscape(basic[0]), url.QueryEscape(basic[1]))
	}
	resp, err := p.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.UnmarshalRead(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

func TestProvider(t *testing.T) {
	t.Parallel()

	t.Run("Discovery", func(t *testing.T) {
		t.Parallel()
		p := webauthtest.New(t)
		doc := getJSON(t, p.HTTPClient(), p.Issuer()+"/.well-known/openid-configuration")
		eps := p.Endpoints()
		if doc["issuer"] != p.Issuer() || doc["authorization_endpoint"] != eps.Authorization || doc["token_endpoint"] != eps.Token || doc["jwks_uri"] != eps.JWKS {
			t.Fatalf("doc %v", doc)
		}
		p.Set(func(o *webauthtest.Options) { o.InsecureEndpoints = true })
		doc = getJSON(t, p.HTTPClient(), p.Issuer()+"/.well-known/openid-configuration")
		if ae, _ := doc["authorization_endpoint"].(string); !strings.HasPrefix(ae, "http://") {
			t.Fatalf("insecure endpoints: %v", doc)
		}
	})

	t.Run("JWKS", func(t *testing.T) {
		t.Parallel()
		p := webauthtest.New(t)
		set := getJSON(t, p.HTTPClient(), p.Endpoints().JWKS)
		keys, _ := set["keys"].([]any)
		if len(keys) != 2 {
			t.Fatalf("keys %v", set)
		}
		before := keys[0].(map[string]any)["kid"]
		if err := p.RotateKeys(); err != nil {
			t.Fatal(err)
		}
		set = getJSON(t, p.HTTPClient(), p.Endpoints().JWKS)
		if after := set["keys"].([]any)[0].(map[string]any)["kid"]; after == before || after != "es256-2" {
			t.Fatalf("rotation: %v -> %v", before, after)
		}
	})

	t.Run("AuthorizeRecords", func(t *testing.T) {
		t.Parallel()
		p := webauthtest.New(t)
		p.Set(func(o *webauthtest.Options) { o.RedirectURI = "https://rp.example.com/cb" })
		params := url.Values{
			"response_type": {"code"}, "client_id": {"enroll-client"}, "redirect_uri": {"https://rp.example.com/cb"},
			"scope": {"openid email"}, "state": {"st"}, "nonce": {"nn"}, "code_challenge": {s256("verifier")},
			"code_challenge_method": {"S256"}, "login_hint": {"user@example.com"},
		}
		status, q := authorize(t, p, params)
		if status != http.StatusFound || q.Get("code") == "" || q.Get("state") != "st" || q.Get("error") != "" {
			t.Fatalf("status %d query %v", status, q)
		}
		rec := p.Authorizes()
		if len(rec) != 1 || rec[0].State != "st" || rec[0].Nonce != "nn" || rec[0].CodeChallenge != s256("verifier") || rec[0].LoginHint != "user@example.com" || rec[0].Scope != "openid email" {
			t.Fatalf("recorded %+v", rec)
		}
		// Rejections.
		bad := func(mutate func(url.Values)) (int, url.Values) {
			v := url.Values{}
			for k, vs := range params {
				v[k] = vs
			}
			mutate(v)
			return authorize(t, p, v)
		}
		if status, _ := bad(func(v url.Values) { v.Set("client_id", "other") }); status != http.StatusBadRequest {
			t.Fatalf("unknown client: %d", status)
		}
		if status, _ := bad(func(v url.Values) { v.Set("redirect_uri", "https://evil.example.com/") }); status != http.StatusBadRequest {
			t.Fatalf("unregistered redirect: %d", status)
		}
		if status, _ := bad(func(v url.Values) { v.Set("redirect_uri", "://bad") }); status != http.StatusBadRequest {
			t.Fatalf("unparseable redirect: %d", status)
		}
		p.Set(func(o *webauthtest.Options) { o.RedirectURI = "" })
		if _, q := bad(func(v url.Values) { v.Set("response_type", "token") }); q.Get("error") != "unsupported_response_type" {
			t.Fatalf("response_type: %v", q)
		}
		if _, q := bad(func(v url.Values) { v.Set("code_challenge_method", "plain") }); q.Get("error") != "invalid_request" {
			t.Fatalf("pkce: %v", q)
		}
	})

	t.Run("AuthorizeError", func(t *testing.T) {
		t.Parallel()
		p := webauthtest.New(t)
		p.Set(func(o *webauthtest.Options) {
			o.AuthorizeError, o.AuthorizeErrorDescription, o.AuthorizeErrorURI = "access_denied", "nope", "https://idp.example.com/help"
		})
		_, q := authorize(t, p, url.Values{"response_type": {"code"}, "client_id": {"enroll-client"}, "redirect_uri": {"https://rp.example.com/cb"}, "state": {"s"}})
		if q.Get("error") != "access_denied" || q.Get("error_description") != "nope" || q.Get("error_uri") != "https://idp.example.com/help" || q.Get("state") != "s" || q.Get("code") != "" {
			t.Fatalf("query %v", q)
		}
	})

	t.Run("Token", func(t *testing.T) {
		t.Parallel()
		p := webauthtest.New(t)
		mint := func() string {
			_, q := authorize(t, p, url.Values{"response_type": {"code"}, "client_id": {"enroll-client"}, "redirect_uri": {"https://rp.example.com/cb"}, "state": {"s"}, "nonce": {"n"}, "code_challenge": {s256("verifier")}, "code_challenge_method": {"S256"}})
			return q.Get("code")
		}
		form := func(code, verifier string) url.Values {
			return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://rp.example.com/cb"}, "code_verifier": {verifier}, "client_id": {"enroll-client"}}
		}
		code := mint()
		status, body := token(t, p, form(code, "verifier"), [2]string{})
		if status != http.StatusOK || body["id_token"] == "" || body["token_type"] != "Bearer" {
			t.Fatalf("status %d body %v", status, body)
		}
		if tk := p.Tokens(); len(tk) != 1 || tk[0].CodeVerifier != "verifier" || tk[0].BasicAuth {
			t.Fatalf("recorded %+v", tk)
		}
		// The code is single use.
		if status, body := token(t, p, form(code, "verifier"), [2]string{}); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
			t.Fatalf("replay: %d %v", status, body)
		}
		if status, body := token(t, p, form(mint(), "wrong"), [2]string{}); status != http.StatusBadRequest || !strings.Contains(body["error_description"].(string), "code_verifier") {
			t.Fatalf("verifier: %d %v", status, body)
		}
		f := form(mint(), "verifier")
		f.Set("redirect_uri", "https://rp.example.com/other")
		if status, body := token(t, p, f, [2]string{}); status != http.StatusBadRequest || !strings.Contains(body["error_description"].(string), "redirect_uri") {
			t.Fatalf("redirect: %d %v", status, body)
		}
		f = form(mint(), "verifier")
		f.Set("grant_type", "refresh_token")
		if status, body := token(t, p, f, [2]string{}); status != http.StatusBadRequest || body["error"] != "unsupported_grant_type" {
			t.Fatalf("grant: %d %v", status, body)
		}
		f = form(mint(), "verifier")
		f.Set("client_id", "other")
		if status, body := token(t, p, f, [2]string{}); status != http.StatusUnauthorized || body["error"] != "invalid_client" {
			t.Fatalf("client: %d %v", status, body)
		}
		// Client secrets by basic and by post.
		p.Set(func(o *webauthtest.Options) { o.ClientSecret = "s/s" })
		if status, _ := token(t, p, form(mint(), "verifier"), [2]string{"enroll-client", "s/s"}); status != http.StatusOK {
			t.Fatalf("basic: %d", status)
		}
		if tk := p.Tokens(); !tk[len(tk)-1].BasicAuth || tk[len(tk)-1].ClientSecret != "s/s" {
			t.Fatalf("basic recorded %+v", tk[len(tk)-1])
		}
		f = form(mint(), "verifier")
		f.Set("client_secret", "s/s")
		if status, _ := token(t, p, f, [2]string{}); status != http.StatusOK {
			t.Fatalf("post: %d", status)
		}
		if status, _ := token(t, p, form(mint(), "verifier"), [2]string{"enroll-client", "wrong"}); status != http.StatusUnauthorized {
			t.Fatalf("wrong secret: %d", status)
		}
		p.Set(func(o *webauthtest.Options) { o.ClientSecret = ""; o.TokenStatus = http.StatusServiceUnavailable })
		if status, body := token(t, p, form(mint(), "verifier"), [2]string{}); status != http.StatusServiceUnavailable || body["error"] != "server_error" {
			t.Fatalf("scripted: %d %v", status, body)
		}
		p.Set(func(o *webauthtest.Options) { o.TokenStatus = 0; o.OmitIDToken = true })
		if status, body := token(t, p, form(mint(), "verifier"), [2]string{}); status != http.StatusOK || body["id_token"] != nil {
			t.Fatalf("omitted: %d %v", status, body)
		}
		p.Set(func(o *webauthtest.Options) { o.OmitIDToken = false; o.Alg = "HS256" })
		if status, _ := token(t, p, form(mint(), "verifier"), [2]string{}); status != http.StatusInternalServerError {
			t.Fatalf("bad alg: %d", status)
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, p.Server.URL+"/token", strings.NewReader("%zz"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := p.HTTPClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad form: %d", resp.StatusCode)
		}
	})

	t.Run("SignIDToken", func(t *testing.T) {
		t.Parallel()
		p := webauthtest.New(t)
		for _, alg := range []string{webauthtest.AlgES256, webauthtest.AlgRS256} {
			p.Set(func(o *webauthtest.Options) { o.Alg = alg })
			raw, err := p.SignIDToken(map[string]any{"sub": "x"})
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(raw, ".")
			if len(parts) != 3 {
				t.Fatalf("%s: %q", alg, raw)
			}
			hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
			if !strings.Contains(string(hdr), alg) {
				t.Fatalf("%s: header %s", alg, hdr)
			}
		}
		p.Set(func(o *webauthtest.Options) { o.UnknownKID = true })
		raw, _ := p.SignIDToken(map[string]any{"sub": "x"})
		hdr, _ := base64.RawURLEncoding.DecodeString(strings.Split(raw, ".")[0])
		if !strings.Contains(string(hdr), "unknown-kid") {
			t.Fatalf("header %s", hdr)
		}
		p.Set(func(o *webauthtest.Options) { o.Alg = "none" })
		if _, err := p.SignIDToken(nil); !errors.Is(err, webauthtest.ErrProvider) {
			t.Fatalf("alg none: %v", err)
		}
		p.Set(func(o *webauthtest.Options) { o.Alg = webauthtest.AlgES256 })
		if _, err := p.SignIDToken(map[string]any{"bad": make(chan int)}); !errors.Is(err, webauthtest.ErrProvider) {
			t.Fatalf("unmarshalable claims: %v", err)
		}
	})

	t.Run("Client", func(t *testing.T) {
		t.Parallel()
		p := webauthtest.New(t)
		rp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/start":
				http.Redirect(w, r, p.Server.URL+"/authorize?response_type=code&client_id=enroll-client&redirect_uri="+url.QueryEscape(rp(r)+"/cb")+"&state=s&code_challenge=c&code_challenge_method=S256", http.StatusFound)
			case "/cb":
				w.Header().Set("Content-Type", "application/x-apple-aspen-config")
				_, _ = io.WriteString(w, "profile "+r.URL.Query().Get("code"))
			case "/loop":
				http.Redirect(w, r, "/loop", http.StatusFound)
			case "/handback":
				http.Redirect(w, r, "apple-remotemanagement-user-login://authentication-results?access-token=abc", http.StatusPermanentRedirect)
			case "/relative":
				http.Redirect(w, r, "/cb?code=rel", http.StatusSeeOther)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(rp.Close)
		view := p.Client(rp.Certificate())
		resp, err := view.Get(context.Background(), rp.URL+"/start")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "profile ") || len(view.Hops) != 3 {
			t.Fatalf("status %d body %q hops %v", resp.StatusCode, body, view.Hops)
		}
		resp, err = view.Get(context.Background(), rp.URL+"/loop")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || len(view.Hops) != view.MaxHops+1 {
			t.Fatalf("loop: %d after %d hops", resp.StatusCode, len(view.Hops))
		}
		resp, err = view.Get(context.Background(), rp.URL+"/handback")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusPermanentRedirect || !strings.HasPrefix(resp.Header.Get("Location"), "apple-remotemanagement-user-login://") {
			t.Fatalf("handback: %d %q", resp.StatusCode, resp.Header.Get("Location"))
		}
		resp, err = view.Get(context.Background(), rp.URL+"/relative")
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "profile rel" {
			t.Fatalf("relative: %q", body)
		}
		if _, err := view.Get(context.Background(), "::bad"); err == nil {
			t.Fatal("bad URL accepted")
		}
		if _, err := view.Get(context.Background(), "https://127.0.0.1:1/"); err == nil {
			t.Fatal("unreachable accepted")
		}
	})
}

// rp reconstructs the test server's own base URL from a request.
func rp(r *http.Request) string { return "https://" + r.Host }
