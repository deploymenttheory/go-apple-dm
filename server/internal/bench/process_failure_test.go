package bench

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestBenchChild is a subprocess fixture that becomes ready, accepts seeding,
// then exits when the parent requests it. The supervisor must observe the exit.
func TestBenchChild(t *testing.T) {
	if os.Getenv("BENCH_CHILD") != "1" {
		return
	}
	srv := &http.Server{
		Addr:              os.Getenv("DM_LISTEN"),
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"Items":[]}`)
		}),
	}
	go func() { _ = srv.ListenAndServeTLS(os.Getenv("DM_TLS_CERT_FILE"), os.Getenv("DM_TLS_KEY_FILE")) }()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		b, err := os.ReadFile(os.Getenv("BENCH_EXIT_FILE"))
		if err == nil {
			code, err := strconv.Atoi(string(b))
			if err != nil {
				os.Exit(4)
			}
			os.Exit(code)
		}
	}
}

func TestSupervisorReportsUnexpectedProcessExit(t *testing.T) {
	for _, code := range []string{"0", "3"} {
		t.Run(code, func(t *testing.T) {
			w := testWorkspace(t, "live")
			exitFile := w.path("exit-code")
			w.Settings = map[string]string{"BENCH_CHILD": "1", "BENCH_EXIT_FILE": exitFile}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			// The test binary only runs its child fixture; no other tests recurse.
			script := w.path("child")
			writeFixture(
				t,
				script,
				[]byte("#!/bin/sh\nexec '"+executable+"' -test.run '^TestBenchChild$'\n"),
			)
			if err := os.Chmod(script, 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Up(ctx, w, script, io.Discard) }()
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case err := <-done:
					t.Fatalf("exited before readiness: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-tick.C:
					if e, err := Attach(w); err == nil {
						e.Client.CloseIdleConnections()
						writeFixture(t, exitFile, []byte(code))
						goto ready
					}
				}
			}
		ready:
			if err := <-done; err == nil {
				t.Fatal("unexpected child exit reported success")
			}
			if _, err := os.Stat(w.path("running.json")); !os.IsNotExist(err) {
				t.Fatal("stale supervisor descriptor retained")
			}
		})
	}
}

func TestStartupFailureLeavesNoSupervisor(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"lock directory", "invalid address", "descriptor directory", "cancelled", "split child exit"} {
		t.Run(name, func(t *testing.T) {
			w := testWorkspace(t, "live")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			binary := ""
			switch name {
			case "lock directory":
				if err := os.Mkdir(w.path("bench.lock"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "invalid address":
				w.Listen = "invalid"
			case "descriptor directory":
				if err := os.Mkdir(w.path("running.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "split child exit":
				w.Storage = "sqlite"
				w.Topology = "split"
				binary = filepath.Join(t.TempDir(), "exit")
				writeFixture(t, binary, []byte("#!/bin/sh\nexit 1\n"))
				if err := os.Chmod(binary, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := Up(ctx, w, binary, io.Discard); err == nil {
				t.Fatal("invalid startup succeeded")
			}
		})
	}
}
