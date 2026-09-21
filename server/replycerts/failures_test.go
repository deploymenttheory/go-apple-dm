package replycerts_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/replycerts"
)

// TestInvalidRotationInputs checks invalid FileVault rotation inputs neither persist identities
// nor modify unrelated commands.
func TestInvalidRotationInputs(t *testing.T) {
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}
	store := state.NewMemory()
	manager := &replycerts.Manager{Store: store}
	for _, tc := range []struct {
		name   string
		kind   string
		change func(*commands.RotateFileVaultKey)
	}{
		{"personal missing password", "personal", func(p *commands.RotateFileVaultKey) { p.FileVaultUnlock.Password = nil }},
		{"personal empty password", "personal", func(p *commands.RotateFileVaultKey) { p.FileVaultUnlock.Password = new("") }},
		{"personal wrong certificate", "personal", func(p *commands.RotateFileVaultKey) { p.NewCertificate = []byte("external") }},
		{"institutional missing unlock", "institutional", func(p *commands.RotateFileVaultKey) { p.FileVaultUnlock.Password = nil }},
		{"institutional incomplete export", "institutional", func(p *commands.RotateFileVaultKey) {
			p.FileVaultUnlock.Password = nil
			p.FileVaultUnlock.PrivateKeyExport = []byte("export")
		}},
		{"institutional wrong certificate", "institutional", func(p *commands.RotateFileVaultKey) { p.ReplyEncryptionCertificate = []byte("external") }},
		{"unknown key type", "unknown", func(*commands.RotateFileVaultKey) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := requireType[*commands.RotateFileVaultKey](t, rotation(t, tc.kind, "uuid").Payload)
			tc.change(p)
			cmd, err := mdm.NewCommand(p, mdm.WithUUID("uuid"))
			if err != nil {
				t.Fatal(err)
			}
			if got, err := manager.Prepare(t.Context(), id, cmd); got != nil || !errors.Is(err, replycerts.ErrInvalid) {
				t.Fatalf("invalid rotation accepted: %v %v", got, err)
			}
		})
	}
	badRaw := &mdm.Command{Raw: []byte("broken")}
	mismatch := rotation(t, "personal", "uuid")
	mismatch.UUID = "different"
	for _, cmd := range []*mdm.Command{badRaw, mismatch} {
		if _, err := manager.Prepare(t.Context(), id, cmd); !errors.Is(err, replycerts.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := (&replycerts.Manager{}).Prepare(t.Context(), id, rotation(t, "personal", "uuid")); !errors.Is(err, replycerts.ErrInvalid) {
		t.Fatal(err)
	}
	if records, err := store.List(t.Context(), "", "", 100); err != nil || len(records) != 0 {
		t.Fatal("invalid input persisted an identity", err)
	}
	cmd, err := mdm.NewCommand(&commands.DeviceInformation{}, mdm.WithUUID("information"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := manager.Prepare(t.Context(), id, cmd); err != nil || got != cmd {
		t.Fatal("unrelated command modified", err)
	}
}

// TestRetainedIdentityCorruption checks that corrupt retained reply identities and conflicting
// retries are rejected without replacing the identity.
func TestRetainedIdentityCorruption(t *testing.T) {
	ctx := t.Context()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}
	store := state.NewMemory()
	manager := &replycerts.Manager{Store: store}
	original := rotation(t, "personal", "rotation")
	prepared, err := manager.Prepare(ctx, id, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EscrowProfile(ctx, id, "escrow", "example.escrow", "Help desk"); err != nil {
		t.Fatal(err)
	}
	records, err := store.List(ctx, "", "", 100)
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	for _, saved := range records {
		var fields map[string]any
		if err := json.Unmarshal(saved.Value, &fields); err != nil {
			t.Fatal(err)
		}
		uuid := "rotation"
		if fields["profileUUID"] != nil {
			uuid = "escrow"
		}
		for _, corruption := range []string{"json", "certificate", "key", "profileUUID"} {
			if corruption == "profileUUID" && uuid != "escrow" {
				continue
			}
			t.Run(uuid+"/"+corruption, func(t *testing.T) {
				r := saved
				if corruption == "json" {
					r.Value = []byte("broken")
				} else {
					var data map[string]any
					if err := json.Unmarshal(saved.Value, &data); err != nil {
						t.Fatal(err)
					}
					data[corruption] = ""
					var err error
					r.Value, err = json.Marshal(data)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := store.Update(ctx, []string{r.Key}, func(tx state.Tx) error { return tx.Put(ctx, r) }); err != nil {
					t.Fatal(err)
				}
				if corruption != "profileUUID" {
					cert, key, err := manager.Recipient(ctx, id, uuid)
					if cert != nil || key != nil || !errors.Is(err, replycerts.ErrInvalid) {
						t.Fatal("corrupt recipient returned", err)
					}
				}
				var err error
				if uuid == "rotation" {
					_, err = manager.Prepare(ctx, id, original)
				} else {
					_, err = manager.EscrowProfile(ctx, id, uuid, "example.escrow", "Help desk")
				}
				if !errors.Is(err, replycerts.ErrInvalid) {
					t.Fatal("corrupt identity accepted on retry", err)
				}
				if err := store.Update(ctx, []string{saved.Key}, func(tx state.Tx) error { return tx.Put(ctx, saved) }); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	p := requireType[*commands.RotateFileVaultKey](t, prepared.Payload)
	p.ReplyEncryptionCertificate = []byte("different certificate")
	conflicting, err := mdm.NewCommand(p, mdm.WithUUID(original.UUID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(ctx, id, conflicting); !errors.Is(err, replycerts.ErrConflict) {
		t.Fatal(err)
	}
	if got, err := manager.Prepare(ctx, id, original); err != nil || !bytes.Equal(got.Raw, prepared.Raw) {
		t.Fatal("conflict changed retained identity", err)
	}
}

type faultTx struct {
	state.Tx
	operation string
}

// Get injects a reply-certificate read failure or delegates to the transaction.
func (tx faultTx) Get(ctx context.Context, key string) (state.Record, error) {
	if tx.operation == "get" {
		return state.Record{}, errUnavailable
	}
	return tx.Tx.Get(ctx, key)
}

// Put injects a reply-certificate write failure or delegates to the transaction.
func (tx faultTx) Put(ctx context.Context, record state.Record) error {
	if tx.operation == "put" {
		return errUnavailable
	}
	return tx.Tx.Put(ctx, record)
}

// Delete injects a reply-certificate delete failure or delegates to the transaction.
func (tx faultTx) Delete(ctx context.Context, key string) error {
	if tx.operation == "delete" {
		return errUnavailable
	}
	return tx.Tx.Delete(ctx, key)
}

type transactionFaultStore struct {
	state.Store
	operation string
}

// Update wraps reply-certificate state transactions with the configured operation failure.
func (s transactionFaultStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(faultTx{Tx: tx, operation: s.operation}) })
}

// TestIdentityStorageFailures checks reply-identity storage and generation failures do not expose
// commands or discard retained keys.
func TestIdentityStorageFailures(t *testing.T) {
	ctx := t.Context()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}
	for _, operation := range []string{"get", "put"} {
		t.Run(operation, func(t *testing.T) {
			store := state.NewMemory()
			manager := &replycerts.Manager{Store: transactionFaultStore{Store: store, operation: operation}}
			if cmd, err := manager.Prepare(ctx, id, rotation(t, "personal", "uuid")); cmd != nil || !errors.Is(err, errUnavailable) {
				t.Fatal("failed persistence exposed command", err)
			}
			if records, err := store.List(ctx, "", "", 100); err != nil || len(records) != 0 {
				t.Fatal("failed persistence retained identity", err)
			}
		})
	}
	store := state.NewMemory()
	manager := &replycerts.Manager{Store: store}
	if _, err := manager.Prepare(ctx, id, rotation(t, "personal", "uuid")); err != nil {
		t.Fatal(err)
	}
	manager.Store = transactionFaultStore{Store: store, operation: "delete"}
	if err := manager.Forget(ctx, id, "uuid"); !errors.Is(err, errUnavailable) {
		t.Fatal(err)
	}
	if _, _, err := manager.Recipient(ctx, id, "uuid"); err != nil {
		t.Fatal("failed delete lost retained key", err)
	}
	for _, m := range []*replycerts.Manager{{}, manager} {
		for _, bad := range []struct {
			id   mdm.EnrollmentID
			uuid string
		}{{id, ""}, {mdm.EnrollmentID{}, "uuid"}} {
			if _, _, err := m.Recipient(ctx, bad.id, bad.uuid); !errors.Is(err, replycerts.ErrInvalid) {
				t.Fatal(err)
			}
			if err := m.Forget(ctx, bad.id, bad.uuid); !errors.Is(err, replycerts.ErrInvalid) {
				t.Fatal(err)
			}
		}
	}
	// An invalid store clock must fail certificate generation without retaining a key.
	store.Now = func() time.Time { return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }
	manager.Store = store
	if cmd, err := manager.Prepare(ctx, id, rotation(t, "personal", "bad-clock")); cmd != nil || err == nil || !strings.Contains(err.Error(), "generate") {
		t.Fatal("invalid certificate time accepted", err)
	}
	if _, _, err := manager.Recipient(ctx, id, "bad-clock"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed generation retained identity", err)
	}
}
