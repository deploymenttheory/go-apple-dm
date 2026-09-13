package eventstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// Status describes all retained destination records. OldestPending includes
// blocked deliveries because they still require operator action.
type Status struct {
	Records       int64     `json:"records"`
	Pending       int64     `json:"pending"`
	Blocked       int64     `json:"blocked"`
	Delivered     int64     `json:"delivered"`
	Retries       int64     `json:"retries"`
	OldestPending time.Time `json:"oldest_pending,omitempty"`
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	var out Status
	var oldest sql.NullInt64
	err := sqlcommon.Query(ctx, s.db).QueryRowContext(ctx, `SELECT
(SELECT COUNT(*) FROM event_records),
COALESCE(SUM(CASE WHEN d.state = 'pending' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN d.state = 'blocked' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN d.state = 'delivered' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN d.attempts > 1 THEN d.attempts - 1 ELSE 0 END), 0),
MIN(CASE WHEN d.state <> 'delivered' THEN r.occurred_at ELSE NULL END)
FROM event_deliveries d JOIN event_records r ON r.event_id = d.event_id`).Scan(&out.Records, &out.Pending, &out.Blocked, &out.Delivered, &out.Retries, &oldest)
	if oldest.Valid {
		out.OldestPending = time.UnixMicro(oldest.Int64).UTC()
	}
	return out, err
}

// Record returns the original allowlisted projection, including occurrences
// that have no external delivery destination.
func (s *Store) Record(ctx context.Context, id string) (eventsink.Record, error) {
	var out eventsink.Record
	if id == "" || len(id) > 64 {
		return out, ErrInvalid
	}
	var raw string
	err := sqlcommon.Query(ctx, s.db).QueryRowContext(ctx, s.d.Rebind("SELECT payload FROM event_records WHERE event_id = ?"), id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

// Records pages captured occurrences by their stable event ID. Delivery state
// is available separately through List, so local-only events remain visible.
func (s *Store) Records(ctx context.Context, kind, after string, limit int) ([]eventsink.Record, error) {
	if limit <= 0 || limit > 1000 || len(kind) > 128 || len(after) > 64 {
		return nil, ErrInvalid
	}
	query := "SELECT payload FROM event_records WHERE event_id > ?"
	args := []any{after}
	if kind != "" {
		query += " AND type = ?"
		args = append(args, kind)
	}
	query += " ORDER BY event_id LIMIT ?"
	args = append(args, limit)
	rows, err := sqlcommon.Query(ctx, s.db).QueryContext(ctx, s.d.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []eventsink.Record{}
	for rows.Next() {
		var rec eventsink.Record
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeliveryView omits lease credentials and event payloads from operator status.
type DeliveryView struct {
	EventID     string    `json:"event_id"`
	Destination string    `json:"destination"`
	State       string    `json:"state"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt"`
	LastCode    string    `json:"last_code,omitempty"`
}

// List pages destinations by event ID then destination, using the last row as
// the next cursor. Empty state includes every delivery state.
func (s *Store) List(ctx context.Context, state, afterEvent, afterDestination string, limit int) ([]DeliveryView, error) {
	if limit <= 0 || limit > 1000 {
		return nil, ErrInvalid
	}
	if state != "" && state != "pending" && state != "blocked" && state != "delivered" {
		return nil, ErrInvalid
	}
	query := "SELECT event_id, destination, state, attempts, next_attempt, last_code FROM event_deliveries WHERE (event_id > ? OR (event_id = ? AND destination > ?))"
	args := []any{afterEvent, afterEvent, afterDestination}
	if state != "" {
		query += " AND state = ?"
		args = append(args, state)
	}
	query += " ORDER BY event_id, destination LIMIT ?"
	args = append(args, limit)
	rows, err := sqlcommon.Query(ctx, s.db).QueryContext(ctx, s.d.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryView{}
	for rows.Next() {
		var row DeliveryView
		var next int64
		if err := rows.Scan(&row.EventID, &row.Destination, &row.State, &row.Attempts, &next, &row.LastCode); err != nil {
			return nil, err
		}
		if next != 0 {
			row.NextAttempt = time.UnixMicro(next).UTC()
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Retry releases a blocked or waiting delivery for a new attempt. Active leases
// and completed deliveries cannot be reset by this operation.
func (s *Store) Retry(ctx context.Context, eventID, destination string) error {
	if eventID == "" || destination == "" {
		return ErrInvalid
	}
	return s.Run(ctx, func(ctx context.Context) error {
		q := sqlcommon.Query(ctx, s.db)
		now, err := s.now(ctx, q)
		if err != nil {
			return err
		}
		res, err := q.ExecContext(ctx, s.d.Rebind("UPDATE event_deliveries SET state = 'pending', next_attempt = 0, lease_token = '', lease_until = 0, last_code = '' WHERE event_id = ? AND destination = ? AND state <> 'delivered' AND lease_until <= ?"), eventID, destination, now.UnixMicro())
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrLease
		}
		return nil
	})
}
