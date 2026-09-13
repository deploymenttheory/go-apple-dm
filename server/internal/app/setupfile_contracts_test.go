package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapSQLSecretsAndEnvironmentOverrides(t *testing.T) {
	for _, storage := range []string{"postgres", "mysql"} {
		t.Run(storage, func(t *testing.T) {
			dir := t.TempDir()
			key := filepath.Join(dir, "original-key")
			setupRequire(t, os.WriteFile(key, bytes.Repeat([]byte("k"), 64), 0o600), nil)
			dsn := "user:password@database.example/database"
			path, err := InitSetupFile(
				SetupInitOptions{
					Directory:         filepath.Join(dir, "managed"),
					Role:              "customer",
					Storage:           storage,
					DSN:               dsn,
					ACMEKeyFile:       key,
					StorageKeyFile:    key,
					AdminTokenFile:    key,
					IssuanceKeyFile:   key,
					StorageKeyAliases: []string{"legacy"},
					AdditionalSecrets: map[string][]byte{EnvSCEPChallenge: []byte("preserved")},
				},
			)
			setupRequire(t, err, nil)
			b, err := os.ReadFile(path)
			setupRequire(t, err, nil)
			if bytes.Contains(b, []byte("password")) {
				t.Fatal("database credential in public bootstrap")
			}
			cfg, err := LoadSetupFile(path, nil)
			setupRequire(t, err, nil)
			if cfg.DSN != dsn || cfg.Storage != storage {
				t.Fatal("database configuration changed")
			}
			var document SetupFile
			setupRequire(t, json.Unmarshal(b, &document), nil)
			document.SecretFiles[EnvAdminToken] = "missing-token"
			document.Setup.VendorTokenFile = "vendor-token"
			b, err = json.Marshal(document)
			setupRequire(t, err, nil)
			setupRequire(t, os.WriteFile(path, b, 0o600), nil)
			if _, err = LoadSetupFile(path, nil); err == nil {
				t.Fatal("missing secret ignored")
			}
			cfg, err = LoadSetupFile(path, func(name string) string {
				if name == EnvAdminToken {
					return "explicit-token"
				}
				return ""
			})
			setupRequire(t, err, nil)
			if cfg.AdminToken != "explicit-token" ||
				cfg.Setup.VendorTokenFile != filepath.Join(filepath.Dir(path), "vendor-token") {
				t.Fatal("explicit override or relative path lost")
			}
		})
	}
}

func TestBootstrapRejectsInvalidReferencesAndPartialSecrets(t *testing.T) {
	for _, mode := range []string{"role", "storage", "key name", "key alias", "missing key", "short key", "missing ACME", "invalid additional", "directory blocked", "secret blocked", "alias blocked", "ACME blocked", "additional blocked", "DSN missing", "DSN blocked", "setup blocked"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			o := SetupInitOptions{
				Directory: filepath.Join(dir, "managed"),
				Role:      "customer",
				Storage:   "sqlite",
			}
			key := filepath.Join(dir, "key")
			setupRequire(t, os.WriteFile(key, []byte("short"), 0o600), nil)
			block := ""
			switch mode {
			case "role":
				o.Role = "unknown"
			case "storage":
				o.Storage = "inmem"
			case "key name":
				o.StorageKeyName = "../key"
			case "key alias":
				o.StorageKeyAliases = []string{"admin"}
			case "missing key":
				o.StorageKeyFile = key + "missing"
			case "short key":
				o.StorageKeyFile = key
			case "missing ACME":
				o.ACMEKeyFile = key + "missing"
			case "invalid additional":
				o.AdditionalSecrets = map[string][]byte{"OTHER": []byte("value")}
			case "directory blocked":
				setupRequire(t, os.WriteFile(o.Directory, []byte("preserve"), 0o600), nil)
			case "secret blocked":
				block = "storage"
			case "alias blocked":
				o.StorageKeyAliases = []string{"legacy"}
				block = "legacy"
			case "ACME blocked":
				o.ACMEKeyFile = key
				block = "acme"
			case "additional blocked":
				o.AdditionalSecrets = map[string][]byte{"DM_TEST": []byte("value")}
				block = "DM_TEST"
			case "DSN missing":
				o.Storage = "postgres"
			case "DSN blocked":
				o.Storage, o.DSN, block = "postgres", "secret-dsn", "database-dsn"
			case "setup blocked":
				setupRequire(t, os.MkdirAll(o.Directory, 0o700), nil)
				setupRequire(
					t,
					os.Symlink("setup.json", filepath.Join(o.Directory, "setup.json")),
					nil,
				)
			}
			if block != "" {
				setupRequire(
					t,
					os.MkdirAll(filepath.Join(o.Directory, "secrets", block), 0o700),
					nil,
				)
			}
			if _, err := InitSetupFile(o); err == nil {
				t.Fatal("invalid bootstrap accepted")
			}
		})
	}
	for _, mode := range []string{"empty", "permissions", "different", "symlink", "directory", "same"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret")
			data := []byte("original")
			switch mode {
			case "directory":
				setupRequire(t, os.Mkdir(path, 0o700), nil)
			case "symlink":
				setupRequire(t, os.WriteFile(path+"target", data, 0o600), nil)
				setupRequire(t, os.Symlink(path+"target", path), nil)
			default:
				if mode == "empty" {
					data = nil
				}
				setupRequire(t, os.WriteFile(path, data, 0o600), nil)
				if mode == "permissions" {
					setupRequire(t, os.Chmod(path, 0o644), nil)
				}
			}
			wanted := []byte("original")
			if mode == "different" {
				wanted = []byte("replacement")
			}
			err := installSetupSecret(path, wanted, true)
			if mode == "same" {
				setupRequire(t, err, nil)
			} else {
				setupRequire(t, err, ErrConfig)
			}
		})
	}
	for _, text := range []string{"invalid JSON", `{"version":2}`, `{"version":1,"environment":{"DM_STORAGE_KEYS_STRICT":"invalid"}}`} {
		path := filepath.Join(t.TempDir(), "setup.json")
		setupRequire(t, os.WriteFile(path, []byte(text), 0o600), nil)
		if _, err := LoadSetupFile(path, nil); err == nil {
			t.Fatal("invalid setup loaded", text)
		}
	}
	if err := writeSetupFile(
		filepath.Join(t.TempDir(), "missing", "file"),
		[]byte("data"),
	); err == nil {
		t.Fatal("missing output directory ignored")
	}
	if _, err := OpenSetup(t.Context(), Config{}); err == nil {
		t.Fatal("missing setup accepted")
	}
	if _, err := OpenSetup(
		t.Context(),
		Config{Storage: "unknown", Setup: &SetupConfig{Role: "customer"}},
	); err == nil {
		t.Fatal("storage failure ignored", err)
	}
}
