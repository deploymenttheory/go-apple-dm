package sqlstore_test

import (
	"bytes"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

func TestDeclarationSecretsEncryptedAndRotated(t *testing.T) {
	ctx := t.Context()
	db := openDB(t)
	provider := secrets.Static{"v1": bytes.Repeat([]byte{1}, 32), "v2": bytes.Repeat([]byte{2}, 32)}
	ring := func(active string, accepted ...string) *crypt.Keyring {
		k, err := crypt.NewKeyring(
			ctx,
			crypt.Options{Keys: crypt.Keys{Active: active, Accepted: accepted}, Provider: provider},
		)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{Keyring: ring("v1")})
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte(`{"Password":"credential-sentinel"}`)
	d := &ddm.Declaration{
		Identifier:  "credential",
		Type:        "test",
		Kind:        conf,
		ServerToken: "v1",
		Canonical:   secret,
		CreatedAt:   t0,
		UpdatedAt:   t0,
	}
	if _, err = s.PutDeclaration(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err = s.PutSnapshot(
		ctx,
		&ddm.Snapshot{
			ID:             dev,
			RefreshedAt:    t0,
			TokenChangedAt: t0,
			Items: []ddm.SnapshotItem{
				{
					Identifier:  d.Identifier,
					Kind:        conf,
					ServerToken: "v1",
					BaseToken:   "v1",
					Expanded:    secret,
				},
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"SELECT canonical FROM ddm_declarations", "SELECT canonical FROM ddm_declaration_versions", "SELECT expanded FROM ddm_snapshot_items"} {
		var raw []byte
		if err = db.QueryRowContext(ctx, query).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if !crypt.IsSealed(raw) || bytes.Contains(raw, secret) {
			t.Fatal("plaintext", query)
		}
	}
	s, err = sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{Keyring: ring("v2", "v1")})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.Rewrap(ctx); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	s, err = sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{Keyring: ring("v2")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDeclaration(ctx, d.Identifier)
	if err != nil || !bytes.Equal(got.Canonical, secret) {
		t.Fatal(got, err)
	}
	snap, err := s.Snapshot(ctx, dev)
	if err != nil || !bytes.Equal(snap.Items[0].Expanded, secret) {
		t.Fatal(snap, err)
	}
	if _, err = db.ExecContext(
		ctx,
		"UPDATE ddm_declarations SET server_token='swapped'",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetDeclaration(ctx, d.Identifier); err == nil {
		t.Fatal("ciphertext substitution accepted")
	}
}
