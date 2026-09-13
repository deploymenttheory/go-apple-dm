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
	if cfg.TLSCertFile == "" && cfg.Setup == nil {
		host, _, err := net.SplitHostPort(cfg.Listen)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() ||
			(cfg.Role == app.RoleDDM && !cfg.DDMAllowInsecureForTests) {
			return fmt.Errorf(
				"%w: HTTP listeners require a literal loopback address; remote listeners and private DDM require TLS",
				app.ErrConfig,
			)
		}
	}

	a, err := app.Build(ctx, cfg)
	if err != nil {
		return wrapError(err)
	}
	defer a.Close() //nolint:contextcheck // Close owns a bounded drain after the request context is canceled.
	if cfg.Setup != nil {
		if _, err := a.LoadTLSCertificate(ctx); err != nil {
			return fmt.Errorf(
				"dmserver: activate the managed HTTPS identity before starting: %w",
				err,
			)
		}
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return wrapError(err)
	}
	defer listener.Close()
	serving := make(chan error, 2)
	if cfg.Setup != nil && cfg.Setup.HTTP01Listen != "" {
		challengeListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Setup.HTTP01Listen)
		if err != nil {
			return wrapError(err)
		}
		challengeServer := &http.Server{
			Handler:           a.Certificates.HTTP01Handler(),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
		}
		defer challengeServer.Close()
		go func() {
			if err := challengeServer.Serve(
				challengeListener,
			); !errors.Is(
				err,
				http.ErrServerClosed,
			) {
				serving <- fmt.Errorf("dmserver: HTTP-01 listener: %w", err)
			}
		}()
	}

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
	if cfg.Setup != nil {
		srv.TLSConfig.GetCertificate = a.TLSCertificate
		srv.TLSConfig.GetConfigForClient = a.ManagedTLSConfig
	}
	workers := make(chan error, 1)
	go func() { workers <- a.Run(workerCtx) }()
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
	if cfg.Setup != nil {
		return wrapError(srv.ServeTLS(listener, "", ""))
	}
	if cfg.TLSCertFile != "" {
		return fmt.Errorf(
			"runtime TLS: %w",
			srv.ServeTLS(listener, cfg.TLSCertFile, cfg.TLSKeyFile),
		)
	}
	return fmt.Errorf("runtime HTTP: %w", srv.Serve(listener))
}
