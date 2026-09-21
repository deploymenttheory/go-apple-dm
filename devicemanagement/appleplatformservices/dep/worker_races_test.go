package dep_test

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
)

type gatedTransport struct {
	base    http.RoundTripper
	path    string
	used    atomic.Bool
	entered chan struct{}
	release chan struct{}
}

// RoundTrip blocks the selected response until the gate opens or the request is canceled.
func (g *gatedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := g.base.RoundTrip(r)
	if err == nil && r.URL.Path == g.path && g.used.CompareAndSwap(false, true) {
		close(g.entered)
		select {
		case <-g.release:
		case <-r.Context().Done():
		}
	}
	return resp, err
}

// gateClient creates a client with a gated transport and registers cleanup to release the gate.
func gateClient(t *testing.T, f *fixture, path string) (*dep.Client, <-chan struct{}, func()) {
	t.Helper()
	g := &gatedTransport{base: f.srv.Client().Transport, path: path, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(g.release) }) }
	t.Cleanup(release)
	c, err := dep.NewClient(dep.ClientConfig{Store: f.store, BaseURL: f.srv.URL(), Clock: f.clk, HTTPClient: &http.Client{Transport: g, Timeout: 10 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return c, g.entered, release
}

// awaitRequest waits for the gated request to arrive, failing the test after five seconds.
func awaitRequest(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach gate")
	}
}

// TestSyncRejectsAnOlderConcurrentSnapshot checks that sync rejects an older concurrent snapshot.
func TestSyncRejectsAnOlderConcurrentSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.srv.AddDevices(device("A"))
	if err := f.store.PutDevices(ctx, acct, []dep.Device{device("A")}, t0); err != nil {
		t.Fatal(err)
	}
	c, entered, release := gateClient(t, f, dep.PathFetchDevices)
	cfg := dep.SyncerConfig{Client: c, Store: f.store, Account: acct, Clock: f.clk}
	first, err := dep.NewSyncer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := first.RunOnce(ctx); done <- err }()
	awaitRequest(t, entered)
	f.srv.DeleteDevice("A")
	second, err := dep.NewSyncer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; !errors.Is(err, dep.ErrConflict) {
		t.Fatalf("stale response accepted: %v", err)
	}
	d, err := f.store.GetDevice(ctx, acct, "A")
	if err != nil || !d.Deleted {
		t.Fatalf("stale page resurrected device: %+v %v", d, err)
	}
}

// TestAssignmentClaimFencesAnExpiredWorker checks assignment claim fences an expired worker.
func TestAssignmentClaimFencesAnExpiredWorker(t *testing.T) {
	f := newAssignFixture(t)
	ctx := t.Context()
	f.srv.AddDevices(device("A"))
	f.sync(t)
	c, entered, release := gateClient(t, f.fixture, dep.PathProfileDevs)
	cfg := dep.AssignerConfig{Client: c, Store: f.store, Account: acct, Clock: f.clk}
	first, err := dep.NewAssigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := first.RunOnce(ctx); done <- err }()
	awaitRequest(t, entered)
	second, err := dep.NewAssigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.RunOnce(ctx); !errors.Is(err, dep.ErrBackoff) {
		t.Fatalf("overlapping assignment: %v", err)
	}
	if n := f.srv.Count(http.MethodPost, dep.PathProfileDevs); n != 1 {
		t.Fatalf("overlapping requests: %d", n)
	}
	f.clock.Advance(3 * time.Minute)
	if _, err := second.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; !errors.Is(err, dep.ErrConflict) {
		t.Fatalf("expired worker wrote its result: %v", err)
	}
	state, err := f.store.AssignmentState(ctx, acct)
	if err != nil || state.Owner != "" {
		t.Fatalf("claim state: %+v %v", state, err)
	}
}

// TestAssignmentReadbackDoesNotResurrectRemovedDevice checks that assignment readback does not
// resurrect removed device.
func TestAssignmentReadbackDoesNotResurrectRemovedDevice(t *testing.T) {
	f := newAssignFixture(t)
	ctx := t.Context()
	f.srv.AddDevices(device("A"))
	f.sync(t)
	c, entered, release := gateClient(t, f.fixture, dep.PathDeviceDetails)
	worker := newAssigner(t, f, func(cfg *dep.AssignerConfig) { cfg.Client = c; cfg.ReadBack = true })
	done := make(chan error, 1)
	go func() { _, err := worker.RunOnce(ctx); done <- err }()
	awaitRequest(t, entered)
	f.srv.DeleteDevice("A")
	f.sync(t)
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	d, err := f.store.GetDevice(ctx, acct, "A")
	if err != nil || !d.Deleted {
		t.Fatalf("readback resurrected tombstone: %+v %v", d, err)
	}
}
