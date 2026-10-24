package inventory

import (
	"context"
	"encoding/json"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
)

// CollectionDocument retains a complete successful list document independently
// of its resources, including included resources and unknown envelope members.
// JSON is byte data so document persistence cannot alter original JSON lexemes.
type CollectionDocument struct {
	Stream              collection.Stream `json:"stream"`
	JSON                []byte            `json:"json"`
	ObservedAt          time.Time         `json:"observed_at"`
	EnumerationComplete bool              `json:"enumeration_complete"`
}

func collectionPrefix(stream collection.Stream) string {
	raw, _ := json.Marshal(stream)
	return digest(string(raw)) + "/"
}

// retainBatch shares the worker's resource/checkpoint transaction. Failed
// outcomes are independent records and never replace successful documents.
func retainBatch(ctx context.Context, tx Tx, batch collection.Batch) error {
	prefix := collectionPrefix(batch.Stream)
	raw, err := json.Marshal(batch.Outcome)
	if err != nil {
		return err
	}
	if err := tx.Put(ctx, "outcome/"+prefix+digest(string(raw)), raw); err != nil {
		return err
	}
	if len(batch.Document) == 0 || !batch.Outcome.Success {
		return nil
	}
	document := CollectionDocument{Stream: batch.Stream, JSON: append([]byte(nil), batch.Document...), ObservedAt: batch.Outcome.At, EnumerationComplete: batch.EnumerationComplete}
	return put(ctx, tx, "document/"+prefix+digest(string(batch.Document)+"\x00"+string(raw)), document)
}

// Documents returns successful original list documents for one selected stream.
func (r *Repository) Documents(ctx context.Context, stream collection.Stream) ([]CollectionDocument, error) {
	out := []CollectionDocument{}
	err := walk(ctx, r.Backend, "document/"+collectionPrefix(stream), func(entry Entry) error {
		var document CollectionDocument
		if err := json.Unmarshal(entry.Value, &document); err != nil {
			return err
		}
		out = append(out, document)
		return nil
	})
	return out, err
}

// Outcomes returns safe classifications separately from successful evidence.
func (r *Repository) Outcomes(ctx context.Context, stream collection.Stream) ([]collection.Outcome, error) {
	out := []collection.Outcome{}
	err := walk(ctx, r.Backend, "outcome/"+collectionPrefix(stream), func(entry Entry) error {
		var outcome collection.Outcome
		if err := json.Unmarshal(entry.Value, &outcome); err != nil {
			return err
		}
		out = append(out, outcome)
		return nil
	})
	return out, err
}
