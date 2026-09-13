package eventstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

//go:embed migrations/*/*.sql
var migrations embed.FS

var (
	ErrInvalid  = errors.New("eventstore: invalid argument")
	ErrLease    = errors.New("eventstore: delivery lease no longer held")
	ErrEmpty    = errors.New("eventstore: no delivery ready")
	ErrNotFound = errors.New("eventstore: event not found")
)

// Store owns event records and destination acknowledgements, not its SQL pool.
type Store struct {
	db   *sql.DB
	d    sqlcommon.Dialect
	unit sqlcommon.UnitOfWork
}

// Open applies the event schema without changing existing domain migrations.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect) (*Store, error) {
	if db == nil {
		return nil, ErrInvalid
	}
	switch d.Name {
	case "sqlite", "postgres", "mysql":
	default:
		return nil, ErrInvalid
	}
	set := sqlcommon.MigrationSet{Table: "event_schema_migrations", FS: sqlcommon.MustSub(migrations, "migrations/"+d.Name)}
	if _, err := sqlcommon.MigrateSet(ctx, db, d, set); err != nil {
		return nil, err
	}
	return &Store{db: db, d: d, unit: sqlcommon.UnitOfWork{DB: db, Dialect: d}}, nil
}

// Run joins the transaction used by the participating domain stores.
func (s *Store) Run(ctx context.Context, fn func(context.Context) error) error {
	return s.unit.Run(ctx, fn)
}

// Capture inserts only the projected record and snapshots its destinations.
// An existing event ID is an error; delivery retries read the original record.
func (s *Store) Capture(ctx context.Context, rec eventsink.Record, destinations []string) error {
	if rec.EventID == "" || len(rec.EventID) > 64 || rec.Type == "" || len(rec.Type) > 128 || rec.At.IsZero() {
		return sqlcommon.Fail(ctx, ErrInvalid)
	}
	destinations = slices.Clone(destinations)
	slices.Sort(destinations)
	destinations = slices.Compact(destinations)
	for _, dest := range destinations {
		if dest == "" || len(dest) > 128 {
			return sqlcommon.Fail(ctx, ErrInvalid)
		}
	}
	data, err := json.Marshal(rec)
	if err != nil || len(data) > 1<<20 {
		return sqlcommon.Fail(ctx, ErrInvalid)
	}
	return s.Run(ctx, func(ctx context.Context) error {
		q := sqlcommon.Query(ctx, s.db)
		if _, err := q.ExecContext(ctx, s.d.Rebind("INSERT INTO event_records (event_id, type, occurred_at, payload) VALUES (?, ?, ?, ?)"), rec.EventID, rec.Type, rec.At.UnixMicro(), string(data)); err != nil {
			return err
		}
		for _, dest := range destinations {
			if _, err := q.ExecContext(ctx, s.d.Rebind("INSERT INTO event_deliveries (event_id, destination, state, attempts, next_attempt, lease_token, lease_until, last_code) VALUES (?, ?, 'pending', 0, 0, '', 0, '')"), rec.EventID, dest); err != nil {
				return err
			}
		}
		return nil
	})
}

// Delivery contains one leased destination. Token fences late worker results.
type Delivery struct {
	Record      eventsink.Record `json:"record"`
	Destination string           `json:"destination"`
	Token       string           `json:"-"`
	Attempts    int              `json:"attempts"`
}

func (s *Store) now(ctx context.Context, q sqlcommon.Queryer) (time.Time, error) {
	query := "SELECT CAST((julianday('now') - 2440587.5) * 86400000000 AS INTEGER)"
	switch s.d.Name {
	case "postgres":
		query = "SELECT CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000000 AS BIGINT)"
	case "mysql":
		query = "SELECT CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(6)) * 1000000 AS SIGNED)"
	}
	var us int64
	err := q.QueryRowContext(ctx, query).Scan(&us)
	return time.UnixMicro(us).UTC(), err
}

// Claim leases one ready destination. Expired claims can be reclaimed after a
// process crash. No network request runs while this transaction is open.
func (s *Store) Claim(ctx context.Context, lease time.Duration) (Delivery, error) {
	if lease <= 0 || lease > time.Hour {
		return Delivery{}, ErrInvalid
	}
	var out Delivery
	err := s.Run(ctx, func(ctx context.Context) error {
		q := sqlcommon.Query(ctx, s.db)
		now, err := s.now(ctx, q)
		if err != nil {
			return err
		}
		query := `SELECT r.payload, d.destination, d.attempts FROM event_deliveries d JOIN event_records r ON r.event_id = d.event_id WHERE d.state = 'pending' AND d.next_attempt <= ? AND d.lease_until <= ? ORDER BY d.next_attempt, r.occurred_at, d.event_id, d.destination LIMIT 1`
		if s.d.ForUpdate != "" {
			query += " FOR UPDATE SKIP LOCKED"
		}
		var raw []byte
		err = q.QueryRowContext(ctx, s.d.Rebind(query), now.UnixMicro(), now.UnixMicro()).Scan(&raw, &out.Destination, &out.Attempts)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEmpty
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &out.Record); err != nil {
			return fmt.Errorf("eventstore: corrupt record: %w", err)
		}
		out.Token = rand.Text()
		out.Attempts++
		_, err = q.ExecContext(ctx, s.d.Rebind("UPDATE event_deliveries SET lease_token = ?, lease_until = ?, attempts = attempts + 1 WHERE event_id = ? AND destination = ?"), out.Token, now.Add(lease).UnixMicro(), out.Record.EventID, out.Destination)
		return err
	})
	if err != nil {
		return Delivery{}, err
	}
	return out, nil
}

// Finish acknowledges, blocks, or schedules another attempt using the lease
// token. Codes must be fixed local categories, never remote response text.
func (s *Store) Finish(ctx context.Context, delivery Delivery, code string, retryAfter time.Duration) error {
	state := "delivered"
	switch code {
	case "":
	case "transport", "timeout", "http-408", "http-429", "http-5xx":
		state = "pending"
	case "http-rejected", "destination-unavailable":
		state = "blocked"
	default:
		return ErrInvalid
	}
	if delivery.Token == "" || retryAfter < 0 || retryAfter > 24*time.Hour {
		return ErrInvalid
	}
	return s.Run(ctx, func(ctx context.Context) error {
		q := sqlcommon.Query(ctx, s.db)
		now, err := s.now(ctx, q)
		if err != nil {
			return err
		}
		res, err := q.ExecContext(ctx, s.d.Rebind("UPDATE event_deliveries SET state = ?, next_attempt = ?, lease_token = '', lease_until = 0, last_code = ? WHERE event_id = ? AND destination = ? AND lease_token = ? AND lease_until > ? AND state = 'pending'"), state, now.Add(retryAfter).UnixMicro(), code, delivery.Record.EventID, delivery.Destination, delivery.Token, now.UnixMicro())
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
