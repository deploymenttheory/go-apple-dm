package app_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func TestPersistentWebhookDeliversAfterRestartingWorkers(t *testing.T) {
	c := newCollector()
	srv := c.server(t)
	cfg := nativeWebhookConfig(t, srv)
	a := build(t, cfg)
	createNativeSubscription(t, a, srv.URL)
	publishSomething(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = build(t, cfg)
	// Capture succeeds before workers run. Readiness must stay false until the
	// configured workers are available, then delivery consumes the saved event.
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("ready without delivery worker", w.Code)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	c.wait(t)
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker failed to drain")
	}
	if body := c.all(); !strings.Contains(body, "UDID-SINK") || strings.Contains(body, "raw_payload") {
		t.Fatal("invalid persisted webhook projection", body)
	}
}

func TestPersistentWebhookRejectsInvalidTrustAndEndpoint(t *testing.T) {
	for _, sinks := range []app.SinkConfig{
		{WebhookURL: "https://webhook.example.test", WebhookRootCAFile: "missing"},
		{WebhookURL: "://invalid"},
	} {
		cfg := app.Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "events.sqlite"), StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, Sinks: sinks, Logger: quiet}
		if a, err := app.Build(t.Context(), cfg); err == nil {
			_ = a.Close()
			t.Fatal("accepted unavailable event destination")
		}
	}
}

func TestEventCaptureFailureMakesReadinessUnavailable(t *testing.T) {
	a, db, _ := eventAdmin(t)
	if _, err := db.ExecContext(t.Context(), "DROP TABLE event_records"); err != nil {
		t.Fatal(err)
	}
	if w := eventRequest(a, "DELETE", "/enrollments/device/absent", "t", ""); w.Code < 400 {
		t.Fatal("accepted unavailable mutation")
	}
	// Authentication denial is also a required occurrence and reports failed
	// capture without accepting the request or exposing database details.
	if w := eventRequest(a, "GET", "/events", "incorrect", ""); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/readyz", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "event recording unavailable") {
		t.Fatal("capture failure absent from readiness", w.Code, w.Body.String())
	}
	for _, path := range []string{"/events", "/events/status", "/events/missing"} {
		w := eventRequest(a, "GET", path, "t", "")
		if w.Code != 503 || strings.Contains(w.Body.String(), "SQL") || strings.Contains(w.Body.String(), "no such table") {
			t.Fatal("event endpoint leaked storage failure", path, w.Code, w.Body.String())
		}
	}
}
