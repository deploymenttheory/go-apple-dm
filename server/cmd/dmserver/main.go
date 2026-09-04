package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dmserver:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, out *os.File) error {
	cfg, err := app.ParseEnv(getenv)
	if err != nil && !errors.Is(err, app.ErrConfig) {
		return err
	}
	fs := flag.NewFlagSet("dmserver", flag.ContinueOnError)
	fs.SetOutput(out)
	var check, sendKey, recvKey, storageKeys string
	role := fs.String("role", string(cfg.Role), "mdm, ddm, or all ("+app.EnvRole+")")
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "listen address ("+app.EnvListen+")")
	fs.StringVar(&cfg.Storage, "storage", cfg.Storage, "sqlite, postgres, mysql, or inmem ("+app.EnvStorage+")")
	fs.StringVar(&cfg.DSN, "dsn", cfg.DSN, "database path or DSN ("+app.EnvDSN+")")
	fs.StringVar(&cfg.DDMURL, "ddm-url", cfg.DDMURL, "mdm role: forward DDM to this ddm role ("+app.EnvDDMURL+")")
	fs.StringVar(&sendKey, "ddm-send-key", string(cfg.DDMSendKey), "HMAC key for what this role sends ("+app.EnvDDMSendKey+")")
	fs.StringVar(&recvKey, "ddm-recv-key", string(cfg.DDMRecvKey), "HMAC key for what this role receives ("+app.EnvDDMRecvKey+")")
	fs.StringVar(&storageKeys, "storage-keys", strings.Join(cfg.StorageKeys, ","), "keys sealing the secret columns, active first ("+app.EnvStorageKeys+")")
	fs.StringVar(&cfg.SecretsDir, "secrets-dir", cfg.SecretsDir, "directory holding the storage key material ("+app.EnvSecretsDir+")")
	fs.StringVar(&cfg.AdminToken, "admin-token", cfg.AdminToken, "bearer token enabling the admin API ("+app.EnvAdminToken+")")
	fs.StringVar(&cfg.CAFile, "ca-file", cfg.CAFile, "PEM roots for device identities, enables Mdm-Signature verification ("+app.EnvCAFile+")")
	fs.StringVar(&cfg.CertHeader, "cert-header", cfg.CertHeader, "header carrying the client certificate from a TLS proxy ("+app.EnvCertHeader+")")
	fs.BoolVar(&cfg.Subscriptions, "ddm-subscriptions", cfg.Subscriptions, "synthesise status subscriptions ("+app.EnvSubscriptions+")")
	fs.StringVar(&check, "check", "", "GET this URL and exit 0 on 200 (container health probe)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if check != "" {
		return probe(ctx, check)
	}
	cfg.Role = app.Role(*role)
	cfg.DDMSendKey, cfg.DDMRecvKey = keyBytes(sendKey), keyBytes(recvKey)
	if storageKeys != "" {
		cfg.StorageKeys = nil
		for name := range strings.SplitSeq(storageKeys, ",") {
			if n := strings.TrimSpace(name); n != "" {
				cfg.StorageKeys = append(cfg.StorageKeys, n)
			}
		}
	}
	cfg.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	return serve(ctx, cfg)
}

func keyBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}

func probe(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("check: %s", resp.Status)
	}
	return nil
}

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

func serve(ctx context.Context, cfg app.Config) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.Close()

	// The workers get their own context so shutdown can stop accepting new
	// requests before the loops are told to finish. Sharing ctx with the
	// signal handler would tear both down at once and let the process exit
	// with a drain half done.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	srv := &http.Server{
		Addr:              cfg.Listen,
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
	go func() {
		cfg.Logger.Info("dmserver: listening", "role", string(cfg.Role), "addr", cfg.Listen, "storage", cfg.Storage)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serving <- err
		}
	}()

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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && first == nil {
		first = fmt.Errorf("dmserver: shutdown: %w", err)
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
