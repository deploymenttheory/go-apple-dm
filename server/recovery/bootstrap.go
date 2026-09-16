package recovery

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
)

// Bootstrap retains the reference server's versioned setup document. During
// capture, every file reference becomes relative to the private bundle root.
type Bootstrap struct {
	Version     int                        `json:"version"`
	Environment map[string]string          `json:"environment"`
	SecretFiles map[string]string          `json:"secretFiles"`
	Setup       map[string]json.RawMessage `json:"setup"`
}

// CaptureBootstrap copies referenced files and all named storage keys. Pass the
// same environment overrides used to start the source processes. External secret
// providers must first export the original named keys into the setup key directory.
func CaptureBootstrap(
	ctx context.Context,
	source, destination string,
	overrides map[string]string,
) (Bootstrap, error) {
	var b Bootstrap
	if err := readJSONFile(source, &b); err != nil {
		return b, err
	}
	if b.Version != 1 || b.Environment == nil || b.Setup == nil {
		return b, ErrInvalid
	}
	if b.SecretFiles == nil {
		b.SecretFiles = map[string]string{}
	}
	for key, value := range overrides {
		if strings.HasPrefix(key, "DM_") && value != "" {
			b.Environment[key] = value
			delete(b.SecretFiles, key)
		}
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return b, wrap(err)
	}
	files := filepath.Join(destination, "files")
	if err := os.Mkdir(files, 0o700); err != nil {
		return b, wrap(err)
	}
	copyReference := func(path string) (string, error) {
		rel := "files/" + strings.ToLower(rand.Text())
		return rel, copyRegular(ctx, path, filepath.Join(destination, rel))
	}
	// Stable ordering makes configuration review independent of map iteration.
	for _, key := range sortedKeys(b.SecretFiles) {
		path := b.SecretFiles[key]
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(source), path)
		}
		if strings.HasSuffix(key, "_FILE") || key == "DM_STORAGE_KEYS" || key == "DM_SECRETS_DIR" ||
			key == "DM_PUBLIC_URL" ||
			key == "DM_STORAGE" ||
			key == "DM_STORAGE_KEYS_STRICT" {
			value, err := readPrivate(path)
			if err != nil {
				return b, err
			}
			b.Environment[key] = strings.TrimSpace(string(value))
			delete(b.SecretFiles, key)
			continue
		}
		rel, err := copyReference(path)
		if err != nil {
			return b, fmt.Errorf("recovery: secret reference %s: %w", key, err)
		}
		b.SecretFiles[key] = rel
	}
	for _, key := range sortedKeys(b.Environment) {
		if !strings.HasSuffix(key, "_FILE") || key == "DM_SETUP_FILE" || b.Environment[key] == "" {
			continue
		}
		rel, err := copyReference(b.Environment[key])
		if err != nil {
			return b, fmt.Errorf("recovery: file reference %s: %w", key, err)
		}
		b.Environment[key] = rel
	}
	delete(b.Environment, "DM_SETUP_FILE")
	var vendorFile string
	if raw := b.Setup["vendorTokenFile"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &vendorFile); err != nil {
			return b, wrap(err)
		}
	}
	if vendorFile != "" {
		if !filepath.IsAbs(vendorFile) {
			vendorFile = filepath.Join(filepath.Dir(source), vendorFile)
		}
		rel, err := copyReference(vendorFile)
		if err != nil {
			return b, err
		}
		b.Setup["vendorTokenFile"], _ = json.Marshal(rel)
	}
	keys := strings.Split(b.Environment["DM_STORAGE_KEYS"], ",")
	keyDir := b.Environment["DM_SECRETS_DIR"]
	if len(keys) == 0 || strings.TrimSpace(keys[0]) == "" {
		return b, ErrInvalid
	}
	if err := os.Mkdir(filepath.Join(destination, "keys"), 0o700); err != nil {
		return b, wrap(err)
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if !validName(key) || strings.Contains(key, "/") {
			return b, ErrInvalid
		}
		env := (secrets.Env{Prefix: "DM_STORAGE_KEY_"}).Key(key)
		path := filepath.Join(destination, "keys", key)
		if value := b.Environment[env]; keyDir == "" && value != "" {
			if err := writePrivate(path, []byte(value)); err != nil {
				return b, err
			}
		} else {
			if keyDir == "" {
				return b, fmt.Errorf("%w: missing named storage key %s", ErrInvalid, key)
			}
			if err := copyRegular(ctx, filepath.Join(keyDir, key), path); err != nil {
				return b, err
			}
		}
		delete(b.Environment, env)
	}
	b.Environment["DM_SECRETS_DIR"] = "keys"
	if _, err := b.Keyring(ctx, destination); err != nil {
		return b, err
	}
	if err := b.validateURL(); err != nil {
		return b, err
	}
	return b, writeJSONFile(filepath.Join(destination, "bootstrap.json"), b)
}

func (b Bootstrap) validateURL() error {
	if b.Version != 1 || b.Setup == nil || b.SecretFiles == nil || b.Environment == nil {
		return ErrInvalid
	}
	u, err := url.Parse(b.Environment["DM_PUBLIC_URL"])
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" {
		return ErrInvalid
	}
	return nil
}

func (b Bootstrap) Keyring(ctx context.Context, directory string) (*crypt.Keyring, error) {
	names := strings.Split(b.Environment["DM_STORAGE_KEYS"], ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	if len(names) == 0 || names[0] == "" || b.Environment["DM_SECRETS_DIR"] != "keys" {
		return nil, ErrInvalid
	}
	provider, err := secrets.NewDir(filepath.Join(directory, "keys"))
	if err != nil {
		return nil, wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(provider.Close)
	k, err := crypt.NewKeyring(
		ctx,
		crypt.Options{
			Provider: provider,
			Keys: crypt.Keys{
				Active:   names[0],
				Accepted: names[1:],
				Strict:   b.Environment["DM_STORAGE_KEYS_STRICT"] == "true",
			},
		},
	)
	return k, wrap(err)
}

// Install writes a new runnable setup file only after isolated database checks.
// It preserves the public URL and original key IDs, replacing only local paths
// and the operator-selected empty target DSN. It never starts the server.
func (b Bootstrap) Install(directory, dsn string) (string, error) {
	if dsn == "" {
		return "", ErrInvalid
	}
	if err := b.validateURL(); err != nil {
		return "", err
	}
	b.Environment, b.SecretFiles, b.Setup = maps.Clone(
		b.Environment,
	), maps.Clone(
		b.SecretFiles,
	), maps.Clone(
		b.Setup,
	)
	directory, err := filepath.Abs(directory)
	if err != nil {
		return "", wrap(err)
	}
	resolve := func(rel string) (string, error) {
		if !validName(rel) || (!strings.HasPrefix(rel, "files/") && rel != "keys") {
			return "", ErrInvalid
		}
		return filepath.Join(directory, rel), nil
	}
	for key, rel := range b.SecretFiles {
		if key == "DM_DSN" {
			continue
		}
		b.SecretFiles[key], err = resolve(rel)
		if err != nil {
			return "", err
		}
	}
	for key, value := range b.Environment {
		if value != "" && (strings.HasSuffix(key, "_FILE") || key == "DM_SECRETS_DIR") {
			b.Environment[key], err = resolve(value)
			if err != nil {
				return "", err
			}
		}
	}
	var vendorFile string
	if raw := b.Setup["vendorTokenFile"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &vendorFile); err != nil {
			return "", wrap(err)
		}
	}
	if vendorFile != "" {
		vendorFile, err = resolve(vendorFile)
		if err != nil {
			return "", err
		}
		b.Setup["vendorTokenFile"], _ = json.Marshal(vendorFile)
	}
	delete(b.Environment, "DM_DSN")
	dsnFile := filepath.Join(directory, "database-dsn")
	if err := writePrivate(dsnFile, []byte(dsn)); err != nil {
		return "", err
	}
	b.SecretFiles["DM_DSN"] = dsnFile
	path := filepath.Join(directory, "setup.json")
	return path, writeJSONFile(path, b)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
