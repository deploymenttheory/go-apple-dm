package lab

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// appsCancellationClock interrupts a selected documented protocol wait without
// changing the reference SQL clock or inventing any HTTP completion.
type appsCancellationClock struct {
	*appsTestClock
	cancel    context.CancelFunc
	interrupt time.Duration
	hit       atomic.Bool
}

// After interrupts only the named wait and delegates all other fixture scheduling.
func (c *appsCancellationClock) After(d time.Duration) <-chan time.Time {
	if d == c.interrupt && c.hit.CompareAndSwap(false, true) {
		c.cancel()
		return make(chan time.Time)
	}
	return c.appsTestClock.After(d)
}

// TestAppsBooksAcceptedSubmissionCancellationRetainsFence distinguishes a lost
// wait from failed submission and never polls early or guesses a cleanup inverse.
func TestAppsBooksAcceptedSubmissionCancellationRetainsFence(t *testing.T) {
	for _, name := range []string{"notification fallback", "pending status wait"} {
		t.Run(name, func(t *testing.T) {
			s, f := appsFixtureSession(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			inner, ok := f.config.Clock.(*appsTestClock)
			if !ok {
				t.Fatal("fixture scheduler unavailable")
			}
			clock := &appsCancellationClock{appsTestClock: inner, cancel: cancel, interrupt: appsNotificationFallback}
			if name == "pending status wait" {
				clock.interrupt = appsPendingInterval
				f.rewrite = func(r *http.Request, m map[string]any) {
					if r.URL.Path == "/status" {
						m["eventStatus"] = "PENDING"
					}
				}
			}
			f.config.Clock = clock
			if err := liveAppsBooksLicensing(ctx, s); !errors.Is(err, context.Canceled) || !clock.hit.Load() {
				t.Fatal("cancelled accepted event was not retained as incomplete", err)
			}
			statuses := f.requestCount("GET /status")
			if f.postCount() != 1 || name == "notification fallback" && statuses != 0 || name == "pending status wait" && statuses != 1 {
				t.Fatal("cancelled wait polled early, replayed or guessed an inverse")
			}
			if _, err := os.Stat(s.Env.Workspace.path("appsbooks", "pending-licensing.json")); err != nil {
				t.Fatal("accepted outcome lost its fence", err)
			}
		})
	}
}

// TestAppsBooksReferenceCompletionCancellationRetainsFence cancels after the
// actual authorized due poll, before the lab has checked its callback/target proof.
func TestAppsBooksReferenceCompletionCancellationRetainsFence(t *testing.T) {
	s, f, c := appsRuntime(t)
	appsInstallDueFixture(t, s, f, c)
	ctx, cancel := context.WithCancel(appsLongContext(t))
	defer cancel()
	transport := s.Env.Client.Transport
	var hit atomic.Bool
	s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if err == nil && strings.HasSuffix(r.URL.Path, "/poll") && response.StatusCode == http.StatusOK && hit.CompareAndSwap(false, true) {
			raw, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				return nil, err
			}
			response.Body = io.NopCloser(bytes.NewReader(raw))
			response.ContentLength = int64(len(raw))
			cancel()
		}
		return response, err
	})
	if err := liveAppsBooksLicensing(ctx, s); !errors.Is(err, context.Canceled) || !hit.Load() {
		t.Fatal("unverified reference completion passed after cancellation", err)
	}
	if f.postCount() != 2 {
		t.Fatal("cancelled completion guessed an inverse or replayed")
	}
	if _, err := os.Stat(s.Env.Workspace.path("appsbooks", "pending-licensing.json")); err != nil {
		t.Fatal("unverified completion lost its fence", err)
	}
}

// TestAppsBooksRevisionFenceCoversEveryLicensingEntry rejects a changed native
// location generation before direct mutation, reference preparation or submission.
func TestAppsBooksRevisionFenceCoversEveryLicensingEntry(t *testing.T) {
	for _, name := range []string{"direct", "prepare", "reference", "final"} {
		t.Run(name, func(t *testing.T) {
			s, f := appsFixtureSession(t)
			a, err := appsOpen(t.Context(), s)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.location.Revision++
			f.mu.Unlock()
			switch name {
			case "direct":
				err = a.sdkJob(t.Context(), s, "forbidden", true)
			case "prepare":
				err = a.referencePrepare(t.Context(), s, "forbidden")
			case "reference":
				err = a.referenceJob(t.Context(), s, "forbidden")
			case "final":
				err = a.finalReference(t.Context(), s, "forbidden")
			}
			if err == nil || f.postCount() != 0 {
				t.Fatal("changed location generation authorized work", err)
			}
		})
	}
}
