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
	"syscall"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/app"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mdmserver:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, out *os.File) error {
	cfg, err := app.ParseEnv(getenv)
	if err != nil && !errors.Is(err, app.ErrConfig) {
		return err
	}
	fs := flag.NewFlagSet("mdmserver", flag.ContinueOnError)
	fs.SetOutput(out)
	var check, sendKey, recvKey string
	role := fs.String("role", string(cfg.Role), "mdm, ddm, or all ("+app.EnvRole+")")
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "listen address ("+app.EnvListen+")")
	fs.StringVar(&cfg.Storage, "storage", cfg.Storage, "sqlite, postgres, mysql, or inmem ("+app.EnvStorage+")")
	fs.StringVar(&cfg.DSN, "dsn", cfg.DSN, "database path or DSN ("+app.EnvDSN+")")
	fs.StringVar(&cfg.DDMURL, "ddm-url", cfg.DDMURL, "mdm role: forward DDM to this ddm role ("+app.EnvDDMURL+")")
	fs.StringVar(&sendKey, "ddm-send-key", string(cfg.DDMSendKey), "HMAC key for what this role sends ("+app.EnvDDMSendKey+")")
	fs.StringVar(&recvKey, "ddm-recv-key", string(cfg.DDMRecvKey), "HMAC key for what this role receives ("+app.EnvDDMRecvKey+")")
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

func serve(ctx context.Context, cfg app.Config) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.Close()
	srv := &http.Server{Addr: cfg.Listen, Handler: a.Handler, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 2)
	go func() { errc <- a.Run(ctx) }()
	go func() {
		cfg.Logger.Info("mdmserver: listening", "role", string(cfg.Role), "addr", cfg.Listen, "storage", cfg.Storage)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
