package app_test

import (
	"bytes"
	"crypto/rsa"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestFileVaultCertificateWorkflow checks FileVault certificate workflow.
func TestFileVaultCertificateWorkflow(t *testing.T) {
	ctx := t.Context()
	cfg := app.Config{
		Storage:        "sqlite",
		DSN:            filepath.Join(t.TempDir(), "encryption.db"),
		BootstrapToken: "secret",
	}
	a := build(t, cfg)
	srv := serve(t, a)
	id := seed(t, a, "D")
	original, err := mdm.NewCommand(
		&commands.RotateFileVaultKey{
			KeyType: "personal",
			FileVaultUnlock: commands.RotateFileVaultKeyFileVaultUnlock{
				Password: new("test-password"),
			},
		},
		mdm.WithUUID("rotate"),
	)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/admin/v1/enrollments/device/D/commands"
	if res := do(t, srv, "POST", endpoint, "wrong", original.Raw); res.StatusCode != 401 {
		t.Fatal("unauthorized", res.StatusCode)
	}
	db, err := sql.Open("sqlite", sqlite.DSN(cfg.DSN, sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(db.Close)
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM protocol_state WHERE record_key LIKE 'pki/command-encryption/%'").
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatal("unauthorized generation", count, err)
	}
	for attempt := range 2 {
		var result struct {
			Queued  int
			Skipped string
		}
		decode(t, do(t, srv, "POST", endpoint, "secret", original.Raw), 200, &result)
		if (attempt == 0 && result.Queued != 1) ||
			(attempt == 1 && (result.Queued != 0 || result.Skipped == "")) {
			t.Fatal("rotation not queued", result)
		}
	}
	page, err := a.Store.Commands(ctx, id, storage.CommandQuery{}, paging.Page{})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("queue", len(page.Items), err)
	}
	queued, err := mdm.DecodeCommand(page.Items[0].Command.Raw)
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := a.ReplyCertificates.Recipient(ctx, id, original.UUID)
	if err != nil ||
		!bytes.Equal(
			cert.Raw,
			requireType[*commands.RotateFileVaultKey](t, queued.Payload).ReplyEncryptionCertificate,
		) {
		t.Fatal("queued certificate", err)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, "SELECT value FROM protocol_state WHERE record_key LIKE 'pki/command-encryption/%'").
		Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !crypt.IsSealed(raw) || bytes.Contains(raw, []byte("PRIVATE KEY")) {
		t.Fatal("certificate binding not encrypted")
	}
	srv.Close()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := build(t, cfg)
	retained, retainedKey, err := restarted.ReplyCertificates.Recipient(ctx, id, original.UUID)
	if err != nil || !bytes.Equal(cert.Raw, retained.Raw) ||
		!requireType[*rsa.PublicKey](t, key.Public()).Equal(retainedKey.Public()) {
		t.Fatal("restart lost encryption identity", err)
	}
}

// TestFileVaultRejectsVolatileCertificateStorage checks that FileVault rejects volatile
// certificate storage.
func TestFileVaultRejectsVolatileCertificateStorage(t *testing.T) {
	a := build(t, app.Config{Storage: "inmem", BootstrapToken: "secret"})
	srv := serve(t, a)
	id := seed(t, a, "D")
	cmd, err := mdm.NewCommand(
		&commands.RotateFileVaultKey{
			KeyType: "personal",
			FileVaultUnlock: commands.RotateFileVaultKeyFileVaultUnlock{
				Password: new("test-password"),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if res := do(
		t,
		srv,
		"POST",
		"/admin/v1/enrollments/device/D/commands",
		"secret",
		cmd.Raw,
	); res.StatusCode != 500 {
		t.Fatal(res.StatusCode)
	}
	page, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("unrecoverable command queued", err)
	}
}

// TestFileVaultEscrowWorkflow checks FileVault escrow workflow.
func TestFileVaultEscrowWorkflow(t *testing.T) {
	a := build(
		t,
		app.Config{
			Storage:        "sqlite",
			DSN:            filepath.Join(t.TempDir(), "escrow.db"),
			BootstrapToken: "secret",
		},
	)
	srv := serve(t, a)
	id := seed(t, a, "D")
	body := []byte(
		`{"CommandUUID":"escrow-1","ProfileIdentifier":"com.example.escrow","Location":"Organization help desk"}`,
	)
	endpoint := "/admin/v1/enrollments/device/D/filevault/escrow"
	if res := do(t, srv, "POST", endpoint, "wrong", body); res.StatusCode != 401 {
		t.Fatal("unauthorized", res.StatusCode)
	}
	var result struct{ Queued int }
	decode(t, do(t, srv, "POST", endpoint, "secret", body), 200, &result)
	if result.Queued != 1 {
		t.Fatal("escrow not queued")
	}
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{})
	if err != nil || len(rows.Items) != 1 || rows.Items[0].Command.RequestType != "InstallProfile" {
		t.Fatal("escrow queue", err)
	}
	if _, _, err := a.ReplyCertificates.Recipient(t.Context(), id, "escrow-1"); err != nil {
		t.Fatal("missing escrow key", err)
	}
	if res := do(t, srv, "POST", endpoint, "secret", []byte(`{}`)); res.StatusCode != 400 {
		t.Fatal("empty request", res.StatusCode)
	}
}
