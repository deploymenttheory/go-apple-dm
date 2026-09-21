package dmctl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	adminsql "github.com/deploymenttheory/go-apple-dm/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/recovery"
)

func TestRootRecoveryRequiresOwnedFenceAndCapturesAudit(t *testing.T) {
	dir := t.TempDir()
	env := noConfig(t)
	if _, _, err := run(t, env, "setup", "init", "-dir", dir); err != nil {
		t.Fatal(err)
	}
	setup, ticket, output := filepath.Join(dir, "setup.json"), filepath.Join(dir, "ticket"), filepath.Join(dir, "root-token")
	initialConfig, err := app.LoadSetupFile(setup, nil)
	if err != nil {
		t.Fatal(err)
	}
	initialDB, err := recovery.OpenDatabase(t.Context(), initialConfig.Storage, initialConfig.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adminsql.Open(t.Context(), initialDB.DB, initialDB.Dialect, adminsql.Options{}); err != nil {
		t.Fatal(err)
	}
	if err := initialDB.DB.Close(); err != nil {
		t.Fatal(err)
	}
	args := []string{"auth", "recover-root", "recovered", "-setup-file", setup, "-ticket-file", ticket, "-token-file", output}
	if _, _, err := run(t, env, args...); err == nil {
		t.Fatal("recovery without a fence succeeded")
	}
	if _, _, err := run(t, env, "recovery", "pause", "-setup-file", setup, "-ticket-file", ticket); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, args...); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- The test owns its isolated credential file.
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Check(output); err != nil {
		t.Fatal("unprotected recovery token", err)
	}
	cfg, err := app.LoadSetupFile(setup, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := recovery.OpenDatabase(t.Context(), cfg.Storage, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.DB.Close() }()
	st, err := adminsql.Open(t.Context(), db.DB, db.Dialect, adminsql.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	m, err := adminauth.New(st, reg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Authenticate(t.Context(), adminauth.Token(strings.TrimSpace(string(raw))))
	if err != nil || !p.Root || len(p.Roles) != 0 {
		t.Fatal("invalid recovered principal", err)
	}
	policies, err := st.Policies(t.Context())
	if err != nil || len(policies) != 0 {
		t.Fatal("recovery granted operational access", err)
	}
	control, err := maintenance.Open(t.Context(), db.DB, db.Dialect, false)
	if err != nil {
		t.Fatal(err)
	}
	status, err := control.Status(t.Context())
	if err != nil || !status.Ready() {
		t.Fatal("recovery released fence", err)
	}
	events, err := eventstore.Open(t.Context(), db.DB, db.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	records, err := events.Records(t.Context(), "admin-action", "", 100)
	if err != nil || len(records) != 1 || records[0].Actor != "local-recovery" || records[0].Fields["Action"] != "recoverRoot" {
		t.Fatal("missing recovery audit", err)
	}
}
