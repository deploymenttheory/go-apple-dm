//go:build acceptance || e2e

package acceptance

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Exercise the operator entry point as well as the server adapter. Keeping this
// in acceptance makes workspace supervision part of the maintained CI bench.
func TestBenchCLI(t *testing.T) {
	cli, server := os.Getenv("BENCH_DMCTL"), os.Getenv("BENCH_DMSERVER")
	if cli == "" || server == "" {
		t.Skip("built CLI lifecycle is selected by make test-acceptance")
	}
	dir := t.TempDir()
	run := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, append([]string{"bench"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bench %s: %v: %s", args[0], err, out)
		}
		return out
	}
	run("init", "-workspace", dir, "-listen", "127.0.0.1:0")
	original, err := os.ReadFile(filepath.Join(dir, "mdm", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	for attempt := range 2 {
		func() {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(
				ctx,
				cli,
				"bench",
				"up",
				"-workspace",
				dir,
				"-dmserver",
				server,
			)
			cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
			cmd.WaitDelay = 15 * time.Second
			var log bytes.Buffer
			cmd.Stdout, cmd.Stderr = &log, &log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			finished := false
			defer func() {
				cancel()
				if !finished {
					<-done
				}
			}()
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			ready := false
			for !ready {
				select {
				case err := <-done:
					finished = true
					t.Fatalf("bench exited before readiness: %v: %s", err, &log)
				case <-deadline.C:
					t.Fatal("bench did not become ready")
				case <-tick.C:
					status := exec.CommandContext(ctx, cli, "bench", "status", "-workspace", dir)
					ready = status.Run() == nil
				}
			}
			if attempt == 0 {
				run("run", "-workspace", dir, "-scenario", "APP-003")
				run("run", "-workspace", dir, "-scenario", "E2E-006")
				run(
					"profile",
					"-workspace",
					dir,
					"-device-id",
					"cli-profile-device",
					"-file",
					filepath.Join(dir, "profile.mobileconfig"),
				)
			}
			run("down", "-workspace", dir)
			select {
			case err := <-done:
				finished = true
				if err != nil {
					t.Fatalf("bench shutdown: %v: %s", err, &log)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("bench shutdown did not finish")
			}
		}()
		if _, err := os.Stat(filepath.Join(dir, "running.json")); !os.IsNotExist(err) {
			t.Fatal("shutdown retained the running descriptor")
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, "mdm", "ca.pem"))
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("restart changed the workspace identity")
	}
}
