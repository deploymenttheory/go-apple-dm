package runtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestSupervisePreservesFirstFailure checks that supervise preserves first failure.
func TestSupervisePreservesFirstFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("worker failed")
	for _, name := range []string{"worker exits first", "worker fails during drain", "worker stuck", "earlier HTTP error"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			workers := make(chan error, 1)
			serving := make(chan error, 1)
			want := failure
			switch name {
			case "worker exits first":
				workers <- failure
			case "worker fails during drain":
				cancel()
				workers <- failure
			case "worker stuck":
				cancel()
				want = errWorkersStuck
			case "earlier HTTP error":
				serving <- failure
			}
			stopped := false
			got := supervise(
				ctx,
				[]*http.Server{{ReadHeaderTimeout: time.Second}},
				serving,
				workers,
				func() { stopped = true },
				func() error { return nil },
				10*time.Millisecond,
			)
			if !errors.Is(got, want) || !stopped {
				t.Fatalf("shutdown: %v; stopped=%v", got, stopped)
			}
		})
	}
}

// TestSuperviseDrainsHTTPBeforeWorkers checks that supervise drains HTTP before workers.
func TestSuperviseDrainsHTTPBeforeWorkers(t *testing.T) {
	t.Parallel()
	for _, expire := range []bool{false, true} {
		t.Run(map[bool]string{false: "drain", true: "timeout"}[expire], func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			srv := httptest.NewServer(
				http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) },
				),
			)
			defer srv.Close()
			resRequest, err := http.NewRequestWithContext(t.Context(), "GET", srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			response := make(chan error, 1)
			go func() {
				res, err := srv.Client().Do(resRequest)
				if err == nil {
					_ = res.Body.Close()
				}
				response <- err
			}()
			<-entered
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			workers := make(chan error, 1)
			done := make(chan error, 1)
			stopped := make(chan struct{})
			timeout := time.Second
			if expire {
				timeout = 10 * time.Millisecond
			}
			go func() {
				done <- supervise(ctx, []*http.Server{srv.Config}, make(chan error), workers, func() { close(stopped); workers <- nil }, func() error { return nil }, timeout)
			}()
			if !expire {
				select {
				case <-stopped:
					t.Fatal("workers stopped before in-flight request drained")
				case <-time.After(20 * time.Millisecond):
				}
				close(release)
			}
			err = <-done
			if expire {
				close(release)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("missing shutdown deadline: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			<-response
		})
	}
}

// TestServeStartupFailures checks listener startup failure for invalid TLS, listener, and runtime
// configuration.
func TestServeStartupFailures(t *testing.T) {
	t.Parallel()
	cfg := app.Config{
		Storage:     "inmem",
		Listen:      "127.0.0.1:0",
		TLSCertFile: "missing.pem",
		TLSKeyFile:  "missing.key",
	}
	if err := Serve(t.Context(), cfg); err == nil {
		t.Fatal("missing TLS certificate accepted")
	}
	cfg.TLSCertFile = ""
	cfg.TLSKeyFile = ""
	cfg.Listen = "invalid address"
	if err := Serve(t.Context(), cfg); err == nil {
		t.Fatal("invalid listener accepted")
	}
	cfg.Storage = "invalid"
	if err := Serve(t.Context(), cfg); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	if err := serveHTTP(&http.Server{ReadHeaderTimeout: time.Second}, listener, app.Config{}); err == nil {
		t.Fatal("closed listener accepted")
	}
}

// TestServeRejectsUnprotectedSecurityBoundaries checks that serve rejects unprotected security
// boundaries.
func TestServeRejectsUnprotectedSecurityBoundaries(t *testing.T) {
	for _, cfg := range []app.Config{
		{Listen: ":8080"},
		{Listen: "0.0.0.0:8080"},
		{Listen: "[::]:8080"},
		{Listen: "localhost:8080"},
		{CertHeader: "Client-Cert", Listen: "0.0.0.0:8443"},
		{CertHeader: "Client-Cert", Listen: "localhost:8443"},
		{Listen: "127.0.0.1:8443"},
		{Listen: "0.0.0.0:8443"},
	} {
		if err := Serve(t.Context(), cfg); !errors.Is(err, app.ErrConfig) {
			t.Fatal("unsafe listener accepted", cfg.Listen, err)
		}
	}
}
