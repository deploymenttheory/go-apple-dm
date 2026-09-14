package eventstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func TestEventStoreRejectsInvalidOperations(t *testing.T) {
	db, s, _ := fixture(t)
	if _, err := eventstore.Open(t.Context(), nil, sqlite.Dialect); err == nil {
		t.Fatal("accepted nil database")
	}
	if _, err := eventstore.Open(t.Context(), db.DB(), sqlcommon.Dialect{Name: "bad"}); err == nil {
		t.Fatal("accepted unsupported dialect")
	}
	if _, err := eventstore.MigrationSet(sqlcommon.Dialect{Name: "bad"}); err == nil {
		t.Fatal("accepted unsupported schema")
	}
	for _, record := range []eventsink.Record{
		{}, {EventID: "id", Type: "type"}, {EventID: strings.Repeat("x", 65), Type: "type", At: time.Now()},
		{EventID: "id", Type: strings.Repeat("x", 129), At: time.Now()},
		{EventID: "id", Type: "type", At: time.Now(), Fields: map[string]any{"bad": make(chan int)}},
		{EventID: "id", Type: "type", At: time.Now(), Fields: map[string]any{"oversize": strings.Repeat("x", 1<<20)}},
	} {
		if err := s.Capture(t.Context(), record, nil); err == nil {
			t.Fatal("accepted invalid record")
		}
	}
	valid := eventsink.Record{EventID: "id", Type: "enrolled", At: time.Now()}
	for _, dest := range []string{"", strings.Repeat("x", 129)} {
		if err := s.Capture(t.Context(), valid, []string{dest}); err == nil {
			t.Fatal("accepted invalid destination")
		}
	}
	for _, lease := range []time.Duration{0, time.Hour + 1} {
		if _, err := s.Claim(t.Context(), lease); err == nil {
			t.Fatal("accepted invalid lease")
		}
	}
	for _, code := range []string{"remote secret response", "unknown"} {
		if err := s.Finish(t.Context(), eventstore.Delivery{Token: "token"}, code, 0); err == nil {
			t.Fatal("accepted remote response as stored code")
		}
	}
	if err := s.Finish(t.Context(), eventstore.Delivery{}, "", 0); err == nil {
		t.Fatal("accepted absent lease token")
	}
	if err := s.Finish(t.Context(), eventstore.Delivery{Token: "token"}, "", -1); err == nil {
		t.Fatal("accepted negative retry delay")
	}
	if _, err := s.List(t.Context(), "", "", "", 0); err == nil {
		t.Fatal("accepted invalid page")
	}
	if _, err := s.List(t.Context(), "unknown", "", "", 5); err == nil {
		t.Fatal("accepted invalid delivery state")
	}
	if err := s.Retry(t.Context(), "", ""); err == nil {
		t.Fatal("accepted empty retry target")
	}
	var p eventstore.Publisher
	if err := p.Run(t.Context(), func(context.Context) error { return nil }); !errors.Is(err, eventstore.ErrCapture) {
		t.Fatal(err)
	}
	if err := p.Publish(t.Context(), event.Event{}); !errors.Is(err, eventstore.ErrCapture) {
		t.Fatal(err)
	}
}

func TestPersistentEventReadAndWriteFailuresSurface(t *testing.T) {
	db, s, p := fixture(t)
	if err := p.Publish(t.Context(), event.Event{ID: "event", Type: event.Enrolled}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := eventstore.Open(t.Context(), db.DB(), sqlite.Dialect); err == nil {
		t.Fatal("ignored migration failure")
	}
	if _, err := s.Record(t.Context(), "event"); err == nil {
		t.Fatal("ignored record read failure")
	}
	if _, err := s.Records(t.Context(), "", "", 5); err == nil {
		t.Fatal("ignored record list failure")
	}
	if _, err := s.List(t.Context(), "", "", "", 5); err == nil {
		t.Fatal("ignored delivery list failure")
	}
	if _, err := s.Status(t.Context()); err == nil {
		t.Fatal("ignored status failure")
	}
	if err := s.Retry(t.Context(), "event", "audit"); err == nil {
		t.Fatal("ignored retry failure")
	}
	if err := (&eventstore.Worker{Store: s}).Run(t.Context()); err == nil {
		t.Fatal("worker continued after database failure")
	}
}

func TestCorruptProjectionAndDeliveryRowsCannotBeDelivered(t *testing.T) {
	db, s, p := fixture(t)
	if err := p.Publish(t.Context(), event.Event{ID: "event", Type: event.Enrolled}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), "UPDATE event_records SET payload = '{'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Record(t.Context(), "event"); err == nil {
		t.Fatal("read corrupt event")
	}
	if _, err := s.Records(t.Context(), "", "", 5); err == nil {
		t.Fatal("listed corrupt event")
	}
	if _, err := s.Claim(t.Context(), time.Minute); err == nil {
		t.Fatal("claimed corrupt event")
	}
	if _, err := db.DB().ExecContext(t.Context(), "UPDATE event_deliveries SET attempts = 'malformed'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(t.Context(), "", "", "", 5); err == nil {
		t.Fatal("listed corrupt delivery")
	}
}

func TestLeaseUpdateFailureCannotAcknowledgeOrLoseAttempt(t *testing.T) {
	db, s, p := fixture(t)
	p.Destinations = []string{"audit"}
	if err := p.Publish(t.Context(), event.Event{ID: "event", Type: event.Enrolled}); err != nil {
		t.Fatal(err)
	}
	const trigger = "CREATE TRIGGER fail_update BEFORE UPDATE ON event_deliveries BEGIN SELECT RAISE(ABORT, 'injected write failure'); END"
	if _, err := db.DB().ExecContext(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(t.Context(), time.Minute); err == nil {
		t.Fatal("ignored lease update failure")
	}
	if err := s.Retry(t.Context(), "event", "audit"); err == nil {
		t.Fatal("ignored retry update failure")
	}
	if _, err := db.DB().ExecContext(t.Context(), "DROP TRIGGER fail_update"); err != nil {
		t.Fatal(err)
	}
	delivery, err := s.Claim(t.Context(), time.Minute)
	if err != nil || delivery.Attempts != 1 {
		t.Fatal("failed lease changed attempt state", delivery, err)
	}
	if _, err := db.DB().ExecContext(t.Context(), trigger); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(t.Context(), delivery, "", 0); err == nil {
		t.Fatal("ignored acknowledgement failure")
	}
}

func TestFailedDenialCaptureAndSubscriberFailureAreVisible(t *testing.T) {
	db, s, p := fixture(t)
	reported := 0
	p.Report = func(error) { reported++ }
	bus := event.New()
	bus.Subscribe(event.All, func(context.Context, event.Event) error { return errors.New("observer failed") })
	p.Subscribers = bus
	if err := p.Publish(t.Context(), event.Event{ID: "captured", Type: event.Enrolled}); err != nil {
		t.Fatal(err)
	}
	if reported != 1 {
		t.Fatal("missing subscriber error report")
	}
	if _, err := s.Record(t.Context(), "captured"); err != nil {
		t.Fatal("observer failure lost persistent occurrence", err)
	}
	reg := eventsink.NewRegistry()
	reg.Register(event.Enrolled, func(any) map[string]any { return map[string]any{"bad": make(chan int)} })
	p.Registry = reg
	if err := p.Publish(t.Context(), event.Event{Type: event.Enrolled, Data: struct{}{}}); !errors.Is(err, eventstore.ErrCapture) {
		t.Fatal(err)
	}
	p.Registry = nil
	if _, err := db.DB().ExecContext(t.Context(), "DROP TABLE event_deliveries"); err != nil {
		t.Fatal(err)
	}
	if err := p.Publish(t.Context(), event.Event{Type: event.AdminDenied}); !errors.Is(err, eventstore.ErrCapture) {
		t.Fatal(err)
	}
	if p.Health().FailedDenials != 1 || reported != 2 {
		t.Fatal("denial recording loss was hidden", p.Health(), reported)
	}
}

func TestCancelledParticipatingTransactionsCannotClaimOrFinish(t *testing.T) {
	_, s, _ := fixture(t)
	for _, operation := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := s.Claim(ctx, time.Minute); return err },
		func(ctx context.Context) error { return s.Finish(ctx, eventstore.Delivery{Token: "token"}, "", 0) },
		func(ctx context.Context) error { return s.Retry(ctx, "event", "audit") },
	} {
		ctx, cancel := context.WithCancel(t.Context())
		err := s.Run(ctx, func(ctx context.Context) error { cancel(); return operation(ctx) })
		if !errors.Is(err, context.Canceled) {
			t.Fatal("ignored cancelled participant transaction", err)
		}
	}
}

func TestMalformedSQLProjectionColumnsFailReads(t *testing.T) {
	db, s, _ := fixture(t)
	db.DB().SetMaxOpenConns(1)
	if _, err := db.DB().ExecContext(t.Context(), "CREATE TEMP VIEW event_records AS SELECT NULL AS payload, 'event' AS event_id, 'enrolled' AS type"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Record(t.Context(), "event"); err == nil {
		t.Fatal("read malformed SQL projection")
	}
	if _, err := s.Records(t.Context(), "", "", 5); err == nil {
		t.Fatal("listed malformed SQL projection")
	}
	if _, err := s.Claim(t.Context(), time.Minute); err == nil {
		t.Fatal("claimed malformed SQL projection")
	}
}

func TestWorkerBackoffCapsAndCancellationKeepsUnknownAttempt(t *testing.T) {
	db, s, p := fixture(t)
	p.Destinations = []string{"webhook"}
	if err := p.Publish(t.Context(), event.Event{ID: "retry", Type: event.Enrolled}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), "UPDATE event_deliveries SET attempts = 20"); err != nil {
		t.Fatal(err)
	}
	w := &eventstore.Worker{Store: s, Destinations: map[string]eventstore.Sender{"webhook": func(context.Context, eventsink.Record) error { return errors.New("transport") }}}
	if err := w.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(t.Context(), "", "", "", 10)
	if err != nil || len(rows) != 1 || time.Until(rows[0].NextAttempt) < 59*time.Minute || time.Until(rows[0].NextAttempt) > time.Hour {
		t.Fatal("backoff cap changed", rows, err)
	}
	if err := s.Retry(t.Context(), "retry", "webhook"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	w.Destinations["webhook"] = func(context.Context, eventsink.Record) error { cancel(); return context.Canceled }
	if err := w.Step(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Retry(t.Context(), "retry", "webhook"); !errors.Is(err, eventstore.ErrLease) {
		t.Fatal("cancelled remote attempt lost its lease", err)
	}
}
