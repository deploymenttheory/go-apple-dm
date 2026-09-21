package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
)

// eventRoutes declares event inspection and retry routes when the persistent event store
// is available.
func (a *App) eventRoutes() []adminRoute {
	if a.eventStore == nil {
		return nil
	}
	return []adminRoute{
		{
			Pattern: "GET /events/status",
			Action:  ActionReadAudit,
			Family:  "events",
			Handler: http.HandlerFunc(a.eventStatus),
		},
		{
			Pattern: "GET /events",
			Action:  ActionReadAudit,
			Family:  "events",
			Handler: http.HandlerFunc(a.listEvents),
		},
		{
			Pattern: "GET /events/deliveries",
			Action:  ActionReadAudit,
			Family:  "events",
			Handler: http.HandlerFunc(a.listEvents),
		},
		{
			Pattern: "GET /events/{event}",
			Action:  ActionReadAudit,
			Family:  "events",
			Handler: http.HandlerFunc(a.getEvent),
		},
		{
			Pattern:       "POST /events/{event}/retry",
			Action:        ActionRetryEvents,
			Family:        "events",
			LocalMutation: true,
			Handler:       http.HandlerFunc(a.retryEvent),
		},
	}
}

// eventStatus returns persistent delivery counts, capture health and supervised worker
// state.
func (a *App) eventStatus(w http.ResponseWriter, r *http.Request) {
	s, err := a.eventStore.Status(r.Context())
	if err != nil {
		a.eventError(w, err)
		return
	}
	writeJSON(
		w,
		http.StatusOK,
		map[string]any{"Delivery": s, "Capture": a.eventPublisher.Health(), "Workers": a.Workers()},
	)
}

// listEvents returns a bounded page of event records or destination deliveries according
// to the requested route.
func (a *App) listEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			a.eventError(w, eventstore.ErrInvalid)
			return
		}
		limit = n
	}
	var items any
	var err error
	if r.URL.Path == "/events/deliveries" {
		items, err = a.eventStore.List(
			r.Context(),
			q.Get("state"),
			q.Get("after_event"),
			q.Get("after_destination"),
			limit,
		)
	} else {
		items, err = a.eventStore.Records(r.Context(), q.Get("type"), q.Get("after_event"), limit)
	}
	if err != nil {
		a.eventError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items})
}

// getEvent returns one retained event record, mapping lookup failures to the event API
// error contract.
func (a *App) getEvent(w http.ResponseWriter, r *http.Request) {
	record, err := a.eventStore.Record(r.Context(), r.PathValue("event"))
	if err != nil {
		a.eventError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

// retryEvent validates and reschedules a projected-event delivery; native webhook
// destinations are rejected and must use their own replay API.
func (a *App) retryEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Destination string `json:"destination"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		a.eventError(w, eventstore.ErrInvalid)
		return
	}
	// Native deliveries require their own replay permission and, for sensitive
	// captures, a separate sensitive replay grant. The generic outbox endpoint cannot grant either.
	if strings.HasPrefix(body.Destination, "native-webhook:") {
		writeError(w, http.StatusForbidden, ErrUnauthorized)
		return
	}
	if err := a.eventStore.Retry(r.Context(), r.PathValue("event"), body.Destination); err != nil {
		a.eventError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// eventError maps event-store sentinel errors to HTTP responses without exposing
// unexpected storage failures.
func (a *App) eventError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	if errors.Is(err, eventstore.ErrInvalid) {
		status = http.StatusBadRequest
	}
	if errors.Is(err, eventstore.ErrLease) {
		status = http.StatusConflict
	}
	if errors.Is(err, eventstore.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeError(w, status, err)
}
