package applications

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

const historyPrefix = "applications/history/"

// HistoryEntry records a committed change. Sequence is monotonic per package;
// metadata snapshots preserve the exact preferences at each change. Failed writes
// do not create successful history entries. Deletion retains this audit history.
type HistoryEntry struct {
	Sequence        uint64    `json:"sequence"`
	PackageID       string    `json:"packageId"`
	Revision        string    `json:"revision"`
	Action          string    `json:"action"`
	Note            string    `json:"note,omitempty"`
	Metadata        Metadata  `json:"metadata"`
	ContentRevision string    `json:"contentRevision,omitempty"`
	Time            time.Time `json:"time"`
}

// appendHistory increments the package sequence and writes its audit snapshot atomically.
func appendHistory(ctx context.Context, tx state.Tx, r *Record, action, note string) error {
	r.HistorySequence++
	e := HistoryEntry{Sequence: r.HistorySequence, PackageID: r.ID, Revision: r.Revision, Action: action, Note: note, Metadata: r.Metadata, Time: tx.Now()}
	if r.Content != nil {
		e.ContentRevision = r.Content.Revision
	}
	return put(ctx, tx, fmt.Sprintf("%s%s/%020d", historyPrefix, r.ID, e.Sequence), e)
}

// History pages changes in sequence order, including after package deletion.
func (m *Manager) History(ctx context.Context, id string, p paging.Page) (paging.Result[HistoryEntry], error) {
	out := paging.Result[HistoryEntry]{Items: []HistoryEntry{}}
	if !ValidID(id) {
		return out, ErrInvalid
	}
	prefix := historyPrefix + id + "/"
	after := ""
	if p.Cursor != "" {
		n, err := strconv.ParseUint(p.Cursor, 10, 64)
		if err != nil || n == 0 {
			return out, ErrInvalid
		}
		after = fmt.Sprintf("%s%020d", prefix, n)
	}
	rows, err := m.cfg.State.List(ctx, prefix, after, p.Size()+1)
	if err != nil {
		return out, err
	}
	for _, row := range rows[:min(len(rows), p.Size())] {
		var e HistoryEntry
		if err = json.Unmarshal(row.Value, &e); err != nil {
			return out, err
		}
		out.Items = append(out.Items, e)
	}
	if len(rows) > p.Size() {
		out.NextCursor = strconv.FormatUint(out.Items[len(out.Items)-1].Sequence, 10)
	}
	return out, nil
}

// AddHistoryNote appends an operator note under optimistic concurrency control.
// The transport authenticates the operator and should keep its own actor audit.
func (m *Manager) AddHistoryNote(ctx context.Context, id, expected, note string) (Record, error) {
	if !ValidID(id) || expected == "" || strings.TrimSpace(note) == "" || !validText(note, 16384, true) {
		return Record{}, ErrInvalid
	}
	var out Record
	err := m.cfg.State.Update(ctx, []string{catalogueLock, recordPrefix + id}, func(tx state.Tx) error {
		r, err := read[Record](ctx, tx, recordPrefix+id)
		if err != nil {
			return err
		}
		if r.Revision != expected || r.Deleting {
			return ErrConflict
		}
		r.Revision = newRevision()
		r.UpdatedAt = tx.Now()
		if err = appendHistory(ctx, tx, &r, "note-added", note); err != nil {
			return err
		}
		if err = put(ctx, tx, recordPrefix+id, r); err != nil {
			return err
		}
		out = r
		return nil
	})
	return out, err
}
