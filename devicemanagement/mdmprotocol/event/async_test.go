package event_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
)

func TestAsyncConfiguration(t *testing.T) {
	for _, cfg := range []event.AsyncConfig{{Workers: -1}, {QueueCapacity: -1}, {DeliveryTimeout: -1}} {
		if b, err := event.NewAsync(cfg); b != nil || !errors.Is(err, event.ErrAsyncConfig) {
			t.Fatalf("NewAsync(%+v) = %v, %v", cfg, b, err)
		}
	}
	b, err := event.NewAsync(event.AsyncConfig{}, event.WithAsync())
	if err != nil {
		t.Fatal(err)
	}
	if s := b.Stats(); s.Workers != 8 || s.QueueCapacity != 1024 ||
		s.DeliveryTimeout != 30*time.Second {
		t.Fatal(s)
	}
	if err := b.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	syncBus := event.New()
	if err := syncBus.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s := syncBus.Stats(); s.Async || !s.Closed || s.Accepted != 0 {
		t.Fatal(s)
	}
}

func assertAccounting(t *testing.T, b *event.Bus) {
	t.Helper()
	s := b.Stats()
	if s.Accepted != s.Delivered+s.Failed+s.Abandoned+uint64(s.Queued+s.InFlight) {
		t.Fatalf("unbalanced stats: %+v", s)
	}
	if s.InFlight > s.Workers || s.Queued > s.QueueCapacity {
		t.Fatalf("limits exceeded: %+v", s)
	}
}

func TestBoundedAsyncRejectionAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var rejected atomic.Int64
		var b *event.Bus
		b, _ = event.NewAsync(
			event.AsyncConfig{Workers: 2, QueueCapacity: 3},
			event.WithErrorHandler(func(_ event.Event, err error) {
				_ = b.Stats() // reporting must run outside bus locks
				if errors.Is(err, event.ErrQueueFull) {
					rejected.Add(1)
				}
			}),
		)
		release := make(chan struct{})
		b.Subscribe(event.All, func(context.Context, event.Event) error { <-release; return nil })
		publish := func() {
			t.Helper()
			if err := b.Publish(t.Context(), event.Event{}); err != nil {
				t.Fatal(err)
			}
		}
		publish()
		publish()
		synctest.Wait()
		for range 3 {
			publish()
		}
		for range 1000 {
			if err := b.Publish(t.Context(), event.Event{}); !errors.Is(err, event.ErrQueueFull) {
				t.Fatal(err)
			}
		}
		assertAccounting(t, b)
		if s := b.Stats(); s.InFlight != 2 || s.Queued != 3 || s.Rejected != 1000 ||
			rejected.Load() != 1000 {
			t.Fatal(s)
		}
		close(release)
		synctest.Wait()
		publish()
		if err := b.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if s := b.Stats(); s.Delivered != 6 || s.Failed != 0 {
			t.Fatal(s)
		}
		assertAccounting(t, b)
	})
}

func TestAsyncExpiryIncludesQueueTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, _ := event.NewAsync(
			event.AsyncConfig{Workers: 1, QueueCapacity: 2, DeliveryTimeout: time.Second},
		)
		release := make(chan struct{})
		var calls atomic.Int64
		b.Subscribe(
			event.All,
			func(context.Context, event.Event) error { calls.Add(1); <-release; return nil },
		)
		_ = b.Publish(t.Context(), event.Event{})
		synctest.Wait()
		_ = b.Publish(t.Context(), event.Event{})
		time.Sleep(2 * time.Second)
		close(release)
		if err := b.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if s := b.Stats(); s.Failed != 2 || s.TimedOut != 2 || calls.Load() != 1 {
			t.Fatal(s, calls.Load())
		}
		assertAccounting(t, b)
	})
}

func TestAsyncCancellationAndDrainTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type key struct{}
		ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "actor"))
		cancel()
		b, _ := event.NewAsync(event.AsyncConfig{Workers: 1, QueueCapacity: 2})
		b.Subscribe(event.All, func(ctx context.Context, _ event.Event) error {
			if ctx.Value(key{}) != "actor" || ctx.Err() != nil {
				t.Error("lost values or inherited cancellation")
			}
			<-ctx.Done()
			return ctx.Err()
		})
		_ = b.Publish(ctx, event.Event{})
		synctest.Wait()
		_ = b.Publish(ctx, event.Event{})
		deadline, stop := context.WithTimeout(t.Context(), time.Second)
		defer stop()
		if err := b.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if err := b.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if s := b.Stats(); s.Abandoned != 1 || s.Failed != 1 || s.Queued != 0 {
			t.Fatal(s)
		}
		if err := b.Publish(ctx, event.Event{}); !errors.Is(err, event.ErrClosed) {
			t.Fatal(err)
		}
		assertAccounting(t, b)
	})
}

func TestConcurrentPublishAndClose(t *testing.T) {
	b, _ := event.NewAsync(event.AsyncConfig{Workers: 2, QueueCapacity: 4})
	boom := errors.New("sink failed")
	b.Subscribe(event.All, func(context.Context, event.Event) error { return boom })
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 100 {
				_ = b.Publish(t.Context(), event.Event{})
				assertAccounting(t, b)
			}
			if err := b.Close(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	assertAccounting(t, b)
}

func TestAsyncDeadlineSkipsRemainingSubscribers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b, _ := event.NewAsync(event.AsyncConfig{Workers: 1, DeliveryTimeout: time.Second})
		b.Subscribe(
			event.All,
			func(ctx context.Context, _ event.Event) error { <-ctx.Done(); return ctx.Err() },
		)
		b.Subscribe(
			event.All,
			func(context.Context, event.Event) error { t.Error("called after expiry"); return nil },
		)
		_ = b.Publish(t.Context(), event.Event{})
		if err := b.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if s := b.Stats(); s.TimedOut != 1 {
			t.Fatal(s)
		}
	})
}
