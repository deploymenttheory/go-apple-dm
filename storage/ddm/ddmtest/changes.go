package ddmtest

import (
	"context"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
)

// pending lists changes due at now.
func pending(t *testing.T, s ddm.Tx, now time.Time, limit int) []ddm.Change {
	t.Helper()
	rows, err := s.PendingChanges(context.Background(), now, limit)
	if err != nil {
		t.Fatalf("PendingChanges: %v", err)
	}
	return rows
}

// seqs projects changes to their sequence numbers.
func seqs(cs []ddm.Change) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.Seq
	}
	return out
}

// RunChangeSuite covers ChangeStore.
func RunChangeSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("RecordPendingCompleteFail", func(t *testing.T) {
		s := newStore(t)
		dev1, dev2 := Device(1), Device(2)
		if err := s.RecordChanges(ctx, []mdm.EnrollmentID{dev1, dev2}, "declaration", t0); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordChanges(ctx, nil, "empty", t0); err != nil {
			t.Fatalf("empty record: %v", err)
		}
		rows := pending(t, s, t0, 0)
		if len(rows) != 2 || rows[0].Seq >= rows[1].Seq || rows[0].ID != dev1 || rows[1].ID != dev2 {
			t.Fatalf("pending: %+v", rows)
		}
		for _, c := range rows {
			if c.Reason != "declaration" || c.Attempts != 0 || c.LastError != "" {
				t.Fatalf("row: %+v", c)
			}
			wantTime(t, "CreatedAt", c.CreatedAt, t0)
			wantTime(t, "NextAttemptAt", c.NextAttemptAt, t0)
		}
		if got := pending(t, s, t0.Add(-time.Second), 0); len(got) != 0 {
			t.Fatalf("pending before due: %+v", got)
		}
		first, second := rows[0].Seq, rows[1].Seq
		retry := t0.Add(time.Minute)
		if err := s.FailChanges(ctx, []int64{first, 999}, "boom", retry); err != nil {
			t.Fatal(err)
		}
		if got := seqs(pending(t, s, t0, 0)); len(got) != 1 || got[0] != second {
			t.Fatalf("pending after fail: %v", got)
		}
		rows = pending(t, s, retry, 0)
		if len(rows) != 2 || rows[0].Seq != first || rows[0].Attempts != 1 || rows[0].LastError != "boom" {
			t.Fatalf("pending at retry: %+v", rows)
		}
		wantTime(t, "NextAttemptAt after fail", rows[0].NextAttemptAt, retry)
		if err := s.FailChanges(ctx, []int64{first}, "again", retry.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		rows = pending(t, s, retry.Add(time.Hour), 0)
		if rows[0].Attempts != 2 || rows[0].LastError != "again" {
			t.Fatalf("second failure: %+v", rows[0])
		}
		if err := s.CompleteChanges(ctx, []int64{second, 999}); err != nil {
			t.Fatal(err)
		}
		if got := seqs(pending(t, s, retry.Add(time.Hour), 0)); len(got) != 1 || got[0] != first {
			t.Fatalf("pending after complete: %v", got)
		}
		if err := s.CompleteChanges(ctx, nil); err != nil {
			t.Fatalf("complete nothing: %v", err)
		}
	})

	t.Run("PendingOrderedBySeq", func(t *testing.T) {
		s := newStore(t)
		// Later rows are due earlier; the order is still by seq.
		for i := range 5 {
			at := t0.Add(-time.Duration(i) * time.Minute)
			if err := s.RecordChanges(ctx, []mdm.EnrollmentID{Device(i + 1)}, "r", at); err != nil {
				t.Fatal(err)
			}
		}
		rows := pending(t, s, t0, 0)
		if len(rows) != 5 {
			t.Fatalf("pending: %+v", rows)
		}
		for i := 1; i < len(rows); i++ {
			if rows[i].Seq <= rows[i-1].Seq {
				t.Fatalf("out of order: %v", seqs(rows))
			}
		}
		limited := pending(t, s, t0, 2)
		if len(limited) != 2 || limited[0].Seq != rows[0].Seq || limited[1].Seq != rows[1].Seq {
			t.Fatalf("limited: %+v", limited)
		}
		if got := pending(t, s, t0.Add(-4*time.Minute), 0); len(got) != 1 || got[0].ID != Device(5) {
			t.Fatalf("only the earliest due: %+v", got)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		s := newStore(t)
		p, f, err := s.ChangeStats(ctx, t0)
		if err != nil || p != 0 || f != 0 {
			t.Fatalf("empty stats: %d %d %v", p, f, err)
		}
		if err := s.RecordChanges(ctx, []mdm.EnrollmentID{Device(1), Device(2), Device(3)}, "r", t0); err != nil {
			t.Fatal(err)
		}
		rows := pending(t, s, t0, 0)
		if err := s.FailChanges(ctx, []int64{rows[0].Seq}, "boom", t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		p, f, err = s.ChangeStats(ctx, t0)
		if err != nil || p != 2 || f != 1 {
			t.Fatalf("stats after fail: %d %d %v", p, f, err)
		}
		p, f, err = s.ChangeStats(ctx, t0.Add(time.Hour))
		if err != nil || p != 3 || f != 1 {
			t.Fatalf("stats at retry: %d %d %v", p, f, err)
		}
		if err := s.CompleteChanges(ctx, []int64{rows[0].Seq}); err != nil {
			t.Fatal(err)
		}
		p, f, err = s.ChangeStats(ctx, t0.Add(time.Hour))
		if err != nil || p != 2 || f != 0 {
			t.Fatalf("stats after complete: %d %d %v", p, f, err)
		}
	})
}
