package eventstore_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
)

// TestRecordListingIncludesOccurrencesWithoutDestinations checks that record listing includes
// occurrences without destinations.
func TestRecordListingIncludesOccurrencesWithoutDestinations(t *testing.T) {
	_, s, p := fixture(t)
	p.Destinations = nil
	for _, id := range []string{"one", "two", "three"} {
		if err := p.Publish(t.Context(), event.Event{ID: id, Type: event.Enrolled}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Records(t.Context(), "enrolled", "", 2)
	if err != nil || len(page) != 2 {
		t.Fatal(page, err)
	}
	next, err := s.Records(t.Context(), "enrolled", page[1].EventID, 2)
	if err != nil || len(next) != 1 || next[0].EventID == page[0].EventID {
		t.Fatal(next, err)
	}
	status, err := s.Status(t.Context())
	if err != nil || status.Records != 3 || status.Pending != 0 {
		t.Fatal(status, err)
	}
	rec, err := s.Record(t.Context(), "one")
	if err != nil || rec.EventID != "one" {
		t.Fatal(rec, err)
	}
	if _, err = s.Record(t.Context(), "absent"); !errors.Is(err, eventstore.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.Record(t.Context(), ""); !errors.Is(err, eventstore.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = s.Records(t.Context(), "", "", 0); !errors.Is(err, eventstore.ErrInvalid) {
		t.Fatal(err)
	}
	if rows, err := s.Records(t.Context(), "other", "", 10); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

// TestOldestPendingExcludesStoppedDeliveries checks that oldest pending excludes stopped
// deliveries.
func TestOldestPendingExcludesStoppedDeliveries(t *testing.T) {
	db, s, p := fixture(t)
	if err := p.Publish(t.Context(), event.Event{ID: "old", Type: event.Enrolled}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"paused", "expired", "cancelled", "delivered"} {
		if _, err := db.DB().ExecContext(t.Context(), "UPDATE event_deliveries SET state = ?", state); err != nil {
			t.Fatal(err)
		}
		status, err := s.Status(t.Context())
		if err != nil || !status.OldestPending.IsZero() {
			t.Fatal(state, status, err)
		}
	}
}
