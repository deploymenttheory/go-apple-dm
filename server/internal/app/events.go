package app

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/clock"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/event"
)

// Environment controls for the audit/webhook bus. Unset or zero uses defaults.
const (
	EnvEventWorkers         = "DM_EVENT_WORKERS"
	EnvEventQueueCapacity   = "DM_EVENT_QUEUE_CAPACITY"
	EnvEventDeliveryTimeout = "DM_EVENT_DELIVERY_TIMEOUT"
)

func parseEventEnv(cfg *event.AsyncConfig, get func(string) string) error {
	for name, dst := range map[string]*int{EnvEventWorkers: &cfg.Workers, EnvEventQueueCapacity: &cfg.QueueCapacity} {
		if raw := get(name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("%w: %s: %w", ErrConfig, name, err)
			}
			if n < 0 {
				return fmt.Errorf("%w: %s must be non-negative", ErrConfig, name)
			}
			*dst = n
		}
	}
	if raw := get(EnvEventDeliveryTimeout); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrConfig, EnvEventDeliveryTimeout, err)
		}
		if d < 0 {
			return fmt.Errorf("%w: %s must be non-negative", ErrConfig, EnvEventDeliveryTimeout)
		}
		cfg.DeliveryTimeout = d
	}
	return nil
}

// Admission failures are reported here even when a publisher ignores its return
// value. No payload, enrollment identity or credential is attached to overflow.
func eventReporter(log *slog.Logger, clk clock.Clock) func(event.Event, error) {
	var mu sync.Mutex
	var next time.Time
	var rejected uint64
	return func(e event.Event, err error) {
		if !errors.Is(err, event.ErrQueueFull) {
			log.Warn("app: event sink failed", "event", string(e.Type), "error", err)
			return
		}
		mu.Lock()
		rejected++
		now := clk.Now()
		if now.Before(next) {
			mu.Unlock()
			return
		}
		next = now.Add(10 * time.Second)
		total := rejected
		mu.Unlock()
		log.Warn("app: event queue full; audit/webhook notifications rejected", "rejected", total)
	}
}

type eventDeliveryStatus struct {
	event.Stats
	DeliveryTimeout string
}

func (a *App) eventStats() *eventDeliveryStatus {
	if a.cfg.Bus == nil {
		return nil
	}
	s := a.cfg.Bus.Stats()
	return &eventDeliveryStatus{Stats: s, DeliveryTimeout: s.DeliveryTimeout.String()}
}

func eventConfigFromEnv(get func(string) string, cfg Config) (Config, error) {
	if err := parseEventEnv(&cfg.Sinks.Dispatch, get); err != nil {
		return Config{}, err
	}
	return securityConfigFromEnv(get, cfg)
}
