package eventsink

import (
	"context"
	"log/slog"
	"sort"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
)

// Slog returns a handler that writes one projected record per event with sorted
// fields. Durability and retention depend on the deployment's log collection;
// the handler does not persist records itself.
func Slog(log *slog.Logger, reg *Registry) event.Handler {
	if log == nil {
		log = slog.Default()
	}
	if reg == nil {
		reg = Default()
	}
	return func(ctx context.Context, e event.Event) error {
		rec := reg.Project(e)
		attrs := []any{"event", rec.Type, "at", rec.At}
		if rec.Actor != "" {
			attrs = append(attrs, "actor", rec.Actor)
		}
		if rec.ID != "" {
			attrs = append(attrs, "enrollment", rec.ID, "channel", rec.Channel)
		}
		if rec.Parent != "" {
			attrs = append(attrs, "parent", rec.Parent)
		}
		keys := make([]string, 0, len(rec.Fields))
		for k := range rec.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			attrs = append(attrs, k, rec.Fields[k])
		}
		log.InfoContext(ctx, "audit", attrs...)
		return nil
	}
}
