package dmctl_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

// TestInventoryCLIValidation rejects incomplete commands before issuing administrative requests.
func TestInventoryCLIValidation(t *testing.T) {
	for _, args := range [][]string{{"devices"}, {"axm"}, {"inventory"}, {"devices", "get"}, {"devices", "collect"}, {"devices", "wrong"}, {"axm", "accounts", "get"}, {"inventory", "wrong"}, {"devices", "list", "--where", "{"}, {"devices", "list", "--unknown"}, {"inventory", "schedules", "set", "a", "--cron", "bad"}} {
		if _, _, err := run(t, noConfig(t), args...); !errors.Is(err, dmctl.ErrUsage) {
			t.Fatal(args, err)
		}
	}
	if _, _, err := run(t, noConfig(t), "axm", "accounts", "create", "--file", "/nonexistent-inventory-input.json"); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, _, err := run(t, noConfig(t), "devices", "list"); err == nil {
		t.Fatal("missing connection accepted")
	}
}

// TestInventoryCLIWaitOutcomes checks malformed jobs, failed polling and non-success outcomes.
func TestInventoryCLIWaitOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, initial, poll string
		status              int
		want                error
	}{
		{"invalid initial", `{`, ``, 200, nil},
		{"failed", `{"state":"failed","finished_at":"2026-01-01T00:00:00Z"}`, ``, 200, dmctl.ErrPartial},
		{"paused", `{"state":"paused"}`, ``, 200, dmctl.ErrPartial},
		{"poll error", `{"id":"job","state":"running"}`, `{"error":"failed"}`, 500, nil},
		{"poll malformed", `{"id":"job","state":"running"}`, `{`, 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprint(w, tc.poll)
				} else {
					_, _ = fmt.Fprint(w, tc.initial)
				}
			}))
			defer srv.Close()
			env := noConfig(t)
			env["DMCTL_SERVER"] = srv.URL
			env["DMCTL_TOKEN"] = "test"
			_, _, err := run(t, env, "axm", "accounts", "sync", "source", "--wait")
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatal(err)
			}
		})
	}
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		received := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `{"id":"job","state":"running"}`)
			close(received)
		}))
		defer srv.Close()
		env := noConfig(t)
		env["DMCTL_SERVER"] = srv.URL
		env["DMCTL_TOKEN"] = "test"
		go func() {
			select {
			case <-received:
				time.Sleep(20 * time.Millisecond)
				cancel()
			case <-ctx.Done():
			}
		}()
		var out, stderr strings.Builder
		err := dmctl.Run(ctx, []string{"axm", "accounts", "sync", "source", "--wait"}, func(k string) string { return env[k] }, strings.NewReader(""), &out, &stderr)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}
