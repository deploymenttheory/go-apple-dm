package dep_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/dep/inmem"
)

func TestSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("RefreshOnce", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if f.srv.SessionCalls() != 1 {
			t.Fatalf("sessions after first call = %d", f.srv.SessionCalls())
		}
		f.srv.InvalidateSessions()
		const n = 12
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := range n {
			wg.Go(func() { _, errs[i] = f.client.Account(ctx, acct) })
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		if got := f.srv.SessionCalls(); got != 2 {
			t.Fatalf("N concurrent 401s produced %d /session calls, want exactly one refresh (2 total)", got)
		}
		// The refreshed session is what the store holds.
		tok, err := f.store.Session(ctx, acct)
		if err != nil || tok != "SESSION-0002" {
			t.Fatalf("stored session %q %v", tok, err)
		}
	})

	t.Run("RotatedTokenPersisted", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, withServer(func(o *deptest.Options) { o.RotateEvery = 2 }))
		for range 5 {
			if _, err := f.client.Account(ctx, acct); err != nil {
				t.Fatal(err)
			}
		}
		// Two rotations happened (requests 2 and 4); each was adopted from
		// the response header, persisted, and used by the next call without
		// another /session.
		if f.srv.SessionCalls() != 1 {
			t.Fatalf("session calls = %d, want 1 (rotations adopted, never re-authenticated)", f.srv.SessionCalls())
		}
		tok, _ := f.store.Session(ctx, acct)
		if tok != "SESSION-0003" {
			t.Fatalf("stored session %q, want the twice-rotated token", tok)
		}
		var sessions []string
		for _, r := range f.srv.Requests() {
			if r.Path == dep.PathAccount {
				sessions = append(sessions, r.Session)
			}
		}
		if strings.Join(sessions, ",") != "SESSION-0001,SESSION-0001,SESSION-0002,SESSION-0002,SESSION-0003" {
			t.Fatalf("sessions used: %v", sessions)
		}
		// A second process sharing the store picks the rotated session up
		// without authenticating.
		other, err := dep.NewClient(dep.ClientConfig{Store: f.store, BaseURL: f.srv.URL(), Clock: f.clk})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if f.srv.SessionCalls() != 1 {
			t.Fatalf("second process authenticated: %d", f.srv.SessionCalls())
		}
	})

	t.Run("SecondFailureIsTypedError", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 401}, deptest.Scripted{Status: 401})
		_, err := f.client.Account(ctx, acct)
		var derr *dep.Error
		if !errors.As(err, &derr) || derr.Status != http.StatusUnauthorized {
			t.Fatalf("err = %v, want *dep.Error 401", err)
		}
		if f.srv.SessionCalls() != 2 {
			t.Fatalf("session calls = %d, want the initial one plus exactly one retry", f.srv.SessionCalls())
		}
		// 403 FORBIDDEN and EXPIRED_TOKEN also re-authenticate once and then
		// succeed when the second answer is good.
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 403, Code: dep.CodeForbidden})
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatalf("after FORBIDDEN: %v", err)
		}
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 400, Code: dep.CodeExpiredToken})
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatalf("after EXPIRED_TOKEN: %v", err)
		}
		if f.srv.SessionCalls() != 4 {
			t.Fatalf("session calls = %d", f.srv.SessionCalls())
		}
		// A 403 with another code is not a session problem: no retry.
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 403, Code: "ORG_NOT_SUPPORTED"})
		_, err = f.client.Account(ctx, acct)
		if !errors.As(err, &derr) || derr.Status != 403 || derr.Code != "ORG_NOT_SUPPORTED" || f.srv.SessionCalls() != 4 {
			t.Fatalf("other 403: %v sessions=%d", err, f.srv.SessionCalls())
		}
		// A refresh that itself fails surfaces the session error.
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 401})
		f.srv.SetRejectTokens(true)
		_, err = f.client.Account(ctx, acct)
		if !errors.Is(err, dep.ErrTokenInvalid) {
			t.Fatalf("refresh rejected: %v", err)
		}
	})

	t.Run("SessionStoreFailures", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		failing := &deptest.Failing{Store: f.store, Fail: map[string]error{"Session": errors.New("db down")}}
		c, err := dep.NewClient(dep.ClientConfig{Store: failing, BaseURL: f.srv.URL(), Clock: f.clk})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Account(ctx, acct); err == nil || !strings.Contains(err.Error(), "db down") {
			t.Fatalf("Session failure: %v", err)
		}
		failing.Fail = map[string]error{"SetSession": errors.New("readonly")}
		if _, err := c.Account(ctx, acct); err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("SetSession failure: %v", err)
		}
		// A rotated session that cannot be persisted is an error too.
		f2 := newFixture(t, withServer(func(o *deptest.Options) { o.RotateEvery = 1 }))
		failing2 := &deptest.Failing{Store: f2.store, Fail: map[string]error{"SetSession": errors.New("readonly")}, After: map[string]int{"SetSession": 2}}
		c2, _ := dep.NewClient(dep.ClientConfig{Store: failing2, BaseURL: f2.srv.URL(), Clock: f2.clk})
		if _, err := c2.Account(ctx, acct); err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("rotation persist failure: %v", err)
		}
		// The session response must carry a token.
		f3 := newFixture(t)
		f3.srv.Script(dep.PathSession, deptest.Scripted{Status: 200, Body: `{}`})
		if _, err := f3.client.Account(ctx, acct); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty session token: %v", err)
		}
		f3.srv.Script(dep.PathSession, deptest.Scripted{Status: 200, Body: `not json`})
		if _, err := f3.client.Account(ctx, acct); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("malformed session: %v", err)
		}
		f3.srv.Script(dep.PathSession, deptest.Scripted{Status: 500, Code: "SERVER_ERROR"})
		var derr *dep.Error
		if _, err := f3.client.Account(ctx, acct); !errors.As(err, &derr) || derr.Status != 500 {
			t.Fatalf("session 500: %v", err)
		}
		// A nonce source that fails surfaces.
		f4 := newFixture(t, withClient(func(c *dep.ClientConfig) { c.Nonce = func() (string, error) { return "", errors.New("entropy") } }))
		if _, err := f4.client.Account(ctx, acct); err == nil || !strings.Contains(err.Error(), "entropy") {
			t.Fatalf("nonce failure: %v", err)
		}
	})
}

func TestError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("CodeFromQuotedAndBareBodies", func(t *testing.T) {
		t.Parallel()
		cases := map[string]string{
			"EXPIRED_CURSOR":               dep.CodeExpiredCursor,
			`"EXPIRED_CURSOR"`:             dep.CodeExpiredCursor,
			"  \n\"T_C_NOT_SIGNED\"\n":     dep.CodeTermsNotSigned,
			`{"code":"INVALID_CURSOR"}`:    dep.CodeInvalidCursor,
			`{"error":"FORBIDDEN"}`:        dep.CodeForbidden,
			`{"code":"","error":"X_1"}`:    "X_1",
			"":                             "",
			"   ":                          "",
			"not a code":                   "",
			"{bad json":                    "",
			`{"code":"lowercase"}`:         "",
			`{"message":"no code here"}`:   "",
			`"unterminated`:                "",
			strings.Repeat("A", 65):        "",
			"<html>Service Unavailable</":  "",
			`"USER_AGENT_MISSING"`:         dep.CodeUserAgentMissing,
			`{"code":"APPLE_SEED_FOR_IT"}`: "APPLE_SEED_FOR_IT",
		}
		for body, want := range cases {
			if got := dep.ParseCode([]byte(body)); got != want {
				t.Errorf("ParseCode(%q) = %q, want %q", body, got, want)
			}
		}
		// Against the fake in both forms, with Retry-After and the body kept.
		for _, quoted := range []bool{false, true} {
			f := newFixture(t, withServer(func(o *deptest.Options) { o.QuotedErrors = quoted }))
			_, err := f.client.SyncDevices(ctx, acct, "bogus", 0)
			var derr *dep.Error
			if !errors.As(err, &derr) || derr.Code != dep.CodeInvalidCursor || derr.Status != 400 || len(derr.Body) == 0 {
				t.Fatalf("quoted=%v: %v", quoted, err)
			}
			if derr.Error() != "dep: HTTP 400 INVALID_CURSOR" {
				t.Fatalf("Error() = %q", derr.Error())
			}
			f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 429, RetryAfter: "7"})
			_, err = f.client.Account(ctx, acct)
			if !errors.As(err, &derr) || derr.Status != 429 || derr.RetryAfter != 7*time.Second || derr.Code != "" || derr.Error() != "dep: HTTP 429" {
				t.Fatalf("429: %+v %v", derr, err)
			}
			f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 503, RetryAfter: t0.Add(90 * time.Second).UTC().Format(http.TimeFormat)})
			_, err = f.client.Account(ctx, acct)
			if !errors.As(err, &derr) || derr.RetryAfter != 90*time.Second {
				t.Fatalf("http-date Retry-After: %+v %v", derr, err)
			}
			f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 503, RetryAfter: "garbage"}, deptest.Scripted{Status: 503, RetryAfter: "-4"}, deptest.Scripted{Status: 503, RetryAfter: t0.Add(-time.Hour).UTC().Format(http.TimeFormat)})
			for range 3 {
				_, err = f.client.Account(ctx, acct)
				if !errors.As(err, &derr) || derr.RetryAfter != 0 {
					t.Fatalf("bad Retry-After: %+v %v", derr, err)
				}
			}
		}
	})

	t.Run("ProfileError", func(t *testing.T) {
		t.Parallel()
		err := (&dep.Profile{}).Validate()
		var pe *dep.ProfileError
		if !errors.As(err, &pe) || !errors.Is(err, dep.ErrProfileInvalid) || pe.Code != dep.CodeConfigNameInvalid || !strings.Contains(err.Error(), pe.Code) {
			t.Fatalf("%v", err)
		}
	})
}

func TestTransport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("ChunkedBodyReplay", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.AddDevices(device("C1"))
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		f.srv.InvalidateSessions()
		f.srv.ResetRequests()
		body := `{"devices":["C1"]}`
		// A body reader without GetBody and an unknown length is what a
		// streaming caller hands over; nanodep's make([]byte, 0, -1) panics here.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.client.URL(dep.PathDeviceDetails, nil), io.NopCloser(strings.NewReader(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.ContentLength = -1
		req.GetBody = nil
		var out dep.DeviceDetailsResponse
		if err := f.client.Do(ctx, acct, req, &out); err != nil {
			t.Fatal(err)
		}
		if out.Devices["C1"].ResponseStatus != dep.StatusSuccess {
			t.Fatalf("details: %+v", out)
		}
		var bodies []string
		for _, r := range f.srv.Requests() {
			if r.Path == dep.PathDeviceDetails {
				bodies = append(bodies, string(r.Body))
			}
		}
		if len(bodies) != 2 || bodies[0] != body || bodies[1] != body {
			t.Fatalf("replayed bodies: %q", bodies)
		}
		// GetBody is used when present, and a body over the bound is refused.
		small, _ := dep.NewClient(dep.ClientConfig{Store: f.store, BaseURL: f.srv.URL(), Clock: f.clk, MaxBodyBytes: 4})
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, f.client.URL(dep.PathDeviceDetails, nil), io.NopCloser(strings.NewReader(body)))
		if err := small.Do(ctx, acct, req, nil); !errors.Is(err, dep.ErrBodyTooLarge) {
			t.Fatalf("over bound: %v", err)
		}
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, f.client.URL(dep.PathDeviceDetails, nil), strings.NewReader(body))
		if err := small.Do(ctx, acct, req, &out); err != nil {
			t.Fatalf("GetBody path over the bound: %v", err)
		}
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, f.client.URL(dep.PathDeviceDetails, nil), io.NopCloser(iotest{}))
		if err := f.client.Do(ctx, acct, req, nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("unreadable body: %v", err)
		}
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, f.client.URL(dep.PathDeviceDetails, nil), strings.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("gone") }
		if err := f.client.Do(ctx, acct, req, nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("GetBody failure: %v", err)
		}
	})

	t.Run("ProtocolVersionHeader", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		f.putAccount(func(a *dep.Account) { a.ProtocolVersion = 7 })
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		var versions []string
		for _, r := range f.srv.Requests() {
			if r.Path == dep.PathAccount {
				versions = append(versions, r.Header.Get(dep.HeaderProtocolVersion))
			}
		}
		if strings.Join(versions, ",") != "10,7" {
			t.Fatalf("protocol versions: %v", versions)
		}
	})

	t.Run("HeadersAlwaysSet", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, withClient(func(c *dep.ClientConfig) { c.UserAgent = "custom-agent/2" }))
		f.srv.AddDevices(device("H1"))
		if _, err := f.client.DeviceDetails(ctx, acct, []string{"H1"}); err != nil {
			t.Fatal(err)
		}
		for _, r := range f.srv.Requests() {
			if r.Header.Get("User-Agent") != "custom-agent/2" {
				t.Errorf("%s: User-Agent %q", r, r.Header.Get("User-Agent"))
			}
			if r.Path == dep.PathSession {
				if r.Header.Get("Authorization") == "" {
					t.Errorf("session without Authorization")
				}
				continue
			}
			if r.Header.Get("Accept") != "application/json;charset=UTF8" || r.Session == "" {
				t.Errorf("%s: headers %v", r, r.Header)
			}
			if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json;charset=UTF8" {
				t.Errorf("%s: Content-Type %q", r, r.Header.Get("Content-Type"))
			}
		}
		// The default agent is never empty: the fake would answer
		// USER_AGENT_MISSING.
		f2 := newFixture(t)
		if _, err := f2.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if ua := f2.srv.Requests()[0].Header.Get("User-Agent"); ua != dep.DefaultUserAgent {
			t.Fatalf("default agent %q", ua)
		}
	})

	t.Run("ClientFailures", func(t *testing.T) {
		t.Parallel()
		if _, err := dep.NewClient(dep.ClientConfig{}); !errors.Is(err, dep.ErrConfig) {
			t.Fatalf("nil store: %v", err)
		}
		if _, err := dep.NewClient(dep.ClientConfig{Store: inmem.New(), BaseURL: "::not a url"}); !errors.Is(err, dep.ErrConfig) {
			t.Fatalf("bad URL: %v", err)
		}
		if _, err := dep.NewClient(dep.ClientConfig{Store: inmem.New(), BaseURL: "relative/path"}); !errors.Is(err, dep.ErrConfig) {
			t.Fatalf("relative URL: %v", err)
		}
		f := newFixture(t)
		if f.client.Store() != f.store {
			t.Fatal("Store() is not the store given")
		}
		if _, err := f.client.Account(ctx, ""); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty account: %v", err)
		}
		if _, err := f.client.Account(ctx, "unknown"); !errors.Is(err, dep.ErrNotFound) {
			t.Fatalf("unknown account: %v", err)
		}
		f.putAccount(func(a *dep.Account) { a.AccessSecret = "" })
		if _, err := f.client.Account(ctx, acct); !errors.Is(err, dep.ErrNoTokens) {
			t.Fatalf("no tokens: %v", err)
		}
		f.putAccount()
		// A 2xx body that is not JSON is ErrInvalid; an empty 2xx body with
		// an output is fine.
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 200, Body: "<html>"})
		if _, err := f.client.Account(ctx, acct); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("non-JSON body: %v", err)
		}
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 204})
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatalf("empty body: %v", err)
		}
		// A request that cannot be built or sent surfaces.
		if _, err := f.client.NewRequest(ctx, "BAD METHOD", "/x", nil, nil); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("bad method: %v", err)
		}
		if _, err := f.client.NewRequest(ctx, http.MethodPost, "/x", nil, make(chan int)); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("unmarshalable body: %v", err)
		}
		dead := newFixture(t)
		dead.srv.Close()
		if _, err := dead.client.Account(ctx, acct); err == nil || errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("closed server: %v", err)
		}
		// A dead server after the session was obtained fails in Do itself.
		f3 := newFixture(t)
		if _, err := f3.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		f3.srv.Close()
		if _, err := f3.client.Account(ctx, acct); err == nil {
			t.Fatal("closed server after session: no error")
		}
		// GetAccount failures propagate.
		failing := &deptest.Failing{Store: f.store, Fail: map[string]error{"GetAccount": errors.New("db down")}}
		c, _ := dep.NewClient(dep.ClientConfig{Store: failing, BaseURL: f.srv.URL(), Clock: f.clk})
		if _, err := c.Account(ctx, acct); err == nil || !strings.Contains(err.Error(), "db down") {
			t.Fatalf("GetAccount failure: %v", err)
		}
	})
}

// iotest is a reader that always fails.
type iotest struct{}

func (iotest) Read([]byte) (int, error) { return 0, errors.New("read failed") }
