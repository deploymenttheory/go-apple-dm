package inventory

import (
	"context"
	"encoding/json"
)

// Entry is one versioned inventory document. Keys are bounded to 512 ASCII bytes.
type Entry struct {
	Key   string
	Value json.RawMessage
}

// Reader provides ordered, bounded reads. Scan returns keys strictly after after.
type Reader interface {
	Get(context.Context, string) (json.RawMessage, error)
	Scan(context.Context, string, string, int) ([]Entry, error)
}

// Tx is a serialized transaction. Values must be copied by the implementation.
type Tx interface {
	Reader
	Put(context.Context, string, json.RawMessage) error
	Delete(context.Context, string) error
}

// Backend supplies atomic multi-document updates. SQL implementations must join
// the application's existing transaction so protocol results and inventory agree.
type Backend interface {
	Reader
	Update(context.Context, func(Tx) error) error
}

// put encodes a domain document into the caller's transaction.
func put(ctx context.Context, tx Tx, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return tx.Put(ctx, key, b)
}

// get decodes one domain document.
func get[T any](ctx context.Context, r Reader, key string) (T, error) {
	var value T
	b, err := r.Get(ctx, key)
	if err != nil {
		return value, err
	}
	err = json.Unmarshal(b, &value)
	return value, err
}

// walk scans without retaining an entire inventory in memory.
func walk(ctx context.Context, r Reader, prefix string, fn func(Entry) error) error {
	after := ""
	for {
		rows, err := r.Scan(ctx, prefix, after, 128)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := fn(row); err != nil {
				return err
			}
			after = row.Key
		}
		if len(rows) < 128 {
			return nil
		}
	}
}
