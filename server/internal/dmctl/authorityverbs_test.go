package dmctl_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/adminclient"
)

func TestAuthorityCommandRequests(t *testing.T) {
	for _, tc := range []struct {
		args                 []string
		method, path, token  string
		body                 map[string]any
		response, wantOutput string
	}{
		{args: []string{"roles", "list", "-cursor", "previous"}, method: "GET", path: "/roles?cursor=previous", response: `{"Items":[{"Name":"reader"}]}`, wantOutput: "reader"},
		{args: []string{"roles", "get", "reader"}, method: "GET", path: "/roles/reader", response: `{"Name":"reader"}`, wantOutput: "reader"},
		{args: []string{"roles", "put", "reader", "-description", "Inventory access"}, method: "PUT", path: "/roles/reader", body: map[string]any{"Description": "Inventory access"}, response: `{"Name":"reader"}`, wantOutput: "reader"},
		{args: []string{"roles", "delete", "reader"}, method: "DELETE", path: "/roles/reader"},
		{args: []string{"auth", "me"}, method: "GET", path: "/auth/me", response: `{"Name":"operator"}`, wantOutput: "operator"},
		{args: []string{"auth", "schema"}, method: "GET", path: "/schema", response: `{"MDM":{"actions":{}}}`, wantOutput: "MDM"},
		{args: []string{"auth", "bootstrap", "first", "-bootstrap-token", "bootstrap-secret", "-expires", "2030-01-01T00:00:00Z"}, method: "POST", path: "/auth/bootstrap", token: "bootstrap-secret", body: map[string]any{"Name": "first", "ExpiresAt": "2030-01-01T00:00:00Z"}, response: `{"Principal":{"Name":"first"},"Token":"issued-secret"}`, wantOutput: "issued-secret"},
		{args: []string{"auth", "bootstrap", "first"}, method: "POST", path: "/auth/bootstrap", body: map[string]any{"Name": "first"}, response: `{"Token":"issued-secret"}`, wantOutput: "issued-secret"},
		{args: []string{"policies", "activate", "inventory"}, method: "POST", path: "/policies/inventory/activation", body: map[string]any{"Active": true}, response: `{"Active":true}`, wantOutput: "true"},
		{args: []string{"policies", "deactivate", "inventory"}, method: "POST", path: "/policies/inventory/activation", body: map[string]any{"Active": false}, response: `{"Active":false}`, wantOutput: "false"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				token := tc.token
				if token == "" {
					token = "operator"
				}
				if r.Method != tc.method || r.URL.RequestURI() != "/admin/v1"+tc.path || r.Header.Get("Authorization") != "Bearer "+token {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if tc.body != nil {
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, tc.body) {
						t.Errorf("body: %+v, %v; want %+v", got, err, tc.body)
					}
				}
				if tc.response == "" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			t.Cleanup(srv.Close)
			args := append([]string{"-server", srv.URL, "-token", "operator"}, tc.args...)
			out, _, err := run(t, noConfig(t), args...)
			if err != nil || calls.Load() != 1 || !strings.Contains(out, tc.wantOutput) {
				t.Fatalf("output %q, requests %d, error %v", out, calls.Load(), err)
			}
		})
	}
}

func TestAuthorityCommandsRejectInputWithoutRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{
		{"roles"},
		{"roles", "unknown"},
		{"roles", "list", "extra"},
		{"roles", "get"},
		{"roles", "put", "one", "two"},
		{"roles", "delete"},
		{"roles", "list", "-unknown"},
		{"auth"},
		{"auth", "unknown"},
		{"auth", "me", "extra"},
		{"auth", "schema", "extra"},
		{"auth", "bootstrap"},
		{"auth", "bootstrap", "-unknown"},
		{"auth", "recover-root"},
		{"auth", "recover-root", "-unknown"},
		{"policies", "activate"},
		{"policies", "deactivate"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, err := run(t, noConfig(t), append([]string{"-server", srv.URL, "-token", "operator"}, args...)...)
			if !errors.Is(err, dmctl.ErrUsage) {
				t.Fatalf("expected usage error, got %v", err)
			}
		})
	}
	if _, _, err := run(t, noConfig(t), "-server", srv.URL, "auth", "bootstrap", "first", "-expires", "not-a-date"); err == nil {
		t.Fatal("invalid expiration accepted")
	}
	for _, args := range [][]string{{"roles", "list"}, {"auth", "me"}} {
		if _, _, err := run(t, noConfig(t), append([]string{"-server", ":bad"}, args...)...); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatalf("invalid server accepted: %v", err)
		}
	}
	for _, args := range [][]string{{"roles", "put", "-h"}, {"auth", "bootstrap", "-h"}, {"auth", "recover-root", "-h"}} {
		if out, stderr, err := run(t, noConfig(t), args...); err != nil || out+stderr == "" {
			t.Fatalf("help: %q %q, %v", out, stderr, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid input or help made %d requests", calls.Load())
	}
}

func TestAuthorityCommandsReportServerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{{"roles", "list"}, {"auth", "me"}, {"auth", "bootstrap", "first"}, {"policies", "activate", "inventory"}} {
		_, _, err := run(t, noConfig(t), append([]string{"-server", srv.URL, "-token", "operator"}, args...)...)
		if !errors.Is(err, adminclient.ErrStatus) || !strings.Contains(err.Error(), "503") {
			t.Fatalf("%v lost server failure: %v", args, err)
		}
	}
}
