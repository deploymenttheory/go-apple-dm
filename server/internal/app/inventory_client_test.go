package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

type tokenBudgetTransport struct {
	base  http.RoundTripper
	url   string
	count atomic.Int64
}

// RoundTrip models an OAuth endpoint that throttles redundant token exchanges.
func (t *tokenBudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() == t.url && t.count.Add(1) > 1 {
		return &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_request"}`)), Request: r}, nil
	}
	return t.base.RoundTrip(r)
}

// TestInventoryClientReusesTokensAcrossRequests models the real Apple throttle exposed by webhook acceptance.
func TestInventoryClientReusesTokensAcrossRequests(t *testing.T) {
	remote := axmtest.NewServer()
	t.Cleanup(remote.Close)
	key := accountTestKey(t)
	parsed, err := axm.ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	remote.RegisterKey("BUSINESSAPI.webhooks", "kid", &parsed.PublicKey)
	budget := &tokenBudgetTransport{base: remote.Client().Transport, url: remote.TokenURL}
	remote.Client().Transport = budget
	a := &App{cfg: Config{AxM: AxMConfig{HTTPClient: remote.Client()}}}
	account := inventory.Account{ID: "account", Revision: 1, CreatedAt: time.Now().UTC(), ClientID: "BUSINESSAPI.webhooks", KeyID: "kid", BaseURL: remote.URL, TokenURL: remote.TokenURL}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			client, err := a.inventoryClient(t.Context(), account, key)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err = client.ListWebhooks(t.Context(), axm.ListOptions{}); err != nil {
				t.Error("redundant token exchange reached provider throttle")
			}
		})
	}
	wg.Wait()
	if got := budget.count.Load(); got != 1 {
		t.Fatal("excess OAuth requests", got)
	}
}

// TestInventoryClientCacheFences keeps tokens scoped to credential identity and current outbound configuration.
func TestInventoryClientCacheFences(t *testing.T) {
	key := accountTestKey(t)
	base := inventory.Account{ID: "account", Revision: 1, CreatedAt: time.Now().UTC(), ClientID: "BUSINESSAPI.webhooks", KeyID: "kid"}
	for _, change := range []string{"account", "revision", "incarnation", "client", "key-id", "scope", "endpoint", "token-endpoint", "key"} {
		t.Run(change, func(t *testing.T) {
			a := &App{}
			first, err := a.inventoryClient(t.Context(), base, key)
			if err != nil {
				t.Fatal(err)
			}
			next := base
			changedKey := key
			switch change {
			case "account":
				next.ID = "other"
			case "revision":
				next.Revision++
			case "incarnation":
				next.CreatedAt = next.CreatedAt.Add(time.Second)
			case "client":
				next.ClientID = "BUSINESSAPI.other"
			case "key-id":
				next.KeyID = "other"
			case "scope":
				next.Scope = axm.ScopeSchool
			case "endpoint":
				next.BaseURL = "https://api.example"
			case "token-endpoint":
				next.TokenURL = "https://auth.example/token"
			case "key":
				changedKey = accountTestKey(t)
			}
			second, err := a.inventoryClient(t.Context(), next, changedKey)
			if err != nil || second == first {
				t.Fatal("credential identity reused another token cache", err)
			}
		})
	}
	a := &App{}
	first, err := a.inventoryClient(t.Context(), base, key)
	if err != nil {
		t.Fatal(err)
	}
	observed := base
	now := time.Now()
	observed.VerifiedAt = &now
	if same, err := a.inventoryClient(t.Context(), observed, key); err != nil || same != first {
		t.Fatal("verification observation invalidated credentials", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.inventoryClient(ctx, base, key); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled factory context accepted", err)
	}
	if _, err := a.inventoryClient(t.Context(), base, []byte("invalid")); err == nil {
		t.Fatal("invalid key was cached")
	}
	if same, err := a.inventoryClient(t.Context(), base, key); err != nil || same != first {
		t.Fatal("failed replacement poisoned cache", err)
	}
	a.cfg.AxM.RootCAFile = filepath.Join(t.TempDir(), "missing-ca.pem")
	if _, err := a.inventoryClient(t.Context(), base, key); err == nil {
		t.Fatal("invalid outbound trust accepted")
	}
}

// TestInventoryClientCacheBoundAndTransport covers bounded retention and custom transport identities.
func TestInventoryClientCacheBoundAndTransport(t *testing.T) {
	a := &App{}
	key := accountTestKey(t)
	account := inventory.Account{ID: "warm", ClientID: "BUSINESSAPI.webhooks", KeyID: "kid"}
	for i := range inventoryClientLimit + 1 {
		account.ID = fmt.Sprintf("account-%03d", i)
		if _, err := a.inventoryClient(t.Context(), account, key); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.inventoryClients.entries) != inventoryClientLimit {
		t.Fatal("client retention is unbounded")
	}
	if _, ok := a.inventoryClients.entries["account-000"]; ok {
		t.Fatal("least recently used client was retained")
	}
	account.ID = "account-001"
	if _, err := a.inventoryClient(t.Context(), account, key); err != nil {
		t.Fatal(err)
	}
	account.ID = "extra"
	if _, err := a.inventoryClient(t.Context(), account, key); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.inventoryClients.entries["account-001"]; !ok {
		t.Fatal("recently used client was evicted")
	}
	client := &http.Client{Transport: &http.Transport{}}
	a.cfg.AxM.HTTPClient = client
	first, err := a.inventoryClient(t.Context(), account, key)
	if err != nil {
		t.Fatal(err)
	}
	client.Transport = &http.Transport{}
	second, err := a.inventoryClient(t.Context(), account, key)
	if err != nil || second == first {
		t.Fatal("transport replacement reused a stale client", err)
	}
	client.Transport = setupRoundTrip(func(*http.Request) (*http.Response, error) { return nil, io.EOF })
	first, err = a.inventoryClient(t.Context(), account, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err = a.inventoryClient(t.Context(), account, key)
	if err != nil || second == first {
		t.Fatal("function transport identity was assumed comparable", err)
	}
	if sameTransport(nil, client.Transport) || sameTransport(&http.Transport{}, client.Transport) {
		t.Fatal("unrelated transports compared equal")
	}
}

// TestAXMTokenThrottleClassification preserves provider throttling without disclosing OAuth diagnostics.
func TestAXMTokenThrottleClassification(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &axm.AuthError{Status: 429, Code: "invalid_request", Description: "private-provider-diagnostic"})
	mapped := accountFailure(err)
	if mapped.status != 429 || mapped.category != "rate_limited" || strings.Contains(mapped.Error(), "private-provider") {
		t.Fatal("OAuth throttle was misclassified", mapped)
	}
}
