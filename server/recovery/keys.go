package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

type sealedBinding struct {
	keys      []string
	composite bool
	separator string
}

// These bindings mirror the stores' authenticated column and row identifiers.
// A sealed value in a new, unregistered column fails verification.
func sealedBindings() map[string]sealedBinding {
	m := map[string]sealedBinding{}
	for _, col := range []string{"authenticate_raw", "token_update_raw", "unlock_token", "bootstrap_token"} {
		m["enrollments."+col] = sealedBinding{keys: []string{"id"}}
	}
	for _, col := range []string{"raw", "result_raw", "result_error_chain"} {
		m["commands."+col] = sealedBinding{keys: []string{"seal_id"}}
	}
	for _, col := range []string{"authenticate_raw", "digest_raw", "auth_token"} {
		m["user_auth."+col] = sealedBinding{keys: []string{"enrollment_id"}}
	}
	m["enrollment_replacements.state_blob"] = sealedBinding{keys: []string{"enrollment_id"}}
	m["push_certs.key_pem"] = sealedBinding{keys: []string{"topic"}}
	for _, col := range []string{"consumer_secret", "access_token", "access_secret"} {
		m["dep_accounts."+col] = sealedBinding{keys: []string{"name"}}
	}
	m["dep_sessions.token"] = sealedBinding{keys: []string{"account"}}
	m["dep_keypairs.key_pem"] = sealedBinding{keys: []string{"account", "stage"}, separator: "/"}
	m["protocol_state.value"] = sealedBinding{keys: []string{"record_key"}, composite: true}
	m["webhook_subscriptions.config"] = sealedBinding{keys: []string{"id"}, composite: true}
	m["webhook_messages.payload"] = sealedBinding{keys: []string{"delivery_id"}, composite: true}
	m["ddm_declarations.canonical"] = sealedBinding{
		keys:      []string{"identifier", "server_token"},
		composite: true,
	}
	m["ddm_declaration_versions.canonical"] = sealedBinding{
		keys:      []string{"identifier", "server_token"},
		composite: true,
	}
	m["ddm_snapshot_items.expanded"] = sealedBinding{
		keys:      []string{"enrollment_id", "kind", "identifier"},
		composite: true,
	}
	return m
}

// ValidateKeys authenticates every sealed SQL value with its original key ID,
// column purpose and row binding. No plaintext is retained or written to disk.
// The authenticated archive must be verified before passing its snapshot here.
func (s SQL) ValidateKeys(ctx context.Context, directory string, ring *crypt.Keyring) error {
	if ring == nil {
		return ErrInvalid
	}
	var info databaseSnapshot
	if err := readJSONFile(filepath.Join(directory, "database.json"), &info); err != nil {
		return err
	}
	if _, err := s.validateSnapshot(info); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(root.Close)
	for _, table := range info.Tables {
		if err := validateTableKeys(ctx, root, table, ring); err != nil {
			return err
		}
	}
	return nil
}

// validateTableKeys checks that encrypted snapshot columns can be opened with the restored
// storage keyring.
func validateTableKeys(
	ctx context.Context,
	root *os.Root,
	table tableSnapshot,
	ring *crypt.Keyring,
) error {
	f, err := root.Open(table.Name + ".jsonl")
	if err != nil {
		return wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(f.Close)
	decoder := json.NewDecoder(f)
	bindings := sealedBindings()
	for i := int64(0); i < table.Rows; i++ {
		if err := ctx.Err(); err != nil {
			return wrap(err)
		}
		var row []cell
		if err := decoder.Decode(&row); err != nil {
			return wrap(err)
		}
		if len(row) != len(table.Columns) {
			return ErrInvalid
		}
		values := map[string]any{}
		for j, column := range table.Columns {
			value, err := decodeCell(row[j])
			if err != nil {
				return err
			}
			values[column] = value
		}
		for column, value := range values {
			blob, ok := value.([]byte)
			if !ok || len(blob) == 0 {
				continue
			}
			purpose := table.Name + "." + column
			binding, known := bindings[purpose]
			if !crypt.IsSealed(blob) {
				if known && ring.Strict() {
					return fmt.Errorf("%w: unsealed %s", ErrInvalid, purpose)
				}
				continue
			}
			if !known {
				return fmt.Errorf("%w: unknown sealed column %s", ErrInvalid, purpose)
			}
			keys := []string{}
			for _, key := range binding.keys {
				v, ok := values[key].(string)
				if !ok {
					return ErrInvalid
				}
				keys = append(keys, v)
			}
			aad := crypt.AAD(purpose, strings.Join(keys, binding.separator))
			if binding.composite {
				aad = sqlcommon.BlobAAD(purpose, keys...)
			}
			plain, _, err := ring.Open(blob, aad)
			clear(plain)
			if err != nil {
				return fmt.Errorf("recovery: authenticate %s: %w", purpose, err)
			}
		}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
