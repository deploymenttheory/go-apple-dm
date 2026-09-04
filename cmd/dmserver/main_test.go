package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/app"
)

// quiet keeps the listening line out of the test log. serve dereferences
// cfg.Logger directly, so it is never optional.
func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// serve must not report success until the background workers have stopped.
// The old path returned srv.Shutdown's error and left the notifier and the
// DEP syncer running, so the process could exit mid-drain.
func TestServeStopsOnContextCancel(t *testing.T) {
	cfg := app.Config{Role: app.RoleAll, Storage: "inmem", Listen: "127.0.0.1:0", Logger: quiet()}
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
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	cfg := app.Config{Role: app.RoleAll, Storage: "inmem", Listen: held.Addr().String(), Logger: quiet()}
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
	cfg := app.Config{Role: app.Role("nonsense"), Storage: "inmem", Listen: "127.0.0.1:0", Logger: quiet()}
	if err := serve(context.Background(), cfg); !errors.Is(err, app.ErrConfig) {
		t.Fatalf("serve = %v, want ErrConfig", err)
	}
}
