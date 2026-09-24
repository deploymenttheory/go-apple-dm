package dmctl_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/recovery"
)

// TestRecoveryCommandsPreserveEnrollmentAndIssuer checks recovery commands preserve enrollment and
// issuer.
func TestRecoveryCommandsPreserveEnrollmentAndIssuer(t *testing.T) {
	dir := t.TempDir()
	env := noConfig(t)
	call := func(args ...string) string {
		t.Helper()
		out, stderr, err := run(t, env, args...)
		if err != nil {
			t.Fatalf("%v: %v; %s", args, err, stderr)
		}
		return out
	}
	source := filepath.Join(dir, "source")
	call("setup", "init", "-dir", source, "-role", "combined")
	setup := filepath.Join(source, "setup.json")
	call("setup", "enrollment-ca", "create", "-setup-file", setup, "-cn", "Recovery issuer")
	call("setup", "enrollment-ca", "activate", "-setup-file", setup, "-revision", "1")
	cfg, err := app.LoadSetupFile(setup, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.OpenSetup(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "already-managed-mac"}
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, CertHash: "original-certificate", CertHashAt: at, TokenUpdatedAt: at, Push: mdm.Push{Topic: "original-push-topic", Token: []byte("original-token"), Magic: "original-magic"}}}); err != nil {
		t.Fatal(err)
	}
	material, err := a.Certificates.LoadMaterial(t.Context(), "enrollment-ca", "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(dir, "recovery.agekey")
	var generated map[string]string
	if err := json.Unmarshal([]byte(call("recovery", "keygen", "-identity-file", identity)), &generated); err != nil {
		t.Fatal(err)
	}
	ticket, archive := filepath.Join(dir, "ticket"), filepath.Join(dir, "checkpoint.age")
	call("recovery", "pause", "-setup-file", setup, "-ticket-file", ticket)
	if !strings.Contains(call("recovery", "status", "-setup-file", setup), "token") {
		t.Fatal("missing persistent fence")
	}
	call("recovery", "backup", "-setup-file", setup, "-ticket-file", ticket, "-archive", archive, "-recipient", generated["recipient"], "-revision", "reviewed-test", "-staging-dir", dir)
	call("recovery", "verify", "-archive", archive, "-identity-file", identity, "-staging-dir", dir)
	env["RECOVERY_VERIFY_DSN"] = filepath.Join(dir, "missing", "verify.sqlite")
	for _, args := range [][]string{
		{"keygen", "-identity-file", identity},
		{"pause", "-setup-file", setup, "-ticket-file", ticket},
		{"resume", "-setup-file", setup},
		{"resume", "-setup-file", setup, "-ticket-file", "missing"},
		{"backup", "-setup-file", setup, "-ticket-file", ticket},
		{"backup", "-setup-file", setup, "-ticket-file", ticket, "-archive", archive, "-revision", "test", "-recipient", "invalid"},
		{"verify", "-archive", archive, "-identity-file", "missing"},
		{"verify", "-archive", archive, "-identity-file", ticket},
		{"verify", "-archive", "missing", "-identity-file", identity, "-staging-dir", dir},
		{"verify", "-archive", archive, "-identity-file", identity, "-staging-dir", dir, "-verify-dsn-env", "RECOVERY_VERIFY_DSN"},
		{"restore", "-archive", archive, "-identity-file", identity, "-staging-dir", dir},
	} {
		if _, _, err := run(t, env, append([]string{"recovery"}, args...)...); err == nil {
			t.Fatal("accepted invalid recovery operation", args)
		}
	}
	if _, _, err := run(t, env, "recovery", "backup", "-setup-file", setup, "-ticket-file", ticket, "-archive", archive, "-recipient", generated["recipient"], "-revision", "reviewed-test", "-staging-dir", dir); err == nil {
		t.Fatal("overwrote existing archive")
	}
	var restored recovery.RestoreResult
	restoreArgs := []string{"recovery", "restore", "-archive", archive, "-identity-file", identity, "-target-dir", filepath.Join(dir, "restored"), "-staging-dir", dir}
	if _, _, err := run(t, env, restoreArgs...); err == nil {
		t.Fatal("restored without explicit source-stopped assertion")
	}
	restoreArgs = append(restoreArgs, "-source-stopped")
	if err := json.Unmarshal([]byte(call(restoreArgs...)), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.State != "paused" || restored.PublicURL != cfg.Enroll.PublicURL {
		t.Fatal(restored)
	}
	if _, _, err := run(t, env, restoreArgs...); err == nil {
		t.Fatal("overwrote restored deployment")
	}
	if _, _, err := run(t, env, "setup", "status", "-setup-file", restored.SetupFile); err == nil {
		t.Fatal("setup bypassed restored fence")
	}
	call("recovery", "resume", "-setup-file", restored.SetupFile, "-ticket-file", restored.TicketFile)
	targetConfig, err := app.LoadSetupFile(restored.SetupFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := app.OpenSetup(t.Context(), targetConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(target.Close)
	got, err := target.Store.Get(t.Context(), id)
	if err != nil || got.CertHash != "original-certificate" || got.Push.Topic != "original-push-topic" || string(got.Push.Token) != "original-token" || !got.TokenUpdatedAt.Equal(at) {
		t.Fatal("enrollment changed", got, err)
	}
	newMaterial, err := target.Certificates.LoadMaterial(t.Context(), "enrollment-ca", "1")
	if err != nil || !bytes.Equal(material.Key, newMaterial.Key) || !bytes.Equal(material.Certificate, newMaterial.Certificate) {
		t.Fatal("issuer identity changed", err)
	}
	for _, path := range []string{identity, archive, ticket, restored.SetupFile, restored.TicketFile} {
		if err := privatefile.Check(path); err != nil {
			t.Fatal("unprotected recovery artifact", path, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".recovery-") {
			t.Fatal("plaintext staging leaked", entry.Name())
		}
	}
}

// TestRecoveryPauseRetainsOwnershipAfterTimeout checks that recovery pause retains ownership after
// timeout.
func TestRecoveryPauseRetainsOwnershipAfterTimeout(t *testing.T) {
	dir, env := t.TempDir(), noConfig(t)
	source := filepath.Join(dir, "source")
	if _, _, err := run(t, env, "setup", "init", "-dir", source, "-role", "combined"); err != nil {
		t.Fatal(err)
	}
	setup := filepath.Join(source, "setup.json")
	cfg, err := app.LoadSetupFile(setup, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.OpenSetup(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(a.Close)
	s, err := recovery.OpenDatabase(t.Context(), cfg.Storage, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(s.DB.Close)
	control, err := maintenance.Open(t.Context(), s.DB, s.Dialect, false)
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := control.Register(t.Context(), "stopped-fixture")
	if err != nil {
		t.Fatal(err)
	}
	ticket := filepath.Join(dir, "owner")
	if _, _, err := run(t, env, "recovery", "pause", "-setup-file", setup, "-ticket-file", ticket, "-wait", "1ms"); err == nil || !strings.Contains(err.Error(), "remains paused") {
		t.Fatal("lost timed-out fence", err)
	}
	var status struct {
		Token   string `json:"Token"`
		Members []struct {
			ID string `json:"ID"`
		} `json:"Members"`
	}
	out, _, err := run(t, env, "recovery", "status", "-setup-file", setup)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatal(err)
	}
	if status.Token == "" || len(status.Members) != 2 {
		t.Fatal("missing persistent ownership", out)
	}
	wrong := filepath.Join(dir, "wrong")
	if err := os.WriteFile(wrong, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"pause", "-ticket-file", filepath.Join(dir, "competing")},
		{"resume", "-ticket-file", wrong},
		{"forget", "-ticket-file", ticket, "-member", status.Members[0].ID},
	} {
		if _, _, err := run(t, env, append([]string{"recovery"}, append(args, "-setup-file", setup)...)...); err == nil {
			t.Fatal("stole maintenance ownership", args)
		}
	}
	// OpenSetup has no background Run; closing it ends its finite operations.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, "recovery", "forget", "-setup-file", setup, "-ticket-file", ticket, "-member", stopped.ID(), "-process-stopped"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, "recovery", "resume", "-setup-file", setup, "-ticket-file", ticket); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		setup     string
		overrides map[string]string
	}{
		{"missing", nil},
		{setup, map[string]string{"DM_DSN": filepath.Join(dir, "missing", "db.sqlite")}},
		{setup, map[string]string{"DM_DSN": filepath.Join(dir, "empty.sqlite")}},
	} {
		for k, v := range tc.overrides {
			env[k] = v
		}
		if _, _, err := run(t, env, "recovery", "status", "-setup-file", tc.setup); err == nil {
			t.Fatal("accepted unavailable control database")
		}
	}
}

// TestRecoveryCommandUsage checks recovery-command usage validation.
func TestRecoveryCommandUsage(t *testing.T) {
	for _, args := range [][]string{
		{}, {"unknown"}, {"keygen"}, {"status"}, {"pause"}, {"verify"}, {"restore"}, {"verify", "-unknown"}, {"status", "extra"},
	} {
		if _, _, err := run(t, noConfig(t), append([]string{"recovery"}, args...)...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, _, err := run(t, noConfig(t), "recovery", "backup", "-h"); err != nil {
		t.Fatal(err)
	}
}
