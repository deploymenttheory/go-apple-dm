// Package runtime owns the reference server HTTP and worker lifecycle.
package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// Server timeouts. A device on a slow mobile network may take a while over
// a large profile or declaration, so the body deadlines are generous; what
// they exist to stop is a connection held open indefinitely.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 1 << 20
	// shutdownTimeout bounds the whole drain: in-flight requests first,
	// then the background workers.
	shutdownTimeout = 10 * time.Second
)

// errWorkersStuck reports workers that outlived the shutdown deadline. It is
// a static error so callers can match it.
var errWorkersStuck = errors.New("dmserver: workers did not stop before the shutdown deadline")

func Serve(ctx context.Context, cfg app.Config) error {
	if cfg.TLSCertFile == "" && (cfg.CertHeader != "" || cfg.Role == app.RoleDDM) {
		host, _, err := net.SplitHostPort(cfg.Listen)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() ||
			(cfg.Role == app.RoleDDM && !cfg.DDMAllowInsecureForTests) {
			return fmt.Errorf(
				"%w: certificate proxy backends require loopback or TLS; private DDM requires TLS",
				app.ErrConfig,
			)
		}
	}

	a, err := app.Build(ctx, cfg)
	if err != nil {
		return wrapError(err)
	}
	defer a.Close() //nolint:contextcheck // Close owns a bounded drain after the request context is canceled.
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return wrapError(err)
	}
	defer listener.Close()

	// The workers get their own context so shutdown can stop accepting new
	// requests before the loops are told to finish. Sharing ctx with the
	// signal handler would tear both down at once and let the process exit
	// with a drain half done.
	workerCtx, stopWorkers := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWorkers()

	srv := &http.Server{
		Addr: cfg.Listen,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ClientAuth: tls.VerifyClientCertIfGiven,
			ClientCAs:  a.TLSClientRoots(),
		},
		Handler:           a.Handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	workers := make(chan error, 1)
	go func() { workers <- a.Run(workerCtx) }()
	serving := make(chan error, 1)
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		logger.
			Info(
				"dmserver: listening",
				"role",
				string(cfg.Role),
				"addr",
				cfg.Listen,
				"storage",
				cfg.Storage,
			)
		if err := serveHTTP(srv, listener, cfg); !errors.Is(err, http.ErrServerClosed) {
			serving <- err
		}
	}()

	return supervise(ctx, srv, serving, workers, stopWorkers, shutdownTimeout)
}

// supervise preserves the first failure while draining HTTP before stopping workers.
func supervise(
	ctx context.Context,
	srv *http.Server,
	serving, workers <-chan error,
	stopWorkers context.CancelFunc,
	timeout time.Duration,
) error {
	var first error
	workersDone := false
	select {
	case <-ctx.Done():
	case err := <-serving:
		first = err
	case err := <-workers:
		workersDone, first = true, err
	}

	// Ordered shutdown: stop accepting and drain in-flight requests, then
	// tell the workers to finish, then wait for them. Reporting success
	// before the notifier and the DEP syncer have stopped is what let the
	// old path exit mid-drain.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		if first == nil {
			first = fmt.Errorf("dmserver: shutdown: %w", err)
		}
	}
	stopWorkers()
	if !workersDone {
		select {
		case err := <-workers:
			if err != nil && first == nil {
				first = err
			}
		case <-shutdownCtx.Done():
			if first == nil {
				first = errWorkersStuck
			}
		}
	}
	return first
}

func serveHTTP(srv *http.Server, listener net.Listener, cfg app.Config) error {
	if cfg.TLSCertFile != "" {
		return fmt.Errorf(
			"runtime TLS: %w",
			srv.ServeTLS(listener, cfg.TLSCertFile, cfg.TLSKeyFile),
		)
	}
	return fmt.Errorf("runtime HTTP: %w", srv.Serve(listener))
}
