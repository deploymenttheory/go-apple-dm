package webauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/state"
)

// SharedStore persists browser state across replicas. The backend commits the
// browser binding check and consumption together; mismatches leave state intact.
type SharedStore struct{ Backend state.Store }

func browserStateKey(key string) string { return "oidc/state/" + browserDigest(key) }

// Put implements StateStore.
func (s *SharedStore) Put(ctx context.Context, key string, st State) error {
	if key == "" || s.Backend == nil {
		return ErrStateKey
	}
	k := browserStateKey(key)
	return s.Backend.Update(ctx, []string{k}, func(tx state.Tx) error {
		if _, err := tx.Get(ctx, k); err == nil {
			return ErrStateExists
		} else if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		body, err := json.Marshal(st)
		if err != nil {
			return fmt.Errorf("webauth: encode state: %w", err)
		}
		return tx.Put(ctx, state.Record{Key: k, Value: body, ExpiresAt: st.ExpiresAt})
	})
}

// Take implements StateStore.
func (s *SharedStore) Take(ctx context.Context, key, browserHash string) (State, error) {
	var st State
	if key == "" || s.Backend == nil {
		return st, ErrStateKey
	}
	k := browserStateKey(key)
	err := s.Backend.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, k)
		if errors.Is(err, state.ErrNotFound) {
			return ErrStateNotFound
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(r.Value, &st); err != nil {
			return fmt.Errorf("webauth: decode state: %w", err)
		}
		if subtle.ConstantTimeCompare([]byte(st.BrowserHash), []byte(browserHash)) != 1 {
			return ErrBrowserBinding
		}
		return tx.Delete(ctx, k)
	})
	if err != nil {
		return State{}, err
	}
	return st, nil
}
