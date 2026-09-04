package audittest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit"
)

// T0 is the fixed base time every case builds on, so stored timestamps are
// comparable across backends.
var T0 = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// NewStore builds an empty store for one subtest.
type NewStore func(t *testing.T) audit.Store

// RunSuite runs every contract case against newStore.
func RunSuite(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("Append", func(t *testing.T) { runAppend(t, newStore) })
	t.Run("List", func(t *testing.T) { runList(t, newStore) })
	t.Run("Filter", func(t *testing.T) { runFilter(t, newStore) })
	t.Run("Pagination", func(t *testing.T) { runPagination(t, newStore) })
	t.Run("Prune", func(t *testing.T) { runPrune(t, newStore) })
}

func device(id string) mdm.EnrollmentID {
	return mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: id}
}

func mustAppend(t *testing.T, s audit.Store, rec audit.Record) audit.Record {
	t.Helper()
	out, err := s.Append(context.Background(), rec)
	if err != nil {
		t.Fatalf("Append(%s): %v", rec.Type, err)
	}
	return out
}

func runAppend(t *testing.T, newStore NewStore) {
	ctx := context.Background()

	t.Run("AssignsRisingIDs", func(t *testing.T) {
		s := newStore(t)
		first := mustAppend(t, s, audit.Record{At: T0, Type: "enrolled", Actor: "device", Enrollment: device("A")})
		second := mustAppend(t, s, audit.Record{At: T0.Add(time.Second), Type: "checked-out", Actor: "device", Enrollment: device("A")})
		if first.ID == 0 || second.ID <= first.ID {
			t.Fatalf("ids = %d, %d; want rising and non-zero", first.ID, second.ID)
		}
	})

	t.Run("RoundTripsEveryField", func(t *testing.T) {
		s := newStore(t)
		want := audit.Record{
			At: T0, Type: "command-result", Actor: "device",
			Enrollment: mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "UDID-1", ParentID: "PARENT-1"},
			Fields:     map[string]any{"status": "Acknowledged", "command_uuid": "cmd-1"},
		}
		got, err := s.Get(ctx, mustAppend(t, s, want).ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != want.Type || got.Actor != want.Actor {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		if got.Enrollment != want.Enrollment {
			t.Fatalf("enrollment = %+v, want %+v", got.Enrollment, want.Enrollment)
		}
		if !got.At.Equal(want.At) {
			t.Fatalf("at = %v, want %v", got.At, want.At)
		}
		if got.Fields["status"] != "Acknowledged" || got.Fields["command_uuid"] != "cmd-1" {
			t.Fatalf("fields = %v", got.Fields)
		}
	})

	t.Run("NoFieldsIsNotAnError", func(t *testing.T) {
		s := newStore(t)
		got, err := s.Get(ctx, mustAppend(t, s, audit.Record{At: T0, Type: "checked-out"}).ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Fields) != 0 {
			t.Fatalf("fields = %v, want none", got.Fields)
		}
	})

	t.Run("RejectsATypelessRecord", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Append(ctx, audit.Record{At: T0}); !errors.Is(err, audit.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("GetIsNotFoundForAnAbsentID", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Get(ctx, 9999); !errors.Is(err, audit.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	// A caller that mutates what it read must not change the trail.
	t.Run("ReadsAreCopies", func(t *testing.T) {
		s := newStore(t)
		id := mustAppend(t, s, audit.Record{At: T0, Type: "enrolled", Fields: map[string]any{"k": "v"}}).ID
		got, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		got.Fields["k"] = "mutated"
		again, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if again.Fields["k"] != "v" {
			t.Fatalf("stored record was mutated through a read: %v", again.Fields)
		}
	})
}

func runList(t *testing.T, newStore NewStore) {
	ctx := context.Background()

	t.Run("NewestFirst", func(t *testing.T) {
		s := newStore(t)
		for i := range 5 {
			mustAppend(t, s, audit.Record{At: T0.Add(time.Duration(i) * time.Minute), Type: "enrolled"})
		}
		res, err := s.List(ctx, audit.Query{}, audit.Page{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 5 {
			t.Fatalf("items = %d, want 5", len(res.Items))
		}
		for i := 1; i < len(res.Items); i++ {
			if res.Items[i-1].ID <= res.Items[i].ID {
				t.Fatalf("not newest first: %d then %d", res.Items[i-1].ID, res.Items[i].ID)
			}
		}
	})

	t.Run("EmptyTrail", func(t *testing.T) {
		res, err := newStore(t).List(ctx, audit.Query{}, audit.Page{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 0 || res.NextCursor != "" {
			t.Fatalf("res = %+v, want empty", res)
		}
	})

	t.Run("RejectsAMalformedCursor", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.List(ctx, audit.Query{}, audit.Page{Cursor: "not-a-number"}); !errors.Is(err, audit.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func runFilter(t *testing.T, newStore NewStore) {
	ctx := context.Background()
	seed := func(t *testing.T) audit.Store {
		s := newStore(t)
		mustAppend(t, s, audit.Record{At: T0, Type: "enrolled", Actor: "device", Enrollment: device("A")})
		mustAppend(t, s, audit.Record{At: T0.Add(time.Hour), Type: "admin-action", Actor: "ops", Enrollment: device("B")})
		mustAppend(t, s, audit.Record{At: T0.Add(2 * time.Hour), Type: "admin-action", Actor: "break-glass"})
		return s
	}

	cases := map[string]struct {
		q    audit.Query
		want int
	}{
		"ByType":             {audit.Query{Type: "admin-action"}, 2},
		"ByActor":            {audit.Query{Actor: "break-glass"}, 1},
		"ByEnrollment":       {audit.Query{Enrollment: "A"}, 1},
		"Since":              {audit.Query{Since: T0.Add(time.Hour)}, 2},
		"Until":              {audit.Query{Until: T0.Add(time.Hour)}, 1},
		"Window":             {audit.Query{Since: T0.Add(time.Hour), Until: T0.Add(2 * time.Hour)}, 1},
		"Combined":           {audit.Query{Type: "admin-action", Actor: "ops"}, 1},
		"MatchesNothing":     {audit.Query{Actor: "nobody"}, 0},
		"UnboundedIsEveryth": {audit.Query{}, 3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := seed(t).List(ctx, tc.q, audit.Page{})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Items) != tc.want {
				t.Fatalf("items = %d, want %d: %+v", len(res.Items), tc.want, res.Items)
			}
		})
	}
}

func runPagination(t *testing.T, newStore NewStore) {
	ctx := context.Background()

	t.Run("FollowsCursorsToTheEnd", func(t *testing.T) {
		s := newStore(t)
		const total = 25
		for i := range total {
			mustAppend(t, s, audit.Record{At: T0.Add(time.Duration(i) * time.Minute), Type: "enrolled"})
		}
		seen := map[int64]bool{}
		page := audit.Page{Limit: 10}
		for pages := 0; ; pages++ {
			if pages > total {
				t.Fatal("pagination did not terminate")
			}
			res, err := s.List(ctx, audit.Query{}, page)
			if err != nil {
				t.Fatal(err)
			}
			for _, rec := range res.Items {
				if seen[rec.ID] {
					t.Fatalf("record %d returned twice", rec.ID)
				}
				seen[rec.ID] = true
			}
			if res.NextCursor == "" {
				break
			}
			page.Cursor = res.NextCursor
		}
		if len(seen) != total {
			t.Fatalf("saw %d records, want %d", len(seen), total)
		}
	})

	t.Run("LastPageHasNoCursor", func(t *testing.T) {
		s := newStore(t)
		for range 3 {
			mustAppend(t, s, audit.Record{At: T0, Type: "enrolled"})
		}
		res, err := s.List(ctx, audit.Query{}, audit.Page{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if res.NextCursor != "" {
			t.Fatalf("cursor = %q on a complete page", res.NextCursor)
		}
	})

	t.Run("ExactPageBoundary", func(t *testing.T) {
		s := newStore(t)
		for range 4 {
			mustAppend(t, s, audit.Record{At: T0, Type: "enrolled"})
		}
		res, err := s.List(ctx, audit.Query{}, audit.Page{Limit: 4})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 4 {
			t.Fatalf("items = %d", len(res.Items))
		}
		if res.NextCursor == "" {
			return // a store may know it is done
		}
		next, err := s.List(ctx, audit.Query{}, audit.Page{Limit: 4, Cursor: res.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(next.Items) != 0 {
			t.Fatalf("a cursor past the end returned %d records", len(next.Items))
		}
	})

	t.Run("FilterSurvivesPaging", func(t *testing.T) {
		s := newStore(t)
		for i := range 10 {
			typ := "enrolled"
			if i%2 == 0 {
				typ = "checked-out"
			}
			mustAppend(t, s, audit.Record{At: T0.Add(time.Duration(i) * time.Minute), Type: typ})
		}
		count, page := 0, audit.Page{Limit: 2}
		for {
			res, err := s.List(ctx, audit.Query{Type: "checked-out"}, page)
			if err != nil {
				t.Fatal(err)
			}
			for _, rec := range res.Items {
				if rec.Type != "checked-out" {
					t.Fatalf("filter leaked %q across a page boundary", rec.Type)
				}
			}
			count += len(res.Items)
			if res.NextCursor == "" {
				break
			}
			page.Cursor = res.NextCursor
		}
		if count != 5 {
			t.Fatalf("filtered count = %d, want 5", count)
		}
	})

	t.Run("LimitIsBounded", func(t *testing.T) {
		s := newStore(t)
		for range 3 {
			mustAppend(t, s, audit.Record{At: T0, Type: "enrolled"})
		}
		res, err := s.List(ctx, audit.Query{}, audit.Page{Limit: audit.MaxPageSize * 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 3 {
			t.Fatalf("items = %d", len(res.Items))
		}
	})
}

func runPrune(t *testing.T, newStore NewStore) {
	ctx := context.Background()

	t.Run("RemovesOnlyWhatIsOlder", func(t *testing.T) {
		s := newStore(t)
		old := mustAppend(t, s, audit.Record{At: T0, Type: "enrolled"})
		kept := mustAppend(t, s, audit.Record{At: T0.Add(2 * time.Hour), Type: "enrolled"})

		n, err := s.Prune(ctx, T0.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("pruned = %d, want 1", n)
		}
		if _, err := s.Get(ctx, old.ID); !errors.Is(err, audit.ErrNotFound) {
			t.Fatalf("the old record survived: %v", err)
		}
		if _, err := s.Get(ctx, kept.ID); err != nil {
			t.Fatalf("the newer record was pruned: %v", err)
		}
	})

	// A boundary record is kept: at < before, not <=, so a retention window
	// expressed as "keep 30 days" keeps the record exactly 30 days old.
	t.Run("BoundaryIsExclusive", func(t *testing.T) {
		s := newStore(t)
		mustAppend(t, s, audit.Record{At: T0, Type: "enrolled"})
		n, err := s.Prune(ctx, T0)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("pruned = %d, want 0 at the boundary", n)
		}
	})

	t.Run("IsIdempotent", func(t *testing.T) {
		s := newStore(t)
		mustAppend(t, s, audit.Record{At: T0, Type: "enrolled"})
		if _, err := s.Prune(ctx, T0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		n, err := s.Prune(ctx, T0.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("a second prune removed %d", n)
		}
	})

	t.Run("EmptyTrail", func(t *testing.T) {
		n, err := newStore(t).Prune(ctx, T0)
		if err != nil || n != 0 {
			t.Fatalf("n = %d, err = %v", n, err)
		}
	})

	// Ids must not be reused after a prune, or a stale cursor would start
	// replaying records that are not the ones it saw.
	t.Run("IDsAreNotReused", func(t *testing.T) {
		s := newStore(t)
		first := mustAppend(t, s, audit.Record{At: T0, Type: "enrolled"})
		if _, err := s.Prune(ctx, T0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		next := mustAppend(t, s, audit.Record{At: T0.Add(2 * time.Hour), Type: "enrolled"})
		if next.ID <= first.ID {
			t.Fatalf("id %d was reused after pruning %d", next.ID, first.ID)
		}
	})
}
