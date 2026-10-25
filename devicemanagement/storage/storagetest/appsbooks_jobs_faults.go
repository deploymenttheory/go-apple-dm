package storagetest

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var errLicensingStorage = errors.New("fixture licensing storage failure")

type licensingFault struct {
	state.Store
	at    int
	calls int
}

// fail injects one deterministic persistence failure at the selected boundary.
func (s *licensingFault) fail() error {
	s.calls++
	if s.calls == s.at {
		return errLicensingStorage
	}
	return nil
}

// Get injects a read failure while retaining ordinary backend reads.
func (s *licensingFault) Get(ctx context.Context, key string) (state.Record, error) {
	if err := s.fail(); err != nil {
		return state.Record{}, err
	}
	return s.Store.Get(ctx, key)
}

// List injects enumeration failures without changing pagination or record ownership.
func (s *licensingFault) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, prefix, after, limit)
}

// Update injects transaction admission and callback persistence failures.
func (s *licensingFault) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	if err := s.fail(); err != nil {
		return err
	}
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(licensingFaultTx{Tx: tx, s: s}) })
}

type licensingFaultTx struct {
	state.Tx
	s *licensingFault
}

// Get injects a read failure while retaining ordinary backend reads.
func (t licensingFaultTx) Get(ctx context.Context, key string) (state.Record, error) {
	if err := t.s.fail(); err != nil {
		return state.Record{}, err
	}
	return t.Tx.Get(ctx, key)
}

// List injects enumeration failures without changing pagination or record ownership.
func (t licensingFaultTx) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if err := t.s.fail(); err != nil {
		return nil, err
	}
	return t.Tx.List(ctx, prefix, after, limit)
}

// Put fails a transactional write to verify all earlier writes roll back.
func (t licensingFaultTx) Put(ctx context.Context, r state.Record) error {
	if err := t.s.fail(); err != nil {
		return err
	}
	return t.Tx.Put(ctx, r)
}

// Delete fails lease release to verify the job and reservation remain atomic.
func (t licensingFaultTx) Delete(ctx context.Context, key string) error {
	if err := t.s.fail(); err != nil {
		return err
	}
	return t.Tx.Delete(ctx, key)
}

// runLicensingFailures verifies each actual persistence boundary. Single-commit
// operations roll back completely; network operations retain inspectable intent
// across failed final commits and never attempt a second POST.
func runLicensingFailures(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	for _, operation := range []string{"create", "submit", "receive", "receive orphan", "bind retained", "poll", "reconcile", "recover", "jobs", "load", "receipt"} {
		t.Run(operation, func(t *testing.T) {
			for point := 1; point < 60; point++ {
				a, _ := factory(t)
				now := &atomic.Int64{}
				now.Store(time.Now().UnixNano())
				s := &licensingFault{Store: &jobTimeStore{Store: a, now: now}}
				f, cfg, token := licensingFixture(t)
				w, err := appsbooks.NewWorkflow(s, cfg)
				failAppsBooksIf(t, err != nil, err)
				l, err := w.Register(t.Context(), "location", 0, token)
				failAppsBooksIf(t, err != nil, err)
				j := appsbooks.LicensingJob{}
				if operation != "create" {
					j, err = w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
					failAppsBooksIf(t, err != nil, err)
				}
				if operation == "receive" || operation == "poll" || operation == "receipt" {
					j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
					failAppsBooksIf(t, err != nil, err)
				}
				if operation == "bind retained" || operation == "reconcile" || operation == "recover" {
					f.mu.Lock()
					f.invalid = true
					f.mu.Unlock()
					j, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
					failAppsBooksIf(t, !errors.Is(err, appsbooks.ErrProtocol), err)
				}
				if operation == "bind retained" {
					_, err = deliverJobCallback(t, w, l.ID, jobCallback("n", "event", "SUCCESS", "s1"))
					failAppsBooksIf(t, err != nil, err)
				}
				if operation == "receipt" {
					_, err = deliverJobCallback(t, w, l.ID, jobCallback("n", "event", "SUCCESS", "s1"))
					failAppsBooksIf(t, err != nil, err)
				}
				now.Add(int64(5 * time.Minute))
				baseline, err := s.Store.List(t.Context(), "appsbooks/", "", 1000)
				failAppsBooksIf(t, err != nil, err)
				s.calls = 0
				s.at = point
				switch operation {
				case "create":
					_, err = w.CreateJob(t.Context(), l.ID, l.Revision, "job", jobRequest())
				case "submit":
					_, err = w.SubmitJob(t.Context(), l.ID, j.ID, "worker", time.Minute)
				case "receive", "receive orphan":
					_, err = deliverJobCallback(t, w, l.ID, jobCallback("n", "event", "SUCCESS", "s1"))
				case "bind retained":
					_, err = w.ReconcileJob(t.Context(), l.ID, j.ID, "event", "read back stopped request", false)
				case "poll":
					_, err = w.PollJob(t.Context(), l.ID, j.ID, "poll", time.Minute)
				case "reconcile":
					_, err = w.ReconcileJob(t.Context(), l.ID, j.ID, "", "operator stopped request", true)
				case "recover":
					_, err = w.RecoverLicensingToken(t.Context(), l.ID, l.Revision, j.ID, token)
				case "jobs":
					_, _, err = w.Jobs(t.Context(), l.ID, "", 100)
				case "load":
					_, err = w.LoadJob(t.Context(), l.ID, j.ID)
				case "receipt":
					_, err = w.LoadNotification(t.Context(), l.ID, "n")
				}
				if point > s.calls {
					failAppsBooksIf(t, err != nil, operation, point, err)
					break
				}
				failAppsBooksIf(t, !errors.Is(err, errLicensingStorage), operation, point, s.calls, err)
				if operation != "submit" && operation != "poll" {
					after, listErr := s.Store.List(t.Context(), "appsbooks/", "", 1000)
					failAppsBooksIf(t, listErr != nil || !reflect.DeepEqual(baseline, after), operation, point, "partial commit", listErr)
				}
				f.mu.Lock()
				posts := f.posts
				f.mu.Unlock()
				failAppsBooksIf(t, posts > 1, "POST replay", posts)
			}
		})
	}
}
