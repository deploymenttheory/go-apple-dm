package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestAuthenticateResult checks atomic outcomes on the SQL implementation.
func TestAuthenticateResult(t *testing.T) {
	storagetest.RunAuthenticateResultSuite(t, func(t *testing.T) storage.Store {
		t.Helper()
		return open(t)
	})
	t.Run("Encrypted", func(t *testing.T) {
		storagetest.RunAuthenticateResultSuite(t, func(t *testing.T) storage.Store {
			t.Helper()
			return openWith(t, filepath.Join(t.TempDir(), "encrypted.sqlite"), keyring(t, "storage-key-v1"))
		})
	})
}

// TestAuthenticateResultRollback distinguishes a failed storage operation from a
// successful operation whose enclosing unit of work subsequently rolls back.
func TestAuthenticateResultRollback(t *testing.T) {
	t0 := time.Now().UTC()
	s := open(t)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	if _, err := s.DB().ExecContext(t.Context(), "CREATE TRIGGER reject_history BEFORE INSERT ON cert_associations BEGIN SELECT RAISE(FAIL, 'history unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	var failed storage.AuthenticateResult
	if err := s.AuthenticateEnrollment(t.Context(), id, storage.AuthenticateChange{Hash: "identity", At: t0, Result: &failed}); err == nil || failed.Known {
		t.Fatalf("failed write published an outcome: %+v %v", failed, err)
	}
	if _, err := s.DB().ExecContext(t.Context(), "DROP TRIGGER reject_history"); err != nil {
		t.Fatal(err)
	}
	outerFailure := errors.New("completion failed")
	unit := sqlcommon.UnitOfWork{DB: s.DB(), Dialect: sqlite.Dialect}
	err := unit.Run(t.Context(), func(ctx context.Context) error {
		var result storage.AuthenticateResult
		if err := s.AuthenticateEnrollment(ctx, id, storage.AuthenticateChange{Hash: "identity", At: t0, Result: &result}); err != nil {
			return err
		}
		if !result.Known || !result.Reset {
			t.Fatalf("operation classification: %+v", result)
		}
		return outerFailure
	})
	if !errors.Is(err, outerFailure) {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), id); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("outer rollback retained enrollment: %v", err)
	}
}
