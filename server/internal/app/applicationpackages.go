package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	applicationaws "github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/aws"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/azure"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/filesystem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/gcp"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/server/applicationpackages"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// ApplicationPackageConfig enables catalogue storage and verified package hosting.
// File loads strict JSON with relative paths resolved against the configuration file.
// Stores provides already-configured adapters for embedding and tests.
type ApplicationPackageConfig struct {
	File               string                               `json:"-"`
	PublicURL          string                               `json:"publicURL"`
	ScratchDir         string                               `json:"scratchDir"`
	ImportDir          string                               `json:"importDir"`
	AllowedHTTPSHosts  []string                             `json:"allowedHTTPSHosts"`
	HTTPSCAFile        string                               `json:"httpsCAFile"`
	SignerCAFile       string                               `json:"signerCAFile"`
	AllowPrivateSigner bool                                 `json:"allowPrivateSigner"`
	RequireTimestamp   bool                                 `json:"requireTimestamp"`
	TeamID             string                               `json:"teamID"`
	MaxBytes           int64                                `json:"maxBytes"`
	Backends           map[string]ApplicationPackageBackend `json:"backends"`
	Stores             map[string]applications.BlobStore    `json:"-"`
}

// ApplicationPackageBackend names one filesystem or cloud distribution source.
// Credentials come from the official SDK credential chain, not this file.
type ApplicationPackageBackend struct {
	Kind       string `json:"kind"`
	Directory  string `json:"directory,omitempty"`
	Bucket     string `json:"bucket,omitempty"`
	Container  string `json:"container,omitempty"`
	AccountURL string `json:"accountURL,omitempty"`
	Region     string `json:"region,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
}

// loadApplicationPackages reads bounded strict configuration without opening backends.
func loadApplicationPackages(cfg ApplicationPackageConfig) (ApplicationPackageConfig, error) {
	if cfg.File == "" {
		return cfg, nil
	}
	f, err := os.Open(cfg.File)
	if err != nil {
		return cfg, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxAdminBody+1))
	if err != nil {
		return cfg, err
	}
	if len(data) > MaxAdminBody {
		return cfg, fmt.Errorf("%w: package configuration exceeds size limit", ErrConfig)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var loaded ApplicationPackageConfig
	if err = decoder.Decode(&loaded); err != nil {
		return cfg, err
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("%w: one package configuration object required", ErrConfig)
	}
	base := filepath.Dir(cfg.File)
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	loaded.ScratchDir = resolve(loaded.ScratchDir)
	loaded.ImportDir = resolve(loaded.ImportDir)
	loaded.HTTPSCAFile = resolve(loaded.HTTPSCAFile)
	loaded.SignerCAFile = resolve(loaded.SignerCAFile)
	for name, backend := range loaded.Backends {
		backend.Directory = resolve(backend.Directory)
		loaded.Backends[name] = backend
	}
	return loaded, nil
}

// packageRoots loads explicit certificate anchors without changing host trust stores.
func packageRoots(file string) (*x509.CertPool, error) {
	if file == "" {
		return nil, nil
	}
	data, err := os.ReadFile(file) // #nosec G304 -- Operator-configured CA path, never derived from an HTTP request.
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%w: package CA file contains no certificates", ErrConfig)
	}
	return roots, nil
}

// wireApplicationPackages constructs the shared library and configured SDK adapters.
func (a *App) wireApplicationPackages(ctx context.Context) error {
	cfg, err := loadApplicationPackages(a.cfg.ApplicationPackages)
	if err != nil {
		return err
	}
	if len(cfg.Backends) == 0 && len(cfg.Stores) == 0 {
		if a.cfg.ApplicationPackages.File != "" {
			return fmt.Errorf("%w: configure at least one package backend", ErrConfig)
		}
		return nil
	}
	if cfg.PublicURL == "" {
		cfg.PublicURL = a.cfg.Enroll.PublicURL
	}
	backends := map[string]applications.BlobStore{}
	for name, store := range cfg.Stores {
		backends[name] = store
	}
	for name, backend := range cfg.Backends {
		if _, exists := backends[name]; exists {
			return fmt.Errorf("%w: duplicate package backend", ErrConfig)
		}
		store, err := a.openApplicationBackend(ctx, backend)
		if err != nil {
			return fmt.Errorf("package backend %s: %w", name, err)
		}
		backends[name] = store
	}
	if cfg.ScratchDir == "" {
		return fmt.Errorf("%w: package scratchDir is required", ErrConfig)
	}
	if err = os.MkdirAll(cfg.ScratchDir, 0o700); err != nil {
		return err
	}
	roots, err := packageRoots(cfg.SignerCAFile)
	if err != nil {
		return err
	}
	st, err := a.protocolState(ctx)
	if err != nil {
		return err
	}
	a.ApplicationPackages, err = applications.New(applications.Config{State: st, Backends: backends, ScratchDir: cfg.ScratchDir, MaxBytes: cfg.MaxBytes, Verification: applications.VerificationPolicy{Anchors: roots, AllowPrivateSigner: cfg.AllowPrivateSigner, TeamID: cfg.TeamID, RequireTimestamp: cfg.RequireTimestamp}})
	if err != nil {
		return err
	}
	a.packageHost, err = applicationpackages.New(applicationpackages.Config{
		Packages: a.ApplicationPackages, State: st, BaseURL: cfg.PublicURL, Now: a.cfg.Clock.Now,
		Target: func(ctx context.Context, id mdm.EnrollmentID) (support.Target, error) {
			return service.EnrollmentTarget(ctx, a.Store, id)
		},
		Authorize: func(ctx context.Context, id mdm.EnrollmentID) error {
			record, err := a.Store.Get(ctx, id)
			if err != nil {
				return err
			}
			if !record.Enabled {
				return applications.ErrNotFound
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	if cfg.ImportDir != "" {
		a.packageImports, err = filesystem.New(cfg.ImportDir)
		if err != nil {
			return err
		}
		a.closers = append(a.closers, a.packageImports.Close)
	}
	httpsRoots, err := packageRoots(cfg.HTTPSCAFile)
	if err != nil {
		return err
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("%w: default HTTP transport", ErrConfig)
	}
	transport = transport.Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: httpsRoots}
	a.packageHTTPS = applications.HTTPSSource{Client: &http.Client{Transport: transport, Timeout: 30 * time.Minute}, AllowedHosts: cfg.AllowedHTTPSHosts}
	a.closers = append(a.closers, func() error { transport.CloseIdleConnections(); return nil })
	a.cfg.ApplicationPackages = cfg
	return nil
}

// openApplicationBackend binds official SDK clients and records owned resources.
func (a *App) openApplicationBackend(ctx context.Context, cfg ApplicationPackageBackend) (applications.BlobStore, error) {
	switch cfg.Kind {
	case "filesystem":
		if cfg.Directory == "" {
			return nil, applications.ErrInvalid
		}
		if err := os.MkdirAll(cfg.Directory, 0o700); err != nil {
			return nil, err
		}
		store, err := filesystem.New(cfg.Directory)
		if err == nil {
			a.closers = append(a.closers, store.Close)
		}
		return store, err
	case "aws":
		if cfg.Bucket == "" {
			return nil, applications.ErrInvalid
		}
		var options []func(*awsconfig.LoadOptions) error
		if cfg.Region != "" {
			options = append(options, awsconfig.WithRegion(cfg.Region))
		}
		cfgSDK, err := awsconfig.LoadDefaultConfig(ctx, options...)
		if err != nil {
			return nil, err
		}
		return applicationaws.New(s3.NewFromConfig(cfgSDK), cfg.Bucket, cfg.Prefix)
	case "azure":
		origin, err := url.Parse(cfg.AccountURL)
		if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || cfg.Container == "" {
			return nil, applications.ErrInvalid
		}
		credential, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, err
		}
		client, err := azblob.NewClient(cfg.AccountURL, credential, nil)
		if err != nil {
			return nil, err
		}
		return azure.New(client, cfg.Container, cfg.Prefix)
	case "gcp":
		if cfg.Bucket == "" {
			return nil, applications.ErrInvalid
		}
		client, err := storage.NewClient(ctx)
		if err != nil {
			return nil, err
		}
		a.closers = append(a.closers, client.Close)
		return gcp.New(client, cfg.Bucket, cfg.Prefix)
	default:
		return nil, fmt.Errorf("%w: unknown package backend %q", applications.ErrInvalid, cfg.Kind)
	}
}
