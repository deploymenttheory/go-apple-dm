package accountdriven_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/enroll"
	"github.com/deploymenttheory/go-apple-dm/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/internal/testpki"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/profile"
	"github.com/deploymenttheory/go-apple-dm/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/service"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newTokens(c *fakeClock) *accountdriven.Tokens {
	return &accountdriven.Tokens{Store: accountdriven.NewMemStore(), Now: c.Now}
}

var alice = accountdriven.Identity{UserIdentifier: "alice@example.com", ManagedAppleAccount: "alice@example.com", Subject: "sub-1"}

// parseBody is a stand-in for enroll/ade's verified parser.
func parseBody(r *http.Request) (*accountdriven.DeviceInfo, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(b, []byte("PRODUCT")) {
		return nil, errors.New("not a device info body")
	}
	return &accountdriven.DeviceInfo{Language: "en", Product: "iPhone17,2", Version: "23A300", Raw: b}, nil
}

func baseProfile(_ context.Context, _ accountdriven.Identity, _ *accountdriven.DeviceInfo) (*enroll.Profile, error) {
	return &enroll.Profile{Identifier: "com.example.mdm", Topic: "com.apple.mgmt.t", ServerURL: "https://mdm.example/mdm", CheckInURL: "https://mdm.example/mdm",
		SCEP: &enroll.SCEP{URL: "https://mdm.example/scep"}}, nil
}

type fixture struct {
	clock  *fakeClock
	tokens *accountdriven.Tokens
	asweb  *accountdriven.AppleAsWeb
	oauth  *accountdriven.OAuth2
	srv    *httptest.Server
}

func newFixture(t *testing.T, version string, oauth bool) *fixture {
	t.Helper()
	ca, err := testpki.NewCA("account-driven test CA")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ca.Issue("profile signer", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{clock: &fakeClock{now: t0}}
	f.tokens = newTokens(f.clock)
	f.asweb = &accountdriven.AppleAsWeb{URL: "https://mdm.example/authenticate", Tokens: f.tokens}
	f.oauth = &accountdriven.OAuth2{AuthorizationURL: "https://mdm.example/oauth2/authorize", TokenURL: "https://mdm.example/oauth2/token",
		RedirectURL: "apple-remotemanagement-user-login:/oauth2/redirection", ClientID: "client-1", Scope: "MDM", Tokens: f.tokens}
	var auth accountdriven.Authenticator = f.asweb
	if oauth {
		auth = f.oauth
	}
	h, err := accountdriven.New(accountdriven.Config{Version: version, Parse: parseBody, Auth: auth, Tokens: f.tokens, Profile: baseProfile, SignCert: signer.Cert, SignKey: signer.Key})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/enroll", h)
	mux.Handle("/oauth2/token", f.oauth.TokenHandler())
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

const body = `<?xml version="1.0"?><plist version="1.0"><dict><key>LANGUAGE</key><string>en</string><key>PRODUCT</key><string>iPhone17,2</string><key>VERSION</key><string>23A300</string></dict></plist>`

func (f *fixture) post(t *testing.T, bearer string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, f.srv.URL+"/enroll", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	t.Run("AccessTokenSingleUse", func(t *testing.T) {
		tk := newTokens(&fakeClock{now: t0})
		tok, err := tk.Issue(ctx, accountdriven.KindAccess, alice, nil)
		if err != nil || tok == "" {
			t.Fatal(err)
		}
		if rec, err := tk.Consume(ctx, accountdriven.KindAccess, tok); err != nil || rec.Identity.ManagedAppleAccount != alice.ManagedAppleAccount {
			t.Fatalf("first use: %+v %v", rec, err)
		}
		if _, err := tk.Consume(ctx, accountdriven.KindAccess, tok); !errors.Is(err, accountdriven.ErrTokenUsed) {
			t.Fatalf("replay = %v, want ErrTokenUsed", err)
		}
		if _, err := tk.Consume(ctx, accountdriven.KindEnrollment, tok); !errors.Is(err, accountdriven.ErrTokenNotFound) {
			t.Fatalf("wrong kind = %v", err)
		}
		if _, err := tk.Check(ctx, accountdriven.KindAccess, ""); !errors.Is(err, accountdriven.ErrTokenNotFound) {
			t.Fatalf("empty = %v", err)
		}
	})
	t.Run("AccessTokenExpires", func(t *testing.T) {
		c := &fakeClock{now: t0}
		tk := newTokens(c)
		tok, _ := tk.Issue(ctx, accountdriven.KindAccess, alice, nil)
		c.now = t0.Add(accountdriven.DefaultAccessTTL)
		if _, err := tk.Consume(ctx, accountdriven.KindAccess, tok); !errors.Is(err, accountdriven.ErrTokenExpired) {
			t.Fatalf("expired = %v", err)
		}
	})
	t.Run("EnrollmentTokenAuthorisesCheckin", func(t *testing.T) {
		tk := newTokens(&fakeClock{now: t0})
		tok, _ := tk.Issue(ctx, accountdriven.KindEnrollment, alice, nil)
		hook := &accountdriven.CheckinHook{Tokens: tk}
		req := &mdm.Request{ID: mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "E1"}, Params: map[string]string{accountdriven.ParamEnrollmentToken: tok}}
		ctx2, err := hook.Before(ctx, &service.Call{Op: "checkin:Authenticate", Request: req})
		if err != nil {
			t.Fatal(err)
		}
		if id, ok := accountdriven.IdentityFromContext(ctx2); !ok || id.ManagedAppleAccount != alice.ManagedAppleAccount {
			t.Fatalf("identity = %+v %v", id, ok)
		}
		bad := &mdm.Request{ID: req.ID, Params: map[string]string{accountdriven.ParamEnrollmentToken: "nope"}}
		if _, err := hook.Before(ctx, &service.Call{Op: "checkin:TokenUpdate", Request: bad}); !errors.Is(err, accountdriven.ErrEnrollmentToken) {
			t.Fatalf("bad token = %v", err)
		}
		// Other channels and ops are not guarded; nil calls are ignored.
		dev := &mdm.Request{ID: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}}
		if _, err := hook.Before(ctx, &service.Call{Op: "checkin:Authenticate", Request: dev}); err != nil {
			t.Fatal(err)
		}
		if _, err := hook.Before(ctx, &service.Call{Op: "connect", Request: bad}); err != nil {
			t.Fatal(err)
		}
		if _, err := hook.Before(ctx, nil); err != nil {
			t.Fatal(err)
		}
		hook.After(ctx, nil, nil)
		only := &accountdriven.CheckinHook{Tokens: tk, Channels: []mdm.Channel{mdm.ChannelDevice}}
		if _, err := only.Before(ctx, &service.Call{Op: "checkin:Authenticate", Request: dev}); !errors.Is(err, accountdriven.ErrEnrollmentToken) {
			t.Fatalf("custom channels = %v", err)
		}
	})
	t.Run("RetriedCheckinSucceeds", func(t *testing.T) {
		tk := newTokens(&fakeClock{now: t0})
		tok, _ := tk.Issue(ctx, accountdriven.KindEnrollment, alice, nil)
		hook := &accountdriven.CheckinHook{Tokens: tk}
		req := &mdm.Request{ID: mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "E1"}, Params: map[string]string{accountdriven.ParamEnrollmentToken: tok}}
		for range 3 {
			if _, err := hook.Before(ctx, &service.Call{Op: "checkin:Authenticate", Request: req}); err != nil {
				t.Fatalf("retry: %v", err)
			}
		}
	})
	t.Run("RefreshRotates", func(t *testing.T) {
		f := newFixture(t, accountdriven.VersionBYOD, true)
		code, _ := f.tokens.Issue(ctx, accountdriven.KindCode, alice, nil)
		first := f.token(t, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {f.oauth.RedirectURL}, "client_id": {f.oauth.ClientID}})
		if first.RefreshToken == "" || first.AccessToken == "" || first.TokenType != "Bearer" || first.ExpiresIn <= 0 {
			t.Fatalf("token response = %+v", first)
		}
		second := f.token(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {f.oauth.ClientID}})
		if second.RefreshToken == "" || second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken {
			t.Fatalf("refresh did not rotate: %+v", second)
		}
		// The old refresh token is gone.
		res := f.tokenRaw(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {f.oauth.ClientID}})
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("reused refresh = %d", res.StatusCode)
		}
	})
	t.Run("StoreErrors", func(t *testing.T) {
		st := accountdriven.NewMemStore()
		if err := st.MarkUsed(ctx, "missing", t0); !errors.Is(err, accountdriven.ErrTokenNotFound) {
			t.Fatal(err)
		}
		if err := st.Delete(ctx, "missing"); err != nil {
			t.Fatal(err)
		}
		if accountdriven.Hash("a") == accountdriven.Hash("b") || len(accountdriven.Hash("a")) != 64 {
			t.Fatal("hash")
		}
	})
}

func (f *fixture) tokenRaw(t *testing.T, form url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, f.srv.URL+"/oauth2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func (f *fixture) token(t *testing.T, form url.Values) accountdriven.TokenResponse {
	t.Helper()
	res := f.tokenRaw(t, form)
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("token endpoint = %d %s", res.StatusCode, data)
	}
	var tr accountdriven.TokenResponse
	if err := json.Unmarshal(data, &tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestHeader(t *testing.T) {
	t.Run("HTTPSOnly", func(t *testing.T) {
		for _, c := range []accountdriven.Challenge{
			{Method: accountdriven.MethodAppleAsWeb, URL: "http://mdm.example/auth"},
			{Method: accountdriven.MethodAppleAsWeb, URL: "::bad"},
			{Method: accountdriven.MethodAppleOAuth2, AuthorizationURL: "http://x", TokenURL: "https://x", RedirectURL: "apple-remotemanagement-user-login:/r", ClientID: "c", Scope: "s"},
			{Method: accountdriven.MethodAppleOAuth2, AuthorizationURL: "https://x", TokenURL: "https://x", RedirectURL: "https://x/r", ClientID: "c", Scope: "s"},
			{Method: accountdriven.MethodAppleOAuth2, AuthorizationURL: "https://x", TokenURL: "https://x", RedirectURL: "apple-remotemanagement-user-login:/r", ClientID: "", Scope: "s"},
			{Method: "basic"},
		} {
			if _, err := c.Header(); !errors.Is(err, accountdriven.ErrChallenge) {
				t.Fatalf("%+v: err = %v", c, err)
			}
		}
		h, err := (accountdriven.Challenge{Method: accountdriven.MethodAppleAsWeb, URL: "https://mdm.example/authenticate"}).Header()
		if err != nil || h != `Bearer method="apple-as-web", url="https://mdm.example/authenticate"` {
			t.Fatalf("header = %q %v", h, err)
		}
		parsed, err := accountdriven.ParseChallenge(h)
		if err != nil || parsed.URL != "https://mdm.example/authenticate" {
			t.Fatalf("parse = %+v %v", parsed, err)
		}
		if _, err := accountdriven.ParseChallenge("Basic realm=x"); !errors.Is(err, accountdriven.ErrChallenge) {
			t.Fatal(err)
		}
		if _, err := accountdriven.ParseChallenge("Bearer nonsense"); !errors.Is(err, accountdriven.ErrChallenge) {
			t.Fatal(err)
		}
	})
}

func TestFirstPost(t *testing.T) {
	t.Run("BodyParsed", func(t *testing.T) {
		f := newFixture(t, accountdriven.VersionBYOD, false)
		res := f.post(t, "")
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d", res.StatusCode)
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, f.srv.URL+"/enroll", strings.NewReader("<plist/>"))
		bad, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		bad.Body.Close()
		if bad.StatusCode != http.StatusBadRequest {
			t.Fatalf("unparsable body = %d", bad.StatusCode)
		}
		get, _ := http.Get(f.srv.URL + "/enroll") //nolint:noctx // test
		get.Body.Close()
		if get.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("GET = %d", get.StatusCode)
		}
	})
	t.Run("ParserPolicyRelayed", func(t *testing.T) {
		ca, _ := testpki.NewCA("ca")
		signer, _ := ca.Issue("s", time.Now().Add(-time.Minute))
		relay := func(*http.Request) (*accountdriven.DeviceInfo, error) {
			return nil, &accountdriven.HTTPError{Status: http.StatusForbidden, ContentType: "application/json", Body: []byte(`{"code":"com.apple.softwareupdate.required"}`), Err: errors.New("old os")}
		}
		tk := newTokens(&fakeClock{now: t0})
		h, err := accountdriven.New(accountdriven.Config{Version: accountdriven.VersionADDE, Parse: relay, Auth: &accountdriven.AppleAsWeb{URL: "https://x/a", Tokens: tk}, Tokens: tk, Profile: baseProfile, SignCert: signer.Cert, SignKey: signer.Key})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(body)))
		if rec.Code != http.StatusForbidden || rec.Header().Get("Content-Type") != "application/json" || !strings.Contains(rec.Body.String(), "softwareupdate.required") {
			t.Fatalf("relay = %d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestFlow(t *testing.T) {
	ctx := context.Background()
	t.Run("AppleAsWeb", func(t *testing.T) {
		f := newFixture(t, accountdriven.VersionBYOD, false)
		t.Run("FirstPost401", func(t *testing.T) {
			res := f.post(t, "")
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d", res.StatusCode)
			}
			if got := res.Header.Get("WWW-Authenticate"); got != `Bearer method="apple-as-web", url="https://mdm.example/authenticate"` {
				t.Fatalf("header = %q", got)
			}
		})
		t.Run("HeaderParams", func(t *testing.T) {
			c, err := accountdriven.ParseChallenge(f.post(t, "").Header.Get("WWW-Authenticate"))
			if err != nil || c.Method != accountdriven.MethodAppleAsWeb {
				t.Fatalf("%+v %v", c, err)
			}
		})
		t.Run("WebAuthPrefill", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/authenticate?user-identifier=alice%40example.com", nil)
			if accountdriven.UserIdentifier(r) != "alice@example.com" {
				t.Fatal("user-identifier not read")
			}
		})
		var access string
		t.Run("HandBack308", func(t *testing.T) {
			rec := httptest.NewRecorder()
			if err := f.asweb.Finish(rec, httptest.NewRequest(http.MethodPost, "/authenticate-results", nil), alice); err != nil {
				t.Fatal(err)
			}
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusPermanentRedirect || !strings.HasPrefix(loc, "apple-remotemanagement-user-login://authentication-results?access-token=") {
				t.Fatalf("hand back = %d %s", rec.Code, loc)
			}
			u, _ := url.Parse(loc)
			access = u.Query().Get("access-token")
			if err := f.asweb.Finish(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/x", nil), accountdriven.Identity{}); !errors.Is(err, accountdriven.ErrManagedAppleAccount) {
				t.Fatalf("no managed id = %v", err)
			}
		})
		t.Run("SecondPostProfile", func(t *testing.T) {
			res := f.post(t, access)
			data, _ := io.ReadAll(res.Body)
			if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != accountdriven.ContentTypeProfile {
				t.Fatalf("second post = %d %s %s", res.StatusCode, res.Header.Get("Content-Type"), data)
			}
			p, err := enroll.Parse(data, profile.ParseOptions{})
			if err != nil {
				t.Fatalf("parse profile: %v", err)
			}
			if p.EnrollmentMode != accountdriven.ModeBYOD || p.AssignedManagedAppleID != alice.ManagedAppleAccount {
				t.Fatalf("profile keys = %q %q", p.EnrollmentMode, p.AssignedManagedAppleID)
			}
			u, _ := url.Parse(p.ServerURL)
			tok := u.Query().Get(accountdriven.ParamEnrollmentToken)
			if _, err := f.tokens.Check(ctx, accountdriven.KindEnrollment, tok); err != nil {
				t.Fatalf("enrollment token in ServerURL: %v", err)
			}
			// Replay of the access token: a fresh challenge, not a profile.
			if res := f.post(t, access); res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") == "" {
				t.Fatalf("replayed bearer = %d", res.StatusCode)
			}
			if res := f.post(t, "garbage"); res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("garbage bearer = %d", res.StatusCode)
			}
		})
	})
	t.Run("AppleOAuth2", func(t *testing.T) {
		f := newFixture(t, accountdriven.VersionADDE, true)
		var challenge accountdriven.Challenge
		t.Run("Header", func(t *testing.T) {
			res := f.post(t, "")
			var err error
			if challenge, err = accountdriven.ParseChallenge(res.Header.Get("WWW-Authenticate")); err != nil {
				t.Fatalf("%q: %v", res.Header.Get("WWW-Authenticate"), err)
			}
			if challenge.Method != accountdriven.MethodAppleOAuth2 || challenge.ClientID != "client-1" || challenge.Scope != "MDM" || challenge.RedirectURL != f.oauth.RedirectURL {
				t.Fatalf("challenge = %+v", challenge)
			}
		})
		var code, state string
		t.Run("AuthorizationCode", func(t *testing.T) {
			state = "340B948D"
			q := url.Values{"response_type": {"code"}, "client_id": {challenge.ClientID}, "redirect_uri": {challenge.RedirectURL}, "state": {state}, "login_hint": {"alice@example.com"}}
			r := httptest.NewRequest(http.MethodGet, "/oauth2/authorize?"+q.Encode(), nil)
			req, err := f.oauth.ParseAuthorization(r)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("LoginHint", func(t *testing.T) {
				if req.LoginHint != "alice@example.com" || accountdriven.LoginHint(r) != "alice@example.com" {
					t.Fatalf("login hint = %q", req.LoginHint)
				}
			})
			rec := httptest.NewRecorder()
			if err := f.oauth.Grant(rec, r, req, alice); err != nil {
				t.Fatal(err)
			}
			loc, _ := url.Parse(rec.Header().Get("Location"))
			if rec.Code != http.StatusPermanentRedirect || loc.Scheme != accountdriven.CallbackScheme {
				t.Fatalf("grant redirect = %d %s", rec.Code, loc)
			}
			t.Run("StateEchoed", func(t *testing.T) {
				if loc.Query().Get("state") != state {
					t.Fatalf("state = %q", loc.Query().Get("state"))
				}
			})
			code = loc.Query().Get("code")
			if code == "" {
				t.Fatal("no code")
			}
			for _, bad := range []url.Values{
				{"response_type": {"token"}, "client_id": {"client-1"}, "redirect_uri": {challenge.RedirectURL}, "state": {"s"}},
				{"response_type": {"code"}, "client_id": {"other"}, "redirect_uri": {challenge.RedirectURL}, "state": {"s"}},
				{"response_type": {"code"}, "client_id": {"client-1"}, "redirect_uri": {"https://evil/"}, "state": {"s"}},
				{"response_type": {"code"}, "client_id": {"client-1"}, "redirect_uri": {challenge.RedirectURL}},
			} {
				if _, err := f.oauth.ParseAuthorization(httptest.NewRequest(http.MethodGet, "/a?"+bad.Encode(), nil)); !errors.Is(err, accountdriven.ErrOAuth2Request) {
					t.Fatalf("%v: %v", bad, err)
				}
			}
			if err := f.oauth.Grant(httptest.NewRecorder(), r, req, accountdriven.Identity{}); !errors.Is(err, accountdriven.ErrManagedAppleAccount) {
				t.Fatal(err)
			}
		})
		var access string
		t.Run("TokenEndpoint", func(t *testing.T) {
			tr := f.token(t, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {challenge.RedirectURL}, "client_id": {challenge.ClientID}})
			if tr.AccessToken == "" || tr.TokenType != "Bearer" || tr.Scope != "MDM" || tr.ExpiresIn != int64(accountdriven.DefaultAccessTTL/time.Second) {
				t.Fatalf("token = %+v", tr)
			}
			access = tr.AccessToken
			res := f.post(t, access)
			data, _ := io.ReadAll(res.Body)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("second post = %d %s", res.StatusCode, data)
			}
			p, err := enroll.Parse(data, profile.ParseOptions{})
			if err != nil || p.EnrollmentMode != accountdriven.ModeADDE {
				t.Fatalf("profile = %+v %v", p, err)
			}
		})
		t.Run("BadCodeRejected", func(t *testing.T) {
			for name, form := range map[string]url.Values{
				"reused code":   {"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {challenge.RedirectURL}, "client_id": {challenge.ClientID}},
				"bad redirect":  {"grant_type": {"authorization_code"}, "code": {"x"}, "redirect_uri": {"https://evil/"}, "client_id": {challenge.ClientID}},
				"unknown grant": {"grant_type": {"password"}, "client_id": {challenge.ClientID}},
				"bad client":    {"grant_type": {"authorization_code"}, "code": {"x"}, "client_id": {"other"}},
				"bad refresh":   {"grant_type": {"refresh_token"}, "refresh_token": {"x"}, "client_id": {challenge.ClientID}},
			} {
				res := f.tokenRaw(t, form)
				if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusUnauthorized {
					t.Fatalf("%s: %d", name, res.StatusCode)
				}
			}
			if res := f.tokenRaw(t, url.Values{}); res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("empty form = %d", res.StatusCode)
			}
			get, _ := http.Get(f.srv.URL + "/oauth2/token") //nolint:noctx // test
			get.Body.Close()
			if get.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("GET token = %d", get.StatusCode)
			}
		})
	})
}

func TestProfile(t *testing.T) {
	base := func() *enroll.Profile {
		return &enroll.Profile{Identifier: "com.example.mdm", Topic: "com.apple.mgmt.t", ServerURL: "https://mdm.example/mdm?x=1", CheckInURL: "https://mdm.example/checkin", SCEP: &enroll.SCEP{URL: "https://mdm.example/scep"}}
	}
	t.Run("BYOD", func(t *testing.T) {
		p := base()
		if err := accountdriven.Finalize(p, accountdriven.VersionBYOD, alice, "tok"); err != nil {
			t.Fatal(err)
		}
		if p.EnrollmentMode != accountdriven.ModeBYOD || p.AssignedManagedAppleID != alice.ManagedAppleAccount {
			t.Fatalf("%+v", p)
		}
		for _, u := range []string{p.ServerURL, p.CheckInURL} {
			parsed, _ := url.Parse(u)
			if parsed.Query().Get(accountdriven.ParamEnrollmentToken) != "tok" {
				t.Fatalf("token missing in %s", u)
			}
		}
		if q, _ := url.Parse(p.ServerURL); q.Query().Get("x") != "1" {
			t.Fatal("existing query lost")
		}
		built, err := p.Build()
		if err != nil {
			t.Fatal(err)
		}
		if m, ok := profile.Find[*profiles.MDM](built); !ok || *m.EnrollmentMode != "BYOD" {
			t.Fatal("built profile lacks EnrollmentMode")
		}
	})
	t.Run("ADDE", func(t *testing.T) {
		p := base()
		if err := accountdriven.Finalize(p, accountdriven.VersionADDE, alice, ""); err != nil || p.EnrollmentMode != accountdriven.ModeADDE {
			t.Fatalf("%+v %v", p, err)
		}
	})
	t.Run("ModeMatchesVersion", func(t *testing.T) {
		p := base()
		p.EnrollmentMode = accountdriven.ModeADDE
		if err := accountdriven.Finalize(p, accountdriven.VersionBYOD, alice, ""); !errors.Is(err, accountdriven.ErrMode) {
			t.Fatalf("mismatch = %v", err)
		}
		if err := accountdriven.Finalize(base(), "mdm-other", alice, ""); !errors.Is(err, accountdriven.ErrMode) {
			t.Fatalf("unknown version = %v", err)
		}
	})
	t.Run("ManagedAppleIDRequired", func(t *testing.T) {
		if err := accountdriven.Finalize(base(), accountdriven.VersionBYOD, accountdriven.Identity{}, ""); !errors.Is(err, accountdriven.ErrManagedAppleAccount) {
			t.Fatalf("empty id = %v", err)
		}
	})
	t.Run("ImmutableOnUpdate", func(t *testing.T) {
		p := base()
		p.AssignedManagedAppleID = "bob@example.com"
		if err := accountdriven.Finalize(p, accountdriven.VersionBYOD, alice, ""); !errors.Is(err, accountdriven.ErrManagedAppleAccount) {
			t.Fatalf("reassign = %v", err)
		}
	})
	t.Run("MacADDESupported", func(t *testing.T) {
		f := newFixture(t, accountdriven.VersionADDE, false)
		if res := f.post(t, ""); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("ADDE first post = %d", res.StatusCode)
		}
	})
	t.Run("BadURL", func(t *testing.T) {
		p := base()
		p.ServerURL = "::bad"
		if err := accountdriven.Finalize(p, accountdriven.VersionBYOD, alice, "tok"); err == nil {
			t.Fatal("bad ServerURL accepted")
		}
	})
}

func TestNew(t *testing.T) {
	tk := newTokens(&fakeClock{now: t0})
	ca, _ := testpki.NewCA("ca")
	signer, _ := ca.Issue("s", time.Now().Add(-time.Minute))
	ok := accountdriven.Config{Version: accountdriven.VersionBYOD, Parse: parseBody, Auth: &accountdriven.AppleAsWeb{URL: "https://x/a", Tokens: tk}, Tokens: tk, Profile: baseProfile, SignCert: signer.Cert, SignKey: signer.Key}
	for name, mutate := range map[string]func(*accountdriven.Config){
		"version": func(c *accountdriven.Config) { c.Version = "mdm-x" },
		"parse":   func(c *accountdriven.Config) { c.Parse = nil },
		"signer":  func(c *accountdriven.Config) { c.SignKey = nil },
	} {
		cfg := ok
		mutate(&cfg)
		if _, err := accountdriven.New(cfg); !errors.Is(err, accountdriven.ErrConfig) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := accountdriven.New(ok); err != nil {
		t.Fatal(err)
	}
}

// Failing paths of the handler: profile hook, challenge builder, signing.
func TestHandlerFailures(t *testing.T) {
	ca, _ := testpki.NewCA("ca")
	signer, _ := ca.Issue("s", time.Now().Add(-time.Minute))
	tk := newTokens(&fakeClock{now: t0})
	access, _ := tk.Issue(context.Background(), accountdriven.KindAccess, alice, nil)
	t.Run("ProfileHookError", func(t *testing.T) {
		h, _ := accountdriven.New(accountdriven.Config{Version: accountdriven.VersionBYOD, Parse: parseBody, Auth: &accountdriven.AppleAsWeb{URL: "https://x/a", Tokens: tk}, Tokens: tk,
			Profile: func(context.Context, accountdriven.Identity, *accountdriven.DeviceInfo) (*enroll.Profile, error) {
				return nil, errors.New("boom")
			}, SignCert: signer.Cert, SignKey: signer.Key})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "boom") {
			t.Fatalf("hook error = %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("ChallengeInvalid", func(t *testing.T) {
		h, _ := accountdriven.New(accountdriven.Config{Version: accountdriven.VersionBYOD, Parse: parseBody, Auth: &accountdriven.AppleAsWeb{URL: "http://insecure/a", Tokens: tk}, Tokens: tk, Profile: baseProfile, SignCert: signer.Cert, SignKey: signer.Key})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(body)))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("insecure challenge = %d", rec.Code)
		}
	})
	t.Run("UnbuildableProfile", func(t *testing.T) {
		access2, _ := tk.Issue(context.Background(), accountdriven.KindAccess, alice, nil)
		h, _ := accountdriven.New(accountdriven.Config{Version: accountdriven.VersionBYOD, Parse: parseBody, Auth: &accountdriven.AppleAsWeb{URL: "https://x/a", Tokens: tk}, Tokens: tk,
			Profile: func(context.Context, accountdriven.Identity, *accountdriven.DeviceInfo) (*enroll.Profile, error) {
				return &enroll.Profile{}, nil
			}, SignCert: signer.Cert, SignKey: signer.Key})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access2)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("unbuildable = %d", rec.Code)
		}
	})
}
