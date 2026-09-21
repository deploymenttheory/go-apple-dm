package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Rollover tracks an enrollment issuer transition independently of APNs renewal.
type Rollover struct {
	IssuerID    string    `json:"issuerId"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	Phase       string    `json:"phase"`
	CreatedAt   time.Time `json:"createdAt"`
	ActivatedAt time.Time `json:"activatedAt,omitempty"`
}

// Migration contains public progress only. Issuance credentials remain in the
// enrollment replacement store, subject to its own authorization checks.
type Migration struct {
	Retry        int       `json:"retry"`
	Device       string    `json:"device"`
	Phase        string    `json:"phase"`
	TrustCommand string    `json:"trustCommand,omitempty"`
	Attempt      string    `json:"attempt,omitempty"`
	Candidate    string    `json:"candidate,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	NextAttempt  time.Time `json:"nextAttempt,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Generation   int64     `json:"generation"`
}

// rolloverKey constructs the namespaced key for rollover state.
func rolloverKey(id, rev string) string { return "pki/lifecycle/rollover/" + id + "/" + rev }

// migrationPrefix constructs the namespaced key for migration state.
func migrationPrefix(id, rev string) string { return "pki/lifecycle/migration/" + id + "/" + rev + "/" }

// migrationKey constructs the namespaced key for migration state.
func migrationKey(id, rev, device string) string {
	return migrationPrefix(id, rev) + fingerprint([]byte(device))
}

// getJSON reads and decodes a workflow record from state storage.
func getJSON(ctx context.Context, s state.Reader, key string, out any) error {
	r, err := s.Get(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(r.Value, out)
}

// putJSON encodes a workflow record through the supplied state transaction.
func putJSON(ctx context.Context, tx state.Tx, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: key, Value: b})
}

// PrepareRollover creates a persistent cohort without switching the default CA.
func (m *Manager) PrepareRollover(ctx context.Context, id, rev string, devices []string) (Rollover, error) {
	k, err := key(id)
	if err != nil {
		return Rollover{}, err
	}
	jobKey := rolloverKey(id, rev)
	var out Rollover
	err = m.Store.Update(ctx, []string{k, jobKey}, func(tx state.Tx) error {
		if err := getJSON(ctx, tx, jobKey, &out); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		r, err := read(ctx, tx, id)
		if err != nil {
			return err
		}
		v, err := revision(&r, rev)
		if err != nil {
			return err
		}
		if r.Kind != Issuer || r.Active == "" || r.Pending != rev || v.Phase != "ready" {
			return ErrConflict
		}
		// Finish and retire the previous authority before starting another
		// transition, so each retained issuer has one unambiguous successor.
		for _, previous := range r.Revisions {
			if previous.Phase == "retained" {
				return fmt.Errorf("%w: retire the preceding issuer before another rollover", ErrConflict)
			}
		}
		out = Rollover{IssuerID: id, From: r.Active, To: rev, Phase: "prepared", CreatedAt: tx.Now()}
		for _, device := range devices {
			if device == "" {
				return ErrInvalid
			}
			if err = putJSON(ctx, tx, migrationKey(id, rev, device), Migration{Device: device, Phase: "queued", UpdatedAt: tx.Now()}); err != nil {
				return err
			}
		}
		return putJSON(ctx, tx, jobKey, out)
	})
	return out, err
}

// Rollover loads the issuer rollover associated with an identity and target revision. An
// invalid identity ID or missing workflow returns an error.
func (m *Manager) Rollover(ctx context.Context, id, rev string) (Rollover, error) {
	if _, err := key(id); err != nil {
		return Rollover{}, err
	}
	var out Rollover
	err := getJSON(ctx, m.Store, rolloverKey(id, rev), &out)
	return out, err
}

// ActivateRollover is called only after the application has installed both
// issuer routes and overlapping trust. Existing devices remain tracked separately.
func (m *Manager) ActivateRollover(ctx context.Context, id, rev string) (Rollover, error) {
	k, err := key(id)
	if err != nil {
		return Rollover{}, err
	}
	jobKey := rolloverKey(id, rev)
	var out Rollover
	err = m.Store.Update(ctx, []string{k, jobKey}, func(tx state.Tx) error {
		if err := getJSON(ctx, tx, jobKey, &out); err != nil {
			return err
		}
		if out.Phase == "migrating" || out.Phase == "complete" {
			return nil
		}
		r, err := read(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.Pending != rev || r.Active != out.From {
			return ErrConflict
		}
		v, err := revision(&r, rev)
		if err != nil {
			return err
		}
		if _, _, _, err = m.validate(r, v.Material, tx.Now()); err != nil {
			return err
		}
		old, _ := revision(&r, r.Active)
		old.Phase = "retained"
		v.Phase = "active"
		v.ActivatedAt = tx.Now()
		r.Active = rev
		r.Pending = ""
		if err = write(ctx, tx, r); err != nil {
			return err
		}
		out.Phase = "migrating"
		out.ActivatedAt = tx.Now()
		return putJSON(ctx, tx, jobKey, out)
	})
	return out, err
}

// Migrations returns one bounded page; after is the last returned device's hash.
func (m *Manager) Migrations(ctx context.Context, id, rev, after string, limit int) ([]Migration, string, error) {
	if _, err := key(id); err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > 1000 {
		return nil, "", ErrInvalid
	}
	prefix := migrationPrefix(id, rev)
	cursor := ""
	if after != "" {
		cursor = prefix + after
	}
	rows, err := m.Store.List(ctx, prefix, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := []Migration{}
	next := ""
	for _, row := range rows {
		var v Migration
		if err = json.Unmarshal(row.Value, &v); err != nil {
			return nil, "", err
		}
		out = append(out, v)
		next = fingerprint([]byte(v.Device))
	}
	if len(rows) < limit {
		next = ""
	}
	return out, next, nil
}

// SaveMigration rejects stale worker updates. An unconfirmed device can never be
// removed from the cohort just because it is offline or its retry grant expired.
func (m *Manager) SaveMigration(ctx context.Context, id, rev string, v Migration) error {
	if _, err := key(id); err != nil {
		return err
	}
	if !validID.MatchString(rev) || v.Device == "" || len(v.Device) > 1024 {
		return ErrInvalid
	}
	jobKey := rolloverKey(id, rev)
	return m.Store.Update(ctx, []string{jobKey}, func(tx state.Tx) error {
		var job Rollover
		if err := getJSON(ctx, tx, jobKey, &job); err != nil {
			return err
		}
		if job.Phase == "complete" {
			return ErrConflict
		}
		k := migrationKey(id, rev, v.Device)
		var current Migration
		err := getJSON(ctx, tx, k, &current)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if current.Generation != v.Generation {
			return ErrConflict
		}
		if current.Phase == "confirmed" && v.Phase != "confirmed" {
			return ErrConflict
		}
		switch v.Phase {
		case "queued", "trust-pending", "identity-pending", "confirmed", "blocked", "disabled":
		default:
			return ErrInvalid
		}
		v.Generation++
		v.UpdatedAt = tx.Now()
		return putJSON(ctx, tx, k, v)
	})
}

// CompleteRollover requires every recorded device to be confirmed or disabled.
// The server must also rescan enabled enrollments for any remaining old-CA pins.
func (m *Manager) CompleteRollover(ctx context.Context, id, rev string) error {
	jobKey := rolloverKey(id, rev)
	return m.Store.Update(ctx, []string{jobKey}, func(tx state.Tx) error {
		var job Rollover
		if err := getJSON(ctx, tx, jobKey, &job); err != nil {
			return err
		}
		if job.Phase == "complete" {
			return nil
		}
		if job.Phase != "migrating" {
			return ErrConflict
		}
		prefix := migrationPrefix(id, rev)
		after := ""
		for {
			rows, err := tx.List(ctx, prefix, after, 100)
			if err != nil {
				return err
			}
			for _, row := range rows {
				var v Migration
				if err = json.Unmarshal(row.Value, &v); err != nil {
					return err
				}
				if v.Phase != "confirmed" && v.Phase != "disabled" {
					return fmt.Errorf("%w: device migration outstanding", ErrConflict)
				}
				after = row.Key
			}
			if len(rows) < 100 {
				break
			}
		}
		job.Phase = "complete"
		return putJSON(ctx, tx, jobKey, job)
	})
}

// RetireIssuer retains signing material for certificate status publication.
func (m *Manager) RetireIssuer(ctx context.Context, id, rev string) (Identity, error) {
	return m.change(ctx, id, func(tx state.Tx, r *record) error {
		if r.Kind != Issuer || r.Active == rev || r.Pending == rev {
			return ErrConflict
		}
		var job Rollover
		if err := getJSON(ctx, tx, rolloverKey(id, r.Active), &job); err != nil {
			return err
		}
		if job.From != rev || job.Phase != "complete" {
			return ErrConflict
		}
		v, err := revision(r, rev)
		if err != nil {
			return err
		}
		v.Phase = "retired"
		return nil
	})
}
