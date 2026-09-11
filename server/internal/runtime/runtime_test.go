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
				&http.Server{},
				serving,
				workers,
				func() { stopped = true },
				10*time.Millisecond,
			)
			if !errors.Is(got, want) || !stopped {
				t.Fatalf("shutdown: %v; stopped=%v", got, stopped)
			}
		})
	}
}

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
			response := make(chan error, 1)
			go func() {
				res, err := srv.Client().Get(srv.URL)
				if err == nil {
					res.Body.Close()
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
				done <- supervise(ctx, srv.Config, make(chan error), workers, func() { close(stopped); workers <- nil }, timeout)
			}()
			if !expire {
				select {
				case <-stopped:
					t.Fatal("workers stopped before in-flight request drained")
				case <-time.After(20 * time.Millisecond):
				}
				close(release)
			}
			err := <-done
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

func TestServeStartupFailures(t *testing.T) {
	t.Parallel()
	cfg := app.Config{
		Role:        app.RoleAll,
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	if err := serveHTTP(&http.Server{}, listener, app.Config{}); err == nil {
		t.Fatal("closed listener accepted")
	}
}

func TestServeRejectsUnprotectedSecurityBoundaries(t *testing.T) {
	for _, cfg := range []app.Config{
		{Role: app.RoleAll, Listen: ":8080"},
		{Role: app.RoleMDM, Listen: "0.0.0.0:8080"},
		{Role: app.RoleAll, Listen: "[::]:8080"},
		{Role: app.RoleAll, Listen: "localhost:8080"},
		{Role: app.RoleAll, CertHeader: "Client-Cert", Listen: "0.0.0.0:8443"},
		{Role: app.RoleAll, CertHeader: "Client-Cert", Listen: "localhost:8443"},
		{Role: app.RoleDDM, Listen: "127.0.0.1:8443"},
		{Role: app.RoleDDM, Listen: "0.0.0.0:8443", DDMAllowInsecureForTests: true},
	} {
		if err := Serve(t.Context(), cfg); !errors.Is(err, app.ErrConfig) {
			t.Fatal("unsafe listener accepted", cfg.Listen, err)
		}
	}
}
