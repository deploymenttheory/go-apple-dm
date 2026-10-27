package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// operationListStore serves only the worker's operation listing; every other
// state method is absent because the worker stops before reaching it.
type operationListStore struct {
	state.Store
	list func(context.Context) error
}

// List reports the configured listing outcome.
func (s operationListStore) List(ctx context.Context, _, _ string, _ int) ([]state.Record, error) {
	return nil, s.list(ctx)
}

// operationListWorker returns an app whose managed content worker lists
// operations through list.
func operationListWorker(t *testing.T, list func(context.Context) error) *App {
	t.Helper()
	workflow, err := managedcontent.New(
		operationListStore{list: list},
		struct{ managedcontent.Licensing }{},
		struct{ managedcontent.Resolver }{},
		struct{ managedcontent.Delivery }{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &App{ManagedContent: workflow}
}

// workerResult waits for the worker to return.
func workerResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("managed content worker did not return")
		return nil
	}
}

// TestManagedContentWorkerStopsCleanlyMidTick checks that cancelling the worker
// while it lists operations is a clean stop rather than a worker failure.
func TestManagedContentWorkerStopsCleanlyMidTick(t *testing.T) {
	listing := make(chan struct{})
	a := operationListWorker(t, func(ctx context.Context) error {
		close(listing)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.runManagedContent(ctx) }()
	select {
	case <-listing:
	case <-time.After(10 * time.Second):
		t.Fatal("managed content worker did not list operations")
	}
	cancel()
	if err := workerResult(t, done); err != nil {
		t.Fatalf("cancelled worker returned %v", err)
	}
}

// TestManagedContentWorkerReturnsListingFailure checks that a listing failure
// while the worker is still running ends the worker with that failure.
func TestManagedContentWorkerReturnsListingFailure(t *testing.T) {
	boom := errors.New("boom")
	a := operationListWorker(t, func(context.Context) error { return boom })
	done := make(chan error, 1)
	go func() { done <- a.runManagedContent(t.Context()) }()
	if err := workerResult(t, done); !errors.Is(err, boom) {
		t.Fatalf("worker returned %v, want %v", err, boom)
	}
}
