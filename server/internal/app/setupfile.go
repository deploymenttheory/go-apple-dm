package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
)

// SetupFile is a versioned bootstrap document. Secret values live in the
// referenced files or environment, outside the database they protect.
type SetupFile struct {
	Version     int               `json:"version"`
	Environment map[string]string `json:"environment"`
	SecretFiles map[string]string `json:"secretFiles"`
	Setup       SetupConfig       `json:"setup"`
}

// LoadSetupFile combines file settings with explicit environment overrides.
func LoadSetupFile(path string, getenv func(string) string) (Config, error) {
	// #nosec G304 -- Local bootstrap configuration selected by the operator.
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, wrapError(err)
	}
	var f SetupFile
	if err = json.Unmarshal(b, &f); err != nil {
		return Config{}, wrapError(err)
	}
	if f.Version != 1 {
		return Config{}, fmt.Errorf("%w: unsupported setup file version", ErrConfig)
	}
	secrets := map[string]string{}
	for name, file := range f.SecretFiles {
		if !filepath.IsAbs(file) {
			file = filepath.Join(filepath.Dir(path), file)
		}
		if getenv != nil && getenv(name) != "" {
			continue
		}
		// #nosec G304 -- Secret reference from trusted local setup configuration.
		b, err := os.ReadFile(file)
		if err != nil {
			return Config{}, fmt.Errorf("app: read secret reference %s: %w", name, err)
		}
		secrets[name] = strings.TrimSpace(string(b))
	}
	cfg, err := ParseEnv(func(name string) string {
		if getenv != nil {
			if v := getenv(name); v != "" {
				return v
			}
		}
		if v := secrets[name]; v != "" {
			return v
		}
		return f.Environment[name]
	})
	if err != nil {
		return cfg, wrapError(err)
	}
	if f.Setup.VendorTokenFile != "" && !filepath.IsAbs(f.Setup.VendorTokenFile) {
		f.Setup.VendorTokenFile = filepath.Join(filepath.Dir(path), f.Setup.VendorTokenFile)
	}
	cfg.Setup = &f.Setup
	return cfg, nil
}

// InitSetupFile creates protected bootstrap files without overwriting identities
// or secrets on retry. Database creation is performed by OpenSetup.
type SetupInitOptions struct {
	Directory, Role, Storage, DSN, PublicURL, Listen, Organization string
	StorageKeyFile, StorageKeyName                                 string
	HTTP01Listen                                                   string
	BootstrapTokenFile, IssuanceKeyFile, ACMEKeyFile               string
	// AdditionalSecrets preserves deployment settings during an explicit import.
	// Values are written to protected files and referenced by environment name.
	AdditionalSecrets map[string][]byte
	StorageKeyAliases []string
}

// InitSetupFile creates a protected setup directory with generated or imported
// secret files and a configuration that references them. It validates deployment
// settings before exposing the resulting configuration path; callers retain
// ownership of the created setup directory.
func InitSetupFile(o SetupInitOptions) (string, error) {
	dir, role, storage, dsn, publicURL, listen, organization := o.Directory, o.Role, o.Storage, o.DSN, o.PublicURL, o.Listen, o.Organization
	storageKeyName := o.StorageKeyName
	if storageKeyName == "" {
		storageKeyName = "storage"
	}
	if strings.ContainsAny(storageKeyName, "/\\") || storageKeyName == "." ||
		storageKeyName == ".." ||
		storageKeyName == "admin" ||
		storageKeyName == "issuance" ||
		storageKeyName == "acme" ||
		storageKeyName == "database-dsn" {
		return "", fmt.Errorf("%w: storage key name", ErrConfig)
	}

	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", wrapError(err)
	}
	if role != "customer" && role != "vendor" && role != "combined" {
		return "", fmt.Errorf("%w: setup role", ErrConfig)
	}
	if storage != "sqlite" && storage != "postgres" && storage != "mysql" {
		return "", fmt.Errorf("%w: persistent storage required", ErrConfig)
	}
	if err = os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		return "", wrapError(err)
	}
	path := filepath.Join(dir, "setup.json")
	if _, err = os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", wrapError(err)
	}
	if storage == "sqlite" {
		if dsn == "" {
			dsn = filepath.Join(dir, "certificates.sqlite")
		}
		dbPath := strings.TrimPrefix(strings.SplitN(dsn, "?", 2)[0], "file:")
		if info, err := os.Stat(dbPath); err == nil && info.Size() > 0 && o.StorageKeyFile == "" {
			return "", fmt.Errorf(
				"%w: adopting an existing database requires its storage key file and original key ID",
				ErrConfig,
			)
		}
	}
	for name, size := range map[string]int{storageKeyName: 32, "admin": 32, "issuance": 32} {
		file := filepath.Join(dir, "secrets", name)
		b := make([]byte, size)
		if _, err = rand.Read(b); err != nil {
			return "", wrapError(err)
		}
		b = []byte(hex.EncodeToString(b))
		source := map[string]string{storageKeyName: o.StorageKeyFile, "admin": o.BootstrapTokenFile, "issuance": o.IssuanceKeyFile}[name]
		if source != "" {
			// #nosec G304 -- Existing key file explicitly selected for local adoption.
			b, err = os.ReadFile(source)
			if err != nil {
				return "", wrapError(err)
			}
			if len(b) < 16 {
				return "", fmt.Errorf("%w: existing storage key is too short", ErrConfig)
			}
		}
		if err = installSetupSecret(file, b, source != ""); err != nil {
			return "", wrapError(err)
		}
	}
	if storage == "sqlite" && dsn == "" {
		dsn = filepath.Join(dir, "certificates.sqlite")
	}
	if dsn == "" {
		return "", fmt.Errorf("%w: DSN required", ErrConfig)
	}
	f := SetupFile{
		Version: 1,
		Setup: SetupConfig{
			Role:      role,
			VendorID:  "vendor",
			PushID:    "push",
			HTTPSID:   "https",
			IssuerID:  "issuer",
			HTTPSCAID: "https-ca",
		},
		Environment: map[string]string{
			EnvStorage:           storage,
			EnvDSN:               dsn,
			EnvListen:            listen,
			EnvPublicURL:         publicURL,
			EnvOrganization:      organization,
			EnvStorageKeys:       storageKeyName,
			EnvSecretsDir:        filepath.Join(dir, "secrets"),
			EnvStorageKeysStrict: "true",
			EnvIdentity:          "acme",
		},
		SecretFiles: map[string]string{
			EnvBootstrapToken: filepath.Join(dir, "secrets", "admin"),
			EnvSCEPHMACKey:    filepath.Join(dir, "secrets", "issuance"),
		},
	}
	f.Setup.HTTP01Listen = o.HTTP01Listen
	for _, alias := range o.StorageKeyAliases {
		if alias == storageKeyName || alias == "admin" || alias == "issuance" || alias == "acme" ||
			alias == "database-dsn" ||
			strings.ContainsAny(alias, "/\\,") ||
			alias == "" ||
			alias == "." ||
			alias == ".." {
			return "", fmt.Errorf("%w: storage key alias", ErrConfig)
		}
		// #nosec G304 -- Operator bootstrap directory and validated key name; no remote path input.
		material, err := os.ReadFile(filepath.Join(dir, "secrets", storageKeyName))
		if err != nil {
			return "", wrapError(err)
		}
		if err := installSetupSecret(
			filepath.Join(dir, "secrets", alias),
			material,
			true,
		); err != nil {
			return "", wrapError(err)
		}
		f.Environment[EnvStorageKeys] += "," + alias
	}
	if o.ACMEKeyFile != "" {
		material, err := os.ReadFile(o.ACMEKeyFile)
		if err != nil {
			return "", wrapError(err)
		}
		path := filepath.Join(dir, "secrets", "acme")
		if err := installSetupSecret(path, material, true); err != nil {
			return "", wrapError(err)
		}
		f.SecretFiles[EnvACMEHMACKey] = path
	}
	for name, material := range o.AdditionalSecrets {
		if !strings.HasPrefix(name, "DM_") || strings.ContainsAny(name, "/\\") {
			return "", ErrConfig
		}
		path := filepath.Join(dir, "secrets", name)
		if err := installSetupSecret(path, material, true); err != nil {
			return "", wrapError(err)
		}
		delete(f.Environment, name)
		f.SecretFiles[name] = path
	}

	if storage != "sqlite" {
		dsnFile := filepath.Join(dir, "secrets", "database-dsn")
		if err = installSetupSecret(dsnFile, []byte(dsn), true); err != nil {
			return "", wrapError(err)
		}
		delete(f.Environment, EnvDSN)
		f.SecretFiles[EnvDSN] = dsnFile
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", wrapError(err)
	}
	if err = writeSetupFile(path, append(b, '\n')); err != nil && !errors.Is(err, os.ErrExist) {
		return "", wrapError(err)
	}
	return path, nil
}

// installSetupSecret checks protected existing files before reusing a bootstrap.
func installSetupSecret(path string, data []byte, mustMatch bool) error {
	if err := writeSetupFile(path, data); !errors.Is(err, os.ErrExist) {
		return wrapError(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return wrapError(err)
	}
	if !info.Mode().IsRegular() || privatefile.Check(path) != nil || info.Size() == 0 {
		return fmt.Errorf(
			"%w: existing secret must be a nonempty protected regular file",
			ErrConfig,
		)
	}
	if mustMatch {
		// #nosec G304 -- Protected secret in the operator's bootstrap directory.
		existing, err := os.ReadFile(path)
		if err != nil {
			return wrapError(err)
		}
		if !bytes.Equal(existing, data) {
			return fmt.Errorf(
				"%w: existing bootstrap secret differs from supplied material",
				ErrConfig,
			)
		}
	}
	return nil
}

// writeSetupFile publishes a fully synced file with an atomic exclusive link.
// A process interrupted before publication leaves no partial destination.
func writeSetupFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := privatefile.CreateTemp(dir, ".setup-*")
	if err != nil {
		return wrapError(err)
	}
	temp := f.Name()
	defer func() { _ = os.Remove(temp) }()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return wrapError(err)
	}
	if closeErr != nil {
		return wrapError(closeErr)
	}
	if err = os.Link(temp, path); err != nil {
		return wrapError(err)
	}
	// #nosec G304 -- Sync the parent of the operator-selected output file.
	directory, err := os.Open(dir)
	if err != nil {
		return wrapError(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(directory.Close)
	return wrapError(privatefile.SyncDirectory(directory))
}
