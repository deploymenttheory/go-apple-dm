package event

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Default asynchronous dispatch limits bound events, not payload byte sizes.
const (
	DefaultWorkers         = 8
	DefaultQueueCapacity   = 1024
	DefaultDeliveryTimeout = 30 * time.Second
)

// ErrQueueFull means the event was not accepted. No automatic replay occurs.
var ErrQueueFull = errors.New("event: queue full")

// ErrAsyncConfig identifies invalid asynchronous dispatch limits.
var ErrAsyncConfig = errors.New("event: invalid async configuration")

// AsyncConfig bounds active deliveries, pending events and time since acceptance.
// Zero fields select defaults; negative fields are invalid.
type AsyncConfig struct {
	Workers         int
	QueueCapacity   int
	DeliveryTimeout time.Duration
}

// Stats is a consistent snapshot. Counters are cumulative for this bus lifetime.
// Failed includes TimedOut; Abandoned counts queued events discarded at shutdown.
// Accepted = Delivered + Failed + Abandoned + Queued + InFlight.
// Synchronous buses report Async=false and zero dispatch counters.
type Stats struct {
	Async           bool
	Closed          bool
	Workers         int
	QueueCapacity   int
	DeliveryTimeout time.Duration
	Queued          int
	InFlight        int
	Accepted        uint64
	Delivered       uint64
	Failed          uint64
	TimedOut        uint64
	Rejected        uint64
	Abandoned       uint64
}

type delivery struct {
	ctx      context.Context
	event    Event
	deadline time.Time
}

// NewAsync creates a bounded asynchronous bus. Explicit limits take precedence
// over WithAsync. Error reporting options work as with New.
func NewAsync(cfg AsyncConfig, opts ...Option) (*Bus, error) {
	if cfg.Workers < 0 || cfg.QueueCapacity < 0 || cfg.DeliveryTimeout < 0 {
		return nil, fmt.Errorf("%w: limits must be non-negative", ErrAsyncConfig)
	}
	defaults := defaultAsyncConfig()
	if cfg.Workers == 0 {
		cfg.Workers = defaults.Workers
	}
	if cfg.QueueCapacity == 0 {
		cfg.QueueCapacity = defaults.QueueCapacity
	}
	if cfg.DeliveryTimeout == 0 {
		cfg.DeliveryTimeout = defaults.DeliveryTimeout
	}
	b := newBus(opts)
	b.async = true
	b.start(cfg)
	return b, nil
}

func defaultAsyncConfig() AsyncConfig {
	return AsyncConfig{
		Workers:         DefaultWorkers,
		QueueCapacity:   DefaultQueueCapacity,
		DeliveryTimeout: DefaultDeliveryTimeout,
	}
}

func (b *Bus) start(cfg AsyncConfig) {
	b.config = cfg
	b.queue = make([]delivery, cfg.QueueCapacity)
	b.cond = sync.NewCond(&b.closeMu)
	b.abort, b.cancel = context.WithCancel(context.Background())
	b.wg.Add(cfg.Workers)
	for range cfg.Workers {
		go b.worker()
	}
	go func() {
		b.wg.Wait()
		b.cancel()
		close(b.done)
	}()
}

// Stats reports bounded dispatch state without exposing event payloads.
func (b *Bus) Stats() Stats {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	out := b.stats
	out.Async, out.Closed = b.async, b.closed
	out.Workers, out.QueueCapacity = b.config.Workers, b.config.QueueCapacity
	out.DeliveryTimeout, out.Queued = b.config.DeliveryTimeout, b.size
	return out
}

func (b *Bus) worker() {
	defer b.wg.Done()
	for {
		b.closeMu.Lock()
		for b.size == 0 && !b.closed {
			b.cond.Wait()
		}
		if b.size == 0 {
			b.closeMu.Unlock()
			return
		}
		job := b.queue[b.head]
		b.queue[b.head] = delivery{}
		b.head = (b.head + 1) % len(b.queue)
		b.size--
		b.stats.InFlight++
		b.closeMu.Unlock()

		ctx, cancel := context.WithDeadline(job.ctx, job.deadline)
		stop := context.AfterFunc(b.abort, cancel)
		var err error
		if b.abort.Err() != nil {
			cancel()
		}
		if ctx.Err() != nil {
			err = ctx.Err()
			b.report(job.event, err)
		} else {
			err = b.deliver(ctx, job.event)
			if ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
				b.report(job.event, ctx.Err())
				err = errors.Join(err, ctx.Err())
			}
		}
		stop()
		cancel()
		b.closeMu.Lock()
		b.stats.InFlight--
		if err != nil {
			b.stats.Failed++
			if errors.Is(err, context.DeadlineExceeded) {
				b.stats.TimedOut++
			}
		} else {
			b.stats.Delivered++
		}
		b.closeMu.Unlock()
	}
}
