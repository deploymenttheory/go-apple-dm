package bench

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceSupervisorRestart(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, "simulated", "sqlite", "all", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	w, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(dir, "mdm", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Up(ctx, w, "", io.Discard) }()
		func() {
			defer cancel()
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			var e *Environment
			for e == nil {
				select {
				case err := <-done:
					t.Fatalf("supervisor exited: %v", err)
				case <-deadline.C:
					t.Fatal("supervisor did not become ready")
				case <-tick.C:
					e, _ = Attach(w)
				}
			}
			defer e.Client.CloseIdleConnections()
			if _, err = e.Control(ctx, "GET", "/status", nil); err != nil {
				t.Fatal(err)
			}
			if _, err = e.Control(ctx, "POST", "/stop", nil); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("supervisor did not drain")
			}
		}()
		if _, err = os.Stat(filepath.Join(dir, "running.json")); !os.IsNotExist(err) {
			t.Fatal("stale process descriptor")
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, "mdm", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(after) {
		t.Fatal("restart changed workspace identity")
	}
}
