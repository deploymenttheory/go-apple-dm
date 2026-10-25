//go:build acceptance || e2e

package acceptance

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

const (
	failureOutputLimit     = 64 << 10
	failureOutputTruncated = "[earlier runtime output truncated]\n"
)

// failureOutput retains only the latest fixture runtime diagnostics. Child stdout
// and stderr can write concurrently; snapshots never expose the mutable buffer.
type failureOutput struct {
	mu        sync.Mutex
	tail      []byte
	truncated bool
}

// Write accepts all bytes while retaining at most the bounded diagnostic tail.
func (o *failureOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	if len(p) >= failureOutputLimit {
		o.truncated = o.truncated || len(o.tail)+len(p) > failureOutputLimit
		o.tail = append(o.tail[:0], p[len(p)-failureOutputLimit:]...)
		return n, nil
	}
	if excess := len(o.tail) + len(p) - failureOutputLimit; excess > 0 {
		copy(o.tail, o.tail[excess:])
		o.tail = o.tail[:len(o.tail)-excess]
		o.truncated = true
	}
	o.tail = append(o.tail, p...)
	return n, nil
}

// snapshot copies the current diagnostic tail and identifies discarded history.
func (o *failureOutput) snapshot() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.truncated {
		return failureOutputTruncated + string(o.tail)
	}
	return string(o.tail)
}

// TestFailureOutput retains final failure diagnostics within a bounded concurrent sink.
func TestFailureOutput(t *testing.T) {
	t.Parallel()
	t.Run("large write", func(t *testing.T) {
		var output failureOutput
		data := append(bytes.Repeat([]byte("prior output\n"), failureOutputLimit), []byte("fatal shutdown cause\n")...)
		n, err := output.Write(data)
		if err != nil || n != len(data) {
			t.Fatal(n, err)
		}
		want := failureOutputTruncated + string(data[len(data)-failureOutputLimit:])
		if got := output.snapshot(); got != want {
			t.Fatal("did not retain the exact final bounded tail")
		}
	})
	t.Run("rolling tail and immutable snapshot", func(t *testing.T) {
		var output failureOutput
		_, _ = output.Write([]byte("startup diagnostic\n"))
		before := output.snapshot()
		for range 32 {
			_, _ = output.Write(bytes.Repeat([]byte("x"), 4096))
		}
		_, _ = output.Write([]byte("fatal after admission closed\n"))
		got := output.snapshot()
		if before != "startup diagnostic\n" || len(got) != len(failureOutputTruncated)+failureOutputLimit ||
			!strings.HasPrefix(got, failureOutputTruncated) || !strings.HasSuffix(got, "fatal after admission closed\n") {
			t.Fatal("rolling writes changed prior snapshots or lost the final failure")
		}
	})
	t.Run("stdout stderr and snapshots", func(t *testing.T) {
		var output failureOutput
		var group sync.WaitGroup
		for range 8 {
			group.Go(func() {
				for range 256 {
					_, _ = output.Write(bytes.Repeat([]byte("concurrent fixture output\n"), 8))
					if len(output.snapshot()) > failureOutputLimit+len(failureOutputTruncated) {
						t.Error("diagnostic capture exceeded its bound")
					}
				}
			})
		}
		group.Wait()
		_, _ = output.Write([]byte("final shutdown failure\n"))
		if !strings.HasSuffix(output.snapshot(), "final shutdown failure\n") {
			t.Fatal("concurrent capture lost the terminal diagnostic")
		}
	})
}
