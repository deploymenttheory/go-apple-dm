package statestore_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

func TestSQLiteCertificateActivationAndWorkflowAreAtomic(t *testing.T) {
	db := sqliteDB(t)
	if _, err := sqlcommon.Migrate(t.Context(), db, sqlite.Dialect); err != nil {
		t.Fatal(err)
	}
	exerciseCertificateActivation(t, db, sqlite.Dialect)
}

func exerciseCertificateActivation(t *testing.T, db *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	ctx := t.Context()
	keys, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "test", Strict: true}, Provider: secrets.Static{"test": bytes.Repeat([]byte{7}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := statestore.Open(ctx, db, dialect, keys)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := testpki.NewCA("test Apple root")
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("atomic-%d", time.Now().UnixNano())
	topic := "com.apple.mgmt.External." + id
	first, err := ca.IssuePush(topic, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := first.PEM()
	if err != nil {
		t.Fatal(err)
	}
	req := lifecycle.Request{ID: id, Kind: lifecycle.Push, Subject: pkix.Name{CommonName: "customer"}}
	m := &lifecycle.Manager{Store: st, Trust: lifecycle.Trust{Apple: []*x509.Certificate{ca.Cert}}, Publish: statestore.PublishCertificate}
	if _, err = m.Adopt(ctx, req, cert, key); err != nil {
		t.Fatal(err)
	}
	live := sqlcommon.New(db, dialect, sqlcommon.WithKeyring(keys))
	if _, err = m.Adopt(ctx, req, cert, key); err != nil {
		t.Fatal(err)
	}
	version, err := live.PushCertVersion(ctx, topic)
	if err != nil || version != 1 {
		t.Fatal("repeated adoption republished identity", version, err)
	}
	pending, err := m.Begin(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	material, err := m.LoadMaterial(ctx, id, pending.Pending)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(material.Key)
	private, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ca.IssuePushWithKey(topic, time.Now().Add(-30*time.Minute), private.(crypto.Signer))
	if err != nil {
		t.Fatal(err)
	}
	renewed, _, err := second.PEM()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Import(ctx, id, pending.Pending, renewed); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("fail after writing runtime certificate")
	m.Publish = func(ctx context.Context, tx state.Tx, i lifecycle.Identity, b lifecycle.Material) error {
		if err := statestore.PublishCertificate(ctx, tx, i, b); err != nil {
			return err
		}
		return fault
	}
	if _, err = m.Activate(ctx, id, pending.Pending); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	version, err = live.PushCertVersion(ctx, topic)
	if err != nil || version != 1 {
		t.Fatal("runtime update escaped rollback", version, err)
	}
	status, err := m.Get(ctx, id)
	if err != nil || status.Active != "1" || status.Pending != "2" {
		t.Fatal("workflow update escaped rollback", status, err)
	}
	var stateBlob, keyBlob []byte
	if err = db.QueryRowContext(ctx, dialect.Rebind("SELECT value FROM protocol_state WHERE record_key = ?"), "pki/lifecycle/identity/"+id).Scan(&stateBlob); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, dialect.Rebind("SELECT key_pem FROM push_certs WHERE topic = ?"), topic).Scan(&keyBlob); err != nil {
		t.Fatal(err)
	}
	if !crypt.IsSealed(stateBlob) || !crypt.IsSealed(keyBlob) {
		t.Fatal("private certificate state stored without encryption")
	}
	reopened, err := statestore.Open(ctx, db, dialect, keys)
	if err != nil {
		t.Fatal(err)
	}
	m = &lifecycle.Manager{Store: reopened, Trust: m.Trust, Publish: statestore.PublishCertificate}
	restored, err := m.LoadMaterial(ctx, id, "2")
	if err != nil || !bytes.Equal(restored.Key, material.Key) {
		t.Fatal("pending key lost on restart", err)
	}
	if _, err = m.Activate(ctx, id, "2"); err != nil {
		t.Fatal(err)
	}
	version, err = live.PushCertVersion(ctx, topic)
	if err != nil || version != 2 {
		t.Fatal(version, err)
	}
	pair, err := live.PushCert(ctx, topic)
	if err != nil || !bytes.Equal(pair.KeyPEM, material.Key) {
		t.Fatal("activated runtime identity mismatch", err)
	}
}
