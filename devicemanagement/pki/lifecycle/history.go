package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type auditContextKey struct{}
type auditContext struct{ Actor, Action string }

// WithAudit attaches application-authenticated attribution to certificate changes.
// It does not authorize operations; callers enforce access before invoking them.
func WithAudit(ctx context.Context, actor, action string) context.Context {
	return context.WithValue(ctx, auditContextKey{}, auditContext{Actor: actor, Action: action})
}

// Activity records public metadata in the same transaction as a state change.
type Activity struct {
	Sequence int64     `json:"sequence"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	At       time.Time `json:"at"`
	Identity Identity  `json:"identity"`
}

func writeActivity(ctx context.Context, tx state.Tx, r record) error {
	attribution, _ := ctx.Value(auditContextKey{}).(auditContext)
	if attribution.Actor == "" {
		attribution.Actor = "application"
	}
	if attribution.Action == "" {
		attribution.Action = "certificate-state-change"
	}
	entry := Activity{Sequence: r.Generation, Actor: attribution.Actor, Action: attribution.Action, At: tx.Now(), Identity: view(r, tx.Now())}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: fmt.Sprintf("pki/lifecycle/history/%s/%020d", r.ID, r.Generation), Value: data})
}

// History returns a bounded page ordered by sequence. The next cursor is opaque.
func (m *Manager) History(ctx context.Context, id, after string, limit int) ([]Activity, string, error) {
	if _, err := key(id); err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > 1000 {
		return nil, "", ErrInvalid
	}
	prefix := "pki/lifecycle/history/" + id + "/"
	cursor := ""
	if after != "" {
		cursor = prefix + after
	}
	rows, err := m.Store.List(ctx, prefix, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := []Activity{}
	next := ""
	for _, row := range rows {
		var entry Activity
		if err := json.Unmarshal(row.Value, &entry); err != nil {
			return nil, "", err
		}
		out = append(out, entry)
		next = fmt.Sprintf("%020d", entry.Sequence)
	}
	if len(rows) < limit {
		next = ""
	}
	return out, next, nil
}
