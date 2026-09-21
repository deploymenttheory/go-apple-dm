package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/internal/buildinfo"
)

// TestVersionWithoutSetup checks that version output does not require loading server setup.
func TestVersionWithoutSetup(t *testing.T) {
	for _, flag := range []string{"--version", "-version"} {
		t.Run(flag, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "version")
			// #nosec G304 -- The test controls this fixture path within its private workspace.
			out, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = out.Close() })
			getenv := func(string) string {
				t.Fatal("version inspection attempted to load configuration")
				return ""
			}
			if err := run(t.Context(), []string{flag}, getenv, out); err != nil {
				t.Fatal(err)
			}
			// #nosec G304 -- The test controls this fixture path within its private workspace.
			raw, err := os.ReadFile(path)
			if err != nil || strings.TrimSpace(string(raw)) != buildinfo.Version() {
				t.Fatalf("version = %q, error = %v", raw, err)
			}
		})
	}
}

// quiet keeps the listening line out of the test log. serve dereferences
// cfg.Logger directly, so it is never optional.
func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// serve must not report success until the background workers have stopped.
// The old path returned srv.Shutdown's error and left the notifier and the
// DEP syncer running, so the process could exit mid-drain.
func TestServeStopsOnContextCancel(t *testing.T) {
	cfg := app.Config{Storage: "inmem", Listen: "127.0.0.1:0", Logger: quiet()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg) }()
	// Give the listener and the workers a moment to come up so the test
	// exercises the drain rather than a race against start-up.
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve = %v, want nil after a clean shutdown", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("serve did not return")
	}
}

// A listener that cannot bind is reported rather than silently ignored.
func TestServeReportsListenError(t *testing.T) {
	held, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(held.Close)

	cfg := app.Config{Storage: "inmem", Listen: held.Addr().String(), Logger: quiet()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("serve = nil, want the bind failure")
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("serve did not report the bind failure")
	}
}

// A configuration Build rejects never reaches the listener.
func TestServeReportsBuildError(t *testing.T) {
	cfg := app.Config{Storage: "nonsense", Listen: "127.0.0.1:0", Logger: quiet()}
	if err := serve(context.Background(), cfg); !errors.Is(err, app.ErrConfig) {
		t.Fatalf("serve = %v, want ErrConfig", err)
	}
}
