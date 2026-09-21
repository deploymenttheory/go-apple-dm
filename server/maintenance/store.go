package maintenance

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

//go:embed migrations/*/*.sql
var migrations embed.FS

var (
	ErrInvalid     = errors.New("maintenance: invalid argument")
	ErrFenced      = errors.New("maintenance: writes paused")
	ErrOwner       = errors.New("maintenance: fence ownership changed")
	ErrParticipant = errors.New("maintenance: participant registration missing")
)

// Store persists maintenance ownership and registered writer acknowledgements in SQL.
type Store struct {
	db *sql.DB
	d  sqlcommon.Dialect
}

// MigrationSet exposes the compiled schema for coordinated recovery.
func MigrationSet(d sqlcommon.Dialect) (sqlcommon.MigrationSet, error) {
	switch d.Name {
	case "sqlite", "postgres", "mysql":
		return sqlcommon.MigrationSet{
			Table: "maintenance_schema_migrations",
			FS:    sqlcommon.MustSub(migrations, "migrations/"+d.Name),
		}, nil
	default:
		return sqlcommon.MigrationSet{}, ErrInvalid
	}
}

// Open optionally initializes the schema. Recovery control clients use false:
// a server version without participation cannot safely be backed up online.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, initialize bool) (*Store, error) {
	set, err := MigrationSet(d)
	if err != nil || db == nil {
		return nil, ErrInvalid
	}
	if initialize {
		if _, err := sqlcommon.MigrateSet(ctx, db, d, set); err != nil {
			return nil, wrap(err)
		}
	}
	s := &Store{db: db, d: d}
	if _, err := s.Status(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Member identifies a registered writer and whether it has drained for the current
// maintenance ticket.
type Member struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Drained bool   `json:"drained"`
}

// Status summarizes the active maintenance ticket and the drain state of registered
// writers.
type Status struct {
	Token   string   `json:"token,omitempty"`
	Members []Member `json:"members"`
}

// Ready reports whether a maintenance ticket is active and every registered writer has
// acknowledged its drain.
func (s Status) Ready() bool {
	if s.Token == "" {
		return false
	}
	for _, m := range s.Members {
		if !m.Drained {
			return false
		}
	}
	return true
}

// transaction serializes control changes against registration and acknowledgement.
func (s *Store) transaction(
	ctx context.Context,
	fn func(context.Context, sqlcommon.Queryer, string) error,
) error {
	w := sqlcommon.UnitOfWork{DB: s.db, Dialect: s.d}
	return wrap(w.Run(ctx, func(ctx context.Context) error {
		q := sqlcommon.Query(ctx, s.db)
		var token string
		if err := q.QueryRowContext(ctx, "SELECT token FROM maintenance_state WHERE id = 1 "+s.d.ForUpdate).
			Scan(&token); err != nil {
			return err
		}
		return fn(ctx, q, token)
	}))
}

// Status reads the maintenance ticket and participant acknowledgements within one store
// transaction.
func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{Members: []Member{}}
	err := s.transaction(ctx, func(ctx context.Context, q sqlcommon.Queryer, token string) error {
		out.Token = token
		rows, err := q.QueryContext(
			ctx,
			"SELECT id, label, drained_token FROM maintenance_participants ORDER BY id",
		)
		if err != nil {
			return err
		}
		defer func(rows *sql.Rows) { _ = rows.Close() }(rows)
		for rows.Next() {
			var m Member
			var ack string
			if err := rows.Scan(&m.ID, &m.Label, &ack); err != nil {
				return err
			}
			m.Drained = token != "" && ack == token
			out.Members = append(out.Members, m)
		}
		return rows.Err()
	})
	return out, err
}

// Request pauses new registration. Persist the caller-generated ticket before
// requesting a fence, so an interrupted backup can be explicitly resumed.
func (s *Store) Request(ctx context.Context, ticket string) error {
	if len(ticket) < 26 || len(ticket) > 128 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(ctx context.Context, q sqlcommon.Queryer, current string) error {
		if current != "" && current != ticket {
			return ErrOwner
		}
		_, err := q.ExecContext(
			ctx,
			s.d.Rebind("UPDATE maintenance_state SET token = ? WHERE id = 1"),
			ticket,
		)
		return err
	})
}

// Resume clears the write pause only when ticket owns the current maintenance fence. An
// empty ticket returns ErrInvalid and a different ticket returns ErrOwner.
func (s *Store) Resume(ctx context.Context, ticket string) error {
	if ticket == "" {
		return ErrInvalid
	}
	return s.transaction(ctx, func(ctx context.Context, q sqlcommon.Queryer, current string) error {
		if current != ticket {
			return ErrOwner
		}
		_, err := q.ExecContext(ctx, "UPDATE maintenance_state SET token = '' WHERE id = 1")
		return err
	})
}

// WaitDrained returns only after every registered process acknowledges this fence.
// Cancellation leaves the persistent fence in place for inspection and recovery.
func (s *Store) WaitDrained(ctx context.Context, ticket string) error {
	for {
		status, err := s.Status(ctx)
		if err != nil {
			return err
		}
		if status.Token != ticket || ticket == "" {
			return ErrOwner
		}
		if status.Ready() {
			return nil
		}
		if err := wait(ctx); err != nil {
			return wrap(err)
		}
	}
}

// Register registers a writer with a unique participant ID. It rejects empty or oversized
// labels and returns ErrFenced while a maintenance pause is active.
func (s *Store) Register(ctx context.Context, label string) (*Participant, error) {
	if label == "" || len(label) > 255 {
		return nil, ErrInvalid
	}
	p := &Participant{store: s, id: rand.Text(), accepting: true}
	err := s.transaction(ctx, func(ctx context.Context, q sqlcommon.Queryer, token string) error {
		if token != "" {
			return ErrFenced
		}
		_, err := q.ExecContext(
			ctx,
			s.d.Rebind(
				"INSERT INTO maintenance_participants (id, label, drained_token) VALUES (?, ?, '')",
			),
			p.id,
			label,
		)
		return err
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// acknowledge records that a registered writer has drained for the current maintenance
// ticket.
func (s *Store) acknowledge(ctx context.Context, id, ticket string) error {
	return s.transaction(ctx, func(ctx context.Context, q sqlcommon.Queryer, current string) error {
		if current != ticket {
			return ErrOwner
		}
		result, err := q.ExecContext(
			ctx,
			s.d.Rebind("UPDATE maintenance_participants SET drained_token = ? WHERE id = ?"),
			ticket,
			id,
		)
		return affected(result, err)
	})
}

// Forget requires an explicit assertion that the named process has stopped.
// Calling this on a running process invalidates the backup's consistency guarantee.
func (s *Store) Forget(ctx context.Context, ticket, id string, processStopped bool) error {
	if !processStopped || id == "" || ticket == "" {
		return ErrInvalid
	}
	return s.transaction(ctx, func(ctx context.Context, q sqlcommon.Queryer, current string) error {
		if current != ticket {
			return ErrOwner
		}
		result, err := q.ExecContext(
			ctx,
			s.d.Rebind("DELETE FROM maintenance_participants WHERE id = ?"),
			id,
		)
		return affected(result, err)
	})
}

// affected requires the expected participant or maintenance row to have been updated.
func affected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return wrap(err)
	}
	if n != 1 {
		return ErrParticipant
	}
	return nil
}

// wait waits 100 milliseconds between maintenance checks, stopping promptly on context
// cancellation.
func wait(ctx context.Context) error {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// wrap classifies a maintenance storage failure while preserving nil success.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("maintenance: %w", err)
}
