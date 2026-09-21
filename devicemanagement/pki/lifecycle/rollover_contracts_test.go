package lifecycle

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// rolloverFixture creates an active issuer and a pending replacement one hour later.
func rolloverFixture(t *testing.T) (*Manager, *faultRepository, Identity) {
	t.Helper()
	m, s, now := testManager(t)
	rootIdentity(t, m, "issuer")
	*now = now.Add(time.Hour)
	v := pending(t, m, "issuer", Issuer)
	v, err := m.CreateIssuer(t.Context(), v.ID, v.Pending, 0)
	requireError(t, err, nil)
	return m, s, v
}

// TestRolloverCohortPaginationConfirmationAndRetirement checks rollover cohort pagination
// confirmation and retirement.
func TestRolloverCohortPaginationConfirmationAndRetirement(t *testing.T) {
	m, _, v := rolloverFixture(t)
	ctx := t.Context()
	devices := make([]string, 101)
	for i := range devices {
		devices[i] = fmt.Sprintf("device-%03d", i)
	}
	job, err := m.PrepareRollover(ctx, v.ID, v.Pending, devices)
	requireError(t, err, nil)
	repeated, err := m.PrepareRollover(ctx, v.ID, v.Pending, []string{"extra"})
	requireError(t, err, nil)
	if repeated != job {
		t.Fatal("retry changed rollover cohort")
	}
	requireError(t, m.CompleteRollover(ctx, v.ID, v.Pending), ErrConflict)
	_, err = m.ActivateRollover(ctx, v.ID, v.Pending)
	requireError(t, err, nil)
	_, err = m.ActivateRollover(ctx, v.ID, v.Pending)
	requireError(t, err, nil)
	_, err = m.RetireIssuer(ctx, v.ID, "1")
	requireError(t, err, ErrConflict)
	_, err = m.RetireIssuer(ctx, v.ID, "2")
	requireError(t, err, ErrConflict)
	requireError(t, m.CompleteRollover(ctx, v.ID, "2"), ErrConflict)
	seen, after := map[string]bool{}, ""
	for {
		page, next, err := m.Migrations(ctx, v.ID, "2", after, 20)
		requireError(t, err, nil)
		for i, migration := range page {
			if seen[migration.Device] {
				t.Fatal("duplicate migration", migration.Device)
			}
			seen[migration.Device] = true
			migration.Phase = "confirmed"
			if i%2 == 0 {
				migration.Phase = "disabled"
			}
			requireError(t, m.SaveMigration(ctx, v.ID, "2", migration), nil)
			requireError(t, m.SaveMigration(ctx, v.ID, "2", migration), ErrConflict)
			if migration.Phase == "confirmed" {
				migration.Generation++
				migration.Phase = "blocked"
				requireError(t, m.SaveMigration(ctx, v.ID, "2", migration), ErrConflict)
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	if len(seen) != len(devices) {
		t.Fatal("incomplete cohort", len(seen))
	}
	requireError(t, m.CompleteRollover(ctx, v.ID, "2"), nil)
	requireError(t, m.CompleteRollover(ctx, v.ID, "2"), nil)
	requireError(
		t,
		m.SaveMigration(ctx, v.ID, "2", Migration{Device: "late", Phase: "queued"}),
		ErrConflict,
	)
	retired, err := m.RetireIssuer(ctx, v.ID, "1")
	requireError(t, err, nil)
	if retired.Active != "2" || retired.Revisions[0].Phase != "retired" {
		t.Fatal(retired)
	}
	mat, err := m.LoadMaterial(ctx, v.ID, "1")
	requireError(t, err, nil)
	if len(mat.Key) == 0 {
		t.Fatal("retirement discarded status signing material")
	}
}

// TestRolloverRejectsInvalidAndStaleTransitions checks that rollover rejects invalid and stale
// transitions.
func TestRolloverRejectsInvalidAndStaleTransitions(t *testing.T) {
	m, s, v := rolloverFixture(t)
	ctx := t.Context()
	_, err := m.PrepareRollover(ctx, "../issuer", "2", nil)
	requireError(t, err, ErrInvalid)
	_, err = m.PrepareRollover(ctx, "missing", "2", nil)
	requireError(t, err, ErrNotFound)
	_, err = m.PrepareRollover(ctx, v.ID, "missing", nil)
	requireError(t, err, ErrNotFound)
	_, err = m.PrepareRollover(ctx, v.ID, "1", nil)
	requireError(t, err, ErrConflict)
	_, err = m.PrepareRollover(ctx, v.ID, "2", []string{"device", ""})
	requireError(t, err, ErrInvalid)
	page, _, err := m.Migrations(ctx, v.ID, "2", "", 10)
	requireError(t, err, nil)
	if len(page) != 0 {
		t.Fatal("partial cohort persisted")
	}
	for _, method := range []func() error{
		func() error { _, err := m.Rollover(ctx, "../bad", "2"); return err },
		func() error { _, err := m.ActivateRollover(ctx, "../bad", "2"); return err },
		func() error { _, _, err := m.Migrations(ctx, "../bad", "2", "", 1); return err },
		func() error { return m.SaveMigration(ctx, "../bad", "2", Migration{}) },
	} {
		requireError(t, method(), ErrInvalid)
	}
	for _, n := range []int{0, 1001} {
		_, _, err = m.Migrations(ctx, v.ID, "2", "", n)
		requireError(t, err, ErrInvalid)
	}
	for _, migration := range []Migration{{}, {Device: strings.Repeat("x", 1025)}} {
		requireError(t, m.SaveMigration(ctx, v.ID, "2", migration), ErrInvalid)
	}
	requireError(t, m.SaveMigration(ctx, v.ID, "../revision", Migration{Device: "d"}), ErrInvalid)
	requireError(t, m.SaveMigration(ctx, v.ID, "2", Migration{Device: "d"}), ErrNotFound)
	requireError(t, m.CompleteRollover(ctx, v.ID, "2"), ErrNotFound)
	_, err = m.ActivateRollover(ctx, v.ID, "2")
	requireError(t, err, ErrNotFound)
	changeRecord(t, m, v.ID, func(r *record) { r.Revisions[0].Phase = "retained" })
	_, err = m.PrepareRollover(ctx, v.ID, "2", nil)
	requireError(t, err, ErrConflict)
	changeRecord(t, m, v.ID, func(r *record) { r.Revisions[0].Phase = "active" })
	_, err = m.PrepareRollover(ctx, v.ID, "2", []string{"device"})
	requireError(t, err, nil)
	requireError(
		t,
		m.SaveMigration(ctx, v.ID, "2", Migration{Device: "new", Phase: "unknown"}),
		ErrInvalid,
	)
	s.list = func(string) error { return errRepository }
	_, _, err = m.Migrations(ctx, v.ID, "2", "", 10)
	requireError(t, err, errRepository)
	s.list = nil
	changeRecord(t, m, v.ID, func(r *record) { r.Pending = "" })
	_, err = m.ActivateRollover(ctx, v.ID, "2")
	requireError(t, err, ErrConflict)
	changeRecord(
		t,
		m,
		v.ID,
		func(r *record) { r.Pending = "2"; r.Revisions[1].Certificate = []byte("corrupt") },
	)
	_, err = m.ActivateRollover(ctx, v.ID, "2")
	requireError(t, err, ErrInvalid)
}

// TestRolloverRepositoryFailuresAreAtomic checks rollover repository failures are atomic.
func TestRolloverRepositoryFailuresAreAtomic(t *testing.T) {
	for _, where := range []string{"job read", "identity read", "cohort write", "job write", "activation read", "activation write", "completion list", "migration read"} {
		t.Run(where, func(t *testing.T) {
			m, s, v := rolloverFixture(t)
			ctx := t.Context()
			jobKey := rolloverKey(v.ID, "2")
			failKey := func(expected string) func(string) error {
				return func(k string) error {
					if k == expected {
						return errRepository
					}
					return nil
				}
			}
			var err error
			switch where {
			case "job read":
				s.txGet = failKey(jobKey)
			case "identity read":
				s.txGet = failKey(prefix + v.ID)
			case "cohort write":
				s.put = failKey(migrationKey(v.ID, "2", "device"))
			case "job write":
				s.put = failKey(jobKey)
			default:
				_, err = m.PrepareRollover(ctx, v.ID, "2", []string{"device"})
				requireError(t, err, nil)
			}
			switch where {
			case "activation read", "activation write":
				if where == "activation read" {
					s.txGet = failKey(prefix + v.ID)
				} else {
					s.put = failKey(prefix + v.ID)
				}
				_, err = m.ActivateRollover(ctx, v.ID, "2")
			case "completion list":
				_, err = m.ActivateRollover(ctx, v.ID, "2")
				requireError(t, err, nil)
				s.list = func(string) error { return errRepository }
				err = m.CompleteRollover(ctx, v.ID, "2")
			case "migration read":
				s.txGet = failKey(migrationKey(v.ID, "2", "device"))
				err = m.SaveMigration(
					ctx,
					v.ID,
					"2",
					Migration{Device: "device", Phase: "confirmed"},
				)
			default:
				_, err = m.PrepareRollover(ctx, v.ID, "2", []string{"device"})
			}
			requireError(t, err, errRepository)
			s.txGet, s.put, s.list = nil, nil, nil
			current, err := m.Get(ctx, v.ID)
			requireError(t, err, nil)
			if where != "completion list" && current.Active != "1" {
				t.Fatal("failed operation switched authority")
			}
		})
	}
}

// TestRolloverCorruptCohortCannotComplete checks that rollover corrupt cohort cannot complete.
func TestRolloverCorruptCohortCannotComplete(t *testing.T) {
	m, s, v := rolloverFixture(t)
	ctx := t.Context()
	_, err := m.PrepareRollover(ctx, v.ID, "2", []string{"device"})
	requireError(t, err, nil)
	_, err = m.ActivateRollover(ctx, v.ID, "2")
	requireError(t, err, nil)
	k := migrationKey(v.ID, "2", "device")
	requireError(
		t,
		s.Update(
			ctx,
			[]string{k},
			func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: k, Value: []byte("corrupt")}) },
		),
		nil,
	)
	if _, _, err = m.Migrations(ctx, v.ID, "2", "", 10); err == nil {
		t.Fatal("corrupt migration accepted")
	}
	if err = m.CompleteRollover(ctx, v.ID, "2"); err == nil {
		t.Fatal("corrupt cohort completed")
	}
	job, err := m.Rollover(ctx, v.ID, "2")
	requireError(t, err, nil)
	if job.Phase != "migrating" {
		t.Fatal(job)
	}
}
