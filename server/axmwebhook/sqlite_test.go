package axmwebhook

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/inventorystore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestSQLiteReceipt runs the callback contract against encrypted shared SQL state.
func TestSQLiteReceipt(t *testing.T) {
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "webhooks.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keys, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "test", Strict: true}, Provider: secrets.Static{"test": bytes.Repeat([]byte{42}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	checkSQLReceipt(t, db, sqlite.Dialect, keys)
}

// checkSQLReceipt exercises concurrent retries, encrypted originals and restart.
func checkSQLReceipt(t *testing.T, db *sql.DB, d sqlcommon.Dialect, keys *crypt.Keyring) {
	t.Helper()
	store, err := inventorystore.Open(t.Context(), db, d, keys)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := inventory.New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	account := inventory.Account{ID: "webhook-test", Enabled: true, Revision: 1, CreatedAt: time.Now().UTC()}
	if err := store.Update(t.Context(), func(tx inventory.Tx) error { return save(t.Context(), tx, "account/webhook-test", account) }); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{Repository: repo}
	b, err := c.Prepare(t.Context(), account.ID, "https://mdm.example")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := c.SigningSecret(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	b, err = c.SetState(t.Context(), b, "active", "remote")
	if err != nil {
		t.Fatal(err)
	}
	body := eventBody("private-event", axm.NotificationDeviceChanged)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := c.Receive(t.Context(), b.ID, signed(t.Context(), body, secret)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	receipts, err := c.Receipts(t.Context(), account.ID, b.ID, "", true)
	if err != nil || len(receipts) != 1 {
		t.Fatal("duplicates persisted", len(receipts), err)
	}
	var sealed []byte
	if err := db.QueryRowContext(t.Context(), d.Rebind("SELECT payload FROM inventory_entries WHERE entry_key = ?"), "axm-webhook/receipt/"+receipts[0].ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if !crypt.IsSealed(sealed) || bytes.Contains(sealed, []byte("private-event")) {
		t.Fatal("receipt stored in plaintext")
	}
	reopened, err := inventorystore.Open(t.Context(), db, d, keys)
	if err != nil {
		t.Fatal(err)
	}
	repo, err = inventory.New(reopened)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &Coordinator{Repository: repo}
	again, err := restarted.Receive(t.Context(), b.ID, signed(t.Context(), body, secret))
	if err != nil || again.ID != receipts[0].ID {
		t.Fatal("restart lost deduplication", err)
	}
	jobs, err := repo.Jobs(t.Context(), "", 100)
	if err != nil || len(jobs) != 1 {
		t.Fatal("duplicate work", len(jobs), err)
	}
}
