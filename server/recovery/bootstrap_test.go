package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapFixture(t *testing.T) (string, Bootstrap) {
	t.Helper()
	dir := t.TempDir()
	keys := filepath.Join(dir, "secrets")
	if err := os.Mkdir(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"original-key.v1", "retired-key.v0", "admin", "policy", "vendor-token", "trust"} {
		if err := writePrivate(filepath.Join(keys, name), []byte(strings.Repeat(name, 4))); err != nil {
			t.Fatal(err)
		}
	}
	if err := writePrivate(filepath.Join(keys, "policy-reference"), []byte(filepath.Join(keys, "policy"))); err != nil {
		t.Fatal(err)
	}
	b := Bootstrap{
		Version:     1,
		Environment: map[string]string{"DM_STORAGE": "sqlite", "DM_DSN": filepath.Join(dir, "database.sqlite"), "DM_PUBLIC_URL": "https://mdm.example.test", "DM_STORAGE_KEYS": "original-key.v1,retired-key.v0", "DM_SECRETS_DIR": keys, "DM_STORAGE_KEYS_STRICT": "true", "DM_CA_FILE": filepath.Join(keys, "trust")},
		SecretFiles: map[string]string{"DM_ADMIN_TOKEN": "secrets/admin", "DM_ENROLLMENT_POLICY_FILE": "secrets/policy-reference"},
		Setup:       map[string]json.RawMessage{"role": json.RawMessage(`"combined"`), "vendorTokenFile": json.RawMessage(`"secrets/vendor-token"`)},
	}
	path := filepath.Join(dir, "setup.json")
	if err := writeJSONFile(path, b); err != nil {
		t.Fatal(err)
	}
	return path, b
}

func TestBootstrapCapturesReferencedFilesAndOriginalKeyNames(t *testing.T) {
	source, original := bootstrapFixture(t)
	dir := filepath.Join(t.TempDir(), "captured")
	b, err := CaptureBootstrap(t.Context(), source, dir, map[string]string{"DM_ADMIN_TOKEN": "replacement-admin-token", "DM_SETUP_FILE": source, "UNRELATED": "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Environment["DM_ADMIN_TOKEN"] != "replacement-admin-token" || b.Environment["UNRELATED"] != "" || b.Environment["DM_SETUP_FILE"] != "" {
		t.Fatal("override precedence changed")
	}
	if b.Environment["DM_STORAGE_KEYS"] != original.Environment["DM_STORAGE_KEYS"] {
		t.Fatal("key IDs changed")
	}
	for _, key := range []string{"original-key.v1", "retired-key.v0"} {
		got, err := readPrivate(filepath.Join(dir, "keys", key))
		want, readErr := readPrivate(filepath.Join(original.Environment["DM_SECRETS_DIR"], key))
		if err != nil || readErr != nil || !bytes.Equal(want, got) {
			t.Fatal("key changed", err, readErr)
		}
	}
	for _, key := range []string{"DM_CA_FILE", "DM_ENROLLMENT_POLICY_FILE"} {
		if !strings.HasPrefix(b.Environment[key], "files/") {
			t.Fatal("file reference not captured", key)
		}
		if _, err := readPrivate(filepath.Join(dir, b.Environment[key])); err != nil {
			t.Fatal(err)
		}
	}
	setup, err := b.Install(dir, filepath.Join(dir, "restored.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var installed Bootstrap
	if err := readJSONFile(setup, &installed); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(installed.Environment["DM_CA_FILE"]) || installed.Environment["DM_PUBLIC_URL"] != original.Environment["DM_PUBLIC_URL"] || installed.SecretFiles["DM_DSN"] == "" {
		t.Fatal("invalid installed references")
	}
	if b.Environment["DM_SECRETS_DIR"] != "keys" {
		t.Fatal("install mutated the verified source document")
	}
	if _, err := b.Install(dir, "another.sqlite"); err == nil {
		t.Fatal("overwrote installed setup")
	}
}

func TestBootstrapCapturesEnvironmentKeyProvider(t *testing.T) {
	source, _ := bootstrapFixture(t)
	var b Bootstrap
	if err := readJSONFile(source, &b); err != nil {
		t.Fatal(err)
	}
	b.Environment["DM_SECRETS_DIR"] = ""
	b.Environment["DM_STORAGE_KEYS"] = "original-key.v1"
	b.Environment["DM_STORAGE_KEY_ORIGINAL_KEY_V1"] = strings.Repeat("k", 32)
	raw, _ := json.Marshal(b)
	if err := os.WriteFile(source, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "captured")
	got, err := CaptureBootstrap(t.Context(), source, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := readPrivate(filepath.Join(dir, "keys", "original-key.v1"))
	if err != nil || string(key) != strings.Repeat("k", 32) || got.Environment["DM_STORAGE_KEY_ORIGINAL_KEY_V1"] != "" {
		t.Fatal("environment key not captured safely", err)
	}
}

func TestCaptureRefusesIncompleteAndUnsafeSetup(t *testing.T) {
	cases := map[string]func(*Bootstrap){
		"version":                  func(b *Bootstrap) { b.Version = 2 },
		"missing environment":      func(b *Bootstrap) { b.Environment = nil },
		"missing setup":            func(b *Bootstrap) { b.Setup = nil },
		"insecure URL":             func(b *Bootstrap) { b.Environment["DM_PUBLIC_URL"] = "http://mdm.example.test" },
		"URL credentials":          func(b *Bootstrap) { b.Environment["DM_PUBLIC_URL"] = "https://user:password@mdm.example.test" },
		"missing key":              func(b *Bootstrap) { b.Environment["DM_STORAGE_KEYS"] = "lost-key" },
		"missing key names":        func(b *Bootstrap) { b.Environment["DM_STORAGE_KEYS"] = "" },
		"key traversal":            func(b *Bootstrap) { b.Environment["DM_STORAGE_KEYS"] = "../storage" },
		"no key provider":          func(b *Bootstrap) { b.Environment["DM_SECRETS_DIR"] = "" },
		"missing secret":           func(b *Bootstrap) { b.SecretFiles["DM_ADMIN_TOKEN"] = "missing-file" },
		"missing reference file":   func(b *Bootstrap) { b.SecretFiles["DM_CA_FILE"] = "missing-file" },
		"missing certificate":      func(b *Bootstrap) { b.Environment["DM_CA_FILE"] = "missing-cert" },
		"invalid vendor path type": func(b *Bootstrap) { b.Setup["vendorTokenFile"] = json.RawMessage(`17`) },
		"missing vendor file":      func(b *Bootstrap) { b.Setup["vendorTokenFile"] = json.RawMessage(`"missing-vendor"`) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			source, b := bootstrapFixture(t)
			mutate(&b)
			raw, _ := json.Marshal(b)
			if err := os.WriteFile(source, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := CaptureBootstrap(t.Context(), source, filepath.Join(t.TempDir(), "capture"), nil); err == nil {
				t.Fatal("accepted incomplete setup")
			}
		})
	}
	source, _ := bootstrapFixture(t)
	if _, err := CaptureBootstrap(t.Context(), "missing", filepath.Join(t.TempDir(), "capture"), nil); err == nil {
		t.Fatal("accepted missing setup")
	}
	if _, err := CaptureBootstrap(t.Context(), source, t.TempDir(), nil); err == nil {
		t.Fatal("overwrote staging directory")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CaptureBootstrap(ctx, source, filepath.Join(t.TempDir(), "cancelled"), nil); err == nil {
		t.Fatal("ignored cancelled capture")
	}
}

func TestBootstrapInstallRejectsEscapingReferences(t *testing.T) {
	for _, change := range []func(*Bootstrap){
		func(b *Bootstrap) { b.Environment["DM_SECRETS_DIR"] = "../elsewhere" },
		func(b *Bootstrap) { b.SecretFiles["DM_ADMIN_TOKEN"] = "/etc/private" },
		func(b *Bootstrap) { b.Setup["vendorTokenFile"] = json.RawMessage(`"../vendor"`) },
		func(b *Bootstrap) { b.Setup["vendorTokenFile"] = json.RawMessage(`false`) },
	} {
		source, _ := bootstrapFixture(t)
		dir := filepath.Join(t.TempDir(), "captured")
		b, err := CaptureBootstrap(t.Context(), source, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		change(&b)
		if _, err := b.Install(dir, "target.sqlite"); err == nil {
			t.Fatal("installed unsafe reference")
		}
	}
	if _, err := (Bootstrap{}).Install(t.TempDir(), "target"); err == nil {
		t.Fatal("installed missing bootstrap")
	}
	if _, err := (Bootstrap{}).Install(t.TempDir(), ""); err == nil {
		t.Fatal("accepted empty target DSN")
	}
	if _, err := (Bootstrap{}).Keyring(t.Context(), t.TempDir()); err == nil {
		t.Fatal("accepted missing keyring")
	}
}

func TestBootstrapUsesOriginalEnvironmentAndRefusesInvalidKeyMaterial(t *testing.T) {
	for _, fault := range []string{"", "duplicate environment key", "short key"} {
		t.Run(fault, func(t *testing.T) {
			source, b := bootstrapFixture(t)
			b.SecretFiles = nil
			b.Environment["DM_SECRETS_DIR"] = ""
			b.Environment["DM_STORAGE_KEYS"] = "original-key.v1"
			b.Environment["DM_STORAGE_KEY_ORIGINAL_KEY_V1"] = strings.Repeat("k", 32)
			if fault == "duplicate environment key" {
				b.Environment["DM_STORAGE_KEYS"] += ",original-key.v1"
			}
			if fault == "short key" {
				b.Environment["DM_STORAGE_KEY_ORIGINAL_KEY_V1"] = "short"
			}
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "capture")
			captured, err := CaptureBootstrap(t.Context(), source, dir, nil)
			if fault != "" {
				if err == nil {
					t.Fatal("accepted invalid storage keys")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			captured.SecretFiles["DM_DSN"] = "previous-database-reference"
			if _, err := captured.Install(dir, "restored.sqlite"); err != nil {
				t.Fatal("failed to replace source DSN", err)
			}
		})
	}
}
