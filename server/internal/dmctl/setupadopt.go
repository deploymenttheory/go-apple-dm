package dmctl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/internal/bench"
)

// runSetupBenchAdoption preserves the database, key IDs, admission settings and
// identifier credentials of an existing live bench. Repeating adoption validates
// the same identities; it never rotates a device pin or generates a new topic.
func runSetupBenchAdoption(ctx context.Context, e *env, source, destination, role string) error {
	w, err := bench.Load(source)
	if err != nil {
		return wrapError(err)
	}
	if w.Mode != "live" || w.Storage != "sqlite" || w.Topology != "all" {
		return fmt.Errorf(
			"%w: bench adoption requires a live SQLite workspace with all roles",
			ErrUsage,
		)
	}
	mdmDir := filepath.Join(w.Directory, "mdm")
	// #nosec G304 -- Fixed filename in the operator-selected local bench workspace.
	key, err := os.ReadFile(filepath.Join(mdmDir, "storage-key"))
	if err != nil {
		return wrapError(err)
	}
	dsn := w.DSN
	if dsn == "" {
		dsn = filepath.Join(mdmDir, "mdm.sqlite")
	}
	if _, err := os.Stat(dsn); err != nil {
		return fmt.Errorf("dmctl: existing enrollment database: %w", err)
	}
	settings := map[string][]byte{
		"DM_AUDIT_STORE": []byte("true"), "DM_ADMIN_STORE": []byte("true"),
		"DM_DISCOVERY":     []byte("Mac=mdm-adde,iPhone=mdm-byod"),
		"DM_PUSH_COALESCE": []byte("-1s"),
	}
	for name, value := range w.Settings {
		settings[name] = []byte(value)
	}
	for _, name := range []string{app.EnvStorage, app.EnvDSN, app.EnvStorageKeys, app.EnvSecretsDir, "DM_TLS_CERT_FILE", "DM_TLS_KEY_FILE", app.EnvEnrollCACertFile, app.EnvEnrollCAKeyFile, app.EnvEnrollTLSAnchorFile, "DM_CA_FILE", app.EnvPushSource} {
		delete(settings, name)
	}
	// The bench derives the ACME key independently from the exact trimmed master
	// string. Keep it even when the new bootstrap has its own SCEP HMAC key.
	if len(settings[app.EnvACMEHMACKey]) == 0 {
		hash := sha256.Sum256([]byte("bench ACME identifiers\x00" + strings.TrimSpace(string(key))))
		settings[app.EnvACMEHMACKey] = []byte(hex.EncodeToString(hash[:]))
	}
	// #nosec G304 -- Fixed filename in the operator-selected local bench workspace.
	challenge, err := os.ReadFile(filepath.Join(mdmDir, "scep-challenge"))
	if err != nil {
		return wrapError(err)
	}
	settings[app.EnvSCEPChallenge] = []byte(strings.TrimSpace(string(challenge)))
	path, err := app.InitSetupFile(
		app.SetupInitOptions{
			Directory:         destination,
			Role:              role,
			Storage:           "sqlite",
			DSN:               dsn,
			Listen:            w.Listen,
			PublicURL:         "https://" + w.Listen,
			Organization:      "go-apple-dm",
			StorageKeyFile:    filepath.Join(mdmDir, "storage-key"),
			StorageKeyName:    "bench",
			StorageKeyAliases: []string{"lab"},
			AdminTokenFile:    filepath.Join(mdmDir, "admin-token"),
			AdditionalSecrets: settings,
		},
	)
	if err != nil {
		return wrapError(err)
	}
	cfg, err := app.LoadSetupFile(path, e.getenv)
	if err != nil {
		return wrapError(err)
	}
	if cfg.DSN != dsn || cfg.Storage != w.Storage {
		return fmt.Errorf(
			"%w: destination setup references a different enrollment database",
			ErrUsage,
		)
	}
	a, err := app.OpenSetup(ctx, cfg)
	if err != nil {
		return wrapError(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(a.Close) // Close owns a bounded drain after cancellation.
	for _, identity := range []struct {
		id        string
		kind      lifecycle.Kind
		cert, key string
	}{
		{cfg.Setup.HTTPSCAID, lifecycle.Issuer, "ca.pem", "ca.key"},
		{cfg.Setup.IssuerID, lifecycle.Issuer, "ca.pem", "ca.key"},
		{cfg.Setup.HTTPSID, lifecycle.HTTPS, "tls.pem", "tls.key"},
		{cfg.Setup.PushID, lifecycle.Push, "push.pem", "push.key"},
	} {
		// #nosec G304 -- Constant certificate names above, under the local operator-selected workspace.
		cert, err := os.ReadFile(filepath.Join(mdmDir, identity.cert))
		if err != nil {
			return wrapError(err)
		}
		// #nosec G304 -- Constant key names above, under the local operator-selected workspace.
		key, err := os.ReadFile(filepath.Join(mdmDir, identity.key))
		if err != nil {
			return wrapError(err)
		}
		if _, err := a.ExecuteSetup(
			ctx,
			identity.kind,
			"adopt",
			app.SetupRequest{
				Request:     lifecycle.Request{ID: identity.id},
				Certificate: cert,
				Key:         key,
			},
		); err != nil {
			return wrapError(err)
		}
	}
	return wrapError(
		json.NewEncoder(e.stdout).
			Encode(map[string]string{"setupFile": path, "nextAction": "start dmserver with this setup file when ready to switch the listener; the existing device enrollment is preserved"}),
	)
}
