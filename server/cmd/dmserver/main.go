package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/internal/buildinfo"
	"github.com/deploymenttheory/go-apple-dm/server/internal/runtime"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dmserver:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, out *os.File) error {
	// Version inspection must work even when setup files are unavailable.
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
		_, err := fmt.Fprintln(out, buildinfo.Version())
		return err
	}
	setupPath := getenv("DM_SETUP_FILE")
	for i, arg := range args {
		if (arg == "--setup-file" || arg == "-setup-file") && i+1 < len(args) {
			setupPath = args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "--setup-file="); ok {
			setupPath = value
		}
		if value, ok := strings.CutPrefix(arg, "-setup-file="); ok {
			setupPath = value
		}
	}
	cfg, err := app.ParseEnv(getenv)
	if setupPath != "" {
		cfg, err = app.LoadSetupFile(setupPath, getenv)
		if err != nil {
			return err
		}
	}
	if err != nil && !errors.Is(err, app.ErrConfig) {
		return err
	}
	fs := flag.NewFlagSet("dmserver", flag.ContinueOnError)
	fs.SetOutput(out)
	showVersion := fs.Bool("version", false, "print the build version and exit")
	fs.StringVar(&setupPath, "setup-file", setupPath, "persistent certificate setup configuration (DM_SETUP_FILE)")
	var check, checkCA, storageKeys string
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
		&cfg.BootstrapToken,
		"bootstrap-token",
		cfg.BootstrapToken,
		"one-time first-root bootstrap token ("+app.EnvBootstrapToken+")",
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
	fs.StringVar(&cfg.ContentCache.PublicURL, "content-cache-url", cfg.ContentCache.PublicURL, "HTTPS origin enabling cache metrics ingestion (DM_CONTENT_CACHE_URL)")
	fs.DurationVar(&cfg.ContentCache.Retention, "content-cache-retention", cfg.ContentCache.Retention, "cache report retention; default 720h (DM_CONTENT_CACHE_RETENTION)")
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
	if *showVersion {
		_, err := fmt.Fprintln(out, buildinfo.Version())
		return err
	}
	if check != "" {
		var managedCertificate *x509.Certificate
		if cfg.Setup != nil && check == "auto" {
			a, err := app.OpenSetup(ctx, cfg)
			if err != nil {
				return err
			}
			defer func(cleanup func() error) { _ = cleanup() }(a.Close)
			material, err := a.Certificates.LoadMaterial(ctx, cfg.Setup.HTTPSID, "")
			if err != nil {
				return err
			}
			block, _ := pem.Decode(material.Certificate)
			if block == nil {
				return app.ErrConfig
			}
			managedCertificate, err = x509.ParseCertificate(block.Bytes)
			if err != nil {
				return err
			}
		}
		return runtime.Probe(ctx, runtime.ProbeConfig{
			URL: check, Listen: cfg.Listen, TLSCertFile: cfg.TLSCertFile, Certificate: managedCertificate,
			TLSKeyFile: cfg.TLSKeyFile, CAFile: checkCA,
		})
	}
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
