package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestSnapshotSecretBindingsRejectSwappedRowsAndPurposes checks snapshot secret bindings reject
// swapped rows and purposes.
func TestSnapshotSecretBindingsRejectSwappedRowsAndPurposes(t *testing.T) {
	for _, tc := range []struct {
		table, column string
		columns, keys []string
		composite     bool
		aadKey        string
	}{
		{"inventory_entries", "payload", []string{"entry_key"}, []string{"credential/source"}, false, "credential/source"},
		{"push_certs", "key_pem", []string{"topic"}, []string{"original-topic"}, false, "original-topic"},
		{"dep_keypairs", "key_pem", []string{"account", "stage"}, []string{"account", "pending"}, false, "account/pending"},
		{"protocol_state", "value", []string{"record_key"}, []string{"pki/issuer"}, true, ""},
		{"webhook_subscriptions", "config", []string{"id"}, []string{"subscription-1"}, true, ""},
		{"webhook_messages", "payload", []string{"delivery_id"}, []string{"delivery-1"}, true, ""},
		{"ddm_snapshot_items", "expanded", []string{"enrollment_id", "kind", "identifier"}, []string{"device", "configuration", "temporary"}, true, ""},
	} {
		t.Run(tc.table, func(t *testing.T) {
			keys, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "original", Strict: true}, Provider: secrets.Static{"original": bytes.Repeat([]byte{7}, 32)}})
			if err != nil {
				t.Fatal(err)
			}
			purpose := tc.table + "." + tc.column
			aad := crypt.AAD(purpose, tc.aadKey)
			if tc.composite {
				aad = sqlcommon.BlobAAD(purpose, tc.keys...)
			}
			sealed, err := keys.Seal([]byte("private material"), aad)
			if err != nil {
				t.Fatal(err)
			}
			good := []cell{}
			for _, key := range tc.keys {
				good = append(good, cell{Kind: "text", Value: key})
			}
			blob, err := encodeCell(sealed, "BLOB")
			if err != nil {
				t.Fatal(err)
			}
			good = append(good, blob)
			table := tableSnapshot{Name: tc.table, Columns: append(tc.columns, tc.column), Rows: 1}
			for _, fault := range []string{"", "swapped row", "wrong key type", "unknown column", "unsealed", "bad cell", "short row", "truncated", "extra row", "cancelled", "missing"} {
				t.Run(fault, func(t *testing.T) {
					row := append([]cell{}, good...)
					tab := table
					tab.Columns = append([]string{}, table.Columns...)
					switch fault {
					case "swapped row":
						row[0].Value += "-swapped"
					case "wrong key type":
						row[0] = cell{Kind: "int", Value: "1"}
					case "unknown column":
						tab.Columns[len(tab.Columns)-1] = "unregistered"
					case "unsealed":
						row[len(row)-1], err = encodeCell([]byte("plaintext"), "BLOB")
					case "bad cell":
						row[0].Kind = "broken"
					case "short row":
						row = row[:1]
					}
					if err != nil {
						t.Fatal(err)
					}
					raw, err := json.Marshal(row)
					if err != nil {
						t.Fatal(err)
					}
					if fault == "truncated" {
						raw = raw[:len(raw)-2]
					}
					if fault == "extra row" {
						raw = append(raw, '\n', '[', ']')
					}
					dir := t.TempDir()
					if fault != "missing" {
						if err := os.WriteFile(filepath.Join(dir, tab.Name+".jsonl"), raw, 0o600); err != nil {
							t.Fatal(err)
						}
					}
					root, err := os.OpenRoot(dir)
					if err != nil {
						t.Fatal(err)
					}
					defer func(cleanup func() error) { _ = cleanup() }(root.Close)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					if fault == "cancelled" {
						cancel()
					}
					err = validateTableKeys(ctx, root, tab, keys)
					if (err == nil) != (fault == "") {
						t.Fatal("invalid secret binding result", err)
					}
				})
			}
		})
	}
}

// TestKeyValidationRequiresMetadataAndKeyring checks that key validation requires metadata and
// keyring.
func TestKeyValidationRequiresMetadataAndKeyring(t *testing.T) {
	s := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
	if err := s.ValidateKeys(t.Context(), t.TempDir(), nil); err == nil {
		t.Fatal("accepted missing keys")
	}
	keys, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "original", Strict: true}, Provider: secrets.Static{"original": bytes.Repeat([]byte{7}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateKeys(t.Context(), t.TempDir(), keys); err == nil {
		t.Fatal("accepted missing metadata")
	}
}
