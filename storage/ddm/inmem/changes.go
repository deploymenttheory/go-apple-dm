package inmem

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
)

// defaultPendingLimit applies when PendingChanges gets a non-positive
// limit.
const defaultPendingLimit = 500

// RecordChanges implements ddm.ChangeStore. Every id is validated before
// any row is written.
func (t *tx) RecordChanges(_ context.Context, ids []mdm.EnrollmentID, reason string, at time.Time) error {
	for _, id := range ids {
		if err := validID(id); err != nil {
			return err
		}
	}
	for _, id := range ids {
		seq := t.st.nextSeq()
		t.st.ids[id.ID] = id
		t.st.changes[seq] = ddm.Change{Seq: seq, ID: id, Reason: reason, CreatedAt: at, NextAttemptAt: at}
	}
	return nil
}

// due reports whether a change is due at now.
func due(c ddm.Change, now time.Time) bool {
	return !c.NextAttemptAt.After(now)
}

// PendingChanges implements ddm.ChangeStore.
func (t *tx) PendingChanges(_ context.Context, now time.Time, limit int) ([]ddm.Change, error) {
	if limit <= 0 {
		limit = defaultPendingLimit
	}
	out := make([]ddm.Change, 0)
	for _, c := range t.st.changes {
		if due(c, now) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b ddm.Change) int { return cmp.Compare(a.Seq, b.Seq) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CompleteChanges implements ddm.ChangeStore. Unknown seqs are ignored.
func (t *tx) CompleteChanges(_ context.Context, seqs []int64) error {
	for _, seq := range seqs {
		delete(t.st.changes, seq)
	}
	return nil
}

// FailChanges implements ddm.ChangeStore. Unknown seqs are ignored.
func (t *tx) FailChanges(_ context.Context, seqs []int64, msg string, nextAttempt time.Time) error {
	for _, seq := range seqs {
		c, ok := t.st.changes[seq]
		if !ok {
			continue
		}
		c.Attempts++
		c.LastError = msg
		c.NextAttemptAt = nextAttempt
		t.st.changes[seq] = c
	}
	return nil
}

// ChangeStats implements ddm.ChangeStore.
func (t *tx) ChangeStats(_ context.Context, now time.Time) (int64, int64, error) {
	var pending, failed int64
	for _, c := range t.st.changes {
		if due(c, now) {
			pending++
		}
		if c.Attempts > 0 {
			failed++
		}
	}
	return pending, failed, nil
}

// RecordChanges implements ddm.ChangeStore.
func (s *Store) RecordChanges(ctx context.Context, ids []mdm.EnrollmentID, reason string, at time.Time) error {
	v, done := s.view()
	defer done()
	return v.RecordChanges(ctx, ids, reason, at)
}

// PendingChanges implements ddm.ChangeStore.
func (s *Store) PendingChanges(ctx context.Context, now time.Time, limit int) ([]ddm.Change, error) {
	v, done := s.view()
	defer done()
	return v.PendingChanges(ctx, now, limit)
}

// CompleteChanges implements ddm.ChangeStore.
func (s *Store) CompleteChanges(ctx context.Context, seqs []int64) error {
	v, done := s.view()
	defer done()
	return v.CompleteChanges(ctx, seqs)
}

// FailChanges implements ddm.ChangeStore.
func (s *Store) FailChanges(ctx context.Context, seqs []int64, msg string, nextAttempt time.Time) error {
	v, done := s.view()
	defer done()
	return v.FailChanges(ctx, seqs, msg, nextAttempt)
}

// ChangeStats implements ddm.ChangeStore.
func (s *Store) ChangeStats(ctx context.Context, now time.Time) (int64, int64, error) {
	v, done := s.view()
	defer done()
	return v.ChangeStats(ctx, now)
}
