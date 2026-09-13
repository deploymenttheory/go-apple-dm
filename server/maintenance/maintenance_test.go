package maintenance

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func fixture(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open(
		"sqlite",
		sqlite.DSN(filepath.Join(t.TempDir(), "maintenance.sqlite"), sqlite.Options{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := Open(t.Context(), db, sqlite.Dialect, true)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFenceDrainsHTTPAndWorkers(t *testing.T) {
	s := fixture(t)
	p, err := s.Register(t.Context(), "replica-one")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	started, stopping, releaseWorker := make(
		chan struct{},
		2,
	), make(
		chan struct{},
		2,
	), make(
		chan struct{},
	)
	go func() {
		done <- p.Run(ctx, func(ctx context.Context) error {
			started <- struct{}{}
			<-ctx.Done()
			stopping <- struct{}{}
			<-releaseWorker
			return ctx.Err()
		})
	}()
	<-started
	entered, releaseHTTP, response := make(
		chan struct{},
	), make(
		chan struct{},
	), make(
		chan *httptest.ResponseRecorder,
		1,
	)
	h := p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-releaseHTTP
		w.WriteHeader(http.StatusNoContent)
	}))
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mutate", nil))
		response <- w
	}()
	<-entered
	ticket := rand.Text()
	if err := s.Request(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	<-stopping
	if st, err := s.Status(t.Context()); err != nil || st.Ready() {
		t.Fatal(st, err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/mutate", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal(w.Code)
	}
	close(releaseWorker)
	if st, err := s.Status(t.Context()); err != nil || st.Ready() {
		t.Fatal("active request was not drained", st, err)
	}
	close(releaseHTTP)
	if (<-response).Code != http.StatusNoContent {
		t.Fatal("in-flight request did not finish")
	}
	deadline, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	if err := s.WaitDrained(deadline, ticket); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(t.Context(), "new-replica"); !errors.Is(err, ErrFenced) {
		t.Fatal(err)
	}
	if err := s.Resume(t.Context(), rand.Text()); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
	if err := s.Resume(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-deadline.Done():
		t.Fatal("workers did not resume")
	}
	served := httptest.NewRecorder()
	p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).
		ServeHTTP(served, httptest.NewRequest("GET", "/", nil))
	if served.Code != 204 {
		t.Fatal(served.Code)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCrashedMemberRequiresExplicitRemoval(t *testing.T) {
	s := fixture(t)
	p, err := s.Register(t.Context(), "stopped-process")
	if err != nil {
		t.Fatal(err)
	}
	ticket := rand.Text()
	if err := s.Request(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	// A second control process observes the same fence and member.
	other, err := Open(t.Context(), s.db, s.d, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := other.WaitDrained(ctx, ticket); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := other.Request(t.Context(), rand.Text()); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
	if err := other.Request(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	if err := other.Forget(t.Context(), ticket, p.ID(), false); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := other.Forget(t.Context(), rand.Text(), p.ID(), true); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
	if err := other.Forget(t.Context(), ticket, p.ID(), true); err != nil {
		t.Fatal(err)
	}
	if err := other.WaitDrained(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	if err := p.store.acknowledge(t.Context(), p.ID(), ticket); !errors.Is(err, ErrParticipant) {
		t.Fatal(err)
	}
}

func TestMaintenanceFailureIsClosed(t *testing.T) {
	s := fixture(t)
	p, err := s.Register(t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	p.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler admitted with unavailable control store") })).
		ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	if err := p.Run(
		t.Context(),
		func(context.Context) error { t.Error("worker started"); return nil },
	); err == nil {
		t.Fatal("missing control-store failure")
	}
	if err := p.Close(t.Context()); err == nil {
		t.Fatal("missing close failure")
	}
}
