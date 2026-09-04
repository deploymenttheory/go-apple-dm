package axm

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/secrets"
)

func TestScope(t *testing.T) {
	t.Parallel()
	t.Run("FromClientID", func(t *testing.T) {
		t.Parallel()
		if got := ScopeFor("BUSINESSAPI.x"); got != ScopeBusiness {
			t.Fatalf("business: %q", got)
		}
		if got := ScopeFor("SCHOOLAPI.x"); got != ScopeSchool {
			t.Fatalf("school: %q", got)
		}
		if got := ScopeFor("OTHER.x"); got != "" {
			t.Fatalf("other: %q", got)
		}
		c, err := New(context.Background(), Config{ClientID: "BUSINESSAPI.x", KeyID: "k", PrivateKey: newKey(t)})
		if err != nil {
			t.Fatal(err)
		}
		if c.Scope() != ScopeBusiness || c.BaseURL() != BusinessBaseURL {
			t.Fatalf("scope %q base %q", c.Scope(), c.BaseURL())
		}
		if _, err := New(context.Background(), Config{ClientID: "OTHER.x", KeyID: "k", PrivateKey: newKey(t)}); !errors.Is(err, ErrConfig) {
			t.Fatalf("underivable scope: %v", err)
		}
	})
	t.Run("Override", func(t *testing.T) {
		t.Parallel()
		c, err := New(context.Background(), Config{ClientID: "OTHER.x", KeyID: "k", PrivateKey: newKey(t), Scope: ScopeSchool})
		if err != nil {
			t.Fatal(err)
		}
		if c.Scope() != ScopeSchool || c.BaseURL() != SchoolBaseURL {
			t.Fatalf("scope %q base %q", c.Scope(), c.BaseURL())
		}
	})
	t.Run("SchoolHost", func(t *testing.T) {
		t.Parallel()
		c, err := New(context.Background(), Config{ClientID: "SCHOOLAPI.x", KeyID: "k", PrivateKey: newKey(t)})
		if err != nil {
			t.Fatal(err)
		}
		if c.Scope() != ScopeSchool || c.BaseURL() != SchoolBaseURL {
			t.Fatalf("scope %q base %q", c.Scope(), c.BaseURL())
		}
		srv := axmtest.NewServer()
		defer srv.Close()
		key := newKey(t)
		srv.RegisterKey("SCHOOLAPI.x", "k", &key.PublicKey)
		c, err = New(context.Background(), Config{ClientID: "SCHOOLAPI.x", KeyID: "k", PrivateKey: key, BaseURL: srv.URL, TokenURL: srv.TokenURL, HTTPClient: srv.Client()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); err != nil {
			t.Fatalf("school scope rejected: %v", err)
		}
		if got := srv.Requests()[0].Body; !strings.Contains(string(got), "scope=school.api") {
			t.Fatalf("token body %q", got)
		}
	})
}

func TestAssertion(t *testing.T) {
	t.Parallel()
	key := newKey(t)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	mint := func(t *testing.T, ttl, skew time.Duration) (axmtest.AssertionHeader, axmtest.AssertionClaims) {
		t.Helper()
		tok, err := Assertion(key, testClientID, testKeyID, now, ttl, skew)
		if err != nil {
			t.Fatal(err)
		}
		if err := axmtest.VerifyAssertion(tok, &key.PublicKey); err != nil {
			t.Fatal(err)
		}
		h, c, err := axmtest.DecodeAssertion(tok)
		if err != nil {
			t.Fatal(err)
		}
		return h, c
	}
	t.Run("Claims", func(t *testing.T) {
		t.Parallel()
		_, c := mint(t, 0, 0)
		if c.Iss != testClientID || c.Sub != testClientID || c.Aud != Audience {
			t.Fatalf("claims %+v", c)
		}
		if _, err := uuid.Parse(c.Jti); err != nil {
			t.Fatalf("jti %q is not a UUID: %v", c.Jti, err)
		}
		if c.Iat != now.Unix() {
			t.Fatalf("iat %d, want %d", c.Iat, now.Unix())
		}
	})
	t.Run("KidHeader", func(t *testing.T) {
		t.Parallel()
		h, _ := mint(t, 0, 0)
		if h.Alg != "ES256" || h.Kid != testKeyID {
			t.Fatalf("header %+v", h)
		}
	})
	t.Run("P256Only", func(t *testing.T) {
		t.Parallel()
		p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Assertion(p384, testClientID, testKeyID, now, 0, 0); !errors.Is(err, ErrKeyType) {
			t.Fatalf("P-384: %v", err)
		}
		if _, err := Assertion(nil, testClientID, testKeyID, now, 0, 0); !errors.Is(err, ErrKeyType) {
			t.Fatalf("nil: %v", err)
		}
		if _, err := New(context.Background(), Config{ClientID: testClientID, KeyID: testKeyID, PrivateKey: p384}); !errors.Is(err, ErrKeyType) {
			t.Fatalf("New with P-384: %v", err)
		}
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := New(context.Background(), Config{ClientID: testClientID, KeyID: testKeyID, PrivateKeyPEM: pkcs8PEM(t, rsaKey)}); !errors.Is(err, ErrKeyType) {
			t.Fatalf("New with RSA: %v", err)
		}
	})
	t.Run("ExpCapped180Days", func(t *testing.T) {
		t.Parallel()
		_, c := mint(t, 400*24*time.Hour, 0)
		if c.Exp != c.Iat+int64(MaxAssertionTTL/time.Second) {
			t.Fatalf("exp %d, iat %d", c.Exp, c.Iat)
		}
		_, c = mint(t, 400*24*time.Hour, time.Hour)
		if c.Exp != c.Iat+int64(MaxAssertionTTL/time.Second) {
			t.Fatalf("with skew: exp %d, iat %d", c.Exp, c.Iat)
		}
	})
	t.Run("DefaultTwentyMinutes", func(t *testing.T) {
		t.Parallel()
		_, c := mint(t, 0, 0)
		if c.Exp != now.Add(20*time.Minute).Unix() {
			t.Fatalf("exp %d, want now+20m", c.Exp)
		}
	})
	t.Run("ClockSkew", func(t *testing.T) {
		t.Parallel()
		_, c := mint(t, 0, 2*time.Minute)
		if c.Iat != now.Add(-2*time.Minute).Unix() {
			t.Fatalf("iat %d, want back-dated 2m", c.Iat)
		}
		if c.Exp != now.Add(20*time.Minute).Unix() {
			t.Fatalf("exp %d must not move with skew", c.Exp)
		}
		_, c = mint(t, 0, -time.Minute)
		if c.Iat != now.Unix() {
			t.Fatalf("negative skew must be ignored: %d", c.Iat)
		}
	})
	t.Run("JTIUnique", func(t *testing.T) {
		t.Parallel()
		seen := map[string]struct{}{}
		for range 20 {
			_, c := mint(t, 0, 0)
			if _, dup := seen[c.Jti]; dup {
				t.Fatalf("duplicate jti %s", c.Jti)
			}
			seen[c.Jti] = struct{}{}
		}
	})
}

func TestKeyLoading(t *testing.T) {
	t.Parallel()
	key := newKey(t)
	t.Run("SEC1", func(t *testing.T) {
		t.Parallel()
		got, err := ParseKey(sec1PEM(t, key))
		if err != nil || !got.Equal(key) {
			t.Fatalf("ParseKey SEC1: %v", err)
		}
	})
	t.Run("PKCS8", func(t *testing.T) {
		t.Parallel()
		got, err := ParseKey(pkcs8PEM(t, key))
		if err != nil || !got.Equal(key) {
			t.Fatalf("ParseKey PKCS8: %v", err)
		}
		c, err := New(context.Background(), Config{ClientID: testClientID, KeyID: testKeyID, PrivateKeyPEM: pkcs8PEM(t, key)})
		if err != nil || !c.key.Equal(key) {
			t.Fatalf("New with PEM: %v", err)
		}
	})
	t.Run("File", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "key.pem")
		if err := os.WriteFile(path, sec1PEM(t, key), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadKeyFile(path)
		if err != nil || !got.Equal(key) {
			t.Fatalf("LoadKeyFile: %v", err)
		}
		if _, err := LoadKeyFile(filepath.Join(t.TempDir(), "missing.pem")); !errors.Is(err, ErrKey) {
			t.Fatalf("missing file: %v", err)
		}
	})
	t.Run("SecretsProvider", func(t *testing.T) {
		t.Parallel()
		p := secrets.Static{"axm.key": sec1PEM(t, key)}
		got, err := LoadKey(context.Background(), p, "axm.key")
		if err != nil || !got.Equal(key) {
			t.Fatalf("LoadKey: %v", err)
		}
		if _, err := LoadKey(context.Background(), p, "other"); !errors.Is(err, ErrKey) || !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("missing secret: %v", err)
		}
		if _, err := LoadKey(context.Background(), nil, "x"); !errors.Is(err, ErrKey) {
			t.Fatalf("nil provider: %v", err)
		}
		c, err := New(context.Background(), Config{ClientID: testClientID, KeyID: testKeyID, Keys: p, KeyName: "axm.key"})
		if err != nil || !c.key.Equal(key) {
			t.Fatalf("New with provider: %v", err)
		}
		if _, err := New(context.Background(), Config{ClientID: testClientID, KeyID: testKeyID, Keys: p, KeyName: "other"}); !errors.Is(err, ErrKey) {
			t.Fatalf("New with missing secret: %v", err)
		}
	})
	t.Run("BadKeyNamesFormats", func(t *testing.T) {
		t.Parallel()
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cases := map[string]struct {
			pem  []byte
			want error
		}{
			"empty":       {nil, ErrKey},
			"garbage":     {[]byte("not pem"), ErrKey},
			"wrong block": {[]byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), ErrKey},
			"bad der":     {[]byte("-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n"), ErrKey},
			"rsa pkcs8":   {pkcs8PEM(t, rsaKey), ErrKeyType},
			"p384 sec1":   {sec1PEM(t, p384), ErrKeyType},
		}
		for name, tc := range cases {
			if _, err := ParseKey(tc.pem); !errors.Is(err, tc.want) {
				t.Errorf("%s: %v, want %v", name, err, tc.want)
			}
		}
	})
}

func TestTokenExchange(t *testing.T) {
	t.Parallel()
	t.Run("FormBody", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		if _, err := c.Token(context.Background()); err != nil {
			t.Fatal(err)
		}
		req := f.srv.Requests()[0]
		if req.Method != http.MethodPost || req.Path != axmtest.TokenPath {
			t.Fatalf("%s %s", req.Method, req.Path)
		}
		form := string(req.Body)
		for _, want := range []string{"grant_type=client_credentials", "client_id=" + strings.ReplaceAll(testClientID, ".", "."), "client_assertion_type=urn%3Aietf%3Aparams%3Aoauth%3Aclient-assertion-type%3Ajwt-bearer", "client_assertion=ey", "scope=business.api"} {
			if !strings.Contains(form, want) {
				t.Errorf("form %q lacks %q", form, want)
			}
		}
		if req.Query.Encode() != "" {
			t.Errorf("parameters must be in the body, not the query: %v", req.Query)
		}
	})
	t.Run("ContentType", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		if _, err := c.Token(context.Background()); err != nil {
			t.Fatal(err)
		}
		req := f.srv.Requests()[0]
		if ct := req.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Fatalf("Content-Type %q", ct)
		}
		if ua := req.Header.Get("User-Agent"); ua != DefaultUserAgent {
			t.Fatalf("User-Agent %q", ua)
		}
	})
	t.Run("ScopeParameter", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, func(cfg *Config) { cfg.Scope = ScopeSchool })
		_, err := c.Token(context.Background())
		var ae *AuthError
		if !errors.As(err, &ae) || ae.Code != "invalid_scope" || ae.Status != http.StatusBadRequest {
			t.Fatalf("wrong scope: %v", err)
		}
		if !strings.Contains(string(f.srv.Requests()[0].Body), "scope=school.api") {
			t.Fatalf("scope not sent: %q", f.srv.Requests()[0].Body)
		}
		if !IsUnauthorized(err) {
			t.Fatal("IsUnauthorized must be true for *AuthError")
		}
	})
	t.Run("EndpointOverride", func(t *testing.T) {
		t.Parallel()
		var hit int32
		var mu sync.Mutex
		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			hit++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"access_token":"t","token_type":"Bearer","expires_in":3600}`))
		}))
		defer tokenSrv.Close()
		f := newFixture(t)
		c := f.client(t, func(cfg *Config) { cfg.TokenURL = tokenSrv.URL + "/token" })
		tok, err := c.Token(context.Background())
		if err != nil || tok != "t" {
			t.Fatalf("token %q: %v", tok, err)
		}
		mu.Lock()
		defer mu.Unlock()
		if hit != 1 || f.srv.TokenRequests() != 0 {
			t.Fatalf("token endpoint hits: override %d, default %d", hit, f.srv.TokenRequests())
		}
	})
	t.Run("BaseURLOverride", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		c := f.client(t, func(cfg *Config) { cfg.BaseURL = f.srv.URL + "/prefix/" })
		if c.BaseURL() != f.srv.URL+"/prefix" {
			t.Fatalf("base %q", c.BaseURL())
		}
		_, err := c.ListOrgDevices(context.Background(), ListOptions{})
		if !IsNotFound(err) {
			t.Fatalf("prefixed base should reach /prefix/v1/orgDevices and 404: %v", err)
		}
		if got := apiRequests(f.srv)[0].Path; got != "/prefix/v1/orgDevices" {
			t.Fatalf("path %q", got)
		}
		c = f.client(t, nil)
		p, err := c.ListOrgDevices(context.Background(), ListOptions{})
		if err != nil || len(p.Items) != 1 {
			t.Fatalf("fake base: %v", err)
		}
	})
	t.Run("Failures", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.RejectNextTokenRequests(1)
		c := f.client(t, nil)
		_, err := c.Token(context.Background())
		var ae *AuthError
		if !errors.As(err, &ae) || ae.Code != "invalid_client" || ae.Description == "" || !strings.Contains(ae.Error(), "invalid_client") {
			t.Fatalf("rejected: %v", err)
		}
		if _, err := c.Token(context.Background()); err != nil {
			t.Fatalf("after fault: %v", err)
		}
		other := newKey(t)
		c = f.client(t, func(cfg *Config) { cfg.PrivateKey = other })
		if _, err := c.Token(context.Background()); !errors.As(err, &ae) || ae.Status != http.StatusBadRequest {
			t.Fatalf("wrong key: %v", err)
		}
		c = f.client(t, func(cfg *Config) { cfg.KeyID = "other-kid" })
		if _, err := c.Token(context.Background()); !errors.As(err, &ae) {
			t.Fatalf("wrong kid: %v", err)
		}
		c = f.client(t, func(cfg *Config) { cfg.TokenURL = "http://127.0.0.1:1/token" })
		if _, err := c.Token(context.Background()); !errors.As(err, &ae) || !errors.Is(err, ErrTransport) {
			t.Fatalf("unreachable: %v", err)
		}
		for name, body := range map[string]string{"nonjson": "<html>", "empty": `{"access_token":""}`} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			c = f.client(t, func(cfg *Config) { cfg.TokenURL = srv.URL })
			if _, err := c.Token(context.Background()); !errors.As(err, &ae) || !errors.Is(err, ErrDecode) {
				t.Errorf("%s: %v", name, err)
			}
			srv.Close()
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c = f.client(t, nil)
		if _, err := c.Token(ctx); !errors.As(err, &ae) {
			t.Fatalf("cancelled: %v", err)
		}
	})
}

func TestTokenCache(t *testing.T) {
	t.Parallel()
	t.Run("ReuseWithinTTL", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		c := f.client(t, nil)
		for range 3 {
			if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if n := f.srv.TokenRequests(); n != 1 {
			t.Fatalf("token requests %d, want 1", n)
		}
	})
	t.Run("RefreshAtMargin", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		fake := clock.NewFake(time.Now())
		f.srv.SetNow(fake.Now)
		c := f.client(t, func(cfg *Config) { cfg.Clock = fake; cfg.RefreshMargin = 5 * time.Minute })
		first, err := c.Token(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		fake.Advance(54 * time.Minute)
		again, err := c.Token(context.Background())
		if err != nil || again != first {
			t.Fatalf("before margin: %q vs %q, %v", again, first, err)
		}
		fake.Advance(2 * time.Minute)
		renewed, err := c.Token(context.Background())
		if err != nil || renewed == first {
			t.Fatalf("at margin: %q vs %q, %v", renewed, first, err)
		}
		if n := f.srv.TokenRequests(); n != 2 {
			t.Fatalf("token requests %d, want 2", n)
		}
		f.srv.SetTokenTTL(2 * time.Minute)
		c.ForceRefresh()
		short, err := c.Token(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		fake.Advance(50 * time.Second)
		if tok, _ := c.Token(context.Background()); tok != short {
			t.Fatal("a margin larger than the TTL must renew at half the TTL, not at once")
		}
		fake.Advance(20 * time.Second)
		if tok, _ := c.Token(context.Background()); tok == short {
			t.Fatal("half the TTL elapsed, token must renew")
		}
	})
	t.Run("Singleflight", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		var wg sync.WaitGroup
		tokens := make([]string, 16)
		for i := range tokens {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tok, err := c.Token(context.Background())
				if err != nil {
					t.Error(err)
				}
				tokens[i] = tok
			}()
		}
		wg.Wait()
		for _, tok := range tokens {
			if tok != tokens[0] {
				t.Fatalf("tokens differ: %v", tokens)
			}
		}
		if n := f.srv.TokenRequests(); n != 1 {
			t.Fatalf("token requests %d, want 1", n)
		}
	})
	t.Run("SingleflightFailure", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.RejectNextTokenRequests(1)
		c := f.client(t, nil)
		var wg sync.WaitGroup
		var mu sync.Mutex
		failed := 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := c.Token(context.Background()); err != nil {
					var ae *AuthError
					if !errors.As(err, &ae) {
						t.Errorf("want *AuthError, got %v", err)
					}
					mu.Lock()
					failed++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if failed == 0 {
			t.Fatal("the rejected exchange must be reported")
		}
	})
	t.Run("SingleflightCancelled", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		c.mu.Lock()
		c.inflight = make(chan struct{})
		c.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.Token(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter must honour the context: %v", err)
		}
	})
	t.Run("ForceRefresh", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		first, err := c.Token(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		c.ForceRefresh()
		second, err := c.Token(context.Background())
		if err != nil || second == first {
			t.Fatalf("ForceRefresh: %q vs %q, %v", second, first, err)
		}
		if n := f.srv.TokenRequests(); n != 2 {
			t.Fatalf("token requests %d, want 2", n)
		}
	})
}

func TestUnauthorized(t *testing.T) {
	t.Parallel()
	t.Run("ReplayOnce", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddOrgDevice("S1", nil)
		c := f.client(t, nil)
		if _, err := c.ListOrgDevices(context.Background(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
		f.srv.ExpireTokens()
		f.srv.Reset()
		p, err := c.ListOrgDevices(context.Background(), ListOptions{})
		if err != nil || len(p.Items) != 1 {
			t.Fatalf("replay: %v", err)
		}
		reqs := f.srv.Requests()
		if len(reqs) != 3 || reqs[0].Status != http.StatusUnauthorized || reqs[1].Path != axmtest.TokenPath || reqs[2].Status != http.StatusOK {
			for _, r := range reqs {
				t.Logf("%s %s -> %d", r.Method, r.Path, r.Status)
			}
			t.Fatal("want 401, token exchange, replayed 200")
		}
		if reqs[0].Header.Get("Authorization") == reqs[2].Header.Get("Authorization") {
			t.Fatal("replay must carry a fresh token")
		}
	})
	t.Run("SecondIsAuthError", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.SetTokenTTL(-time.Second)
		c := f.client(t, nil)
		_, err := c.ListOrgDevices(context.Background(), ListOptions{})
		var ae *AuthError
		if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized || !IsUnauthorized(err) {
			t.Fatalf("second 401: %v", err)
		}
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized || apiErr.Code() != "UNAUTHORIZED" {
			t.Fatalf("wrapped API error: %v", err)
		}
		if n := len(apiRequests(f.srv)); n != 2 {
			t.Fatalf("API requests %d, want exactly one replay", n)
		}
	})
}

func TestConfig(t *testing.T) {
	t.Parallel()
	key := newKey(t)
	cases := map[string]Config{
		"no client id":    {KeyID: "k", PrivateKey: key},
		"no key id":       {ClientID: testClientID, PrivateKey: key},
		"no key":          {ClientID: testClientID, KeyID: "k"},
		"bad base url":    {ClientID: testClientID, KeyID: "k", PrivateKey: key, BaseURL: "ftp://x"},
		"unparsable base": {ClientID: testClientID, KeyID: "k", PrivateKey: key, BaseURL: "http://[::1]:x"},
		"bad token url":   {ClientID: testClientID, KeyID: "k", PrivateKey: key, TokenURL: "http://[::1]:x"},
		"negative skew":   {ClientID: testClientID, KeyID: "k", PrivateKey: key, ClockSkew: -1},
		"negative retry":  {ClientID: testClientID, KeyID: "k", PrivateKey: key, Retry: Retry{Max: -1, Base: 1}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(context.Background(), cfg); !errors.Is(err, ErrConfig) {
				t.Fatalf("%v, want ErrConfig", err)
			}
		})
	}
	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		c, err := New(context.Background(), Config{ClientID: testClientID, KeyID: "k", PrivateKey: key, AssertionTTL: 400 * 24 * time.Hour, Retry: Retry{Max: 2}})
		if err != nil {
			t.Fatal(err)
		}
		if c.cfg.AssertionTTL != MaxAssertionTTL || c.cfg.ClockSkew != DefaultClockSkew || c.cfg.RefreshMargin != DefaultRefreshMargin ||
			c.cfg.PageCap != DefaultPageCap || c.cfg.Retry.Base != DefaultRetry.Base || c.cfg.Retry.Cap != DefaultRetry.Base ||
			c.cfg.TokenURL != DefaultTokenURL || c.cfg.HTTPClient.Timeout != DefaultTimeout || c.cfg.PrivateKey != nil {
			t.Fatalf("defaults not applied: %+v", c.cfg)
		}
	})
	t.Run("RequestBuild", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		c := f.client(t, nil)
		if _, err := c.roundTrip(context.Background(), request{method: "BAD METHOD", path: "/v1/x"}); !errors.Is(err, ErrArgument) && !errors.Is(err, ErrTransport) {
			t.Fatalf("bad method: %v", err)
		}
		if _, err := c.roundTrip(context.Background(), request{method: http.MethodPost, path: "/v1/x", body: make(chan int)}); !errors.Is(err, ErrArgument) {
			t.Fatalf("unencodable body: %v", err)
		}
		if err := c.do(context.Background(), request{method: http.MethodGet, path: "/v1/orgDevices"}, new(chan int)); !errors.Is(err, ErrDecode) {
			t.Fatalf("undecodable response: %v", err)
		}
		hung := stub(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("{"))
		})
		sc := stubClient(t, hung, nil)
		if _, err := sc.ListOrgDevices(context.Background(), ListOptions{}); !errors.Is(err, ErrTransport) {
			t.Fatalf("truncated body: %v", err)
		}
	})
}

// readAll drains a reader for tests.
func readAll(t *testing.T, r io.ReadCloser) string {
	t.Helper()
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
