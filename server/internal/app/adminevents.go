package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
)

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

func (a *App) getEvent(w http.ResponseWriter, r *http.Request) {
	record, err := a.eventStore.Record(r.Context(), r.PathValue("event"))
	if err != nil {
		a.eventError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

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
	if err := a.eventStore.Retry(r.Context(), r.PathValue("event"), body.Destination); err != nil {
		a.eventError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

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
