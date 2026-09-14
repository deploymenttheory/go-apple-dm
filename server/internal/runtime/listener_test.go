package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func TestServeListenerUsesReservedSocket(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	addr := listener.Addr().String()
	// The socket remains bound before the runtime starts. A second bind must
	// fail, while serving the original listener must succeed.
	if other, err := net.Listen("tcp", addr); err == nil {
		other.Close()
		t.Fatal("reserved address was available to another listener")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	done := make(chan struct{})
	var serveErr error
	go func() {
		defer close(done)
		serveErr = ServeListener(ctx, app.Config{
			Role: app.RoleAll, Storage: "inmem", Listen: "unused:0",
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, listener)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
			if serveErr != nil {
				t.Errorf("runtime: %v", serveErr)
			}
		case <-time.After(15 * time.Second):
			t.Error("runtime did not stop")
		}
	}()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("runtime did not become ready")
		case <-done:
			t.Fatalf("runtime exited before readiness: %v", serveErr)
		case <-tick.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
		}
	}
}

func TestServeListenerClosesOnStartupFailure(t *testing.T) {
	t.Parallel()
	if err := ServeListener(t.Context(), app.Config{}, nil); !errors.Is(err, app.ErrConfig) {
		t.Fatalf("nil listener: %v", err)
	}
	for _, name := range []string{"configuration", "TLS", "actual address"} {
		t.Run(name, func(t *testing.T) {
			addr := "127.0.0.1:0"
			cfg := app.Config{Role: app.RoleAll, Storage: "inmem", Listen: addr}
			switch name {
			case "configuration":
				cfg.Storage = "invalid"
			case "TLS":
				cfg.TLSCertFile, cfg.TLSKeyFile = "missing.pem", "missing.key"
			case "actual address":
				// A claimed loopback cfg.Listen cannot bypass the actual bound
				// address's transport requirements.
				addr = "0.0.0.0:0"
			}
			listener, err := net.Listen("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			if err := ServeListener(t.Context(), cfg, listener); err == nil {
				t.Fatal("invalid runtime started")
			}
			_ = listener.(*net.TCPListener).SetDeadline(time.Now())
			if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("listener not closed after failure: %v", err)
			}
		})
	}
}
