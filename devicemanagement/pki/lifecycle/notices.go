package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Notice is persistent operator guidance for one expiry threshold. It contains
// only public metadata and is independent of delivery to a logging service.
type Notice struct {
	Identity   string    `json:"identity"`
	Revision   string    `json:"revision"`
	Severity   string    `json:"severity"`
	NextAction string    `json:"nextAction"`
	At         time.Time `json:"at"`
}

// RecordNotice returns true once per revision and threshold, across replicas.
func (m *Manager) RecordNotice(ctx context.Context, id string) (Notice, bool, error) {
	k, err := key(id)
	if err != nil {
		return Notice{}, false, err
	}
	var notice Notice
	created := false
	err = m.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := read(ctx, tx, id)
		if err != nil {
			return err
		}
		item := view(r, tx.Now())
		if item.Severity == "" {
			return nil
		}
		nk := "pki/lifecycle/notice/" + id + "/" + item.Active + "/" + item.Severity
		if _, err := tx.Get(ctx, nk); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		notice = Notice{Identity: id, Revision: item.Active, Severity: item.Severity, NextAction: item.NextAction, At: tx.Now()}
		data, err := json.Marshal(notice)
		if err != nil {
			return err
		}
		created = true
		return tx.Put(ctx, state.Record{Key: nk, Value: data})
	})
	return notice, created && err == nil, err
}
