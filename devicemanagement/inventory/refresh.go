package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// RefreshRequest selects source reads for one actual opaque device resource ID.
// It performs no assignment, correlation or absence inference.
type RefreshRequest struct {
	AccountID  string                 `json:"account_id"`
	ResourceID string                 `json:"resource_id"`
	Operations []collection.Operation `json:"operations"`
}

// EnqueueRefresh retains targeted work in its own transaction.
func (r *Repository) EnqueueRefresh(ctx context.Context, req RefreshRequest, now time.Time) (Job, error) {
	var j Job
	err := r.Backend.Update(ctx, func(tx Tx) error { var err error; j, err = r.EnqueueRefreshTx(ctx, tx, req, now); return err })
	return j, err
}

// EnqueueRefreshTx joins receipt retention to targeted work. The caller must use
// this repository's serialized backend transaction. Queued work coalesces; work
// arriving while a refresh runs creates/coalesces a successor generation so a
// completed in-flight read cannot swallow a later notification.
func (r *Repository) EnqueueRefreshTx(ctx context.Context, tx Tx, req RefreshRequest, now time.Time) (Job, error) {
	var out Job
	if req.ResourceID == "" || req.AccountID == "" || len(req.Operations) == 0 {
		return out, ErrInvalid
	}
	for _, op := range req.Operations {
		if op != collection.Detail && op != collection.Assignment && op != collection.Coverage {
			return out, ErrInvalid
		}
	}
	a, err := get[Account](ctx, tx, "account/"+req.AccountID)
	if err != nil {
		return out, err
	}
	if !a.Enabled {
		return out, ErrInvalid
	}
	err = walk(ctx, tx, "axm-refresh-job/", func(e Entry) error {
		var j Job
		if err := json.Unmarshal(e.Value, &j); err != nil {
			return err
		}
		if j.Kind == "axm-refresh" && (j.State == "queued" || j.State == "deferred") && j.AccountID == a.ID && j.Revision == a.Revision && j.AccountCreatedAt.Equal(a.CreatedAt) && j.ResourceID == req.ResourceID {
			out = j
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if out.ID == "" {
		out = Job{ID: ID(), Generation: ID(), AccountID: a.ID, Revision: a.Revision, AccountCreatedAt: a.CreatedAt, Kind: "axm-refresh", State: "queued", Stage: "refresh", ResourceID: req.ResourceID, CreatedAt: now, UpdatedAt: now, Force: true}
	}
	for _, op := range req.Operations {
		exists := false
		for _, old := range out.Operations {
			exists = exists || old == op
		}
		if !exists {
			out.Operations = append(out.Operations, op)
		}
	}
	return out, put(ctx, tx, jobKey(out.Kind, out.ID), out)
}

// refresh commits complete source reads under the existing renewable job fence.
// A failed stream preserves successful evidence and is recorded separately.
func (s *Syncer) refresh(ctx context.Context, c *axm.Client, a Account, j *Job) error {
	for _, op := range j.Operations {
		if err := s.Repository.commitJob(ctx, j, nil); err != nil {
			return err
		}
		resources, err := bounded(ctx, func(ctx context.Context) ([]collection.Resource, error) {
			return s.collectResources(ctx, c, a, j, op, j.ResourceID)
		})
		if err != nil {
			var local collectionCommitError
			if errors.As(err, &local) {
				return local.error
			}
			if axm.IsRateLimited(err) || axm.IsUnauthorized(err) || errors.Is(err, ErrStopped) || errors.Is(err, ErrLease) || ctx.Err() != nil {
				return err
			}
			j.Failed++
			if err := s.Repository.commitJob(ctx, j, func(tx Tx) error {
				return attemptTx(ctx, tx, source.Reference{Kind: "axm." + string(op), AccountID: a.ID, ResourceID: j.ResourceID}, time.Now().UTC(), errorCode(err))
			}); err != nil {
				return err
			}
			continue
		}
		kind, ttl := "axm."+string(op), a.DeviceTTL
		var raw json.RawMessage
		if op == collection.Coverage {
			ttl = a.CoverageTTL
			plans := make([]json.RawMessage, 0, len(resources))
			for _, resource := range resources {
				plans = append(plans, resource.Raw)
			}
			raw, err = json.Marshal(plans)
			j.Coverage += len(resources)
		} else if len(resources) == 1 {
			raw = resources[0].Raw
		} else {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		observation, err := ResourceObservation(source.Reference{Kind: kind, AccountID: a.ID, ResourceID: j.ResourceID}, raw, time.Now().UTC(), ttl)
		if err != nil {
			return err
		}
		observation.Generation = j.Generation
		if err := s.Repository.commitJob(ctx, j, func(tx Tx) error {
			if _, err := s.Repository.ObserveTx(ctx, tx, "", observation); err != nil {
				return err
			}
			if op == collection.Detail {
				var device axm.OrgDevice
				if err := json.Unmarshal(raw, &device); err != nil {
					return err
				}
				original := observation
				original.Source.Kind = "axm.device"
				_, err := s.Repository.ObserveTx(ctx, tx, device.Attributes.SerialNumber, original)
				return err
			}
			return nil
		}); err != nil {
			return err
		}
		if op == collection.Detail {
			j.Devices++
		}
	}
	j.Stage = "complete"
	return nil
}
