package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
)

// TestCollectionDocumentsAndOutcomes retains complete list envelopes independently
// from resources, including successful pages preceding a failed continuation.
func TestCollectionDocumentsAndOutcomes(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	stream := collection.Stream{AccountID: "a", Generation: "generation", Operation: collection.Devices}
	original := []byte("{\n \"data\": [], \"included\": [{\"future\":9007199254740993}], \"unknown\": [null,false,0,[]], \"label\": \"<>&\"\n}")
	batches := []collection.Batch{
		{Stream: stream, Document: original, Outcome: collection.Outcome{At: at, Success: true}},
		{Stream: stream, Outcome: collection.Outcome{At: at.Add(time.Hour), Failure: "permission", HTTPStatus: 403, Err: errors.New("sensitive host/body")}},
	}
	for _, batch := range batches {
		if err := r.Backend.Update(t.Context(), func(tx Tx) error { return retainBatch(t.Context(), tx, batch) }); err != nil {
			t.Fatal(err)
		}
	}
	documents, err := r.Documents(t.Context(), stream)
	if err != nil || len(documents) != 1 || string(documents[0].JSON) != string(original) || documents[0].EnumerationComplete {
		t.Fatal(documents, err)
	}
	outcomes, err := r.Outcomes(t.Context(), stream)
	if err != nil || len(outcomes) != 2 {
		t.Fatal(outcomes, err)
	}
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			t.Fatal("unsafe original error persisted")
		}
	}
	// Replaying an acknowledged batch remains harmless and a different account
	// cannot read its source documents through an identically named generation.
	if err := r.Backend.Update(t.Context(), func(tx Tx) error { return retainBatch(t.Context(), tx, batches[0]) }); err != nil {
		t.Fatal(err)
	}
	documents, err = r.Documents(t.Context(), stream)
	if err != nil || len(documents) != 1 {
		t.Fatal(documents, err)
	}
	stream.AccountID = "other"
	documents, err = r.Documents(t.Context(), stream)
	if err != nil || len(documents) != 0 {
		t.Fatal("cross-account document", documents, err)
	}
}

// TestCollectionEvidenceFailures checks every storage boundary without allowing
// a source document or safe outcome to commit outside its checkpoint transaction.
func TestCollectionEvidenceFailures(t *testing.T) {
	r := repository(t)
	stream := collection.Stream{AccountID: "a", Generation: "g", Operation: collection.Devices}
	batch := collection.Batch{Stream: stream, Document: json.RawMessage(`{"data":[]}`), Outcome: collection.Outcome{At: time.Now().UTC(), Success: true}, EnumerationComplete: true}
	if err := r.Backend.Update(t.Context(), func(tx Tx) error { return retainBatch(t.Context(), tx, batch) }); err != nil {
		t.Fatal(err)
	}
	seed := cloneMemory(testMemory(t, r))
	operations := []func(*Repository) error{
		func(r *Repository) error {
			return r.Backend.Update(t.Context(), func(tx Tx) error { return retainBatch(t.Context(), tx, batch) })
		},
		func(r *Repository) error { _, err := r.Documents(t.Context(), stream); return err },
		func(r *Repository) error { _, err := r.Outcomes(t.Context(), stream); return err },
	}
	for i, op := range operations {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			baseline := &faultBackend{Memory: cloneMemory(seed)}
			if err := op(&Repository{Backend: baseline}); err != nil {
				t.Fatal(err)
			}
			for n := 1; n <= baseline.calls; n++ {
				backend := &faultBackend{Memory: cloneMemory(seed), failAt: n}
				if err := op(&Repository{Backend: backend}); !errors.Is(err, errStorageFailure) {
					t.Fatal(n, err)
				}
				if !reflect.DeepEqual(seed.data, backend.data) {
					t.Fatal("partial collection evidence survived")
				}
			}
		})
	}
	for _, prefix := range []string{"document/", "outcome/"} {
		testMemory(t, r).data[prefix+collectionPrefix(stream)+"bad"] = json.RawMessage(`42`)
	}
	if _, err := r.Documents(t.Context(), stream); err == nil {
		t.Fatal("corrupt document metadata")
	}
	if _, err := r.Outcomes(t.Context(), stream); err == nil {
		t.Fatal("corrupt outcome metadata")
	}
	batch.Outcome.At = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := r.Backend.Update(t.Context(), func(tx Tx) error { return retainBatch(t.Context(), tx, batch) }); err == nil {
		t.Fatal("invalid outcome timestamp")
	}
}
