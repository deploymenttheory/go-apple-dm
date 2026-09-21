package inventory

import (
	"context"
	"encoding/json"
	"time"
)

// PruneJobs removes terminal job metadata older than retention. Device evidence,
// disconnected sources, paused jobs and resumable checkpoints are never removed.
func (r *Repository) PruneJobs(ctx context.Context, now time.Time, retention time.Duration) (int, error) {
	if retention <= 0 {
		return 0, ErrInvalid
	}
	removed := 0
	err := r.Backend.Update(ctx, func(tx Tx) error {
		return walk(ctx, tx, "job/", func(entry Entry) error {
			var j Job
			if err := json.Unmarshal(entry.Value, &j); err != nil {
				return err
			}
			if j.FinishedAt != nil && j.FinishedAt.Before(now.Add(-retention)) {
				if e := walk(ctx, tx, "jobpage/"+j.ID+"/", func(page Entry) error { return tx.Delete(ctx, page.Key) }); e != nil {
					return e
				}
				if err := tx.Delete(ctx, entry.Key); err != nil {
					return err
				}
				removed++
			}
			return nil
		})
	})
	return removed, err
}
