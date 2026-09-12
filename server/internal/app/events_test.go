package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
)

func TestEventLimitsEnvironment(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		bad        bool
	}{
		{EnvEventWorkers, "3", false},
		{EnvEventQueueCapacity, "5", false},
		{EnvEventDeliveryTimeout, "4s", false},
		{EnvEventWorkers, "-1", true},
		{EnvEventWorkers, "bad", true},
		{EnvEventQueueCapacity, "-1", true},
		{EnvEventQueueCapacity, "bad", true},
		{EnvEventDeliveryTimeout, "-1s", true},
		{EnvEventDeliveryTimeout, "bad", true},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			cfg, err := ParseEnv(func(key string) string {
				if key == tc.key {
					return tc.value
				}
				if key == EnvStorage {
					return "inmem"
				}
				return ""
			})
			if tc.bad {
				if !errors.Is(err, ErrConfig) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := cfg.Sinks.Dispatch; d.Workers != 3 && d.QueueCapacity != 5 &&
				d.DeliveryTimeout != 4*time.Second {
				t.Fatal(d)
			}
		})
	}
	for _, d := range []event.AsyncConfig{{Workers: -1}, {QueueCapacity: -1}, {DeliveryTimeout: -1}} {
		_, err := Build(
			t.Context(),
			Config{Role: RoleAll, Storage: "inmem", Sinks: SinkConfig{Dispatch: d}},
		)
		if !errors.Is(err, ErrConfig) {
			t.Fatal(err)
		}
	}
}

func TestEventReporterRateLimitsWithoutPayloads(t *testing.T) {
	var logs bytes.Buffer
	clk := clock.NewFake(time.Now())
	report := eventReporter(slog.New(slog.NewJSONHandler(&logs, nil)), clk)
	e := event.Event{Data: "SECRET"}
	for range 100 {
		report(e, event.ErrQueueFull)
	}
	if strings.Count(logs.String(), "event queue full") != 1 {
		t.Fatal(logs.String())
	}
	clk.Advance(10 * time.Second)
	report(e, event.ErrQueueFull)
	report(e, errors.New("sink unavailable"))
	if strings.Count(logs.String(), "event queue full") != 2 ||
		!strings.Contains(logs.String(), `"rejected":101`) ||
		strings.Contains(logs.String(), "SECRET") {
		t.Fatal(logs.String())
	}
	if !strings.Contains(logs.String(), "sink unavailable") {
		t.Fatal(logs.String())
	}
}

func TestEventBusStatsAndOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		a, err := Build(
			t.Context(),
			Config{
				Role:       RoleAll,
				Storage:    "inmem",
				AdminToken: "token",
				Logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
				Sinks: SinkConfig{
					Audit:    true,
					Dispatch: event.AsyncConfig{Workers: 1, QueueCapacity: 2},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		release := make(chan struct{})
		a.cfg.Bus.Subscribe(
			event.All,
			func(context.Context, event.Event) error { <-release; return nil },
		)
		_ = a.cfg.Bus.Publish(t.Context(), event.Event{})
		synctest.Wait()
		_ = a.cfg.Bus.Publish(t.Context(), event.Event{})
		_ = a.cfg.Bus.Publish(t.Context(), event.Event{})
		for range 20 {
			_ = a.cfg.Bus.Publish(t.Context(), event.Event{})
		}
		request := httptest.NewRequest(http.MethodGet, "/admin/v1/config", nil)
		request.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, request)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"Rejected":20`) ||
			!strings.Contains(w.Body.String(), `"DeliveryTimeout":"30s"`) {
			t.Error(w.Code, w.Body.String())
		}
		close(release)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		if !a.eventStats().Closed {
			t.Fatal("owned bus not closed")
		}
	})
	b := event.New()
	a, err := Build(t.Context(), Config{Role: RoleAll, Storage: "inmem", Bus: b})
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	if b.Stats().Closed {
		t.Fatal("closed injected bus")
	}
	if s := (&App{}).eventStats(); s != nil {
		t.Fatal(s)
	}
}

func TestEventBusCleanupOnBuildFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := Build(
			t.Context(),
			Config{
				Role:    RoleAll,
				Storage: "inmem",
				Sinks:   SinkConfig{WebhookURL: "http://invalid.example"},
			},
		)
		if err == nil {
			t.Fatal("expected sink construction failure")
		}
		// The bubble cannot finish if Build leaked its owned workers.
		synctest.Wait()
	})
}
