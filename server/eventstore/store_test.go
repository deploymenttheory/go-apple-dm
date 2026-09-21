package eventstore_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// fixture opens an SQLite event store with a domain-mutation table and audit and webhook publisher
// destinations.
func fixture(t *testing.T) (*sqlite.Store, *eventstore.Store, *eventstore.Publisher) {
	t.Helper()
	db, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "events.sqlite"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := eventstore.Open(t.Context(), db.DB(), sqlite.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB().ExecContext(t.Context(), "CREATE TABLE domain_mutations (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return db, s, &eventstore.Publisher{Store: s, Destinations: []string{"audit", "webhook"}}
}

// TestRequiredCaptureRollsBackDomainMutation checks required capture rolls back domain mutation.
func TestRequiredCaptureRollsBackDomainMutation(t *testing.T) {
	db, _, p := fixture(t)
	notified := 0
	bus := event.New()
	bus.Subscribe(event.All, func(context.Context, event.Event) error { notified++; return nil })
	p.Subscribers = bus
	// Force a storage failure after the domain write by removing only the outbox.
	if _, err := db.DB().ExecContext(t.Context(), "DROP TABLE event_deliveries"); err != nil {
		t.Fatal(err)
	}
	err := p.Run(t.Context(), func(ctx context.Context) error {
		if _, err := sqlcommon.Query(ctx, db.DB()).ExecContext(ctx, "INSERT INTO domain_mutations (id) VALUES (1)"); err != nil {
			return err
		}
		_ = p.Publish(ctx, event.Event{Type: event.CommandQueued, Data: map[string]any{"unprojected_secret": "never store"}})
		return nil
	})
	if !errors.Is(err, eventstore.ErrCapture) {
		t.Fatal(err)
	}
	for _, table := range []string{"domain_mutations", "event_records"} {
		var n int
		if err := db.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	if notified != 0 || p.Health().LastFailure.IsZero() {
		t.Fatal("failure was not exposed", notified, p.Health())
	}
}

// TestDeliveryLeaseFencesStaleWorkerAndPreservesProjection checks that delivery lease fences stale
// worker and preserves projection.
func TestDeliveryLeaseFencesStaleWorkerAndPreservesProjection(t *testing.T) {
	db, s, p := fixture(t)
	if err := p.Publish(t.Context(), event.Event{Type: event.AdminAction, Data: map[string]any{"action": "write", "credential": "safe-id", "password": "secret-value"}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.Claim(t.Context(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Claim(t.Context(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.EventID == "" || first.Record.EventID != second.Record.EventID || first.Destination == second.Destination {
		t.Fatal("destination identity lost")
	}
	if _, err = s.Claim(t.Context(), time.Minute); !errors.Is(err, eventstore.ErrEmpty) {
		t.Fatal(err)
	}
	var payload string
	if err = db.DB().QueryRowContext(t.Context(), "SELECT payload FROM event_records").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "secret-value") || strings.Contains(payload, "password") {
		t.Fatal("unprojected data stored")
	}
	if _, err = db.DB().ExecContext(t.Context(), "UPDATE event_deliveries SET lease_until = 0 WHERE destination = ?", first.Destination); err != nil {
		t.Fatal(err)
	}
	// A new Store represents a restarted process using the same persistent state.
	restarted, err := eventstore.Open(t.Context(), db.DB(), sqlite.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	reclaimed, err := restarted.Claim(t.Context(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.Token == first.Token || reclaimed.Attempts != 2 {
		t.Fatal("lease was not replaced")
	}
	if err = s.Finish(t.Context(), first, "", 0); !errors.Is(err, eventstore.ErrLease) {
		t.Fatal("stale acknowledgement accepted", err)
	}
	if err = restarted.Finish(t.Context(), reclaimed, "http-429", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(t.Context(), second, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(t.Context(), time.Minute); !errors.Is(err, eventstore.ErrEmpty) {
		t.Fatal("backoff ignored", err)
	}
}

// TestDenialSurvivesOperationRollback checks that denial survives operation rollback.
func TestDenialSurvivesOperationRollback(t *testing.T) {
	db, s, p := fixture(t)
	denied := errors.New("denied")
	err := p.Run(t.Context(), func(ctx context.Context) error {
		if _, err := sqlcommon.Query(ctx, db.DB()).ExecContext(ctx, "INSERT INTO domain_mutations (id) VALUES (1)"); err != nil {
			return err
		}
		if err := p.Publish(ctx, event.Event{Type: event.AdminDenied}); err != nil {
			return err
		}
		return denied
	})
	if !errors.Is(err, denied) {
		t.Fatal(err)
	}
	var n int
	if err = db.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM domain_mutations").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	d, err := s.Claim(t.Context(), time.Minute)
	if err != nil || d.Record.Type != string(event.AdminDenied) {
		t.Fatal(d, err)
	}
}

// TestCaptureHealthOnlyAdvancesAfterCommit checks capture health only advances after commit.
func TestCaptureHealthOnlyAdvancesAfterCommit(t *testing.T) {
	_, _, p := fixture(t)
	fault := errors.New("domain validation failed")
	err := p.Run(t.Context(), func(ctx context.Context) error {
		if err := p.Publish(ctx, event.Event{Type: event.CommandQueued}); err != nil {
			return err
		}
		if !p.Health().LastSuccess.IsZero() {
			t.Error("reported successful capture before commit")
		}
		return fault
	})
	if !errors.Is(err, fault) || !p.Health().LastSuccess.IsZero() {
		t.Fatal("rolled-back capture reported successful", err, p.Health())
	}
	if err = p.Publish(t.Context(), event.Event{Type: event.CommandQueued}); err != nil || p.Health().LastSuccess.IsZero() {
		t.Fatal("committed capture was not reported", err, p.Health())
	}
}

// TestDeferredAdditionalCaptureOwnsDenialData checks deferred additional capture owns denial data.
func TestDeferredAdditionalCaptureOwnsDenialData(t *testing.T) {
	_, _, p := fixture(t)
	var reason string
	p.CaptureAdditional = func(_ context.Context, e event.Event) error {
		data, ok := e.Data.(map[string]string)
		if !ok {
			t.Fatal("concrete event type changed")
		}
		reason = data["reason"]
		return nil
	}
	fault := errors.New("denied")
	err := p.Run(t.Context(), func(ctx context.Context) error {
		data := map[string]string{"reason": "original"}
		if err := p.Publish(ctx, event.Event{Type: event.AdminDenied, Data: data}); err != nil {
			return err
		}
		data["reason"] = "mutated-after-publish"
		return fault
	})
	if !errors.Is(err, fault) || reason != "original" {
		t.Fatal("deferred capture observed released data", reason, err)
	}
	if err := p.Publish(t.Context(), event.Event{Type: event.AdminDenied, Data: make(chan int)}); !errors.Is(err, eventstore.ErrCapture) {
		t.Fatal(err)
	}
}
