package maintenance

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestControlRejectsInvalidOwnershipAndRegistration checks that control rejects invalid ownership
// and registration.
func TestControlRejectsInvalidOwnershipAndRegistration(t *testing.T) {
	s := fixture(t)
	ctx := t.Context()
	if _, err := MigrationSet(sqlcommon.Dialect{Name: "unknown"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Open(ctx, nil, sqlite.Dialect, false); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, label := range []string{"", strings.Repeat("x", 256)} {
		if _, err := s.Register(ctx, label); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, ticket := range []string{"", "short", strings.Repeat("x", 129)} {
		if err := s.Request(ctx, ticket); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := s.Resume(ctx, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.WaitDrained(ctx, ""); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
	if err := s.WaitDrained(ctx, rand.Text()); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
	if (Status{}).Ready() {
		t.Fatal("an open server reported drained")
	}
	ticket := rand.Text()
	if err := s.Request(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget(ctx, ticket, "missing", true); !errors.Is(err, ErrParticipant) {
		t.Fatal(err)
	}
	if err := s.acknowledge(ctx, "missing", "wrong-ticket"); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
	if err := s.Resume(ctx, ticket); err != nil {
		t.Fatal(err)
	}
	p, err := s.Register(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(ctx, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestWorkerFailureClosesAdmission checks worker failure closes admission, including worker.
func TestWorkerFailureClosesAdmission(t *testing.T) {
	for _, workerError := range []error{nil, errors.New("worker failed")} {
		t.Run("worker", func(t *testing.T) {
			s := fixture(t)
			p, err := s.Register(t.Context(), "failed-worker")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = p.Close(t.Context()) }()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err = p.Run(ctx, func(context.Context) error { return workerError })
			if workerError == nil && !errors.Is(err, ErrParticipant) || workerError != nil && !errors.Is(err, workerError) {
				t.Fatal(err)
			}
			p.mu.Lock()
			accepting := p.accepting
			p.mu.Unlock()
			if accepting {
				t.Fatal("failed worker left requests enabled")
			}
		})
	}
}

// TestControlFailureCancelsActiveWorker checks that control failure cancels active worker.
func TestControlFailureCancelsActiveWorker(t *testing.T) {
	s := fixture(t)
	p, err := s.Register(t.Context(), "control-failure")
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	err = p.Run(t.Context(), func(ctx context.Context) error {
		if err := s.db.Close(); err != nil {
			return err
		}
		<-ctx.Done()
		stopped = true
		return errors.New("worker drain failure")
	})
	if err == nil || !stopped {
		t.Fatal("control failure did not stop and join worker", err)
	}
}

// TestMalformedPersistentControlStateNeverReportsReady checks that malformed persistent control
// state never reports ready.
func TestMalformedPersistentControlStateNeverReportsReady(t *testing.T) {
	for _, corrupt := range []string{
		"DROP TABLE maintenance_participants",
		"DELETE FROM maintenance_state",
	} {
		t.Run(corrupt, func(t *testing.T) {
			s := fixture(t)
			if _, err := s.db.ExecContext(t.Context(), corrupt); err != nil {
				t.Fatal(err)
			}
			if status, err := s.Status(t.Context()); err == nil || status.Ready() {
				t.Fatal(status, err)
			}
			if _, err := Open(t.Context(), s.db, s.d, false); err == nil {
				t.Fatal("opened corrupt control state")
			}
			if err := s.WaitDrained(t.Context(), rand.Text()); err == nil {
				t.Fatal("accepted corrupt control state")
			}
		})
	}
	s := fixture(t)
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE maintenance_participants"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "CREATE TABLE maintenance_participants (id TEXT, label TEXT, drained_token TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO maintenance_participants VALUES ('corrupt', NULL, '')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Status(t.Context()); err == nil {
		t.Fatal("accepted a malformed participant")
	}
}

type failedResult struct{}

// RowsAffected returns a synthetic affected-row confirmation failure.
func (failedResult) RowsAffected() (int64, error) { return 0, errors.New("cannot confirm write") }

// LastInsertId returns an error for the unsupported fixture result operation.
func (failedResult) LastInsertId() (int64, error) { return 0, errors.New("unused") }

// TestFailedControlWriteCannotAcknowledge checks that failed control write cannot acknowledge.
func TestFailedControlWriteCannotAcknowledge(t *testing.T) {
	if err := affected(nil, errors.New("write failed")); err == nil {
		t.Fatal("lost write failure")
	}
	if err := affected(failedResult{}, nil); err == nil {
		t.Fatal("accepted unverifiable acknowledgement")
	}
	s := fixture(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), s.db, s.d, true); err == nil {
		t.Fatal("ignored migration failure")
	}
}
