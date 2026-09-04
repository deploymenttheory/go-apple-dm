package dep_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep/deptest"
)

func TestToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("ExpiredFailsFast", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(t0.Add(-time.Second)) })
		if _, err := f.client.Account(ctx, acct); !errors.Is(err, dep.ErrTokenExpired) {
			t.Fatalf("err = %v", err)
		}
		if n := len(f.srv.Requests()); n != 0 {
			t.Fatalf("%d HTTP calls were made for an expired token", n)
		}
		// Exactly at expiry is expired too; one second before is not.
		f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(t0) })
		if _, err := f.client.Account(ctx, acct); !errors.Is(err, dep.ErrTokenExpired) {
			t.Fatalf("at expiry: %v", err)
		}
		f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(t0.Add(time.Second)) })
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatalf("before expiry: %v", err)
		}
		// StoreTokens refuses an expired token before any call as well.
		tokens := f.srv.Tokens()
		tokens.AccessTokenExpiry = dep.Time(t0.Add(-time.Hour))
		f.srv.ResetRequests()
		if _, err := f.client.StoreTokens(ctx, "new", tokens); !errors.Is(err, dep.ErrTokenExpired) {
			t.Fatalf("StoreTokens expired: %v", err)
		}
		if len(f.srv.Requests()) != 0 {
			t.Fatal("StoreTokens called the service with an expired token")
		}
	})

	t.Run("ExpiringEvent", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, withClient(func(c *dep.ClientConfig) { c.ExpiryWarning = 10 * 24 * time.Hour; c.ExpiryWarningInterval = time.Hour }))
		f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(t0.Add(5 * 24 * time.Hour)) })
		for range 3 {
			if _, err := f.client.Account(ctx, acct); err != nil {
				t.Fatal(err)
			}
		}
		evs := f.eventsOf(dep.EventTokenExpiring)
		if len(evs) != 1 {
			t.Fatalf("expiring events = %d, want 1 inside the interval", len(evs))
		}
		data, ok := evs[0].Data.(dep.TokenExpiringEvent)
		if !ok || data.Account != acct || !data.Expiry.Equal(t0.Add(5*24*time.Hour)) || evs[0].Actor != dep.Actor {
			t.Fatalf("event: %+v", evs[0])
		}
		f.clock.Advance(2 * time.Hour)
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if len(f.eventsOf(dep.EventTokenExpiring)) != 2 {
			t.Fatal("no second warning after the interval")
		}
		// Outside the window nothing is published.
		f.resetEvents()
		f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(t0.Add(60 * 24 * time.Hour)) })
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if len(f.eventsOf(dep.EventTokenExpiring)) != 0 {
			t.Fatal("warning outside the window")
		}
		// Without a bus nothing is published and nothing breaks.
		quiet, _ := dep.NewClient(dep.ClientConfig{Store: f.store, BaseURL: f.srv.URL(), Clock: f.clk})
		f.putAccount(func(a *dep.Account) { a.AccessTokenExpiry = dep.Time(f.clk.Now().Add(time.Hour)) })
		if _, err := quiet.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("TermsNotSignedState", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.SetTermsNotSigned(true)
		_, err := f.client.Account(ctx, acct)
		var derr *dep.Error
		if !errors.Is(err, dep.ErrTermsNotSigned) || !errors.As(err, &derr) || derr.Code != dep.CodeTermsNotSigned {
			t.Fatalf("err = %v", err)
		}
		if st := f.account().State; st != (dep.AccountState{TermsExpired: true}) {
			t.Fatalf("state = %+v", st)
		}
		// StoreTokens on the existing account records the same state and
		// writes nothing else.
		if _, err := f.client.StoreTokens(ctx, acct, f.srv.Tokens()); !errors.Is(err, dep.ErrTermsNotSigned) {
			t.Fatalf("StoreTokens: %v", err)
		}
		if f.account().OrgName != "" {
			t.Fatal("StoreTokens wrote account detail despite T_C_NOT_SIGNED")
		}
		// 401 from /session records TokenInvalid alongside.
		f.srv.SetTermsNotSigned(false)
		f.srv.SetRejectTokens(true)
		if _, err := f.client.Account(ctx, acct); !errors.Is(err, dep.ErrTokenInvalid) {
			t.Fatalf("rejected: %v", err)
		}
		if st := f.account().State; st != (dep.AccountState{TermsExpired: true, TokenInvalid: true}) {
			t.Fatalf("state = %+v", st)
		}
		if _, err := f.client.StoreTokens(ctx, acct, f.srv.Tokens()); !errors.Is(err, dep.ErrTokenInvalid) {
			t.Fatalf("StoreTokens rejected: %v", err)
		}
		// Recording the state can itself fail; the cause still surfaces.
		failing := &deptest.Failing{Store: f.store, Fail: map[string]error{"SetAccountState": errors.New("readonly")}}
		c, _ := dep.NewClient(dep.ClientConfig{Store: failing, BaseURL: f.srv.URL(), Clock: f.clk})
		f.putAccount()
		_, err = c.Account(ctx, acct)
		if !errors.Is(err, dep.ErrTokenInvalid) || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("state write failure: %v", err)
		}
		if _, err := c.StoreTokens(ctx, acct, f.srv.Tokens()); !errors.Is(err, dep.ErrTokenInvalid) || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("StoreTokens state write failure: %v", err)
		}
	})

	t.Run("StateClearsOnlyOnSuccess", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.srv.SetTermsNotSigned(true)
		if _, err := f.client.Account(ctx, acct); !errors.Is(err, dep.ErrTermsNotSigned) {
			t.Fatal(err)
		}
		// A transient failure of /session is not a verdict: the flag stays.
		f.srv.SetTermsNotSigned(false)
		f.srv.Script(dep.PathSession, deptest.Scripted{Status: 503})
		if _, err := f.client.Account(ctx, acct); err == nil || errors.Is(err, dep.ErrTermsNotSigned) {
			t.Fatalf("503: %v", err)
		}
		if st := f.account().State; !st.TermsExpired {
			t.Fatalf("state cleared by a 503: %+v", st)
		}
		// Only a 200 with a token clears it.
		if _, err := f.client.Account(ctx, acct); err != nil {
			t.Fatal(err)
		}
		if st := f.account().State; st != (dep.AccountState{}) {
			t.Fatalf("state after success: %+v", st)
		}
		// Clearing the state can fail; the session is not returned then.
		f.srv.SetRejectTokens(true)
		if _, err := f.client.Account(ctx, acct); !errors.Is(err, dep.ErrTokenInvalid) {
			t.Fatal(err)
		}
		f.srv.SetRejectTokens(false)
		failing := &deptest.Failing{Store: f.store, Fail: map[string]error{"SetAccountState": errors.New("readonly")}}
		c, _ := dep.NewClient(dep.ClientConfig{Store: failing, BaseURL: f.srv.URL(), Clock: f.clk})
		if _, err := c.Account(ctx, acct); err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("clear failure: %v", err)
		}
		if !f.account().State.TokenInvalid {
			t.Fatal("state cleared despite the write failing")
		}
	})

	t.Run("ValidatedWithAccountOnStore", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, withoutAccount)
		if _, err := f.client.StoreTokens(ctx, "", f.srv.Tokens()); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("empty name: %v", err)
		}
		if _, err := f.client.StoreTokens(ctx, "fresh", dep.Tokens{ConsumerKey: "only"}); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("incomplete tokens: %v", err)
		}
		if len(f.srv.Requests()) != 0 {
			t.Fatal("invalid tokens reached the service")
		}
		// A /account failure writes nothing.
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 500})
		var derr *dep.Error
		if _, err := f.client.StoreTokens(ctx, "fresh", f.srv.Tokens()); !errors.As(err, &derr) || derr.Status != 500 {
			t.Fatalf("account 500: %v", err)
		}
		f.srv.Script(dep.PathAccount, deptest.Scripted{Status: 200, Body: "<html>"})
		if _, err := f.client.StoreTokens(ctx, "fresh", f.srv.Tokens()); !errors.Is(err, dep.ErrInvalid) {
			t.Fatalf("account non-JSON: %v", err)
		}
		if _, err := f.store.GetAccount(ctx, "fresh"); !errors.Is(err, dep.ErrNotFound) {
			t.Fatal("account written despite validation failing")
		}
		// T_C_NOT_SIGNED for a new account surfaces and writes nothing.
		f.srv.SetTermsNotSigned(true)
		if _, err := f.client.StoreTokens(ctx, "fresh", f.srv.Tokens()); !errors.Is(err, dep.ErrTermsNotSigned) {
			t.Fatalf("terms: %v", err)
		}
		f.srv.SetTermsNotSigned(false)
		if _, err := f.store.GetAccount(ctx, "fresh"); !errors.Is(err, dep.ErrNotFound) {
			t.Fatal("account written despite T_C_NOT_SIGNED")
		}
		// Success records the detail and the session.
		detail, err := f.client.StoreTokens(ctx, "fresh", f.srv.Tokens())
		if err != nil {
			t.Fatal(err)
		}
		if detail.OrgName != "Deployment Theory" || detail.ServerUUID != "SERVER-UUID-DEPTEST" || detail.Limits()[dep.PathSyncDevices].Maximum != 1000 {
			t.Fatalf("detail: %+v", detail)
		}
		a, err := f.store.GetAccount(ctx, "fresh")
		if err != nil {
			t.Fatal(err)
		}
		if a.OrgName != detail.OrgName || a.ServerUUID != detail.ServerUUID || a.AdminID != detail.AdminID || a.OrgID != detail.OrgID || a.ServerName != detail.ServerName || a.Limits[dep.PathFetchDevices].Maximum != 1000 || !sameTokens(a.Tokens(), f.srv.Tokens()) || a.State != (dep.AccountState{}) || !a.CreatedAt.Equal(t0) {
			t.Fatalf("stored account: %+v", a)
		}
		if tok, _ := f.store.Session(ctx, "fresh"); tok == "" {
			t.Fatal("session not persisted")
		}
		// The stored session is reused: the next call authenticates nothing.
		calls := f.srv.SessionCalls()
		if _, err := f.client.Account(ctx, "fresh"); err != nil {
			t.Fatal(err)
		}
		if f.srv.SessionCalls() != calls {
			t.Fatalf("session calls = %d, want %d (stored session reused)", f.srv.SessionCalls(), calls)
		}
		// Storing again updates the existing account in place.
		f.srv.SetAccount(dep.AccountDetail{OrgName: "Renamed"})
		if _, err := f.client.StoreTokens(ctx, "fresh", f.srv.Tokens()); err != nil {
			t.Fatal(err)
		}
		if a, _ := f.store.GetAccount(ctx, "fresh"); a.OrgName != "Renamed" || !a.CreatedAt.Equal(t0) || len(a.Limits) != 0 {
			t.Fatalf("updated account: %+v", a)
		}
		// Store failures surface: reading the existing account and writing.
		failing := &deptest.Failing{Store: f.store, Fail: map[string]error{"GetAccount": errors.New("db down")}}
		c, _ := dep.NewClient(dep.ClientConfig{Store: failing, BaseURL: f.srv.URL(), Clock: f.clk})
		if _, err := c.StoreTokens(ctx, "fresh", f.srv.Tokens()); err == nil || !strings.Contains(err.Error(), "db down") {
			t.Fatalf("GetAccount failure: %v", err)
		}
		failing.Fail = map[string]error{"PutAccount": errors.New("readonly")}
		if _, err := c.StoreTokens(ctx, "fresh", f.srv.Tokens()); err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("PutAccount failure: %v", err)
		}
		failing.Fail = map[string]error{"SetSession": errors.New("readonly")}
		if _, err := c.StoreTokens(ctx, "fresh", f.srv.Tokens()); err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("SetSession failure: %v", err)
		}
		// A dead service is a transport error.
		f.srv.Close()
		if _, err := f.client.StoreTokens(ctx, "fresh2", f.srv.Tokens()); err == nil {
			t.Fatal("dead service: no error")
		}
	})
}

// sameTokens compares credentials by value; AccessTokenExpiry is a pointer.
func sameTokens(a, b dep.Tokens) bool {
	if a.ConsumerKey != b.ConsumerKey || a.ConsumerSecret != b.ConsumerSecret || a.AccessToken != b.AccessToken || a.AccessSecret != b.AccessSecret {
		return false
	}
	switch {
	case a.AccessTokenExpiry == nil && b.AccessTokenExpiry == nil:
		return true
	case a.AccessTokenExpiry == nil || b.AccessTokenExpiry == nil:
		return false
	}
	return a.AccessTokenExpiry.Equal(*b.AccessTokenExpiry)
}
