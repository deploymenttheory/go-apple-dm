package eventstore_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// TestScopedDestinationsPreserveProjectionAndSubscribers exercises selection at
// the durable capture boundary without exposing unprojected source credentials.
func TestScopedDestinationsPreserveProjectionAndSubscribers(t *testing.T) {
	_, store, publisher := fixture(t)
	// Spare capacity catches an append that silently changes the backing array.
	backing := []string{"audit", "webhook", "untouched", "untouched"}
	publisher.Destinations = backing[:2]
	var selected []eventsink.Record
	publisher.SelectDestinations = func(record eventsink.Record) []string {
		selected = append(selected, record)
		if record.Fields["Method"] == "mdm" {
			return []string{"managed-content-wake", "audit"}
		}
		return nil
	}
	var notifications int
	bus := event.New()
	bus.Subscribe(event.All, func(context.Context, event.Event) error {
		notifications++
		return nil
	})
	publisher.Subscribers = bus
	for _, item := range []struct{ id, method string }{{"scoped-mdm", "mdm"}, {"unscoped-ddm", "ddm"}} {
		if err := publisher.Publish(t.Context(), event.Event{ID: item.id, Type: "managed-content-dispatch", Data: map[string]any{
			"Operation": item.id, "Method": item.method, "sToken": "private-token", "payload": "private-payload",
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if notifications != 2 || !reflect.DeepEqual(backing, []string{"audit", "webhook", "untouched", "untouched"}) {
		t.Fatal("selection changed subscribers or shared destinations", notifications, backing)
	}
	if len(selected) != 2 {
		t.Fatal("selector did not receive both projected events", len(selected))
	}
	for _, record := range selected {
		if len(record.Fields) != 2 || record.Fields["Operation"] != record.EventID || record.Fields["sToken"] != nil || record.Fields["payload"] != nil {
			t.Fatal("selector received unprojected source data", record.Fields)
		}
	}
	deliveries, err := store.List(t.Context(), "", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, delivery := range deliveries {
		got[delivery.EventID] = append(got[delivery.EventID], delivery.Destination)
	}
	want := map[string][]string{
		"scoped-mdm":   {"audit", "managed-content-wake", "webhook"},
		"unscoped-ddm": {"audit", "webhook"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("scoped destinations leaked into later capture or replaced base deliveries", got)
	}
}

// TestInvalidScopedDestinationRollsBackParticipation proves that a bad selection
// cannot commit its associated mutation or notify in-process subscribers.
func TestInvalidScopedDestinationRollsBackParticipation(t *testing.T) {
	db, store, publisher := fixture(t)
	publisher.SelectDestinations = func(eventsink.Record) []string { return []string{""} }
	var notifications int
	bus := event.New()
	bus.Subscribe(event.All, func(context.Context, event.Event) error {
		notifications++
		return nil
	})
	publisher.Subscribers = bus
	err := publisher.Run(t.Context(), func(ctx context.Context) error {
		if _, err := sqlcommon.Query(ctx, db.DB()).ExecContext(ctx, "INSERT INTO domain_mutations (id) VALUES (1)"); err != nil {
			return err
		}
		return publisher.Publish(ctx, event.Event{ID: "invalid-selection", Type: "managed-content-dispatch"})
	})
	if !errors.Is(err, eventstore.ErrCapture) || !errors.Is(err, eventstore.ErrInvalid) {
		t.Fatal("invalid selection did not fail required capture", err)
	}
	var mutations int
	if err := db.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM domain_mutations").Scan(&mutations); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if mutations != 0 || notifications != 0 || status.Records != 0 || status.Pending != 0 {
		t.Fatal("failed capture left domain or delivery effects", mutations, notifications, status)
	}
}
