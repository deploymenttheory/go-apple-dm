package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/internal/runtime"
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
	var check, checkCA, sendKey, recvKey, storageKeys string
	role := fs.String("role", string(cfg.Role), "mdm, ddm, or all ("+app.EnvRole+")")
	fs.StringVar(
		&cfg.TLSCertFile,
		"tls-cert",
		cfg.TLSCertFile,
		"native TLS certificate PEM (DM_TLS_CERT_FILE)",
	)
	fs.StringVar(
		&cfg.TLSKeyFile,
		"tls-key",
		cfg.TLSKeyFile,
		"native TLS private key PEM (DM_TLS_KEY_FILE)",
	)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "listen address ("+app.EnvListen+")")
	fs.StringVar(
		&cfg.Storage,
		"storage",
		cfg.Storage,
		"sqlite, postgres, mysql, or inmem ("+app.EnvStorage+")",
	)
	fs.StringVar(&cfg.DSN, "dsn", cfg.DSN, "database path or DSN ("+app.EnvDSN+")")
	fs.StringVar(
		&cfg.DDMURL,
		"ddm-url",
		cfg.DDMURL,
		"mdm role: forward DDM to this ddm role ("+app.EnvDDMURL+")",
	)
	fs.StringVar(
		&sendKey,
		"ddm-send-key",
		string(cfg.DDMSendKey),
		"HMAC key for what this role sends ("+app.EnvDDMSendKey+")",
	)
	fs.StringVar(
		&recvKey,
		"ddm-recv-key",
		string(cfg.DDMRecvKey),
		"HMAC key for what this role receives ("+app.EnvDDMRecvKey+")",
	)
	fs.StringVar(
		&storageKeys,
		"storage-keys",
		strings.Join(cfg.StorageKeys, ","),
		"keys sealing the secret columns, active first ("+app.EnvStorageKeys+")",
	)
	fs.StringVar(
		&cfg.SecretsDir,
		"secrets-dir",
		cfg.SecretsDir,
		"directory holding the storage key material ("+app.EnvSecretsDir+")",
	)
	fs.StringVar(
		&cfg.AdminToken,
		"admin-token",
		cfg.AdminToken,
		"bearer token enabling the admin API ("+app.EnvAdminToken+")",
	)
	fs.StringVar(
		&cfg.CAFile,
		"ca-file",
		cfg.CAFile,
		"PEM roots for device identities, enables Mdm-Signature verification ("+app.EnvCAFile+")",
	)
	fs.StringVar(
		&cfg.CertHeader,
		"cert-header",
		cfg.CertHeader,
		"header carrying the client certificate from a TLS proxy ("+app.EnvCertHeader+")",
	)
	fs.BoolVar(
		&cfg.Subscriptions,
		"ddm-subscriptions",
		cfg.Subscriptions,
		"synthesise status subscriptions ("+app.EnvSubscriptions+")",
	)
	fs.StringVar(
		&check,
		"check",
		"",
		"GET a URL or use auto for configured listener health; exit 0 on 200",
	)
	fs.StringVar(
		&checkCA,
		"check-ca-file",
		"",
		"private CA bundle for an explicit health-check URL",
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if check != "" {
		return runtime.Probe(ctx, runtime.ProbeConfig{
			URL: check, Listen: cfg.Listen, TLSCertFile: cfg.TLSCertFile,
			TLSKeyFile: cfg.TLSKeyFile, CAFile: checkCA,
		})
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

// serve attaches process signals; runtime owns startup and ordered shutdown.
func serve(ctx context.Context, cfg app.Config) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runtime.Serve(ctx, cfg)
}

const shutdownTimeout = 10 * time.Second
