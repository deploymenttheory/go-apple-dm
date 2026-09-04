package deptest_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/server/depstore/inmem"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// TestServer proves the fake's own contract from the outside: OAuth 1.0a
// verification, session enforcement, cursor ageing, and scripted errors.
func TestServer(t *testing.T) {
	ctx := context.Background()
	newClient := func(t *testing.T, srv *deptest.Server, clk clock.Clock) (*dep.Client, *inmem.Store) {
		t.Helper()
		st := inmem.New()
		c, err := dep.NewClient(dep.ClientConfig{Store: st, BaseURL: srv.URL(), Clock: clk})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.StoreTokens(ctx, "acct", srv.Tokens()); err != nil {
			t.Fatal(err)
		}
		return c, st
	}
	t.Run("OAuthVerification", func(t *testing.T) {
		clk := clock.NewFake(t0)
		srv := deptest.NewServer(deptest.Options{Clock: clk})
		defer srv.Close()
		// No credentials at all.
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL()+"/session", http.NoBody)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("no auth = %d", res.StatusCode)
		}
		// Wrong secret.
		bad := srv.Tokens()
		bad.ConsumerSecret = "wrong"
		st := inmem.New()
		c, _ := dep.NewClient(dep.ClientConfig{Store: st, BaseURL: srv.URL(), Clock: clk})
		if _, err := c.StoreTokens(ctx, "acct", bad); !errors.Is(err, dep.ErrTokenInvalid) {
			t.Fatalf("wrong secret = %v", err)
		}
		// A request without a session is refused; with one it works.
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, srv.URL()+"/account", http.NoBody)
		res, _ = http.DefaultClient.Do(req)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("no session = %d", res.StatusCode)
		}
		good, _ := newClient(t, srv, clk)
		if _, err := good.Account(ctx, "acct"); err != nil {
			t.Fatal(err)
		}
		if srv.SessionCalls() != 1 {
			t.Fatalf("session calls = %d", srv.SessionCalls())
		}
		// Invalidated sessions force one re-authentication.
		srv.InvalidateSessions()
		if _, err := good.Account(ctx, "acct"); err != nil {
			t.Fatal(err)
		}
		if srv.SessionCalls() != 2 {
			t.Fatalf("session calls after invalidation = %d", srv.SessionCalls())
		}
	})
	t.Run("CursorAgeing", func(t *testing.T) {
		clk := clock.NewFake(t0)
		srv := deptest.NewServer(deptest.Options{Clock: clk})
		defer srv.Close()
		c, _ := newClient(t, srv, clk)
		srv.AddDevices(dep.Device{SerialNumber: "A"})
		page, err := c.FetchDevices(ctx, "acct", "", 0)
		if err != nil || len(page.Devices) != 1 || page.Cursor == "" {
			t.Fatalf("fetch = %+v %v", page, err)
		}
		if _, err := c.SyncDevices(ctx, "acct", page.Cursor, 0); err != nil {
			t.Fatalf("sync: %v", err)
		}
		clk.Advance(8 * 24 * time.Hour)
		var derr *dep.Error
		if _, err := c.SyncDevices(ctx, "acct", page.Cursor, 0); !errors.As(err, &derr) || derr.Code != dep.CodeExpiredCursor {
			t.Fatalf("aged cursor = %v", err)
		}
		if _, err := c.SyncDevices(ctx, "acct", "nope", 0); !errors.As(err, &derr) || derr.Code != dep.CodeInvalidCursor {
			t.Fatalf("unknown cursor = %v", err)
		}
	})
	t.Run("ScriptedAnswers", func(t *testing.T) {
		clk := clock.NewFake(t0)
		srv := deptest.NewServer(deptest.Options{Clock: clk, QuotedErrors: true})
		defer srv.Close()
		c, _ := newClient(t, srv, clk)
		srv.Script(dep.PathAccount, deptest.Scripted{Status: 429, RetryAfter: "7"}, deptest.Scripted{Status: 400, Code: dep.CodeUserAgentInvalid})
		var derr *dep.Error
		if _, err := c.Account(ctx, "acct"); !errors.As(err, &derr) || derr.Status != http.StatusTooManyRequests || derr.RetryAfter != 7*time.Second {
			t.Fatalf("429 = %v", err)
		}
		if _, err := c.Account(ctx, "acct"); !errors.As(err, &derr) || derr.Code != dep.CodeUserAgentInvalid {
			t.Fatalf("quoted code = %v", err)
		}
		if _, err := c.Account(ctx, "acct"); err != nil {
			t.Fatalf("scripts exhausted: %v", err)
		}
		if n := srv.Count(http.MethodGet, dep.PathAccount); n < 3 {
			t.Fatalf("request log = %d", n)
		}
		found := false
		for _, r := range srv.Requests() {
			if r.Path == dep.PathAccount && strings.Contains(r.Header.Get("User-Agent"), "go-apple-dm") {
				found = true
			}
		}
		if !found {
			t.Fatal("User-Agent not recorded")
		}
	})
}
