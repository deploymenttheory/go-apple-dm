package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// EvidenceModelKey is the atomic migration marker. Backends reject writes to
// retired aggregate namespaces once this key exists, including old native workers.
const EvidenceModelKey = "model/source-evidence-v1"

// RetiredKey identifies the former aggregate records and their derived indexes.
func RetiredKey(key string) bool {
	for _, prefix := range []string{"device/", "source/", "alias/", "serial/", "serialconflict/", "field/", "indexes/"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// RecoveryNeed records legacy evidence that cannot be reconstructed. The legacy
// identity is only an operational refresh hint, never physical-device evidence.
type RecoveryNeed struct {
	LegacyID string `json:"legacy_id"`
	Reason   string `json:"reason"`
}

// Migrate atomically replaces legacy aggregates with their original observations.
// It never derives source values, serials, authentication or enrollment methods
// from normalized fields, alias indexes or enrollment identifiers. Missing originals
// and native provenance remain incomplete and refreshable. Accounts, credentials,
// schedules, terminal job history and unrelated domain stores are preserved.
// An interrupted migration rolls back completely and may be retried.
func (r *Repository) Migrate(ctx context.Context, now time.Time) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		if done, err := get[bool](ctx, tx, EvidenceModelKey); err == nil {
			if !done {
				return ErrConflict
			}
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}

		err := walk(ctx, tx, "device/", func(entry Entry) error {
			var legacy struct {
				ID      string                     `json:"id"`
				Sources map[string]json.RawMessage `json:"sources"`
			}
			if err := json.Unmarshal(entry.Value, &legacy); err != nil {
				return err
			}
			if len(legacy.Sources) == 0 {
				return put(ctx, tx, "recovery/"+digest(entry.Key), RecoveryNeed{legacy.ID, "missing original source observations"})
			}
			for _, envelope := range legacy.Sources {
				var o Observation
				if err := json.Unmarshal(envelope, &o); err != nil {
					return err
				}
				if o.Source.Kind == "" || o.Source.ResourceID == "" {
					if err := tx.Put(ctx, "orphan/"+digest(entry.Key)+"/"+digest(string(envelope)), envelope); err != nil {
						return err
					}
					if err := put(ctx, tx, "recovery/"+digest(entry.Key), RecoveryNeed{legacy.ID, "missing original source identity"}); err != nil {
						return err
					}
					continue
				}
				if err := tx.Put(ctx, "envelope/"+o.Source.Key()+"/"+digest(string(envelope)), envelope); err != nil {
					return err
				}
				var outcome struct {
					AttemptedAt time.Time `json:"attempted_at"`
					Error       string    `json:"error"`
				}
				if err := json.Unmarshal(envelope, &outcome); err != nil {
					return err
				}
				if outcome.Error != "" && !outcome.AttemptedAt.IsZero() {
					if err := attemptTx(ctx, tx, o.Source, outcome.AttemptedAt, outcome.Error); err != nil {
						return err
					}
				}
				// Retain source fields and their original per-field provenance.
				// Aggregate normalized fields and aliases are never consulted.
				o.Fields = nil
				o.FieldTimes = nil

				o.Native = nil
				o.Raw = migratedOriginal(o.Source, o.Raw)
				incomplete := !completeOriginal(o.Source, o.Raw) || o.ObservedAt.IsZero()
				var record ResourceRecord
				if incomplete {
					record = ResourceRecord{ID: o.Source.Key(), Source: o.Source, Snapshot: o, CreatedAt: o.ObservedAt, UpdatedAt: o.ObservedAt, Incomplete: true}
					old, err := get[ResourceRecord](ctx, tx, "resource/"+record.ID)
					if err != nil && !errors.Is(err, ErrNotFound) {
						return err
					}
					if old.ID != "" {
						continue
					}
					if err := putResource(ctx, tx, record); err != nil {
						return err
					}
				} else {
					var err error
					record, err = r.ObserveTx(ctx, tx, "", o)
					if err != nil {
						return err
					}
					if !strings.HasPrefix(o.Source.Kind, "axm.") {
						record.Incomplete = true
						if err := putResource(ctx, tx, record); err != nil {
							return err
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		// Changing the existing owner is a fence old compiled workers already check.
		// Keep the outbound lease deadline so a replacement waits for any old request.
		l, err := get[lease](ctx, tx, "lease/axm")
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			l.Owner = "migration-" + ID()
			if err := put(ctx, tx, "lease/axm", l); err != nil {
				return err
			}
		}
		if err := walk(ctx, tx, "job/", func(entry Entry) error {
			var j Job
			if err := json.Unmarshal(entry.Value, &j); err != nil {
				return err
			}
			if j.FinishedAt != nil || j.State == "success" || j.State == "partial" || j.State == "failed" || j.State == "cancelled" {
				return nil
			}
			if err := tx.Put(ctx, "jobmigration/"+digest(entry.Key)+"/"+digest(string(entry.Value)), entry.Value); err != nil {
				return err
			}
			// Old aggregate cursors and checkpoints address a different data model.
			// A new generation forces enumeration from the beginning without loss of
			// already retained evidence or invalid absence decisions.
			j.Generation = ID()
			j.Stage = "devices"
			j.Cursor = ""
			j.Checkpoint = nil
			j.Devices = 0
			j.Coverage = 0
			j.CoverageAttempts = 0
			j.Failed = 0
			j.Error = ""
			j.UpdatedAt = now
			if j.State == "running" {
				j.State = "queued"
			}
			return put(ctx, tx, entry.Key, j)
		}); err != nil {
			return err
		}
		for _, prefix := range []string{"device/", "source/", "alias/", "serial/", "serialconflict/", "field/", "indexes/", "jobpage/"} {
			if err := walk(ctx, tx, prefix, func(entry Entry) error { return tx.Delete(ctx, entry.Key) }); err != nil {
				return err
			}
		}
		return put(ctx, tx, EvidenceModelKey, true)
	})
}

// RecoveryNeeds lists incomplete legacy records without fabricating source values.
func (r *Repository) RecoveryNeeds(ctx context.Context) ([]RecoveryNeed, error) {
	out := []RecoveryNeed{}
	err := walk(ctx, r.Backend, "recovery/", func(entry Entry) error {
		var need RecoveryNeed
		if err := json.Unmarshal(entry.Value, &need); err != nil {
			return err
		}
		out = append(out, need)
		return nil
	})
	return out, err
}

// migratedOriginal unwraps only original source JSON retained by the former
// storage format. A synthesized assignment false/null is not a vendor response.
func migratedOriginal(ref source.Reference, raw json.RawMessage) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		if ref.Kind == "axm.assignment" {
			return nil
		}
		return raw
	}
	switch ref.Kind {
	case "axm.assignment":
		var identity struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &identity) == nil && identity.ID != "" && identity.Type == "mdmServers" {
			return raw
		}
		original := object["mdm_server_raw"]
		if json.Unmarshal(original, &identity) == nil && identity.ID != "" && identity.Type == "mdmServers" {
			return original
		}
		return nil
	case "ddm.status":
		if _, backfilled := object["backfilled_effective_values"]; backfilled {
			return nil
		}
		report, hasReport := object["report"]
		_, hasEffective := object["effective"]
		if hasReport && hasEffective {
			var reportObject map[string]json.RawMessage
			if json.Unmarshal(report, &reportObject) != nil || reportObject == nil {
				return nil
			}
			if _, backfilled := reportObject["backfilled_effective_values"]; backfilled {
				return nil
			}
			return report
		}
	}
	return raw
}
