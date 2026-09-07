package accountdriven

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/state"
)

// AtomicTokenStore commits consumption, rotation and replacement together.
// Implementations must revalidate expiry and first use under the transaction lock.
type AtomicTokenStore interface {
	TokenStore
	Exchange(ctx context.Context, hash string, at time.Time, validate func(Record) error, replacements map[string]Record) error
}

// StateTokenStore implements TokenStore over an atomic memory or SQL backend.
type StateTokenStore struct{ Backend state.Store }

func tokenKey(hash string) string { return "account/token/" + hash }

func readToken(ctx context.Context, r state.Reader, hash string) (Record, error) {
	v, err := r.Get(ctx, tokenKey(hash))
	if errors.Is(err, state.ErrNotFound) {
		return Record{}, ErrTokenNotFound
	}
	if err != nil {
		return Record{}, err
	}
	var rec Record
	err = json.Unmarshal(v.Value, &rec)
	return rec, err
}
func putToken(ctx context.Context, tx state.Tx, hash string, rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: tokenKey(hash), Value: data, ExpiresAt: rec.ExpiresAt})
}

// Put implements TokenStore.
func (s *StateTokenStore) Put(ctx context.Context, hash string, rec Record) error {
	return s.Backend.Update(ctx, []string{tokenKey(hash)}, func(tx state.Tx) error { return putToken(ctx, tx, hash, rec) })
}

// Get implements TokenStore.
func (s *StateTokenStore) Get(ctx context.Context, hash string) (Record, error) {
	return readToken(ctx, s.Backend, hash)
}

// Delete implements TokenStore.
func (s *StateTokenStore) Delete(ctx context.Context, hash string) error {
	return s.Backend.Update(ctx, []string{tokenKey(hash)}, func(tx state.Tx) error { return tx.Delete(ctx, tokenKey(hash)) })
}

// MarkUsed implements TokenStore.
func (s *StateTokenStore) MarkUsed(ctx context.Context, hash string, at time.Time) error {
	return s.Exchange(ctx, hash, at, func(Record) error { return nil }, nil)
}

// Exchange implements AtomicTokenStore. An old access token linked to a refresh
// token is invalidated in the same transaction as refresh rotation.
func (s *StateTokenStore) Exchange(ctx context.Context, hash string, at time.Time, validate func(Record) error, replacements map[string]Record) error {
	prior, err := s.Get(ctx, hash)
	if err != nil {
		return err
	}
	keys := []string{tokenKey(hash)}
	if old := prior.Meta["access_hash"]; old != "" {
		keys = append(keys, tokenKey(old))
	}
	for h := range replacements {
		keys = append(keys, tokenKey(h))
	}
	return s.Backend.Update(ctx, keys, func(tx state.Tx) error {
		rec, err := readToken(ctx, tx, hash)
		if err != nil {
			return err
		}
		if !at.Before(rec.ExpiresAt) {
			return ErrTokenExpired
		}
		if !rec.UsedAt.IsZero() {
			return ErrTokenUsed
		}
		if err := validate(rec); err != nil {
			return err
		}
		rec.UsedAt = at
		if err := putToken(ctx, tx, hash, rec); err != nil {
			return err
		}
		if old := rec.Meta["access_hash"]; old != "" {
			if old != prior.Meta["access_hash"] {
				return ErrOAuth2Grant
			}
			if err := tx.Delete(ctx, tokenKey(old)); err != nil {
				return err
			}
		}
		for h, r := range replacements {
			if err := putToken(ctx, tx, h, r); err != nil {
				return err
			}
		}
		return nil
	})
}

// AssociationStore returns the association store on the same backend, when the
// token store provides transactional state. Custom token verifiers may instead
// supply an independent Associations instance to Config and CheckinHook.
func (t *Tokens) AssociationStore() *Associations {
	if t == nil {
		return nil
	}
	switch s := t.Store.(type) {
	case *StateTokenStore:
		return &Associations{Store: s.Backend}
	case *MemStore:
		return &Associations{Store: s.Backend}
	}
	return nil
}
