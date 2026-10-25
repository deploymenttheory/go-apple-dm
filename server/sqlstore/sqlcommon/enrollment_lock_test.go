package sqlcommon_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestEnrollmentLockRequiresParticipatingPool verifies that the locking read
// cannot silently create a transaction that ends before the caller's mutation.
func TestEnrollmentLockRequiresParticipatingPool(t *testing.T) {
	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "locking.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "selected"}
	if _, err := s.GetForUpdate(t.Context(), mdm.EnrollmentID{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatal("invalid identity accepted", err)
	}
	if _, err := s.GetForUpdate(t.Context(), id); !errors.Is(err, sqlcommon.ErrTransaction) {
		t.Fatal("locking read acquired an independent transaction", err)
	}
	other := openRaw(t)
	err = (sqlcommon.UnitOfWork{DB: other, Dialect: sqlite.Dialect}).Run(t.Context(), func(ctx context.Context) error {
		_, err := s.GetForUpdate(ctx, id)
		return err
	})
	if !errors.Is(err, sqlcommon.ErrTransaction) {
		t.Fatal("another pool's transaction authorized a locking read", err)
	}
	err = (sqlcommon.UnitOfWork{DB: s.DB(), Dialect: sqlite.Dialect}).Run(t.Context(), func(ctx context.Context) error {
		_, err := s.GetForUpdate(ctx, id)
		return err
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("missing enrollment lost its error classification", err)
	}
}

// TestEnrollmentLockOpensSealedFacts verifies that the new read has the same
// credential-opening contract as an ordinary native enrollment read.
func TestEnrollmentLockOpensSealedFacts(t *testing.T) {
	keyring, err := crypt.NewKeyring(t.Context(), crypt.Options{
		Keys:     crypt.Keys{Active: "fixture", Strict: true},
		Provider: secrets.Static{"fixture": bytes.Repeat([]byte{7}, 32)},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sealed-locking.db")
	s, err := sqlite.Open(t.Context(), path, sqlite.Options{Keyring: keyring})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "sealed-selected"}
	now := time.Now().UTC()
	record := storage.Enrollment{ID: id, Enabled: true, CertHash: "fixture-pin", UnlockToken: []byte("fixture-unlock"), AuthenticateRaw: []byte("fixture-native-authenticate"), LastSeenAt: now, EnrolledAt: now}
	if err := s.Import(t.Context(), storage.EnrollmentExport{Enrollment: record}); err != nil {
		t.Fatal(err)
	}
	err = (sqlcommon.UnitOfWork{DB: s.DB(), Dialect: sqlite.Dialect}).Run(t.Context(), func(ctx context.Context) error {
		got, err := s.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if got.ID != id || !got.Enabled || got.CertHash != record.CertHash || !bytes.Equal(got.UnlockToken, record.UnlockToken) || !bytes.Equal(got.AuthenticateRaw, record.AuthenticateRaw) {
			t.Error("locking read did not return the selected native facts")
		}
		if _, ok := sqlcommon.CurrentTransaction(ctx, s.DB()); !ok {
			t.Error("locking read detached the caller's transaction")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := sqlite.Open(t.Context(), path, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	err = (sqlcommon.UnitOfWork{DB: plain.DB(), Dialect: sqlite.Dialect}).Run(t.Context(), func(ctx context.Context) error {
		_, err := plain.GetForUpdate(ctx, id)
		return err
	})
	if !errors.Is(err, crypt.ErrNoKeyring) {
		t.Fatal("locking read exposed sealed bytes without a keyring", err)
	}
}
