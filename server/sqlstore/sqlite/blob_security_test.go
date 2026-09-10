package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

func TestBlobRotationEnforcesKeysAndPreservesFailedWrites(t *testing.T) {
	ctx := t.Context()
	s := openWith(t, filepath.Join(t.TempDir(), "blobs.db"), nil)
	db := s.DB()
	columns := []sqlcommon.BlobColumn{
		{Table: "security_blobs", Column: "blob", Keys: []string{"id"}},
	}
	if _, err := db.ExecContext(
		ctx,
		"CREATE TABLE security_blobs(id TEXT, blob BLOB)",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		ctx,
		"INSERT INTO security_blobs VALUES ('secret',?),('empty',NULL)",
		[]byte("sensitive"),
	); err != nil {
		t.Fatal(err)
	}
	k := keyring(t, "storage-key-v1")
	if _, err := sqlcommon.RewrapBlobs(
		ctx,
		db,
		sqlite.Dialect,
		nil,
		columns,
	); !errors.Is(
		err,
		crypt.ErrNoKeyring,
	) {
		t.Fatal(err)
	}
	strict, err := crypt.NewKeyring(
		ctx,
		crypt.Options{
			Keys:     crypt.Keys{Active: "storage-key-v1", Strict: true},
			Provider: keyProvider,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqlcommon.RewrapBlobs(
		ctx,
		db,
		sqlite.Dialect,
		strict,
		columns,
	); !errors.Is(
		err,
		crypt.ErrUnsealed,
	) {
		t.Fatal("strict rotation accepted plaintext", err)
	}
	if _, err = db.ExecContext(
		ctx,
		"CREATE TRIGGER deny_rotation BEFORE UPDATE ON security_blobs BEGIN SELECT RAISE(FAIL,'rotation denied'); END",
	); err != nil {
		t.Fatal(err)
	}
	if n, err := sqlcommon.RewrapBlobs(ctx, db, sqlite.Dialect, k, columns); err == nil || n != 0 {
		t.Fatal(n, err)
	}
	var before []byte
	if err = db.QueryRowContext(ctx, "SELECT blob FROM security_blobs WHERE id='secret'").
		Scan(&before); err != nil ||
		string(before) != "sensitive" {
		t.Fatal("failed rotation changed data", err)
	}
	if _, err = db.ExecContext(ctx, "DROP TRIGGER deny_rotation"); err != nil {
		t.Fatal(err)
	}
	if n, err := sqlcommon.RewrapBlobs(ctx, db, sqlite.Dialect, k, columns); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err := sqlcommon.RewrapBlobs(ctx, db, sqlite.Dialect, k, columns); err != nil || n != 0 {
		t.Fatal("active-key rows rewritten", n, err)
	}
	var sealed []byte
	if err = db.QueryRowContext(ctx, "SELECT blob FROM security_blobs WHERE id='secret'").
		Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = sqlcommon.OpenBlob(
		nil,
		"security_blobs.blob",
		sealed,
		"secret",
	); !errors.Is(
		err,
		crypt.ErrNoKeyring,
	) {
		t.Fatal(err)
	}
	if _, err = sqlcommon.OpenBlob(
		k,
		"security_blobs.blob",
		sealed,
		"another",
	); !errors.Is(
		err,
		crypt.ErrTampered,
	) {
		t.Fatal("row substitution accepted", err)
	}
	if _, err = db.ExecContext(
		ctx,
		"UPDATE security_blobs SET id=NULL WHERE id='secret'",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = sqlcommon.RewrapBlobs(ctx, db, sqlite.Dialect, k, columns); err == nil {
		t.Fatal("unreadable row key ignored")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = sqlcommon.RewrapBlobs(ctx, db, sqlite.Dialect, k, columns); err == nil {
		t.Fatal("rotation with unavailable storage succeeded")
	}
}
