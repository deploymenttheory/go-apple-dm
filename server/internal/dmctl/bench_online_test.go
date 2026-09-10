package dmctl_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/bench"
)

func TestBenchCommandsUseMaintainedRuntime(t *testing.T) {
	env := noConfig(t)
	dir := t.TempDir()
	if err := bench.Init(dir, "simulated", "inmem", "all", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	w, err := bench.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bench.Up(ctx, w, "", io.Discard) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	var instance *bench.Environment
	for instance == nil {
		select {
		case <-deadline.C:
			t.Fatal("supervisor not ready")
		case <-tick.C:
			instance, _ = bench.Attach(w)
		}
	}
	defer instance.Client.CloseIdleConnections()
	call := func(args ...string) (string, error) {
		t.Helper()
		args = append(args, "-workspace", dir)
		out, _, err := run(t, env, append([]string{"bench"}, args...)...)
		return out, err
	}
	out, err := call("status")
	if err != nil || strings.Contains(out, instance.ControlToken) ||
		strings.Contains(out, "ControlToken") {
		t.Fatalf("status leaked token or failed: %s %v", out, err)
	}
	profile := filepath.Join(dir, "enrollment.mobileconfig")
	if _, err := call("profile", "-device-id", "test-device", "-file", profile); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(profile); err != nil || !strings.Contains(string(b), "com.apple.mdm") {
		t.Fatalf("profile: %v", err)
	}
	if _, err := call("profile", "-device-id", "test-device", "-file", profile); err == nil {
		t.Fatal("profile overwritten")
	}
	report := filepath.Join(dir, "report")
	out, err = call(
		"run",
		"-scenario",
		"E2E-024",
		"-report-dir",
		report,
		"-revision",
		"test-revision",
	)
	if err != nil {
		t.Fatal(err)
	}
	var result bench.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Status != "passed" {
		t.Fatalf("scenario: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(report, "results.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := call("run", "-scenario", "LIVE-001"); err == nil {
		t.Fatal("unsupported live scenario passed")
	}
	for _, args := range [][]string{{"replace"}, {"replace", "-device-id", "absent"}, {"unknown"}, {"profile"}, {"profile", "-file", filepath.Join(dir, "new.mobileconfig")}, {"run", "-scenario", "no-such-scenario"}, {"up", "-dmserver", "missing"}, {"list", "extra"}, {"list", "-invalid"}} {
		if _, err := call(args...); err == nil {
			t.Errorf("invalid command accepted: %v", args)
		}
	}
	if _, err := call("down"); err != nil {
		t.Fatal(err)
	}
}
