package event_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
)

func TestSyncDelivery(t *testing.T) {
	t.Parallel()
	b := event.New()
	var order []string
	unsub := b.Subscribe(event.Enrolled, func(_ context.Context, e event.Event) error {
		order = append(order, "typed:"+string(e.Type))
		return nil
	})
	b.Subscribe(event.All, func(_ context.Context, e event.Event) error {
		order = append(order, "all:"+string(e.Type))
		if e.At.IsZero() {
			t.Error("At not defaulted")
		}
		return nil
	})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D"}
	if err := b.Publish(context.Background(), event.Event{Type: event.Enrolled, Enrollment: id}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(context.Background(), event.Event{Type: event.CheckedOut, Enrollment: id}); err != nil {
		t.Fatal(err)
	}
	unsub()
	unsub() // idempotent
	if err := b.Publish(context.Background(), event.Event{Type: event.Enrolled, Enrollment: id}); err != nil {
		t.Fatal(err)
	}
	want := []string{"typed:enrolled", "all:enrolled", "all:checked-out", "all:enrolled"}
	if len(order) != len(want) {
		t.Fatalf("order = %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestHandlerErrors(t *testing.T) {
	t.Parallel()
	var reported []error
	b := event.New(event.WithErrorHandler(func(_ event.Event, err error) { reported = append(reported, err) }))
	boom := errors.New("boom")
	b.Subscribe(event.All, func(context.Context, event.Event) error { return boom })
	b.Subscribe(event.All, func(context.Context, event.Event) error { return nil })
	err := b.Publish(context.Background(), event.Event{Type: event.TokenUpdated})
	if !errors.Is(err, boom) || len(reported) != 1 {
		t.Fatalf("err = %v, reported = %v", err, reported)
	}
}

func TestAsyncAndClose(t *testing.T) {
	t.Parallel()
	b := event.New(event.WithAsync())
	var mu sync.Mutex
	got := 0
	b.Subscribe(event.CommandQueued, func(context.Context, event.Event) error {
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		got++
		mu.Unlock()
		return nil
	})
	for range 5 {
		if err := b.Publish(context.Background(), event.Event{Type: event.CommandQueued, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got != 5 {
		t.Fatalf("delivered %d, want 5", got)
	}
	if err := b.Publish(context.Background(), event.Event{Type: event.CommandQueued}); !errors.Is(err, event.ErrClosed) {
		t.Fatalf("publish after close: %v", err)
	}
}

func TestCloseTimeout(t *testing.T) {
	t.Parallel()
	b := event.New(event.WithAsync())
	release := make(chan struct{})
	b.Subscribe(event.All, func(context.Context, event.Event) error { <-release; return nil })
	_ = b.Publish(context.Background(), event.Event{Type: event.Enrolled})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want deadline", err)
	}
	close(release)
}
