package webhook

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestMaintenanceWaitsForDeferredObservation checks a completed protocol response
// remains admitted until its deferred webhook observation is persisted.
func TestMaintenanceWaitsForDeferredObservation(t *testing.T) {
	observing, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	s := testStore(t, Config{CommandType: func(ctx context.Context, _ mdm.EnrollmentID, _ string) (string, error) {
		close(observing)
		select {
		case <-release:
			return "DeviceInformation", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}})
	sub := subscribe(t, s, PayloadPolicy{})
	control, err := maintenance.Open(t.Context(), s.db, sqlite.Dialect, true)
	if err != nil {
		t.Fatal(err)
	}
	p, err := control.Register(t.Context(), "webhook-observer")
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorkers := context.WithCancel(t.Context())
	workers, started, stopped := make(chan error, 1), make(chan struct{}), make(chan struct{})
	go func() {
		workers <- p.Run(workerCtx, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		})
	}()
	t.Cleanup(func() {
		unblock()
		stopWorkers()
		if err := <-workers; !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not start")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mdm", func(w http.ResponseWriter, r *http.Request) {
		ObserveMDM(r.Context(), nil, &mdm.Response{ID: mdm.EnrollmentID{ID: "device-1", Channel: mdm.ChannelDevice}, CommandUUID: "command-1", Status: mdm.StatusAcknowledged}, true)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := p.Wrap(s.Observe(mux, mux))
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/mdm", nil))
		response <- w
	}()
	select {
	case <-observing:
	case <-time.After(3 * time.Second):
		t.Fatal("deferred observation did not start")
	}
	ticket := rand.Text()
	if err := control.Request(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("maintenance did not stop workers")
	}
	deadline, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := control.WaitDrained(deadline, ticket); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("acknowledged before deferred persistence completed", err)
	}
	if got := deliveries(t, s, sub.Subscription.ID); len(got) != 0 {
		t.Fatal("observation persisted before release", got)
	}
	unblock()
	if w := <-response; w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
	deadline, cancel = context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := control.WaitDrained(deadline, ticket); err != nil {
		t.Fatal(err)
	}
	if got := deliveries(t, s, sub.Subscription.ID); len(got) != 1 {
		t.Fatal("acknowledged without persisted observation", got)
	}
}
