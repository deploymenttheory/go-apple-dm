package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/audit"
	auditinmem "github.com/deploymenttheory/go-apple-dm/audit/inmem"
	auditsql "github.com/deploymenttheory/go-apple-dm/audit/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/eventsink"
	"github.com/deploymenttheory/go-apple-dm/mdm"
)

// DefaultAuditRetention is how long records are kept when a retention is
// configured without a window. Long enough to investigate an incident
// reported weeks late, short enough that the table is not a liability.
const DefaultAuditRetention = 90 * 24 * time.Hour

// DefaultAuditPruneInterval is how often the retention worker runs.
const DefaultAuditPruneInterval = time.Hour

// auditStore resolves the audit trail, following the same three-way choice as
// the other satellite stores: an injected store wins, then the process's own
// database, then memory when there is none.
func (a *App) auditStore(ctx context.Context) (audit.Store, error) {
	switch {
	case a.cfg.Sinks.AuditStore != nil:
		return a.cfg.Sinks.AuditStore, nil
	case !a.cfg.Sinks.Persist:
		return nil, nil
	case a.db == nil:
		return auditinmem.New(), nil
	default:
		s, err := auditsql.Open(ctx, a.db, a.dialect, auditsql.Options{})
		if err != nil {
			return nil, fmt.Errorf("app: audit store: %w", err)
		}
		return s, nil
	}
}

// auditSink writes each projected event to the trail. A failed write is
// returned to the bus, which logs it through the error handler: losing an
// audit record is worth a line, and it must not stop the other sinks.
func auditSink(store audit.Store, reg *eventsink.Registry) event.Handler {
	return func(ctx context.Context, e event.Event) error {
		rec := reg.Project(e)
		_, err := store.Append(ctx, audit.Record{
			At:         rec.At,
			Type:       rec.Type,
			Actor:      rec.Actor,
			Enrollment: e.Enrollment,
			Fields:     rec.Fields,
		})
		if err != nil {
			return fmt.Errorf("app: audit append: %w", err)
		}
		return nil
	}
}

// runAuditRetention prunes the trail on an interval. It follows the DEP
// worker's shape: driven by the injected clock so it is testable, a
// non-positive interval means disabled, and a failed prune is logged and
// retried at the next tick rather than stopping the loop.
func (a *App) runAuditRetention(ctx context.Context) error {
	if a.audit == nil || a.cfg.Sinks.Retention <= 0 {
		<-ctx.Done()
		return nil
	}
	interval := a.cfg.Sinks.PruneInterval
	if interval <= 0 {
		interval = DefaultAuditPruneInterval
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.cfg.Clock.After(interval):
		}
		before := a.cfg.Clock.Now().Add(-a.cfg.Sinks.Retention)
		n, err := a.audit.Prune(ctx, before)
		if err != nil {
			a.cfg.Logger.WarnContext(ctx, "app: audit retention", "error", err)
			continue
		}
		if n > 0 {
			a.cfg.Logger.InfoContext(ctx, "app: audit retention", "pruned", n, "before", before)
		}
	}
}

// auditRoutes serve the trail.
//
//	GET /audit          list, newest first, filtered and paged
//	GET /audit/{id}     one record
func (a *App) auditRoutes() []adminRoute {
	return []adminRoute{
		{
			Pattern: "GET /audit", Action: ActionReadAudit, Family: "audit",
			Handler: http.HandlerFunc(a.listAudit),
		},
		{
			Pattern: "GET /audit/{id}", Action: ActionReadAudit, Family: "audit",
			Handler: http.HandlerFunc(a.getAudit),
		},
	}
}

func (a *App) listAudit(w http.ResponseWriter, r *http.Request) {
	q, err := auditQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%w: limit %q", audit.ErrInvalid, v))
			return
		}
		limit = n
	}
	res, err := a.audit.List(r.Context(), q, audit.Page{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		a.writeAuditError(w, r, err)
		return
	}
	// Items and NextCursor are the page shape every other listing uses, so
	// dmctl reads it with the client's existing paging helper.
	writeJSON(w, http.StatusOK, map[string]any{"Items": auditViews(res.Items), "NextCursor": res.NextCursor})
}

func (a *App) getAudit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%w: id %q", audit.ErrInvalid, r.PathValue("id")))
		return
	}
	rec, err := a.audit.Get(r.Context(), id)
	if err != nil {
		a.writeAuditError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, auditView(rec))
}

// writeAuditError maps the store's sentinels to statuses and keeps the cause
// out of the body: a storage failure is not the caller's business.
func (a *App) writeAuditError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, audit.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, audit.ErrInvalid):
		writeError(w, http.StatusBadRequest, err)
	default:
		a.cfg.Logger.WarnContext(r.Context(), "app: audit", "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("app: audit unavailable"))
	}
}

// auditQuery parses the filters. An unparsable time is the caller's error,
// not an empty result set that looks like "nothing happened".
func auditQuery(r *http.Request) (audit.Query, error) {
	v := r.URL.Query()
	q := audit.Query{
		Type:       v.Get("type"),
		Actor:      v.Get("actor"),
		Enrollment: v.Get("enrollment"),
	}
	for _, f := range []struct {
		key string
		dst *time.Time
	}{{"since", &q.Since}, {"until", &q.Until}} {
		raw := v.Get(f.key)
		if raw == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return audit.Query{}, fmt.Errorf("%w: %s must be RFC 3339: %q", audit.ErrInvalid, f.key, raw)
		}
		*f.dst = ts
	}
	return q, nil
}

// auditRecordView is the wire shape: the enrollment is flattened so a reader
// does not have to know how mdm.EnrollmentID is spelled.
type auditRecordView struct {
	ID         int64
	At         time.Time
	Type       string
	Actor      string
	Channel    string         `json:",omitempty"`
	Enrollment string         `json:",omitempty"`
	Parent     string         `json:",omitempty"`
	Fields     map[string]any `json:",omitempty"`
}

func auditView(rec audit.Record) auditRecordView {
	v := auditRecordView{
		ID: rec.ID, At: rec.At, Type: rec.Type, Actor: rec.Actor,
		Enrollment: rec.Enrollment.ID, Parent: rec.Enrollment.ParentID, Fields: rec.Fields,
	}
	if rec.Enrollment.ID != "" || rec.Enrollment.Channel != mdm.ChannelUnknown {
		v.Channel = rec.Enrollment.Channel.String()
	}
	return v
}

func auditViews(recs []audit.Record) []auditRecordView {
	out := make([]auditRecordView, 0, len(recs))
	for _, rec := range recs {
		out = append(out, auditView(rec))
	}
	return out
}
