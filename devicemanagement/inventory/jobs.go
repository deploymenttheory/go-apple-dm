package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Job persists progress independently of a worker process. Counters count committed work.
type Job struct {
	ID               string          `json:"id"`
	AccountID        string          `json:"account_id,omitempty"`
	Revision         int64           `json:"revision"`
	Kind             string          `json:"kind"`
	State            string          `json:"state"`
	Stage            string          `json:"stage"`
	Cursor           string          `json:"cursor,omitempty"`
	Checkpoint       json.RawMessage `json:"checkpoint,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	Devices          int             `json:"devices"`
	Coverage         int             `json:"coverage"`
	CoverageAttempts int             `json:"coverage_attempts"`
	Failed           int             `json:"failed"`
	Error            string          `json:"error,omitempty"`
	Force            bool            `json:"force"`
	Owner            string          `json:"-"`
	LeaseUntil       time.Time       `json:"-"`
}

type lease struct {
	JobID string
	Owner string
	Until time.Time
}

// Enqueue coalesces outstanding syncs for the same connection.
func (r *Repository) Enqueue(ctx context.Context, account string, force bool, now time.Time) (Job, error) {
	var out Job
	err := r.Backend.Update(ctx, func(tx Tx) error {
		a, err := get[Account](ctx, tx, "account/"+account)
		if err != nil {
			return err
		}
		if !a.Enabled {
			return ErrInvalid
		}
		err = walk(ctx, tx, "job/", func(e Entry) error {
			var j Job
			if e := json.Unmarshal(e.Value, &j); e != nil {
				return e
			}
			if j.AccountID == account && j.Revision == a.Revision && (j.State == "queued" || j.State == "running" || j.State == "paused") {
				out = j
			}
			return nil
		})
		if err != nil {
			return err
		}
		if out.ID != "" {
			return nil
		}
		out = Job{ID: ID(), AccountID: account, Revision: a.Revision, Kind: "axm", State: "queued", Stage: "devices", CreatedAt: now, UpdatedAt: now, Force: force}
		return put(ctx, tx, "job/"+out.ID, out)
	})
	return out, err
}

// Job returns durable progress.
func (r *Repository) Job(ctx context.Context, id string) (Job, error) {
	return get[Job](ctx, r.Backend, "job/"+id)
}

// Jobs returns bounded job metadata ordered by key. Cursor is the last ID seen.
func (r *Repository) Jobs(ctx context.Context, after string, limit int) ([]Job, error) {
	es, err := r.Backend.Scan(ctx, "job/", "job/"+after, limit)
	if err != nil {
		return nil, err
	}
	out := []Job{}
	for _, e := range es {
		var j Job
		if err := json.Unmarshal(e.Value, &j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// Control pauses, resumes, or cancels a job. A running worker is fenced at its next commit.
func (r *Repository) Control(ctx context.Context, id, action string, now time.Time) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		j, e := get[Job](ctx, tx, "job/"+id)
		if e != nil {
			return e
		}
		switch action {
		case "pause":
			if j.State != "running" && j.State != "queued" {
				return ErrConflict
			}
			j.State = "paused"
		case "resume":
			if j.State != "paused" {
				return ErrConflict
			}
			j.State = "queued"
		case "cancel":
			if j.FinishedAt != nil {
				return ErrConflict
			}
			j.State = "cancelled"
			j.FinishedAt = &now
		default:
			return ErrInvalid
		}
		// Keep the lease until the worker acknowledges the stop, avoiding overlapping Apple requests.
		j.UpdatedAt = now
		return put(ctx, tx, "job/"+id, j)
	})
}

// claim enforces one AxM worker across all accounts and server processes.
func (r *Repository) claim(ctx context.Context, owner string, now time.Time) (Job, error) {
	var out Job
	err := r.Backend.Update(ctx, func(tx Tx) error {
		l, e := get[lease](ctx, tx, "lease/axm")
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
		if l.Until.After(now) {
			return ErrLease
		}
		e = walk(ctx, tx, "job/", func(e Entry) error {
			var j Job
			if err := json.Unmarshal(e.Value, &j); err != nil {
				return err
			}
			if j.State == "queued" || j.State == "running" {
				if out.ID == "" || j.CreatedAt.Before(out.CreatedAt) {
					out = j
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
		if out.ID == "" {
			return ErrNotFound
		}
		a, e := get[Account](ctx, tx, "account/"+out.AccountID)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
		if e != nil || !a.Enabled || a.Revision != out.Revision {
			out.State = "cancelled"
			out.FinishedAt = &now
			return put(ctx, tx, "job/"+out.ID, out)
		}
		out.State = "running"
		out.Owner = owner
		out.LeaseUntil = now.Add(2 * time.Minute)
		out.UpdatedAt = now
		if e := put(ctx, tx, "lease/axm", lease{out.ID, owner, out.LeaseUntil}); e != nil {
			return e
		}
		// Private lease fields are persisted separately, not exposed in job JSON.
		return put(ctx, tx, "job/"+out.ID, out)
	})
	return out, err
}

// commitJob checks owner, lease and account revision before atomically advancing progress.
func (r *Repository) commitJob(ctx context.Context, j *Job, fn func(Tx) error) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		now := time.Now().UTC()
		l, e := get[lease](ctx, tx, "lease/axm")
		if e != nil {
			return e
		}
		if l.JobID != j.ID || l.Owner != j.Owner || !l.Until.After(now) {
			return ErrLease
		}
		old, e := get[Job](ctx, tx, "job/"+j.ID)
		if e != nil {
			return e
		}
		if old.State != "running" {
			return ErrStopped
		}
		a, e := get[Account](ctx, tx, "account/"+j.AccountID)
		if e != nil {
			return e
		}
		if a.Revision != j.Revision || !a.Enabled {
			return ErrStopped
		}
		if fn != nil {
			if e := fn(tx); e != nil {
				return e
			}
		}
		j.UpdatedAt = now
		l.Until = now.Add(2 * time.Minute)
		if e := put(ctx, tx, "lease/axm", l); e != nil {
			return e
		}
		return put(ctx, tx, "job/"+j.ID, j)
	})
}

// release releases only the lease still owned by the departing worker.
func (r *Repository) release(ctx context.Context, j Job) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		l, e := get[lease](ctx, tx, "lease/axm")
		if e != nil {
			return e
		}
		if l.Owner == j.Owner && l.JobID == j.ID {
			return tx.Delete(ctx, "lease/axm")
		}
		return nil
	})
}

// DeleteAccount disconnects observations and fences jobs before removing credentials.
// Historical device records survive. Native enrollment is never modified.
func (r *Repository) DeleteAccount(ctx context.Context, id string, revision int64, now time.Time) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		a, e := get[Account](ctx, tx, "account/"+id)
		if e != nil {
			return e
		}
		if a.Revision != revision {
			return ErrConflict
		}
		if e := walk(ctx, tx, "job/", func(entry Entry) error {
			var j Job
			if e := json.Unmarshal(entry.Value, &j); e != nil {
				return e
			}
			if j.AccountID == id && j.FinishedAt == nil {
				j.State = "cancelled"
				j.FinishedAt = &now
				return put(ctx, tx, entry.Key, j)
			}
			return nil
		}); e != nil {
			return e
		}
		if e := walk(ctx, tx, "device/", func(entry Entry) error {
			var d DeviceRecord
			if e := json.Unmarshal(entry.Value, &d); e != nil {
				return e
			}
			changed := false
			for k, o := range d.Sources {
				if o.Source.AccountID == id && strings.HasPrefix(o.Source.Kind, "axm.") {
					o.Disconnected = true
					d.Sources[k] = o
					changed = true
				}
			}
			if changed {
				rebuild(&d)
				return r.saveRecord(ctx, tx, d)
			}
			return nil
		}); e != nil {
			return e
		}
		if e := tx.Delete(ctx, "credential/"+id); e != nil {
			return e
		}
		if e := tx.Delete(ctx, "schedule/"+id); e != nil {
			return e
		}
		return tx.Delete(ctx, "account/"+id)
	})
}

// CancelAll atomically stops queued, paused and running jobs while retaining the active lease.
func (r *Repository) CancelAll(ctx context.Context, now time.Time) (int, error) {
	count := 0
	err := r.Backend.Update(ctx, func(tx Tx) error {
		return walk(ctx, tx, "job/", func(entry Entry) error {
			var j Job
			if err := json.Unmarshal(entry.Value, &j); err != nil {
				return err
			}
			if j.FinishedAt != nil {
				return nil
			}
			j.State = "cancelled"
			j.FinishedAt = &now
			j.UpdatedAt = now
			if err := put(ctx, tx, entry.Key, j); err != nil {
				return err
			}
			count++
			return nil
		})
	})
	return count, err
}
