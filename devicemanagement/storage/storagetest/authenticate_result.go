package storagetest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// RunAuthenticateResultSuite checks stores that opt into authoritative
// AuthenticateChange results without requiring the capability of legacy stores.
func RunAuthenticateResultSuite(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("ResetRetryAndFailure", func(t *testing.T) {
		s := factory(t)
		id := device(1)
		for _, c := range []struct {
			change storage.AuthenticateChange
			reset  bool
			fail   bool
		}{
			{storage.AuthenticateChange{Hash: "first"}, true, false},
			{storage.AuthenticateChange{Hash: "first"}, false, false},
			{storage.AuthenticateChange{ExpectedHash: "wrong", Hash: "second"}, false, true},
			{storage.AuthenticateChange{ExpectedHash: "first", Hash: "second"}, true, false},
		} {
			var result storage.AuthenticateResult
			c.change.At, c.change.Result = t0, &result
			c.change.Raw = []byte("retained authentication")
			err := s.AuthenticateEnrollment(t.Context(), id, c.change)
			if c.fail {
				if !errors.Is(err, storage.ErrConflict) || result.Known {
					t.Fatalf("failed authentication published an outcome: %+v %v", result, err)
				}
			} else if err != nil || !result.Known || result.Reset != c.reset {
				t.Fatalf("authentication outcome: %+v %v; reset=%v", result, err, c.reset)
			}
		}
	})
	t.Run("ConcurrentDuplicate", func(t *testing.T) {
		s := factory(t)
		start := make(chan struct{})
		results := make(chan storage.AuthenticateResult, 2)
		failures := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				<-start
				var result storage.AuthenticateResult
				failures <- s.AuthenticateEnrollment(t.Context(), device(1), storage.AuthenticateChange{Hash: "same", At: t0, Result: &result})
				results <- result
			})
		}
		close(start)
		wg.Wait()
		resets := 0
		for range 2 {
			if err := <-failures; err != nil {
				t.Fatal(err)
			}
			result := <-results
			if !result.Known {
				t.Fatal("missing atomic outcome")
			}
			if result.Reset {
				resets++
			}
		}
		if resets != 1 {
			t.Fatalf("duplicate requests reset %d times", resets)
		}
	})
	t.Run("FailingWrapperForwardsResult", func(t *testing.T) {
		boom := errors.New("injected authentication failure")
		s := &Failing{Store: factory(t), Fail: map[string]error{"AuthenticateEnrollment": boom}}
		var result storage.AuthenticateResult
		change := storage.AuthenticateChange{Hash: "identity", At: t0.Add(time.Minute), Result: &result}
		if err := s.AuthenticateEnrollment(t.Context(), device(1), change); !errors.Is(err, boom) || result.Known {
			t.Fatalf("wrapper override bypassed: %+v %v", result, err)
		}
		delete(s.Fail, "AuthenticateEnrollment")
		if err := s.AuthenticateEnrollment(t.Context(), device(1), change); err != nil || !result.Known || !result.Reset {
			t.Fatalf("forwarded result missing: %+v %v", result, err)
		}
	})
}
