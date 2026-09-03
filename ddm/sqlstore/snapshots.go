package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
)

var snapshotCols = []string{"enrollment_id", "channel", "parent_id", "declarations_token", "token_changed_at", "refreshed_at"}

// PutSnapshot implements ddm.SnapshotStore. The snapshot row is upserted
// first so concurrent writers for one enrollment queue on its lock, then
// the items are replaced in the order given.
func (t *txStore) PutSnapshot(ctx context.Context, s *ddm.Snapshot) error {
	if s == nil {
		return fmt.Errorf("%w: nil snapshot", ddm.ErrInvalid)
	}
	if err := validID(s.ID); err != nil {
		return err
	}
	for _, it := range s.Items {
		if err := validName("snapshot item identifier", it.Identifier); err != nil {
			return err
		}
	}
	if _, err := t.exec(ctx, "upsert snapshot", t.upsert("ddm_snapshots", snapshotCols, snapshotCols[:1], nil),
		s.ID.ID, int(s.ID.Channel), s.ID.ParentID, s.DeclarationsToken, utc(s.TokenChangedAt), utc(s.RefreshedAt)); err != nil {
		return err
	}
	if _, err := t.exec(ctx, "delete snapshot items", "DELETE FROM ddm_snapshot_items WHERE enrollment_id = ?", s.ID.ID); err != nil {
		return err
	}
	for i, it := range s.Items {
		if _, err := t.exec(ctx, "insert snapshot item", "INSERT INTO ddm_snapshot_items (enrollment_id, kind, identifier, server_token, base_token, expanded, pos) VALUES (?, ?, ?, ?, ?, ?, ?)",
			s.ID.ID, string(it.Kind), it.Identifier, it.ServerToken, it.BaseToken, nullBytes(it.Expanded), i); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot implements ddm.SnapshotStore.
func (t *txStore) Snapshot(ctx context.Context, id mdm.EnrollmentID) (*ddm.Snapshot, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	snap := ddm.Snapshot{ID: id}
	var channel int
	found, err := t.row(ctx, "get snapshot", "SELECT channel, parent_id, declarations_token, token_changed_at, refreshed_at FROM ddm_snapshots WHERE enrollment_id = ?",
		[]any{id.ID}, &channel, &snap.ID.ParentID, &snap.DeclarationsToken, &snap.TokenChangedAt, &snap.RefreshedAt)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, notFound("snapshot for enrollment", id.ID)
	}
	snap.ID.Channel = mdm.Channel(channel) // #nosec G115 -- stored from a uint8
	snap.TokenChangedAt, snap.RefreshedAt = snap.TokenChangedAt.UTC(), snap.RefreshedAt.UTC()
	err = t.each(ctx, "get snapshot items", "SELECT kind, identifier, server_token, base_token, expanded FROM ddm_snapshot_items WHERE enrollment_id = ? ORDER BY pos",
		[]any{id.ID}, func(rows *sql.Rows) error {
			var it ddm.SnapshotItem
			if err := rows.Scan(&it.Kind, &it.Identifier, &it.ServerToken, &it.BaseToken, &it.Expanded); err != nil {
				return wrap("scan snapshot item", err)
			}
			snap.Items = append(snap.Items, it)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// PutSnapshot implements ddm.SnapshotStore.
func (s *Store) PutSnapshot(ctx context.Context, snap *ddm.Snapshot) error {
	return s.write(ctx, func(t *txStore) error { return t.PutSnapshot(ctx, snap) })
}

// Snapshot implements ddm.SnapshotStore.
func (s *Store) Snapshot(ctx context.Context, id mdm.EnrollmentID) (*ddm.Snapshot, error) {
	return s.view().Snapshot(ctx, id)
}
