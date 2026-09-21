package runtime

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestSuperviseRetainsUndrainedApplication checks shutdown deadlines preserve the
// SQL participant and shared database while a handler or worker remains active.
func TestSuperviseRetainsUndrainedApplication(t *testing.T) {
	for _, blocked := range []string{"main HTTP", "HTTP-01", "worker"} {
		t.Run(blocked, func(t *testing.T) {
			db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "shutdown.sqlite"), sqlite.Options{}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			control, err := maintenance.Open(t.Context(), db, sqlite.Dialect, true)
			if err != nil {
				t.Fatal(err)
			}
			participant, err := control.Register(t.Context(), "runtime")
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			workerCtx, stopWorkers := context.WithCancel(t.Context())
			workers := make(chan error, 1)
			started, workerExited := make(chan struct{}), make(chan struct{})
			go func() {
				err := participant.Run(workerCtx, func(ctx context.Context) error {
					close(started)
					<-ctx.Done()
					if blocked == "worker" {
						<-release
					}
					return ctx.Err()
				})
				if errors.Is(err, context.Canceled) {
					err = nil
				}
				workers <- err
				close(workerExited)
			}()
			entered := make(chan struct{})
			main := httptest.NewServer(participant.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if blocked == "main HTTP" {
					close(entered)
					<-release
				}
				w.WriteHeader(http.StatusNoContent)
			})))
			challenge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if blocked == "HTTP-01" {
					close(entered)
					<-release
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(func() {
				unblock()
				stopWorkers()
				main.Close()
				challenge.Close()
				select {
				case <-workerExited:
					if err := participant.Close(context.Background()); err != nil {
						t.Error(err)
					}
				case <-time.After(3 * time.Second):
					t.Error("released worker did not finish")
				}
			})
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("workers did not start")
			}
			if blocked != "worker" {
				target := main
				if blocked == "HTTP-01" {
					target = challenge
				}
				request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				go func() {
					response, err := target.Client().Do(request)
					if err == nil {
						_ = response.Body.Close()
					}
				}()
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("request did not start")
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			cleanupCalls := 0
			done := make(chan error, 1)
			go func() {
				done <- supervise(ctx, []*http.Server{main.Config, challenge.Config}, make(chan error), workers, stopWorkers, func() error {
					cleanupCalls++
					return errors.Join(participant.Close(context.Background()), db.Close())
				}, 30*time.Millisecond)
			}()
			select {
			case err := <-done:
				want := error(context.DeadlineExceeded)
				if blocked == "worker" {
					want = errWorkersStuck
				}
				if !errors.Is(err, want) {
					t.Fatal("missing shutdown failure", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown entered an unbounded application cleanup")
			}
			if cleanupCalls != 0 {
				t.Fatal("closed an undrained application")
			}
			status, err := control.Status(t.Context())
			if err != nil || len(status.Members) != 1 || status.Members[0].ID != participant.ID() {
				t.Fatal("lost the undrained participant or shared database", status, err)
			}
		})
	}
}

// TestSuperviseDrainsBothListeners checks cleanup and worker cancellation wait
// for active requests on both the main and auxiliary HTTP listeners.
func TestSuperviseDrainsBothListeners(t *testing.T) {
	entered := make(chan struct{}, 2)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var unblocks []func()
	var servers []*http.Server
	for _, release := range releases {
		unblock := sync.OnceFunc(func() { close(release) })
		unblocks = append(unblocks, unblock)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			entered <- struct{}{}
			<-release
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(func() { unblock(); srv.Close() })
		servers = append(servers, srv.Config)
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			response, err := srv.Client().Do(request)
			if err == nil {
				_ = response.Body.Close()
			}
		}()
	}
	for range servers {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("request did not start")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	workers, done := make(chan error, 1), make(chan error, 1)
	stopped := make(chan struct{})
	cleanupCalls := 0
	go func() {
		done <- supervise(ctx, servers, make(chan error), workers, func() { close(stopped); workers <- nil }, func() error { cleanupCalls++; return nil }, time.Second)
	}()
	unblocks[0]()
	select {
	case <-stopped:
		t.Fatal("stopped workers before the auxiliary listener drained")
	case <-time.After(20 * time.Millisecond):
	}
	unblocks[1]()
	if err := <-done; err != nil || cleanupCalls != 1 {
		t.Fatal("application was not cleaned up exactly once", err, cleanupCalls)
	}
}

// TestSuperviseReturnsCleanupFailures checks cleanup failures remain observable
// after a normal stop and alongside the original runtime failure.
func TestSuperviseReturnsCleanupFailures(t *testing.T) {
	cleanupFailure := errors.New("cleanup failed")
	serveFailure := errors.New("listener failed")
	for _, first := range []error{nil, serveFailure} {
		ctx, cancel := context.WithCancel(t.Context())
		serving, workers := make(chan error, 1), make(chan error, 1)
		if first == nil {
			cancel()
		} else {
			serving <- first
		}
		cleanupCalls := 0
		err := supervise(ctx, nil, serving, workers, func() { workers <- nil }, func() error { cleanupCalls++; return cleanupFailure }, time.Second)
		cancel()
		if !errors.Is(err, cleanupFailure) || first != nil && !errors.Is(err, first) || cleanupCalls != 1 {
			t.Fatal("cleanup lost a failure", err, cleanupCalls)
		}
	}
}

// TestServeStartupFailureCleansApplication checks listener startup failures still
// unregister the application and report any failure of that cleanup.
func TestServeStartupFailureCleansApplication(t *testing.T) {
	for _, rejectCleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "unregister", true: "cleanup failure"}[rejectCleanup], func(t *testing.T) {
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			dsn := filepath.Join(t.TempDir(), "startup.sqlite")
			db, err := sql.Open("sqlite", sqlite.DSN(dsn, sqlite.Options{}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			control, err := maintenance.Open(t.Context(), db, sqlite.Dialect, true)
			if err != nil {
				t.Fatal(err)
			}
			if rejectCleanup {
				if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER refuse_unregister BEFORE DELETE ON maintenance_participants BEGIN SELECT RAISE(ABORT, 'injected unregister failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err = Serve(t.Context(), app.Config{Storage: "sqlite", DSN: dsn, Listen: listener.Addr().String(), StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}})
			var listenError *net.OpError
			if !errors.As(err, &listenError) || listenError.Op != "listen" {
				t.Fatal("missing startup listener failure", err)
			}
			status, statusErr := control.Status(t.Context())
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if rejectCleanup {
				if !strings.Contains(err.Error(), "injected unregister failure") || len(status.Members) != 1 {
					t.Fatal("cleanup failure was suppressed or participant lost", err, status)
				}
			} else if len(status.Members) != 0 {
				t.Fatal("startup failure left a participant registered", status)
			}
		})
	}
}
