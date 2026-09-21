package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type certificateState struct {
	state.Store
	capture *Store
}

// ObserveCertificates joins SQL-backed managed certificate transitions and
// capture in one transaction. The supplied store must use this Store's SQL pool.
// The concrete state.Tx reaches the callback unchanged, preserving publication
// adapters; private material is never extracted from state records.
func (s *Store) ObserveCertificates(repository state.Store) state.Store {
	return &certificateState{Store: repository, capture: s}
}

type certificateSummary struct {
	ID         string               `json:"id"`
	Kind       lifecycle.Kind       `json:"kind"`
	Active     string               `json:"Active"`
	Pending    string               `json:"Pending"`
	Generation int64                `json:"Generation"`
	Revisions  []lifecycle.Revision `json:"Revisions"`
}

// readCertificate loads the public workflow view used to describe a certificate transition.
func readCertificate(ctx context.Context, tx state.Tx, key string) (certificateSummary, error) {
	record, err := tx.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return certificateSummary{}, nil
	}
	if err != nil {
		return certificateSummary{}, err
	}
	var out certificateSummary
	err = json.Unmarshal(record.Value, &out)
	return out, err
}

// Update observes certificate-state transitions inside the underlying transaction and
// captures their webhook outcomes.
func (s *certificateState) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.capture.unit.Run(ctx, func(ctx context.Context) error {
		var events []Event
		err := s.Store.Update(ctx, keys, func(tx state.Tx) error {
			before := map[string]certificateSummary{}
			for _, key := range keys {
				if strings.HasPrefix(key, "pki/lifecycle/identity/") {
					value, err := readCertificate(ctx, tx, key)
					if err != nil {
						return err
					}
					before[key] = value
				}
			}
			if err := fn(tx); err != nil {
				return err
			}
			for key, old := range before {
				current, err := readCertificate(ctx, tx, key)
				if err != nil {
					return err
				}
				if reflect.DeepEqual(old, current) {
					continue
				}
				op, version := "updated", current.Pending
				if old.ID == "" {
					op = "prepared"
				}
				if current.Active != old.Active {
					op, version = "activated", current.Active
				}
				if current.ID == "" {
					op = "deleted"
				}
				id := strings.TrimPrefix(key, "pki/lifecycle/identity/")
				events = append(events, Event{Type: "server.certificate.lifecycle", OccurredAt: tx.Now(), CorrelationID: CorrelationID(ctx), Subject: &Subject{Kind: "certificate", ID: id}, Data: map[string]any{"operation": op, "certificate_id": id, "version": version, "outcome": "succeeded", "generation": current.Generation, "kind": current.Kind}})
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, e := range events {
			if err := s.capture.Capture(ctx, e); err != nil {
				return err
			}
		}
		return nil
	})
}
