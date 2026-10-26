package lab

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// appsReadbackContender uses the genuine SDK claim/save over the fixture's sealed
// store. Its only delivery operation reads the actual native inspector; the gate
// holds the resulting lease after that HTTP read has released its SQL transaction.
type appsReadbackContender struct {
	s             *Session
	base          string
	held, release chan struct{}
}

// Resolve rejects any unexpected pre-dispatch work in this observation-only probe.
func (*appsReadbackContender) Resolve(context.Context, managedcontent.Selection) ([]managedcontent.Target, error) {
	return nil, errors.New("observation probe attempted target resolution")
}

// Submit refuses to create installation or licensing evidence.
func (*appsReadbackContender) Submit(context.Context, managedcontent.Dispatch) (managedcontent.Observation, error) {
	return managedcontent.Observation{}, errors.New("observation probe attempted dispatch")
}

// Inspect retains exact authenticated provider evidence, without synthesizing facts.
func (g *appsReadbackContender) Inspect(ctx context.Context, d managedcontent.Dispatch) (managedcontent.Observation, error) {
	var native app.ManagedContentNativeObservation
	if err := appsRequest(ctx, g.s, "GET", g.base+"/native-observation", nil, &native); err != nil {
		return native.Observation, err
	}
	if native.OperationID != strings.TrimPrefix(g.base, "/managed-content/operations/") || !native.Observation.Installed || native.Observation.AppVersion != "2.4" || len(native.Observation.Raw) == 0 {
		return native.Observation, errors.New("observation probe lacks genuine installed-app evidence")
	}
	close(g.held)
	select {
	case <-g.release:
		return native.Observation, nil
	case <-ctx.Done():
		return native.Observation, ctx.Err()
	}
}

// TestAppsBooksDeclarativeProofWaitsForContendingWorker forces the actual proof
// callsite to encounter a genuine SDK worker lease. An unconditional POST /step
// receives 409; read-only observation must wait for the real claim to settle.
func TestAppsBooksDeclarativeProofWaitsForContendingWorker(t *testing.T) {
	t.Parallel()
	var busyRead atomic.Bool
	appsNativeInstallationRuntime(t, managedcontent.DDM, false, func(ctx context.Context, s *Session, f *appsFixture, base string) func(managedcontent.Operation) {
		// Let the actual supervisor first persist the signed native facts. Once
		// terminal, it stops claiming this operation, making the probe deterministic.
		if err := waitLive(ctx, func() (bool, error) {
			var current managedcontent.Operation
			if err := appsRequest(ctx, s, "GET", base, nil, &current); err != nil {
				return false, err
			}
			return current.Worker == "" && current.LeaseUntil.IsZero() && current.Observation != nil && current.Observation.Terminal && current.Observation.Installed && current.Observation.AppVersion == "2.4", nil
		}); err != nil {
			t.Fatal(err)
		}
		store := appsRuntimeSQL(t, s)
		licenses, err := appsbooks.NewWorkflow(store, appsbooks.Config{MDMID: f.config.MDMID, BaseURL: f.config.BaseURL, HTTPClient: f.config.HTTPClient, Clock: f.config.Clock})
		if err != nil {
			t.Fatal(err)
		}
		gate := &appsReadbackContender{s: s, base: base, held: make(chan struct{}), release: make(chan struct{})}
		contender, err := managedcontent.New(store, licenses, gate, gate)
		if err != nil {
			t.Fatal(err)
		}
		const worker = "lab-observation-contender"
		done := make(chan error, 1)
		go func() {
			_, err := contender.Step(ctx, strings.TrimPrefix(base, "/managed-content/operations/"), worker, 30*time.Second)
			done <- err
		}()
		var once sync.Once
		release := func() { once.Do(func() { close(gate.release) }) }
		t.Cleanup(func() {
			release()
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Error("genuine observation worker did not settle", err)
			}
		})
		select {
		case <-gate.held:
		case <-ctx.Done():
			t.Fatal("observation worker did not acquire its actual lease", ctx.Err())
		}
		var held managedcontent.Operation
		if err := appsRequest(ctx, s, "GET", base, nil, &held); err != nil || held.Worker != worker || !held.LeaseUntil.After(time.Now()) {
			t.Fatal("genuine SDK claim was not retained at the proof boundary", err)
		}
		t.Logf("genuine SDK lease held: worker=%s fence=%d; native installed version=2.4", held.Worker, held.Fence)
		return func(current managedcontent.Operation) {
			if current.Worker == worker && current.LeaseUntil.After(time.Now()) {
				busyRead.Store(true)
				release()
			}
		}
	})
	if !busyRead.Load() {
		t.Fatal("proof callsite did not observe the actual competing lease")
	}
}
