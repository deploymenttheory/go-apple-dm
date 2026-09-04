package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
)

// defaultPendingLimit applies when PendingChanges gets a non-positive
// limit.
const defaultPendingLimit = 500

// RecordChanges implements ddm.ChangeStore. Every id is validated before
// any row is written.
func (t *txStore) RecordChanges(ctx context.Context, ids []mdm.EnrollmentID, reason string, at time.Time) error {
	for _, id := range ids {
		if err := validID(id); err != nil {
			return err
		}
	}
	for _, id := range ids {
		if _, err := t.exec(ctx, "insert change", "INSERT INTO ddm_changes ("+enrollmentIDCols+", reason, created_at, attempts, last_error, next_attempt_at) VALUES (?, ?, ?, ?, ?, 0, '', ?)",
			id.ID, int(id.Channel), id.ParentID, reason, utc(at), utc(at)); err != nil {
			return err
		}
	}
	return nil
}

// PendingChanges implements ddm.ChangeStore.
func (t *txStore) PendingChanges(ctx context.Context, now time.Time, limit int) ([]ddm.Change, error) {
	if limit <= 0 {
		limit = defaultPendingLimit
	}
	out := make([]ddm.Change, 0)
	err := t.each(ctx, "pending changes", "SELECT seq, "+enrollmentIDCols+", reason, created_at, attempts, last_error, next_attempt_at FROM ddm_changes WHERE next_attempt_at <= ? ORDER BY seq LIMIT ?",
		[]any{utc(now), limit}, func(rows *sql.Rows) error {
			var c ddm.Change
			var channel int
			if err := rows.Scan(&c.Seq, &c.ID.ID, &channel, &c.ID.ParentID, &c.Reason, &c.CreatedAt, &c.Attempts, &c.LastError, &c.NextAttemptAt); err != nil {
				return wrap("scan change", err)
			}
			c.ID.Channel = mdm.Channel(channel) // #nosec G115 -- stored from a uint8
			c.CreatedAt, c.NextAttemptAt = c.CreatedAt.UTC(), c.NextAttemptAt.UTC()
			out = append(out, c)
			return nil
		})
	return out, err
}

// seqArgs converts sequence numbers to query arguments.
func seqArgs(seqs []int64) []any {
	args := make([]any, len(seqs))
	for i, seq := range seqs {
		args[i] = seq
	}
	return args
}

// CompleteChanges implements ddm.ChangeStore. Unknown seqs are ignored.
func (t *txStore) CompleteChanges(ctx context.Context, seqs []int64) error {
	if len(seqs) == 0 {
		return nil
	}
	_, err := t.exec(ctx, "complete changes", "DELETE FROM ddm_changes WHERE seq IN ("+placeholders(len(seqs))+")", seqArgs(seqs)...)
	return err
}

// FailChanges implements ddm.ChangeStore. Unknown seqs are ignored.
func (t *txStore) FailChanges(ctx context.Context, seqs []int64, msg string, nextAttempt time.Time) error {
	if len(seqs) == 0 {
		return nil
	}
	_, err := t.exec(ctx, "fail changes", "UPDATE ddm_changes SET attempts = attempts + 1, last_error = ?, next_attempt_at = ? WHERE seq IN ("+placeholders(len(seqs))+")",
		append([]any{msg, utc(nextAttempt)}, seqArgs(seqs)...)...)
	return err
}

// ChangeStats implements ddm.ChangeStore.
func (t *txStore) ChangeStats(ctx context.Context, now time.Time) (pending, failed int64, err error) {
	_, err = t.row(ctx, "change stats", "SELECT COALESCE(SUM(CASE WHEN next_attempt_at <= ? THEN 1 ELSE 0 END), 0), COALESCE(SUM(CASE WHEN attempts > 0 THEN 1 ELSE 0 END), 0) FROM ddm_changes",
		[]any{utc(now)}, &pending, &failed)
	return pending, failed, err
}

// RecordChanges implements ddm.ChangeStore.
func (s *Store) RecordChanges(ctx context.Context, ids []mdm.EnrollmentID, reason string, at time.Time) error {
	return s.write(ctx, func(t *txStore) error { return t.RecordChanges(ctx, ids, reason, at) })
}

// PendingChanges implements ddm.ChangeStore.
func (s *Store) PendingChanges(ctx context.Context, now time.Time, limit int) ([]ddm.Change, error) {
	return s.view().PendingChanges(ctx, now, limit)
}

// CompleteChanges implements ddm.ChangeStore.
func (s *Store) CompleteChanges(ctx context.Context, seqs []int64) error {
	return s.view().CompleteChanges(ctx, seqs)
}

// FailChanges implements ddm.ChangeStore.
func (s *Store) FailChanges(ctx context.Context, seqs []int64, msg string, nextAttempt time.Time) error {
	return s.view().FailChanges(ctx, seqs, msg, nextAttempt)
}

// ChangeStats implements ddm.ChangeStore.
func (s *Store) ChangeStats(ctx context.Context, now time.Time) (pending, failed int64, err error) {
	return s.view().ChangeStats(ctx, now)
}
