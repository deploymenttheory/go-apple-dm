package bench

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

// Native child fixtures avoid depending on a shell or executable script support.
func TestMain(m *testing.M) {
	switch os.Getenv("BENCH_CHILD") {
	case "1":
		runBenchChild()
	case "exit":
		os.Exit(3)
	case "wait":
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(m.Run())
}

// runBenchChild becomes ready, accepts seeding, then exits at the parent's request.
func runBenchChild() {
	srv := &http.Server{
		Addr:              os.Getenv("DM_LISTEN"),
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Items":[]}`)
		}),
	}
	go func() { _ = srv.ListenAndServeTLS(os.Getenv("DM_TLS_CERT_FILE"), os.Getenv("DM_TLS_KEY_FILE")) }()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		// #nosec G703 -- The test controls this fixture path within its private workspace.
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

// TestSupervisorReportsUnexpectedProcessExit checks that supervisor reports unexpected process
// exit.
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
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Up(ctx, w, executable, io.Discard) }()
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

// TestStartupFailureLeavesNoSupervisor checks startup failure leaves no supervisor.
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
				w.Settings = map[string]string{"DM_ROLE": "split"}
				var err error
				binary, err = os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				w.Settings = map[string]string{"BENCH_CHILD": "exit"}
			}
			if err := Up(ctx, w, binary, io.Discard); err == nil {
				t.Fatal("invalid startup succeeded")
			}
		})
	}
}
