package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/internal/app"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/secrets"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

// TestPersistentStorageNeedsAKeyring holds a persistent deployment to a keyring. The
// secret columns hold the APNs push key, the escrowed bootstrap and unlock
// tokens and the user auth token, so without one a stolen backup, replica or
// volume is a fleet compromise rather than a data loss.
func TestPersistentStorageNeedsAKeyring(t *testing.T) {
	t.Parallel()
	_, err := app.Build(context.Background(), app.Config{
		Role: app.RoleAll, Storage: "sqlite",
		DSN: filepath.Join(t.TempDir(), "nokey.db"), Logger: quiet,
	})
	if !errors.Is(err, app.ErrConfig) {
		t.Fatalf("Build = %v, want a configuration error", err)
	}
	// An in-memory store has nothing persistent to protect.
	a, err := app.Build(context.Background(), app.Config{
		Role: app.RoleAll, Storage: "inmem", Logger: quiet,
	})
	if err != nil {
		t.Fatalf("inmem: %v", err)
	}
	_ = a.Close()
}

// TestSecretsAreSealedByTheAssembledServer reads the database the reference
// server writes, rather than one a test opened with a keyring of its own. The
// storage layer sealed correctly all along; nothing passed it a keyring, so
// every secret column was written in clear.
func TestSecretsAreSealedByTheAssembledServer(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "sealed.db")
	const token = "bootstrap-secret"
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "sqlite", DSN: dsn,
		StorageKeys: []string{"storage-v1"},
		Secrets:     secrets.Static{"storage-v1": []byte("0123456789abcdef0123456789abcdef")},
	})
	ctx := context.Background()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-SEALED"}
	pki, err := testpki.NewCA("sealed")
	if err != nil {
		t.Fatal(err)
	}
	device, err := pki.Issue(id.ID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticateAs(t, a, id.ID, device.Cert); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := a.Store.StoreBootstrapToken(ctx, id, []byte(token), time.Now()); err != nil {
		t.Fatalf("escrow: %v", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw []byte
	if err := db.QueryRowContext(
		ctx, "SELECT bootstrap_token FROM enrollments WHERE id = ?", id.ID,
	).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(token)) {
		t.Fatalf("bootstrap token is plaintext on disk: %q", raw)
	}
	if !crypt.IsSealed(raw) {
		t.Fatal("bootstrap token is not sealed")
	}
	if name, ok := crypt.KeyName(raw); !ok || name != "storage-v1" {
		t.Fatalf("sealed under %q", name)
	}
	// The server still reads its own ciphertext back.
	got, err := a.Store.BootstrapToken(ctx, id)
	if err != nil || string(got) != token {
		t.Fatalf("read back = %q %v", got, err)
	}
}

// TestStorageKeySources covers the three ways key material reaches the
// keyring, and the failures each one turns into a startup error rather than a
// surprise at the first write.
func TestStorageKeySources(t *testing.T) {
	const material = "0123456789abcdef0123456789abcdef"
	sqliteAt := func(t *testing.T) string {
		t.Helper()
		return filepath.Join(t.TempDir(), "keys.db")
	}

	t.Run("Directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "mounted"), []byte(material), 0o600); err != nil {
			t.Fatal(err)
		}
		a, err := app.Build(context.Background(), app.Config{
			Role: app.RoleAll, Storage: "sqlite", DSN: sqliteAt(t), Logger: quiet,
			StorageKeys: []string{"mounted"}, SecretsDir: dir,
		})
		if err != nil {
			t.Fatalf("secrets directory: %v", err)
		}
		_ = a.Close()
	})

	t.Run("DirectoryMissing", func(t *testing.T) {
		t.Parallel()
		_, err := app.Build(context.Background(), app.Config{
			Role: app.RoleAll, Storage: "sqlite", DSN: sqliteAt(t), Logger: quiet,
			StorageKeys: []string{"mounted"},
			SecretsDir:  filepath.Join(t.TempDir(), "absent"),
		})
		if err == nil {
			t.Fatal("a missing secrets directory was accepted")
		}
	})

	t.Run("Environment", func(t *testing.T) {
		// t.Setenv forbids a parallel test.
		t.Setenv("DM_STORAGE_KEY_FROM_ENV", material)
		a, err := app.Build(context.Background(), app.Config{
			Role: app.RoleAll, Storage: "sqlite", DSN: sqliteAt(t), Logger: quiet,
			StorageKeys: []string{"from-env"},
		})
		if err != nil {
			t.Fatalf("environment provider: %v", err)
		}
		_ = a.Close()
	})

	t.Run("KeyNotFound", func(t *testing.T) {
		t.Parallel()
		_, err := app.Build(context.Background(), app.Config{
			Role: app.RoleAll, Storage: "sqlite", DSN: sqliteAt(t), Logger: quiet,
			StorageKeys: []string{"never-set"},
		})
		if err == nil {
			t.Fatal("an unresolvable key name was accepted")
		}
	})
}
