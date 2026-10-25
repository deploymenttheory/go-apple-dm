package appsbooks

import (
	"context"
	"encoding/json/jsontext"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Refresh replaces full asset evidence and appends incremental assignment/user
// source batches. maxPages bounds EACH traversal. It never filters assets, so the
// omitted unlimited parameter includes Apple's sentinel representation as well as
// any separate unlimitedAssets returned by the service. Checkpoints are published
// together only after all three traversals succeed. Errors preserve the previous
// successful evidence/checkpoints; a changed local revision fences the result.
func (w *Workflow) Refresh(ctx context.Context, id string, revision uint64, maxPages int) (Location, error) {
	if maxPages <= 0 {
		return Location{}, ErrInput
	}
	l, c, err := w.session(ctx, id, revision, false)
	if err != nil {
		return Location{}, err
	}
	_, prior, err := w.LoadEvidence(ctx, id)
	if err != nil {
		return Location{}, err
	}
	assets, err := collectPages(ctx, maxPages, "", func(ctx context.Context, index int) (jsontext.Value, Pagination, error) {
		p, err := c.Assets(ctx, AssetsQuery{PageIndex: index})
		return p.Raw, p.Pagination, err
	})
	if err != nil {
		return Location{}, err
	}
	assignments, err := collectPages(ctx, maxPages, lastVersion(prior.Assignments), func(ctx context.Context, index int) (jsontext.Value, Pagination, error) {
		p, err := c.Assignments(ctx, AssignmentsQuery{PageIndex: index, SinceVersionID: lastVersion(prior.Assignments)})
		return p.Raw, p.Pagination, err
	})
	if err != nil {
		return Location{}, err
	}
	users, err := collectPages(ctx, maxPages, lastVersion(prior.Users), func(ctx context.Context, index int) (jsontext.Value, Pagination, error) {
		p, err := c.Users(ctx, UsersQuery{PageIndex: index, SinceVersionID: lastVersion(prior.Users)})
		return p.Raw, p.Pagination, err
	})
	if err != nil {
		return Location{}, err
	}
	return w.commit(ctx, l, func(tx state.Tx, _ *Location, e *LocationEvidence) error {
		assets.ObservedAt, assignments.ObservedAt, users.ObservedAt = tx.Now(), tx.Now(), tx.Now()
		e.Assets = assets
		e.Assignments = append(e.Assignments, assignments)
		e.Users = append(e.Users, users)
		return nil
	})
}

// lastVersion selects the checkpoint from the last completely published batch.
func lastVersion(batches []SourceBatch) string {
	if len(batches) == 0 {
		return ""
	}
	return batches[len(batches)-1].VersionID
}

// collectPages follows empty pages and changing version IDs, retaining the first
// version and refusing unbounded traversal or invalid pagination.
func collectPages(ctx context.Context, maxPages int, since string, fetch func(context.Context, int) (jsontext.Value, Pagination, error)) (SourceBatch, error) {
	batch := SourceBatch{SinceVersionID: since}
	index := 0
	for n := 0; n < maxPages; n++ {
		if err := ctx.Err(); err != nil {
			return SourceBatch{}, err
		}
		raw, p, err := fetch(ctx, index)
		if err != nil {
			return SourceBatch{}, err
		}
		if err := checkPage(p, index); err != nil {
			return SourceBatch{}, err
		}
		if n == 0 {
			if p.VersionID == "" {
				return SourceBatch{}, ErrProtocol
			}
			batch.VersionID = p.VersionID
		}
		batch.Pages = append(batch.Pages, append([]byte(nil), raw...))
		if p.NextPageIndex == nil {
			return batch, nil
		}
		index = *p.NextPageIndex
	}
	return SourceBatch{}, ErrLimit
}
