package app

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
)

type adminThrottleTransport struct{ next setupRoundTrip }

// RoundTrip supplies independent provider responses through a stable cached transport.
func (t *adminThrottleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return t.next(r) }

// TestNamedAXMThrottleGuidance checks authorized HTTP timing, redaction and uncertain provisioning retention.
func TestNamedAXMThrottleGuidance(t *testing.T) {
	for _, endpoint := range []string{"token", "api"} {
		t.Run(endpoint, func(t *testing.T) {
			tokens, api := 0, 0
			transport := &adminThrottleTransport{next: func(r *http.Request) (*http.Response, error) {
				status, body := 429, `{"errors":[{"detail":"private-diagnostic"}]}`
				if r.URL.Path == "/token" {
					tokens++
					if endpoint == "api" {
						status, body = 200, `{"access_token":"private-token","expires_in":3600}`
					} else {
						body = `{"error":"invalid_request","error_description":"private-diagnostic"}`
					}
				} else {
					api++
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			}}
			a, err := Build(t.Context(), Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "server.db"), StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, BootstrapToken: "bootstrap-secret", AxM: AxMConfig{HTTPClient: &http.Client{Transport: transport}}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			// #nosec G101 -- In-process HTTPS fixture endpoints; key material is generated per test.
			_, err = a.Inventory.SaveAccount(t.Context(), inventory.Account{ID: "apple", Name: "Apple", ClientID: "BUSINESSAPI.fixture", KeyID: "kid", Enabled: true, BaseURL: "https://api.example", TokenURL: "https://auth.example/token"}, accountTestKey(t), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			root := bootstrapRBAC(t, a)
			policy := `{"Source":"permit(principal, action in [MDM::Action::\"readBusinessManager\",MDM::Action::\"manageAxMAccounts\"], resource);"}`
			if w := rbacRequest(a, "PUT", "/policies/throttle", root, policy); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			w := rbacRequest(a, "POST", "/axm/accounts/apple/webhooks", root, `{"name":"unique","enabled":true,"event_types":["ORG_DEVICE_ATTRIBUTE_CHANGED"],"public_url":"https://receiver.example"}`)
			if w.Code != 429 || w.Header().Get("Retry-After") != "3600" || !strings.Contains(w.Body.String(), "rate_limited") || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("status/header/redaction: %d %v %s", w.Code, w.Header(), w.Body.String())
			}
			if tokens != 1 || (endpoint == "token" && api != 0) || (endpoint == "api" && api != 1) {
				t.Fatalf("unexpected protected mutation: token=%d api=%d", tokens, api)
			}
			before := tokens + api
			w = rbacRequest(a, "GET", "/axm/accounts/apple/webhooks", root, "")
			if w.Code != 429 || w.Header().Get("Retry-After") != "3600" || tokens+api != before {
				t.Fatal("named client lost cooldown", w.Code, tokens, api)
			}
			bindings, err := a.AXMWebhooks.Bindings(t.Context(), "apple", "")
			if err != nil || len(bindings) != 1 || bindings[0].State != "uncertain" {
				t.Fatal("failed create lost its reconciliation intent", bindings, err)
			}
		})
	}
}

// TestAXMThrottleHeaderMapping emits only positive numeric guidance from structured errors.
func TestAXMThrottleHeaderMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		header bool
	}{
		{"token fallback", &axm.AuthError{Status: 429, RetryAfter: 2 * time.Second, Description: "private"}, true},
		{"API fallback", &axm.Error{Status: 429, RetryAfter: 2 * time.Second, Body: []byte("private")}, true},
		{"no guidance", &axm.AuthError{Status: 429, Description: "private"}, false},
		{"expired guidance", &axm.RateLimitError{Endpoint: "token", RetryAt: time.Now().Add(-time.Hour), Err: &axm.AuthError{Status: 429}}, false},
		{"subsecond", &axm.RateLimitError{Endpoint: "api", RetryAt: time.Now().Add(500 * time.Millisecond), Err: &axm.Error{Status: 429}}, true},
		{"maximum", &axm.RateLimitError{Endpoint: "token", RetryAt: time.Now().Add(time.Duration(1<<63 - 1)), Err: &axm.AuthError{Status: 429}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			inventoryError(w, accountFailure(errors.Join(errors.New("wrapped"), tc.cause)))
			value := w.Header().Get("Retry-After")
			if w.Code != 429 || (value != "") != tc.header || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("status=%d header=%q body=%s", w.Code, value, w.Body.String())
			}
			if value != "" {
				seconds, err := strconv.ParseInt(value, 10, 64)
				if err != nil || seconds < 1 {
					t.Fatal("invalid retry guidance", value, err)
				}
			}
		})
	}
}
