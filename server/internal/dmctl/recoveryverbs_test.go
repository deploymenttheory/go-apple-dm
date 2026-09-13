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
	"github.com/deploymenttheory/go-apple-dm/server/recovery"
)

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
	call("setup", "issuer", "create", "-setup-file", setup, "-cn", "Recovery issuer")
	call("setup", "issuer", "activate", "-setup-file", setup, "-revision", "1")
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
	material, err := a.Certificates.LoadMaterial(t.Context(), "issuer", "1")
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
	defer target.Close()
	got, err := target.Store.Get(t.Context(), id)
	if err != nil || got.CertHash != "original-certificate" || got.Push.Topic != "original-push-topic" || string(got.Push.Token) != "original-token" || !got.TokenUpdatedAt.Equal(at) {
		t.Fatal("enrollment changed", got, err)
	}
	newMaterial, err := target.Certificates.LoadMaterial(t.Context(), "issuer", "1")
	if err != nil || !bytes.Equal(material.Key, newMaterial.Key) || !bytes.Equal(material.Certificate, newMaterial.Certificate) {
		t.Fatal("issuer identity changed", err)
	}
	for _, path := range []string{identity, archive, ticket, restored.SetupFile, restored.TicketFile} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
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
