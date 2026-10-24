package inventorystore_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/inventorystore"
	"github.com/deploymenttheory/go-apple-dm/server/recovery"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// checkMigrationContract uses the same SQLite/PostgreSQL/MySQL test pool after
// the ordinary backend contract has recreated an empty, inactive-fence schema.
func checkMigrationContract(t *testing.T, db *sql.DB, d sqlcommon.Dialect) {
	t.Helper()
	ctx := t.Context()
	keys, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "old", Strict: true}, Provider: secrets.Static{"old": bytes.Repeat([]byte{35}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := inventorystore.Open(ctx, db, d, keys)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := inventory.New(store)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	ref := source.Reference{Kind: "axm.device", AccountID: "original-account", ResourceID: "opaque"}
	original := json.RawMessage(`{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"ORIGINAL","future":[null,false,0,[],9007199254740993]},"relationships":{"owner":{"data":null}}}`)
	raw, err := json.Marshal(map[string]any{"id": "legacy", "serial_number": "FALSE-ALIAS", "fields": map[string]any{"model": "invented"}, "sources": map[string]inventory.Observation{ref.Key(): {Source: ref, ObservedAt: at, Raw: original}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, func(tx inventory.Tx) error {
		if err := tx.Put(ctx, "device/legacy", raw); err != nil {
			return err
		}
		if err := tx.Put(ctx, "alias/provisional", json.RawMessage(`"legacy"`)); err != nil {
			return err
		}
		return tx.Put(ctx, "credential/retained", json.RawMessage(`"sensitive-original"`))
	}); err != nil {
		t.Fatal(err)
	}
	// The guard activation, source extraction and old-key removal roll back with
	// the native protocol/event transaction rather than leaking a partial model.
	rollback := errors.New("native observer transaction rollback")
	u := sqlcommon.UnitOfWork{DB: db, Dialect: d}
	if err := u.Run(ctx, func(ctx context.Context) error {
		if err := repo.Migrate(ctx, at); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var model int
	if err := db.QueryRowContext(ctx, "SELECT evidence_model FROM inventory_lock WHERE id = 1").Scan(&model); err != nil || model != 0 {
		t.Fatal("partial fence activation", model, err)
	}
	if _, err := store.Get(ctx, "device/legacy"); err != nil {
		t.Fatal("legacy evidence lost on rollback", err)
	}
	// Hold migration after all its writes, while a transaction representing an
	// old native worker has already begun and read the previous schema model.
	var oldTx *sql.Tx
	if d.Name == "sqlite" {
		// SQLite's BEGIN IMMEDIATE owns the write lock immediately. Such an old
		// writer commits before migration and its evidence is included in extraction.
		oldTx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		beforeRaw, err := keys.Seal([]byte(`{"id":"no-original"}`), crypt.AAD("inventory_entries.payload", "device/before-migration"))
		if err != nil {
			_ = oldTx.Rollback()
			t.Fatal(err)
		}
		if _, err := oldTx.ExecContext(ctx, d.Rebind("INSERT INTO inventory_entries (entry_key,payload) VALUES (?,?)"), "device/before-migration", beforeRaw); err != nil {
			_ = oldTx.Rollback()
			t.Fatal(err)
		}
		if err := oldTx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := repo.Migrate(ctx, at); err != nil {
			t.Fatal(err)
		}
		oldTx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		oldTx, err = db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		if err := oldTx.QueryRowContext(ctx, "SELECT evidence_model FROM inventory_lock WHERE id = 1").Scan(&model); err != nil {
			_ = oldTx.Rollback()
			t.Fatal(err)
		}
		if err := repo.Migrate(ctx, at); err != nil {
			_ = oldTx.Rollback()
			t.Fatal(err)
		}
	}
	defer func() { _ = oldTx.Rollback() }()
	// These are the exact old Update lock statement and old native write. Under
	// PostgreSQL repeatable read the changed lock row may itself reject the old
	// transaction; under MySQL the trigger must perform a current locking read.
	_, lockErr := oldTx.ExecContext(ctx, "UPDATE inventory_lock SET version = version + 1 WHERE id = 1")
	if lockErr != nil && d.Name != "postgres" {
		t.Fatal(lockErr)
	}
	if lockErr == nil {
		_, err = oldTx.ExecContext(ctx, d.Rebind("INSERT INTO inventory_entries (entry_key,payload) VALUES (?,?)"), "device/late-old-observer", []byte(`{}`))
		if err == nil {
			t.Fatal("old native worker crossed committed migration fence")
		}
	}
	if err := oldTx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Get(ctx, "device/late-old-observer"); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatal(err)
	}
	resource, err := repo.SourceResource(ctx, ref)
	if err != nil || string(resource.Snapshot.Raw) != string(original) || resource.Incomplete {
		t.Fatal(resource, err)
	}
	if err := repo.Migrate(ctx, at.Add(time.Hour)); err != nil {
		t.Fatal("repeat migration", err)
	}
	for _, prefix := range []string{"device/", "alias/", "field/", "serial/", "source/"} {
		rows, err := store.Scan(ctx, prefix, "", 100)
		if err != nil || len(rows) != 0 {
			t.Fatal("retired source state survived", prefix, rows, err)
		}
	}
	if d.Name == "mysql" {
		ready := make(chan struct{})
		results := make(chan error, 6)
		for range 6 {
			go func() { <-ready; _, err := inventorystore.Open(ctx, db, d, keys); results <- err }()
		}
		close(ready)
		for range 6 {
			if err := <-results; err != nil {
				t.Fatal("concurrent fence open", err)
			}
		}
		if err := db.QueryRowContext(ctx, "SELECT evidence_model FROM inventory_lock WHERE id = 1").Scan(&model); err != nil || model != 1 {
			t.Fatal("concurrent open reset active fence", model, err)
		}
	}
	// All payloads, including history and model/checkpoint documents, remain
	// row-bound sealed records. Rotation accepts the old key while the new key
	// seals fresh evidence without modifying retained historical originals.
	rotated, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "new", Accepted: []string{"old"}, Strict: true}, Provider: secrets.Static{"old": bytes.Repeat([]byte{35}, 32), "new": bytes.Repeat([]byte{36}, 32)}})
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := inventorystore.Open(ctx, db, d, rotated)
	if err != nil {
		t.Fatal(err)
	}
	r, err := inventory.New(reopened)
	if err != nil {
		t.Fatal(err)
	}
	history, err := r.History(ctx, ref)
	if err != nil || len(history) != 1 || string(history[0].Raw) != string(original) {
		t.Fatal(history, err)
	}
	if value, err := reopened.Get(ctx, "credential/retained"); err != nil || string(value) != `"sensitive-original"` {
		t.Fatal("credential rotation lost state", err)
	}
	fresh := resource.Snapshot
	fresh.ObservedAt = at.Add(time.Hour)
	if _, err := r.Observe(ctx, "", fresh); err != nil {
		t.Fatal("new active key write", err)
	}
	// An authenticated source cannot be moved to another document key.
	var ciphertext []byte
	if err := db.QueryRowContext(ctx, d.Rebind("SELECT payload FROM inventory_entries WHERE entry_key = ?"), "resource/"+ref.Key()).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	name, sealed := crypt.KeyName(ciphertext)
	if !sealed || name != "new" || bytes.Contains(ciphertext, []byte("ORIGINAL")) {
		t.Fatal("unsealed migrated evidence")
	}
	checkActiveFenceRecovery(t, db, d, rotated, ref, original)
}

// checkFenceInstallationRetries verifies MySQL's nontransactional DDL recovery.
// Each partial install is retried without resetting an activated model marker.
func checkFenceInstallationRetries(t *testing.T, db *sql.DB, d sqlcommon.Dialect) {
	t.Helper()
	if d.Name != "mysql" {
		return
	}
	ctx := t.Context()
	set, err := inventorystore.MigrationSet(d)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := os.ReadFile("fences/mysql.sql")
	if err != nil {
		t.Fatal(err)
	}
	statements := strings.Split(strings.TrimSpace(string(fence)), "\n")
	for n := range len(statements) + 1 {
		t.Run(fmt.Sprintf("fence-ddl-interruption-%d", n), func(t *testing.T) {
			if _, err := sqlcommon.RollbackSet(ctx, db, d, set, 1); err != nil {
				t.Fatal(err)
			}
			// Deliberately leave schema version one while persisting a partial sequence.
			// A connection owns PREPARE session state across these fixture statements.
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range statements[:n] {
				if _, err := conn.ExecContext(ctx, statement); err != nil {
					_ = conn.Close()
					t.Fatal(err)
				}
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := inventorystore.Open(ctx, db, d, nil); err != nil {
				t.Fatal("recover partial installation", err)
			}
			if _, err := db.ExecContext(ctx, "UPDATE inventory_lock SET evidence_model = 1 WHERE id = 1"); err != nil {
				t.Fatal(err)
			}
			// Simulate the last DDL having committed before schema-version recording.
			if _, err := db.ExecContext(ctx, "DELETE FROM inventory_schema_migrations WHERE version = 2"); err != nil {
				t.Fatal(err)
			}
			if _, err := inventorystore.Open(ctx, db, d, nil); err != nil {
				t.Fatal("recover active installation", err)
			}
			var model int
			if err := db.QueryRowContext(ctx, "SELECT evidence_model FROM inventory_lock WHERE id = 1").Scan(&model); err != nil || model != 1 {
				t.Fatal("active fence reset", model, err)
			}
		})
	}
	// Return the empty inactive schema expected by ordinary evidence contracts.
	if _, err := sqlcommon.RollbackSet(ctx, db, d, set, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := inventorystore.Open(ctx, db, d, nil); err != nil {
		t.Fatal(err)
	}
}

// checkActiveFenceRecovery proves real encrypted snapshots restore the installer
// schema, original evidence and active old-write fence on every SQL backend.
func checkActiveFenceRecovery(t *testing.T, db *sql.DB, d sqlcommon.Dialect, keys *crypt.Keyring, ref source.Reference, original json.RawMessage) {
	t.Helper()
	ctx := t.Context()
	set, err := inventorystore.MigrationSet(d)
	if err != nil {
		t.Fatal(err)
	}
	schema := []sqlcommon.MigrationSet{set}
	source := recovery.SQL{DB: db, Dialect: d, Schema: schema}
	directory := filepath.Join(t.TempDir(), "snapshot")
	if err := source.Snapshot(ctx, directory); err != nil {
		t.Fatal(err)
	}
	if err := source.ValidateKeys(ctx, directory, keys); err != nil {
		t.Fatal("restored evidence AAD binding", err)
	}
	reset := func() {
		t.Helper()
		if _, err := sqlcommon.RollbackSet(ctx, db, d, set, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "DROP TABLE inventory_schema_migrations"); err != nil {
			t.Fatal(err)
		}
	}

	copySnapshot := func() string {
		t.Helper()
		target := t.TempDir()
		sourceRoot := snapshotRoot(t, directory)
		targetRoot := snapshotRoot(t, target)
		entries, err := fs.ReadDir(sourceRoot.FS(), ".")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			raw, err := sourceRoot.ReadFile(entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if err := targetRoot.WriteFile(entry.Name(), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return target
	}
	// A fence signature mismatch fails before initializing any target schema.
	mismatch := copySnapshot()
	mismatchRoot := snapshotRoot(t, mismatch)
	metadata, err := mismatchRoot.ReadFile("database.json")
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &info); err != nil {
		t.Fatal(err)
	}
	var descriptions []map[string]json.RawMessage
	if err := json.Unmarshal(info["schemas"], &descriptions); err != nil {
		t.Fatal(err)
	}
	descriptions[0]["signature"] = json.RawMessage(`"changed-fence"`)
	info["schemas"], err = json.Marshal(descriptions)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err = json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := mismatchRoot.WriteFile("database.json", metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	reset()
	if err := source.Restore(ctx, mismatch); !errors.Is(err, recovery.ErrInvalid) {
		t.Fatal("fence fingerprint mismatch accepted", err)
	}
	// A malformed row after an earlier row was read rolls back the complete data
	// transaction, including the source-model marker and inventory lock contents.
	interrupted := copySnapshot()
	interruptedRoot := snapshotRoot(t, interrupted)
	rows, err := interruptedRoot.ReadFile("inventory_entries.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	firstLine := bytes.IndexByte(rows, '\n')
	if firstLine < 0 {
		t.Fatal("snapshot has no complete row")
	}
	rows = append(append([]byte(nil), rows[:firstLine+1]...), []byte("{interrupted\n")...)
	if err := interruptedRoot.WriteFile("inventory_entries.jsonl", rows, 0o600); err != nil {
		t.Fatal(err)
	}
	interruptedErr := source.Restore(ctx, interrupted)
	if interruptedErr == nil {
		t.Fatal("interrupted restore succeeded")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM inventory_entries").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial restored evidence survived rollback", count, err, "restore error:", interruptedErr)
	}
	var model int
	if err := db.QueryRowContext(ctx, "SELECT evidence_model FROM inventory_lock WHERE id = 1").Scan(&model); err != nil || model != 0 {
		t.Fatal("partial active model survived failed restore", model, err)
	}
	reset()
	if err := source.Restore(ctx, directory); err != nil {
		t.Fatal("restore retry", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT evidence_model FROM inventory_lock WHERE id = 1").Scan(&model); err != nil || model != 1 {
		t.Fatal("active fence was not restored", model, err)
	}
	// Also exercise a restored marker beside the guard column's initial zero.
	// Normal reopening repairs this invariant before Migrate's done-marker return.
	if _, err := db.ExecContext(ctx, "UPDATE inventory_lock SET evidence_model = 0 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	reopened, err := inventorystore.Open(ctx, db, d, keys)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := inventory.New(reopened)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Migrate(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	record, err := repo.SourceResource(ctx, ref)
	if err != nil || string(record.Snapshot.Raw) != string(original) {
		t.Fatal("restored original lost", record, err)
	}
	if err := reopened.Update(ctx, func(tx inventory.Tx) error { return tx.Put(ctx, "device/after-restore", json.RawMessage(`{}`)) }); err == nil {
		t.Fatal("old writer bypassed restored fence")
	}
	if err := db.QueryRowContext(ctx, "SELECT evidence_model FROM inventory_lock WHERE id = 1").Scan(&model); err != nil || model != 1 {
		t.Fatal("reopened done marker left fence inactive", model, err)
	}
}

// snapshotRoot confines recovery-fixture reads and mutations to a private
// directory, including filenames obtained from the snapshot's directory entries.
func snapshotRoot(t *testing.T, directory string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root
}
