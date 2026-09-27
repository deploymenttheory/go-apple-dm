package app

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/filesystem"
)

// TestApplicationPackageConfigFile validates strict size-bounded configuration
// and resolves paths independently of the process working directory.
func TestApplicationPackageConfigFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "packages.json")
	absoluteCA := filepath.Join(root, "absolute", "https.pem")
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", fmt.Sprintf(`{"publicURL":"https://mdm.example","scratchDir":"scratch","importDir":"imports","signerCAFile":"signer.pem","httpsCAFile":%q,"backends":{"local":{"kind":"filesystem","directory":"blobs"}}}`, absoluteCA), true},
		{"unknown", `{"unknown":true}`, false},
		{"trailing", `{} {}`, false},
		{"invalid", `{`, false},
		{"oversized", `{}` + strings.Repeat(" ", MaxAdminBody), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(file, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadApplicationPackages(ApplicationPackageConfig{File: file})
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
			if tc.valid && (cfg.ScratchDir != filepath.Join(root, "scratch") || cfg.ImportDir != filepath.Join(root, "imports") || cfg.SignerCAFile != filepath.Join(root, "signer.pem") || cfg.HTTPSCAFile != absoluteCA || cfg.Backends["local"].Directory != filepath.Join(root, "blobs")) {
				t.Fatal("incorrect relative paths", cfg)
			}
		})
	}
	for _, path := range []string{filepath.Join(root, "absent"), root} {
		if _, err := loadApplicationPackages(ApplicationPackageConfig{File: path}); err == nil {
			t.Fatal("accepted unreadable configuration")
		}
	}
	if _, err := packageRoots(filepath.Join(root, "absent")); err == nil {
		t.Fatal("missing CA accepted")
	}
	if _, err := packageRoots(file); err == nil {
		t.Fatal("non-certificate CA accepted")
	}
}

// TestApplicationPackageBackendConstruction binds official SDK clients without
// contacting cloud services and retains the environment's default AWS region.
func TestApplicationPackageBackendConstruction(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "fixture")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fixture")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("STORAGE_EMULATOR_HOST", "http://127.0.0.1:1")
	for _, cfg := range []ApplicationPackageBackend{
		{Kind: "filesystem", Directory: t.TempDir()},
		{Kind: "aws", Bucket: "fixture", Prefix: "packages"},
		{Kind: "aws", Bucket: "fixture", Region: "eu-west-1"},
		{Kind: "azure", AccountURL: "https://fixture.blob.core.windows.net", Container: "packages"},
		{Kind: "gcp", Bucket: "fixture", Prefix: "packages"},
	} {
		t.Run(cfg.Kind+cfg.Region, func(t *testing.T) {
			a := &App{}
			defer func() {
				for _, close := range a.closers {
					if err := close(); err != nil {
						t.Error(err)
					}
				}
			}()
			store, err := a.openApplicationBackend(t.Context(), cfg)
			if err != nil || store == nil {
				t.Fatal(err)
			}
		})
	}
	for _, cfg := range []ApplicationPackageBackend{
		{Kind: "filesystem"},
		{Kind: "aws"},
		{Kind: "gcp"},
		{Kind: "unknown"},
		{Kind: "azure", AccountURL: "http://fixture", Container: "packages"},
		{Kind: "azure", AccountURL: "https://fixture"},
		{Kind: "azure", AccountURL: "https://fixture?token=secret", Container: "packages"},
	} {
		if _, err := (&App{}).openApplicationBackend(t.Context(), cfg); !errors.Is(err, applications.ErrInvalid) {
			t.Fatal(cfg.Kind, err)
		}
	}
	bad := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(bad, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&App{}).openApplicationBackend(t.Context(), ApplicationPackageBackend{Kind: "filesystem", Directory: bad}); err == nil {
		t.Fatal("invalid storage directory accepted")
	}
	if err := os.WriteFile(bad, []byte("[profile broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", bad)
	t.Setenv("AWS_PROFILE", "missing-profile")
	if _, err := (&App{}).openApplicationBackend(t.Context(), ApplicationPackageBackend{Kind: "aws", Bucket: "fixture"}); err == nil {
		t.Fatal("invalid AWS configuration accepted")
	}
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", bad)
	if _, err := (&App{}).openApplicationBackend(t.Context(), ApplicationPackageBackend{Kind: "gcp", Bucket: "fixture"}); err == nil {
		t.Fatal("invalid GCP credentials accepted")
	}
}

// TestApplicationPackageWiringFailures fails startup for unusable package storage
// or trust policies and closes resources already acquired during construction.
func TestApplicationPackageWiringFailures(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "not-directory")
	if err := os.WriteFile(bad, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "empty.json")
	if err := os.WriteFile(empty, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	disk, err := filesystem.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = disk.Close() }()
	for _, tc := range []struct {
		name string
		edit func(*ApplicationPackageConfig)
	}{
		{"missing file", func(c *ApplicationPackageConfig) { c.File = filepath.Join(root, "missing") }},
		{"empty file", func(c *ApplicationPackageConfig) { c.File = empty }},
		{"unknown backend", func(c *ApplicationPackageConfig) {
			c.Backends = map[string]ApplicationPackageBackend{"bad": {Kind: "unknown"}}
		}},
		{"duplicate backend", func(c *ApplicationPackageConfig) { c.Stores = map[string]applications.BlobStore{"local": disk} }},
		{"no scratch", func(c *ApplicationPackageConfig) { c.ScratchDir = "" }},
		{"invalid scratch", func(c *ApplicationPackageConfig) { c.ScratchDir = bad }},
		{"missing signer", func(c *ApplicationPackageConfig) { c.SignerCAFile = filepath.Join(root, "absent") }},
		{"negative size", func(c *ApplicationPackageConfig) { c.MaxBytes = -1 }},
		{"invalid origin", func(c *ApplicationPackageConfig) { c.PublicURL = "http://example.test" }},
		{"missing import", func(c *ApplicationPackageConfig) { c.ImportDir = filepath.Join(root, "absent") }},
		{"missing HTTPS CA", func(c *ApplicationPackageConfig) { c.HTTPSCAFile = filepath.Join(root, "absent") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ApplicationPackageConfig{PublicURL: "https://mdm.example", ScratchDir: filepath.Join(t.TempDir(), "scratch"), Backends: map[string]ApplicationPackageBackend{"local": {Kind: "filesystem", Directory: t.TempDir()}}}
			tc.edit(&cfg)
			a, err := Build(t.Context(), Config{Storage: "inmem", ApplicationPackages: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if a != nil {
				_ = a.Close()
			}
			if err == nil {
				t.Fatal("invalid package configuration accepted")
			}
		})
	}
	a, err := Build(t.Context(), Config{Storage: "inmem", ApplicationPackages: ApplicationPackageConfig{Stores: map[string]applications.BlobStore{"local": disk}, ScratchDir: root}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if a != nil {
		_ = a.Close()
	}
	if err == nil {
		t.Fatal("empty hosting origin accepted")
	}
}

// TestPackageJSONAndErrorClassification preserves request limits and separates
// malformed requests from internal storage errors.
func TestPackageJSONAndErrorClassification(t *testing.T) {
	for _, body := range []io.Reader{strings.NewReader(`{"unknown":true}`), strings.NewReader(`{} {}`), strings.NewReader(`{}` + strings.Repeat(" ", MaxAdminBody)), errReader{}} {
		var value struct {
			Note string `json:"note"`
		}
		r, _ := http.NewRequestWithContext(t.Context(), "POST", "https://example.test", body)
		if err := packageJSON(r, &value); err == nil {
			t.Fatal("bad body accepted")
		}
	}
}
