package statestore_test

import (
	"bytes"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
	"github.com/deploymenttheory/go-apple-dm/state"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

func TestProtocolStateSealingAndRotation(t *testing.T) {
	ctx := t.Context()
	db := sqliteDB(t)
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
	s, err := statestore.Open(ctx, db, sqlite.Dialect, ring("v1"))
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("profile-secret-and-oidc-verifier")
	if err = s.Update(
		ctx,
		[]string{"profile"},
		func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: "profile", Value: secret}) },
	); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = db.QueryRowContext(ctx, "SELECT value FROM protocol_state WHERE record_key='profile'").
		Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !crypt.IsSealed(raw) || bytes.Contains(raw, secret) {
		t.Fatal("plaintext state")
	}
	s, err = statestore.Open(ctx, db, sqlite.Dialect, ring("v2", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.Rewrap(ctx); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	s, err = statestore.Open(ctx, db, sqlite.Dialect, ring("v2"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(ctx, "profile")
	if err != nil || !bytes.Equal(r.Value, secret) {
		t.Fatal(r, err)
	}
	if _, err = db.ExecContext(
		ctx,
		"UPDATE protocol_state SET record_key='other' WHERE record_key='profile'",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "other"); err == nil {
		t.Fatal("row substitution accepted")
	}
}
