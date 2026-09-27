package applications

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

const (
	catalogueLock = "applications/catalogue-lock"
	recordPrefix  = "applications/records/"
	contentPrefix = "applications/content/"
)

// read decodes private metadata and normalizes missing records.
func read[T any](ctx context.Context, r state.Reader, key string) (T, error) {
	var v T
	row, err := r.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(row.Value, &v)
	return v, err
}

// put writes metadata within the caller's transaction.
func put(ctx context.Context, tx state.Tx, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: key, Value: b})
}

// Create records metadata without creating content or contacting a distribution backend.
func (m *Manager) Create(ctx context.Context, meta Metadata) (Record, error) {
	if err := ValidateMetadata(meta); err != nil {
		return Record{}, err
	}
	r := Record{ID: rand.Text(), Revision: rand.Text(), Metadata: meta, CloudTransferStatus: "MISSING"}
	err := m.cfg.State.Update(ctx, []string{catalogueLock, recordPrefix + r.ID}, func(tx state.Tx) error {
		if err := validateParent(ctx, tx, r.ID, meta.ParentPackageID); err != nil {
			return err
		}
		r.CreatedAt = tx.Now()
		r.UpdatedAt = tx.Now()
		if err := appendHistory(ctx, tx, &r, "created", ""); err != nil {
			return err
		}
		return put(ctx, tx, recordPrefix+r.ID, r)
	})
	return r, err
}

// Get returns package metadata, including a pending or failed deletion's state.
func (m *Manager) Get(ctx context.Context, id string) (Record, error) {
	if !ValidID(id) {
		return Record{}, ErrInvalid
	}
	return read[Record](ctx, m.cfg.State, recordPrefix+id)
}

// List pages catalogue entries in opaque ID order.
func (m *Manager) List(ctx context.Context, p paging.Page) (paging.Result[Record], error) {
	var out paging.Result[Record]
	if p.Cursor != "" && !ValidID(p.Cursor) {
		return out, ErrInvalid
	}
	rows, err := m.cfg.State.List(ctx, recordPrefix, recordPrefix+p.Cursor, p.Size()+1)
	if err != nil {
		return out, err
	}
	out.Items = []Record{}
	for _, row := range rows[:min(len(rows), p.Size())] {
		var r Record
		if err = json.Unmarshal(row.Value, &r); err != nil {
			return out, err
		}
		out.Items = append(out.Items, r)
	}
	if len(rows) > p.Size() {
		out.NextCursor = strings.TrimPrefix(rows[p.Size()-1].Key, recordPrefix)
	}
	return out, nil
}

// Update replaces metadata only if expected matches the current catalogue revision.
// Existing content retains the metadata used when its manifest was created.
func (m *Manager) Update(ctx context.Context, id, expected string, meta Metadata) (Record, error) {
	if !ValidID(id) || expected == "" {
		return Record{}, ErrInvalid
	}
	if err := ValidateMetadata(meta); err != nil {
		return Record{}, err
	}
	var r Record
	err := m.cfg.State.Update(ctx, []string{catalogueLock, recordPrefix + id}, func(tx state.Tx) error {
		var err error
		r, err = read[Record](ctx, tx, recordPrefix+id)
		if err != nil {
			return err
		}
		if r.Revision != expected || r.Deleting {
			return ErrConflict
		}
		if err = validateParent(ctx, tx, id, meta.ParentPackageID); err != nil {
			return err
		}
		if r.Content != nil {
			if err = meta.Digests.Match(r.Content.Digests); err != nil {
				return err
			}
		}
		r.Metadata = meta
		r.Revision = rand.Text()
		r.UpdatedAt = tx.Now()
		if err = appendHistory(ctx, tx, &r, "updated", ""); err != nil {
			return err
		}
		return put(ctx, tx, recordPrefix+id, r)
	})
	return r, err
}

// Revision retrieves an immutable package content revision.
func (m *Manager) Revision(ctx context.Context, id, revision string) (Content, error) {
	if !ValidID(id) || !ValidID(revision) {
		return Content{}, ErrInvalid
	}
	r, err := m.Get(ctx, id)
	if err != nil {
		return Content{}, err
	}
	if r.Deleting {
		return Content{}, ErrConflict
	}
	return read[Content](ctx, m.cfg.State, contentPrefix+id+"/"+revision)
}

// Delete marks the package unavailable before deleting its private blobs and metadata.
// A failed backend deletion leaves a retryable tombstone with the same revision.
func (m *Manager) Delete(ctx context.Context, id, expected string) error {
	if !ValidID(id) || expected == "" {
		return ErrInvalid
	}
	key := recordPrefix + id
	err := m.cfg.State.Update(ctx, []string{catalogueLock, key}, func(tx state.Tx) error {
		r, err := read[Record](ctx, tx, key)
		if err != nil {
			return err
		}
		if r.Revision != expected {
			return ErrConflict
		}
		if r.Deleting {
			return nil
		}
		if err = checkNoChildren(ctx, tx, id); err != nil {
			return err
		}
		r.Deleting = true
		r.CloudTransferStatus = "DELETING"
		r.UpdatedAt = tx.Now()
		if err = appendHistory(ctx, tx, &r, "deletion-started", ""); err != nil {
			return err
		}
		return put(ctx, tx, key, r)
	})
	if err != nil {
		return err
	}
	prefix := contentPrefix + id + "/"
	for {
		rows, err := m.cfg.State.List(ctx, prefix, "", 100)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			var c Content
			if err = json.Unmarshal(row.Value, &c); err != nil {
				return err
			}
			backend := m.cfg.Backends[c.Backend]
			if backend == nil {
				return fmt.Errorf("%w: backend unavailable", ErrInvalid)
			}
			if err = backend.Delete(ctx, c.Key); err != nil {
				return err
			}
			if err = m.cfg.State.Update(ctx, []string{catalogueLock, key}, func(tx state.Tx) error { return tx.Delete(ctx, row.Key) }); err != nil {
				return err
			}
		}
	}
	return m.cfg.State.Update(ctx, []string{catalogueLock, key}, func(tx state.Tx) error {
		r, err := read[Record](ctx, tx, key)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !r.Deleting || r.Revision != expected {
			return ErrConflict
		}
		if err = appendHistory(ctx, tx, &r, "deleted", ""); err != nil {
			return err
		}
		return tx.Delete(ctx, key)
	})
}

// newRevision generates an opaque optimistic concurrency token.
func newRevision() string { return rand.Text() }

// validateParent rejects missing parents, deletion races and cycles. All catalogue
// writers take catalogueLock so the relationship check and write are atomic.
func validateParent(ctx context.Context, tx state.Tx, id string, parent *string) error {
	current := value(parent)
	seen := map[string]bool{id: true}
	for current != "" {
		if seen[current] {
			return fmt.Errorf("%w: parent package cycle", ErrInvalid)
		}
		if len(seen) > 1000 {
			return fmt.Errorf("%w: parent chain exceeds 1000 packages", ErrInvalid)
		}
		seen[current] = true
		r, err := read[Record](ctx, tx, recordPrefix+current)
		if err != nil {
			return fmt.Errorf("parent package: %w", err)
		}
		if r.Deleting {
			return ErrConflict
		}
		current = value(r.Metadata.ParentPackageID)
	}
	return nil
}

// checkNoChildren prevents deletion from leaving dangling parent references.
func checkNoChildren(ctx context.Context, tx state.Tx, id string) error {
	after := ""
	for {
		rows, err := tx.List(ctx, recordPrefix, after, 1000)
		if err != nil {
			return err
		}
		for _, row := range rows {
			var r Record
			if err = json.Unmarshal(row.Value, &r); err != nil {
				return err
			}
			if value(r.Metadata.ParentPackageID) == id {
				return fmt.Errorf("%w: package has children", ErrConflict)
			}
		}
		if len(rows) < 1000 {
			return nil
		}
		after = rows[len(rows)-1].Key
	}
}
