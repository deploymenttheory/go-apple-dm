package apppush_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"

	"github.com/deploymenttheory/go-apple-dm/server/apppush"
)

// readFailure is a state store whose transactions cannot read: every Get fails with a
// backend error that is not a missing record.
type readFailure struct{ state.Store }

// Update runs fn against a transaction whose Get fails.
func (f readFailure) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return f.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(failingTx{tx}) })
}

type failingTx struct{ state.Tx }

// Get fails with a backend error.
func (failingTx) Get(context.Context, string) (state.Record, error) {
	return state.Record{}, errBackend
}

var errBackend = errors.New("backend unavailable")

// TestPutReportsABackendReadFailure checks that a store failure while reading the
// previous credential is returned as itself, not mistaken for a first version, and that
// a configured clock is the one validity is judged by.
func TestPutReportsABackendReadFailure(t *testing.T) {
	ctx := t.Context()
	ca, err := testpki.NewCA("app credentials")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.IssueApp("com.example.a", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	s := &apppush.Store{State: readFailure{state.NewMemory()}, Now: time.Now}
	if _, err := s.Put(ctx, "com.example.a", cert, key); !errors.Is(err, errBackend) {
		t.Fatalf("backend failure lost: %v", err)
	}
	// A clock in the past puts the certificate before its validity.
	s = &apppush.Store{State: state.NewMemory(), Now: func() time.Time { return time.Now().Add(-48 * time.Hour) }}
	if _, err := s.Put(ctx, "com.example.a", cert, key); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("configured clock ignored: %v", err)
	}
}
