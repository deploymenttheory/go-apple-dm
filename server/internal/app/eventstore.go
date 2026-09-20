package app

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/audit"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
)

func (c Config) publisher() event.Publisher {
	if c.persistentEvents != nil {
		return c.persistentEvents
	}
	if c.Bus != nil {
		return c.Bus
	}
	return nil
}

func (a *App) wirePersistentSinks(ctx context.Context) error {
	s, err := eventstore.Open(ctx, a.db, a.dialect)
	if err != nil {
		return fmt.Errorf("app: event store: %w", err)
	}
	reg := eventsink.Default()
	senders := map[string]eventstore.Sender{}
	transactional := map[string]eventstore.Sender{}
	destinations := []string{}
	store, err := a.auditStore(ctx)
	if err != nil {
		return err
	}
	if store != nil {
		a.audit = store
		destinations = append(destinations, "audit")
		send := func(ctx context.Context, rec eventsink.Record) error {
			var channel mdm.Channel
			for ch := mdm.ChannelDevice; ch.Valid(); ch++ {
				if ch.String() == rec.Channel {
					channel = ch
					break
				}
			}
			_, err := store.Append(
				ctx,
				audit.Record{
					EventID: rec.EventID,
					At:      rec.At,
					Type:    rec.Type,
					Actor:   rec.Actor,
					Enrollment: mdm.EnrollmentID{
						Channel:  channel,
						ID:       rec.ID,
						ParentID: rec.Parent,
					},
					Fields: rec.Fields,
				},
			)
			return wrapError(err)
		}
		if a.cfg.Sinks.AuditStore == nil {
			// auditStore opened the native store using a.db. Retention cannot
			// prune an append before its delivery acknowledgement commits.
			transactional["audit"] = send
		} else {
			senders["audit"] = send
		}
	}
	p := &eventstore.Publisher{
		Store:        s,
		Registry:     reg,
		Destinations: destinations,
		Subscribers:  a.cfg.publisher(),
		Report:       func(err error) { a.cfg.Logger.Error("app: event recording or notification failed", "error", err) },
	}
	a.eventStore, a.eventPublisher = s, p
	if err := a.openWebhooks(ctx, s); err != nil {
		return err
	}
	if a.webhooks != nil {
		p.CaptureAdditional = a.webhooks.CaptureOutcome
	}
	a.cfg.persistentEvents = p
	if a.cfg.Sinks.Audit && a.cfg.Bus != nil {
		a.cfg.Bus.Subscribe(event.All, eventsink.Slog(a.cfg.Logger, reg))
	}
	w := &eventstore.Worker{
		Store:                     s,
		Destinations:              senders,
		TransactionalDestinations: transactional,
	}
	a.addWorker("event-delivery", w.Run)
	if a.webhooks != nil {
		w.Resolve = a.webhooks.Resolve
	}
	return nil
}
