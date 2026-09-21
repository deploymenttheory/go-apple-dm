package blueprints_test

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// TestProfileEncryptionRotationAndRestart checks profile encryption rotation and restart.
func TestProfileEncryptionRotationAndRestart(t *testing.T) {
	ctx := t.Context()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "blueprints.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	provider := secrets.Static{"old": bytes.Repeat([]byte{1}, 32), "new": bytes.Repeat([]byte{2}, 32)}
	open := func(active string, accepted ...string) (*blueprints.Manager, *configurationprofile.Manager, *statestore.Store, *sqlstore.Store) {
		t.Helper()
		keys, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: active, Accepted: accepted}, Provider: provider})
		if err != nil {
			t.Fatal(err)
		}
		st, err := statestore.Open(ctx, db, sqlite.Dialect, keys)
		if err != nil {
			t.Fatal(err)
		}
		ds, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{Keyring: keys})
		if err != nil {
			t.Fatal(err)
		}
		e, err := ddm.New(ddm.Config{Store: ds})
		if err != nil {
			t.Fatal(err)
		}
		profiles, err := configurationprofile.New(configurationprofile.Config{Engine: e, State: st, BaseURL: "https://mdm.example"})
		if err != nil {
			t.Fatal(err)
		}
		m, err := blueprints.New(blueprints.Config{Engine: e, State: st, ConfigurationProfiles: profiles, Run: (sqlcommon.UnitOfWork{DB: db, Dialect: sqlite.Dialect}).Run})
		if err != nil {
			t.Fatal(err)
		}
		return m, profiles, st, ds
	}
	m, profiles, _, _ := open("old")
	data := profileBytes(t, "secret-sentinel")
	info, err := profiles.Upload(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Publish(ctx, blueprint.Spec{Identifier: "sealed", Declarations: []blueprint.Declaration{{Identifier: "profile", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: info.Revision}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, "SELECT value FROM protocol_state")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		if !crypt.IsSealed(b) || bytes.Contains(b, []byte("secret-sentinel")) {
			t.Fatal("plaintext source or profile")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_, _, st, ds := open("new", "old")
	if _, err := st.Rewrap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.Rewrap(ctx); err != nil {
		t.Fatal(err)
	}
	m, profiles, _, _ = open("new")
	got, _, err := profiles.Data(ctx, info.Revision)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("profile after rotation", err)
	}
	saved, err := m.Get(ctx, "sealed")
	if err != nil || saved.Revision != r.Revision {
		t.Fatal("source after restart", err)
	}
}
