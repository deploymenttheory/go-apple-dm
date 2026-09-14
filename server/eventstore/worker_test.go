package eventstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/audit"
	auditsql "github.com/deploymenttheory/go-apple-dm/server/audit/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func TestWorkerRetryAndOperatorRecovery(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		code, state string
	}{
		{"success", nil, "", "delivered"},
		{"network", errors.New("remote secret must never be stored"), "transport", "pending"},
		{"timeout", context.DeadlineExceeded, "timeout", "pending"},
		{"request timeout", &eventsink.HTTPError{Status: 408}, "http-408", "pending"},
		{"rate limit", &eventsink.HTTPError{Status: 429, RetryAfter: time.Hour}, "http-429", "pending"},
		{"unavailable", &eventsink.HTTPError{Status: 503}, "http-5xx", "pending"},
		{"forbidden", &eventsink.HTTPError{Status: 403}, "http-rejected", "blocked"},
		{"redirect", &eventsink.HTTPError{Status: 302}, "http-rejected", "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, s, p := fixture(t)
			p.Destinations = []string{"receiver"}
			if err := p.Publish(t.Context(), event.Event{Type: event.Enrolled}); err != nil {
				t.Fatal(err)
			}
			var eventID string
			w := &eventstore.Worker{Store: s, Destinations: map[string]eventstore.Sender{"receiver": func(_ context.Context, r eventsink.Record) error { eventID = r.EventID; return tc.err }}}
			if err := w.Step(t.Context()); err != nil {
				t.Fatal(err)
			}
			rows, err := s.List(t.Context(), "", "", "", 10)
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			row := rows[0]
			if row.EventID != eventID || row.State != tc.state || row.LastCode != tc.code || row.Attempts != 1 {
				t.Fatal(row)
			}
			status, err := s.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if tc.state == "delivered" {
				if status.Delivered != 1 || !status.OldestPending.IsZero() {
					t.Fatal(status)
				}
				if err := s.Retry(t.Context(), row.EventID, row.Destination); !errors.Is(err, eventstore.ErrLease) {
					t.Fatal(err)
				}
				return
			}
			if status.OldestPending.IsZero() {
				t.Fatal("pending age lost")
			}
			if err := w.Step(t.Context()); !errors.Is(err, eventstore.ErrEmpty) {
				t.Fatal("backoff or block ignored", err)
			}
			if err := s.Retry(t.Context(), row.EventID, row.Destination); err != nil {
				t.Fatal(err)
			}
			w.Destinations["receiver"] = func(_ context.Context, r eventsink.Record) error {
				if r.EventID != eventID {
					t.Error("retry changed occurrence ID")
				}
				return nil
			}
			if err := w.Step(t.Context()); err != nil {
				t.Fatal(err)
			}
			status, err = s.Status(t.Context())
			if err != nil || status.Delivered != 1 || status.Retries != 1 {
				t.Fatal(status, err)
			}
		})
	}
}

func TestNativeAuditAppendAndAcknowledgementCommitTogether(t *testing.T) {
	for _, failure := range []string{"append", "acknowledgement", "lease"} {
		t.Run(failure, func(t *testing.T) {
			db, s, p := fixture(t)
			trail, err := auditsql.Open(t.Context(), db.DB(), sqlite.Dialect, auditsql.Options{})
			if err != nil {
				t.Fatal(err)
			}
			p.Destinations = []string{"audit"}
			if err := p.Publish(t.Context(), event.Event{Type: event.Enrolled}); err != nil {
				t.Fatal(err)
			}
			if failure == "acknowledgement" {
				_, err = db.DB().ExecContext(t.Context(), "CREATE TRIGGER fail_finish BEFORE UPDATE OF state ON event_deliveries BEGIN SELECT RAISE(ABORT, 'injected acknowledgement failure'); END")
				if err != nil {
					t.Fatal(err)
				}
			}
			fault := errors.New("injected local failure")
			inject := true
			send := func(ctx context.Context, rec eventsink.Record) error {
				if _, ok := sqlcommon.CurrentTransaction(ctx, db.DB()); !ok {
					t.Error("native audit is outside the delivery transaction")
				}
				if _, err := trail.Append(ctx, audit.Record{EventID: rec.EventID, Type: rec.Type, At: rec.At}); err != nil {
					return err
				}
				if inject && failure == "append" {
					return fault
				}
				if inject && failure == "lease" {
					_, err := sqlcommon.Query(ctx, db.DB()).ExecContext(ctx, "UPDATE event_deliveries SET lease_until = 0")
					return err
				}
				return nil
			}
			w := &eventstore.Worker{Store: s, TransactionalDestinations: map[string]eventstore.Sender{"audit": send}}
			if err := w.Step(t.Context()); err == nil {
				t.Fatal("injected failure was ignored")
			}
			var count int
			if err := db.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_records").Scan(&count); err != nil || count != 0 {
				t.Fatal("unacknowledged audit append survived rollback", count, err)
			}
			status, err := s.Status(t.Context())
			if err != nil || status.Delivered != 0 {
				t.Fatal("failed append was acknowledged", status, err)
			}
			if failure == "acknowledgement" {
				if _, err := db.DB().ExecContext(t.Context(), "DROP TRIGGER fail_finish"); err != nil {
					t.Fatal(err)
				}
			}
			// A restarted worker can reclaim the failed attempt after lease expiry.
			if _, err := db.DB().ExecContext(t.Context(), "UPDATE event_deliveries SET lease_until = 0"); err != nil {
				t.Fatal(err)
			}
			inject = false
			if err := w.Step(t.Context()); err != nil {
				t.Fatal(err)
			}
			if n, err := trail.Prune(t.Context(), time.Now().Add(time.Hour)); err != nil || n != 1 {
				t.Fatal("committed audit occurrence missing", n, err)
			}
			if err := w.Step(t.Context()); !errors.Is(err, eventstore.ErrEmpty) {
				t.Fatal("retention made acknowledged delivery replayable", err)
			}
		})
	}
}

func TestRemoteDeliveryDoesNotHoldSQLTransaction(t *testing.T) {
	db, s, p := fixture(t)
	db.DB().SetMaxOpenConns(1)
	p.Destinations = []string{"remote"}
	if err := p.Publish(t.Context(), event.Event{Type: event.Enrolled}); err != nil {
		t.Fatal(err)
	}
	send := func(ctx context.Context, _ eventsink.Record) error {
		if _, ok := sqlcommon.CurrentTransaction(ctx, db.DB()); ok {
			t.Error("remote request holds a SQL transaction")
		}
		// The only connection must be available to independent work.
		_, err := db.DB().ExecContext(ctx, "INSERT INTO domain_mutations (id) VALUES (1)")
		return err
	}
	w := &eventstore.Worker{Store: s, Timeout: time.Second, Destinations: map[string]eventstore.Sender{"remote": send}, TransactionalDestinations: map[string]eventstore.Sender{"remote": send}}
	if err := w.Step(t.Context()); !errors.Is(err, eventstore.ErrInvalid) {
		t.Fatal("ambiguous destination registration accepted", err)
	}
	w.TransactionalDestinations = nil
	if err := w.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(t.Context())
	if err != nil || status.Delivered != 1 {
		t.Fatal("remote delivery failed", status, err)
	}
}

func TestWorkerMissingDestinationAndCancellation(t *testing.T) {
	_, s, p := fixture(t)
	p.Destinations = []string{"retired-receiver"}
	if err := p.Publish(t.Context(), event.Event{Type: event.Enrolled}); err != nil {
		t.Fatal(err)
	}
	w := &eventstore.Worker{Store: s, PollInterval: time.Millisecond}
	if err := w.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(t.Context(), "blocked", "", "", 10)
	if err != nil || len(rows) != 1 || rows[0].LastCode != "destination-unavailable" {
		t.Fatal(rows, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := (&eventstore.Worker{}).Step(t.Context()); !errors.Is(err, eventstore.ErrInvalid) {
		t.Fatal(err)
	}
	w.Timeout = time.Hour
	if err := w.Step(t.Context()); !errors.Is(err, eventstore.ErrInvalid) {
		t.Fatal(err)
	}
}
