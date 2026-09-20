package webhook

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
)

// Test queues a synthetic occurrence directly to the selected subscription.
func (s *Store) Test(ctx context.Context, id string, root bool) (string, error) {
	var delivery string
	err := s.unit.Run(ctx, func(ctx context.Context) error {
		sub, err := s.load(ctx, id, true)
		if err != nil {
			return err
		}
		if sub.Payload.Sensitive() && !root {
			return ErrForbidden
		}
		if sub.Deleted || !sub.Enabled {
			return ErrConflict
		}
		now := s.cfg.Now().UTC()
		e := Event{SchemaVersion: SchemaVersion, EventID: rand.Text(), Type: "webhook.test", OccurredAt: now, Source: s.cfg.Source, Data: map[string]any{"synthetic": true}}
		delivery, err = s.captureFor(ctx, e, sub.Subscription, now.Add(s.cfg.PayloadRetention))
		return err
	})
	return delivery, err
}

// Admin serves after the host application's ordinary Cedar authorization. Root
// remains a separate mandatory gate for sensitive destination operations.
func (s *Store) Admin(w http.ResponseWriter, r *http.Request, root bool) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/webhooks"), "/")
	parts := strings.Split(path, "/")
	method := r.Method
	decode := func(v any) error {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
			return ErrInvalid
		}
		return nil
	}
	var result any
	var err error
	status := http.StatusOK
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			apiError(w, ErrInvalid)
			return
		}
	}
	switch {
	case path == "catalogue" && method == http.MethodGet:
		result = Catalogue()
	case path == "status" && method == http.MethodGet:
		result, err = s.Status(r.Context())
	case path == "" && method == http.MethodGet:
		result, err = s.List(r.Context(), r.URL.Query().Get("after"), limit)
	case path == "" && method == http.MethodPost:
		var spec Spec
		err = decode(&spec)
		if err == nil {
			result, err = s.Create(r.Context(), spec, root)
			status = http.StatusCreated
		}
	case path == "replays" && method == http.MethodPost:
		var req ReplayRequest
		err = decode(&req)
		if err == nil {
			result, err = s.Replay(r.Context(), req, root)
			status = http.StatusAccepted
		}
	case parts[0] == "deliveries" && method == http.MethodGet:
		id := r.URL.Query().Get("subscription_id")
		if len(parts) == 2 {
			id = parts[1]
		} else if len(parts) != 1 {
			err = ErrNotFound
			break
		}
		result, err = s.Deliveries(r.Context(), id, r.URL.Query().Get("after"), limit)
		if err == nil && len(parts) == 2 {
			items, ok := result.([]Delivery)
			if !ok || len(items) != 1 || items[0].ID != id {
				err = ErrNotFound
			} else {
				result = items[0]
			}
		}
	case len(parts) == 3 && parts[0] == "deliveries" && parts[2] == "retry" && method == http.MethodPost:
		err = s.Retry(r.Context(), parts[1], root)
		status = http.StatusNoContent
	case len(parts) == 1 && path != "" && method == http.MethodGet:
		result, err = s.Get(r.Context(), parts[0])
	case len(parts) == 1 && path != "" && method == http.MethodPut:
		var change struct {
			Revision int  `json:"revision"`
			Spec     Spec `json:"spec"`
		}
		err = decode(&change)
		if err == nil {
			result, err = s.Update(r.Context(), parts[0], change.Revision, change.Spec, root)
		}
	case len(parts) == 1 && path != "" && method == http.MethodDelete:
		result, err = s.SetState(r.Context(), parts[0], "delete", root)
	case len(parts) == 2 && method == http.MethodPost:
		switch parts[1] {
		case "pause", "resume", "disable", "enable":
			result, err = s.SetState(r.Context(), parts[0], parts[1], root)
		case "test":
			var id string
			id, err = s.Test(r.Context(), parts[0], root)
			result = map[string]string{"delivery_id": id}
			status = http.StatusAccepted
		case "credentials":
			var body struct {
				OverlapSeconds *int64 `json:"overlap_seconds,omitempty"`
			}
			err = decode(&body)
			if err == nil {
				overlap := 24 * time.Hour
				if body.OverlapSeconds != nil {
					if *body.OverlapSeconds < 0 || *body.OverlapSeconds > 604800 {
						err = ErrInvalid
						break
					}
					overlap = time.Duration(*body.OverlapSeconds) * time.Second
				}
				result, err = s.Rotate(r.Context(), parts[0], overlap, root)
			}
		default:
			err = ErrNotFound
		}
	default:
		err = ErrNotFound
	}
	if err != nil {
		apiError(w, err)
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	b, err := json.Marshal(result)
	if err != nil {
		apiError(w, err)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func apiError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(err, ErrInvalid):
		status, code = 400, "invalid"
	case errors.Is(err, ErrForbidden):
		status, code = 403, "forbidden"
	case errors.Is(err, ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, ErrConflict) || errors.Is(err, eventstore.ErrLease):
		status, code = 409, "conflict"
	case errors.Is(err, ErrExpired):
		status, code = 410, "expired"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
