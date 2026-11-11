package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
)

// Job persists progress independently of a worker process. Counters count committed work.
type Job struct {
	// RetryAt defers throttled work without marking it finished.
	RetryAt          time.Time              `json:"retry_at,omitzero"`
	ResourceID       string                 `json:"resource_id,omitempty"`
	Operations       []collection.Operation `json:"operations,omitempty"`
	AccountCreatedAt time.Time              `json:"account_created_at,omitzero"`
	ID               string                 `json:"id"`
	Generation       string                 `json:"generation"`
	AccountID        string                 `json:"account_id,omitempty"`
	Revision         int64                  `json:"revision"`
	Kind             string                 `json:"kind"`
	State            string                 `json:"state"`
	Stage            string                 `json:"stage"`
	Cursor           string                 `json:"cursor,omitempty"`
	Checkpoint       json.RawMessage        `json:"checkpoint,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
	FinishedAt       *time.Time             `json:"finished_at,omitempty"`
	Devices          int                    `json:"devices"`
	Coverage         int                    `json:"coverage"`
	CoverageAttempts int                    `json:"coverage_attempts"`
	Failed           int                    `json:"failed"`
	Error            string                 `json:"error,omitempty"`
	Force            bool                   `json:"force"`
	Owner            string                 `json:"-"`
	LeaseUntil       time.Time              `json:"-"`
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
			if j.Kind == "axm" && j.AccountID == account && j.Revision == a.Revision && (j.State == "queued" || j.State == "running" || j.State == "paused" || j.State == "deferred") {
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
		out = Job{ID: ID(), Generation: ID(), AccountID: account, Revision: a.Revision, AccountCreatedAt: a.CreatedAt, Kind: "axm", State: "queued", Stage: "devices", CreatedAt: now, UpdatedAt: now, Force: force}
		return put(ctx, tx, jobKey(out.Kind, out.ID), out)
	})
	return out, err
}

// Job returns durable progress.
func (r *Repository) Job(ctx context.Context, id string) (Job, error) {
	return readJob(ctx, r.Backend, id)
}

// Jobs returns bounded job metadata ordered by key. Cursor is the last ID seen.
func (r *Repository) Jobs(ctx context.Context, after string, limit int) ([]Job, error) {
	out := []Job{}
	for _, prefix := range []string{"job/", "axm-refresh-job/"} {
		entries, err := r.Backend.Scan(ctx, prefix, prefix+after, limit)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			var job Job
			if err := json.Unmarshal(entry.Value, &job); err != nil {
				return nil, err
			}
			out = append(out, job)
		}
	}
	slices.SortFunc(out, func(a, b Job) int { return strings.Compare(a.ID, b.ID) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Control pauses, resumes, or cancels a job. A running worker is fenced at its next commit.
func (r *Repository) Control(ctx context.Context, id, action string, now time.Time) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		j, e := readJob(ctx, tx, id)
		if e != nil {
			return e
		}
		switch action {
		case "pause":
			if j.State != "running" && j.State != "queued" && j.State != "deferred" {
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
		return put(ctx, tx, jobKey(j.Kind, j.ID), j)
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
		e = walkJobs(ctx, tx, func(e Entry) error {
			var j Job
			if err := json.Unmarshal(e.Value, &j); err != nil {
				return err
			}
			if j.State == "queued" || j.State == "running" || j.State == "deferred" {
				ready, err := readyJob(ctx, tx, j, now)
				if err != nil {
					return err
				}
				if !ready {
					return nil
				}
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
		if e != nil || !a.Enabled || a.Revision != out.Revision || (!out.AccountCreatedAt.IsZero() && !out.AccountCreatedAt.Equal(a.CreatedAt)) {
			out.State = "cancelled"
			out.FinishedAt = &now
			return put(ctx, tx, jobKey(out.Kind, out.ID), out)
		}
		if out.Kind == "axm-refresh" && !out.RetryAt.IsZero() {
			// A targeted retry reads all requested operations again in the same generation.
			// Counters describe that completed pass; retained failed attempts remain evidence.
			out.Devices, out.Coverage, out.Failed = 0, 0, 0
		}
		out.RetryAt = time.Time{}
		if out.Error == "apple_rate_limited" {
			out.Error = ""
		}
		out.State = "running"
		out.Owner = owner
		out.LeaseUntil = now.Add(2 * time.Minute)
		out.UpdatedAt = now
		if e := put(ctx, tx, "lease/axm", lease{out.ID, owner, out.LeaseUntil}); e != nil {
			return e
		}
		// Private lease fields are persisted separately, not exposed in job JSON.
		return put(ctx, tx, jobKey(out.Kind, out.ID), out)
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
		old, e := get[Job](ctx, tx, jobKey(j.Kind, j.ID))
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
		if a.Revision != j.Revision || !a.Enabled || (!j.AccountCreatedAt.IsZero() && !j.AccountCreatedAt.Equal(a.CreatedAt)) {
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
		return put(ctx, tx, jobKey(j.Kind, j.ID), j)
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
// Historical source evidence survives. Native enrollment is never modified.
func (r *Repository) DeleteAccount(ctx context.Context, id string, revision int64, now time.Time) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		a, e := get[Account](ctx, tx, "account/"+id)
		if e != nil {
			return e
		}
		if a.Revision != revision {
			return ErrConflict
		}
		if e := walkJobs(ctx, tx, func(entry Entry) error {
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
		if e := walk(ctx, tx, "resource/", func(entry Entry) error {
			var d ResourceRecord
			if e := json.Unmarshal(entry.Value, &d); e != nil {
				return e
			}
			if d.Source.AccountID == id && strings.HasPrefix(d.Source.Kind, "axm.") {
				d.Snapshot.Disconnected = true
				if err := putResource(ctx, tx, d); err != nil {
					return err
				}
				return invalidateLinks(ctx, tx, d.Source)
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
		return walkJobs(ctx, tx, func(entry Entry) error {
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

// jobKey isolates targeted work from older workers that only understand full sync.
func jobKey(kind, id string) string {
	if kind == "axm-refresh" {
		return "axm-refresh-job/" + id
	}
	return "job/" + id
}

// readJob preserves the existing public job-ID addressing across both queues.
func readJob(ctx context.Context, r Reader, id string) (Job, error) {
	job, err := get[Job](ctx, r, "job/"+id)
	if errors.Is(err, ErrNotFound) {
		return get[Job](ctx, r, "axm-refresh-job/"+id)
	}
	return job, err
}

// walkJobs enumerates both queues without exposing targeted work to old workers.
func walkJobs(ctx context.Context, r Reader, fn func(Entry) error) error {
	if err := walk(ctx, r, "job/", fn); err != nil {
		return err
	}
	return walk(ctx, r, "axm-refresh-job/", fn)
}
