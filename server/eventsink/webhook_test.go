package eventsink_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// receiver records what a webhook endpoint was sent.
type receiver struct {
	mu     sync.Mutex
	bodies []string
	heads  []http.Header
	status int
	fail   int // fail this many deliveries before succeeding
}

func (r *receiver) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, string(body))
		r.heads = append(r.heads, req.Header.Clone())
		n, fail := len(r.bodies), r.fail
		r.mu.Unlock()
		if n <= fail {
			http.Error(w, "later", http.StatusServiceUnavailable)
			return
		}
		if r.status != 0 {
			w.WriteHeader(r.status)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func (r *receiver) sent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

func newWebhook(t *testing.T, cfg eventsink.WebhookConfig) (event.Handler, *receiver) {
	t.Helper()
	rec := &receiver{}
	srv := httptest.NewServer(rec.handler())
	t.Cleanup(srv.Close)
	cfg.URL = srv.URL
	if cfg.Logger == nil {
		cfg.Logger = quiet()
	}
	if cfg.Client == nil {
		cfg.Client = srv.Client()
	}
	h, err := eventsink.Webhook(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, rec
}

// The envelope a MicroMDM or NanoMDM receiver expects, minus the one field
// this package refuses to send.
func TestWebhookSendsTheMicroMDMEnvelope(t *testing.T) {
	h, rec := newWebhook(t, eventsink.WebhookConfig{Clock: clock.Real{}})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-1"}
	if err := h(context.Background(), event.Event{
		Type: event.TokenUpdated, At: t0, Actor: "device", Enrollment: id,
		Data: tokenUpdateWithSecrets(),
	}); err != nil {
		t.Fatal(err)
	}
	sent := rec.sent()
	if len(sent) != 1 {
		t.Fatalf("deliveries = %d", len(sent))
	}
	var env struct {
		Topic     string    `json:"topic"`
		CreatedAt time.Time `json:"created_at"`
		Checkin   *struct {
			UDID       string         `json:"udid"`
			Fields     map[string]any `json:"fields"`
			RawPayload string         `json:"raw_payload"`
		} `json:"checkin_event"`
	}
	if err := json.Unmarshal([]byte(sent[0]), &env); err != nil {
		t.Fatal(err)
	}
	if env.Topic != "mdm.token-updated" {
		t.Errorf("topic = %q", env.Topic)
	}
	if !env.CreatedAt.Equal(t0) {
		t.Errorf("created_at = %v, want the event time", env.CreatedAt)
	}
	if env.Checkin == nil || env.Checkin.UDID != "UDID-1" {
		t.Fatalf("checkin_event = %+v", env.Checkin)
	}
	if env.Checkin.RawPayload != "" {
		t.Errorf("raw_payload was sent: %q", env.Checkin.RawPayload)
	}
	if env.Checkin.Fields["topic"] != "com.apple.mgmt.External.test" {
		t.Errorf("fields = %v", env.Checkin.Fields)
	}
}

// The whole reason the envelope differs: what NanoMDM and MicroMDM put in
// raw_payload must not be on the wire at all.
func TestWebhookNeverSendsSecrets(t *testing.T) {
	h, rec := newWebhook(t, eventsink.WebhookConfig{Clock: clock.Real{}})
	for _, e := range events() {
		if err := h(context.Background(), e); err != nil {
			t.Fatalf("%s: %v", e.Type, err)
		}
	}
	for _, body := range rec.sent() {
		for _, s := range sentinels() {
			if strings.Contains(body, s) {
				t.Errorf("the webhook leaked %q:\n%s", s, body)
			}
		}
	}
}

// A command result becomes the acknowledge event, which is the split both
// references make.
func TestWebhookSplitsAcknowledgeFromCheckin(t *testing.T) {
	h, rec := newWebhook(t, eventsink.WebhookConfig{Clock: clock.Real{}})
	err := h(context.Background(), event.Event{
		Type: event.CommandResult, At: t0,
		Enrollment: mdm.EnrollmentID{ID: "UDID-1"},
		Data:       &mdm.Response{CommandUUID: "cmd-1", Status: "Acknowledged"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := rec.sent()[0]
	if !strings.Contains(body, `"acknowledge_event"`) || strings.Contains(body, `"checkin_event"`) {
		t.Fatalf("wrong envelope arm:\n%s", body)
	}
	for _, want := range []string{`"command_uuid":"cmd-1"`, `"status":"Acknowledged"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s:\n%s", want, body)
		}
	}
}

func TestWebhookSignsTheBody(t *testing.T) {
	key := []byte("shared-secret")
	h, rec := newWebhook(t, eventsink.WebhookConfig{HMACKey: key, Clock: clock.Real{}})
	if err := h(context.Background(), event.Event{Type: event.CheckedOut, At: t0}); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(rec.sent()[0]))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got := rec.heads[0].Get(eventsink.HMACHeader); got != want {
		t.Fatalf("%s = %q, want %q", eventsink.HMACHeader, got, want)
	}
}

// NanoMDM gives up after one attempt. A receiver that is briefly down should
// not cost an audit record.
func TestWebhookRetriesThenSucceeds(t *testing.T) {
	rec := &receiver{fail: 2}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	h, err := eventsink.Webhook(eventsink.WebhookConfig{
		URL: srv.URL, Client: srv.Client(), Logger: quiet(),
		Retries: 2, Backoff: time.Millisecond, Clock: clock.Real{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h(context.Background(), event.Event{Type: event.CheckedOut, At: t0}); err != nil {
		t.Fatalf("delivery = %v, want success on the third attempt", err)
	}
	if got := len(rec.sent()); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestWebhookReportsAPersistentFailure(t *testing.T) {
	rec := &receiver{fail: 99}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	h, err := eventsink.Webhook(eventsink.WebhookConfig{
		URL: srv.URL, Client: srv.Client(), Logger: quiet(),
		Retries: 1, Backoff: time.Millisecond, Clock: clock.Real{},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = h(context.Background(), event.Event{Type: event.CheckedOut, At: t0})
	if err == nil {
		t.Fatal("a refused delivery reported success")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, want the receiver's status", err)
	}
	if got := len(rec.sent()); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestWebhookStopsOnCancellation(t *testing.T) {
	rec := &receiver{fail: 99}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	h, err := eventsink.Webhook(eventsink.WebhookConfig{
		URL: srv.URL, Client: srv.Client(), Logger: quiet(),
		Retries: 5, Backoff: time.Hour, Clock: clock.Real{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h(ctx, event.Event{Type: event.CheckedOut, At: t0}); err == nil {
		t.Fatal("a cancelled delivery reported success")
	}
}

func TestWebhookNeedsAURL(t *testing.T) {
	if _, err := eventsink.Webhook(eventsink.WebhookConfig{}); !errors.Is(err, eventsink.ErrWebhookConfig) {
		t.Fatalf("err = %v, want ErrWebhookConfig", err)
	}
}

// An unreachable receiver is an error, not a panic.
func TestWebhookReportsATransportFailure(t *testing.T) {
	h, err := eventsink.Webhook(eventsink.WebhookConfig{
		URL: "http://127.0.0.1:1", Logger: quiet(), Retries: -1, Clock: clock.Real{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h(context.Background(), event.Event{Type: event.CheckedOut, At: t0}); err == nil {
		t.Fatal("an unreachable receiver reported success")
	}
}

// A zero event time is stamped from the clock rather than sent as year zero.
func TestWebhookStampsAMissingTime(t *testing.T) {
	fake := clock.NewFake(t0)
	h, rec := newWebhook(t, eventsink.WebhookConfig{Clock: fake})
	if err := h(context.Background(), event.Event{Type: event.CheckedOut}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.sent()[0], t0.Format("2006-01-02")) {
		t.Fatalf("created_at was not stamped:\n%s", rec.sent()[0])
	}
}

// An unusable URL fails at construction. Deferring it to delivery would mean
// every event failing quietly for the life of the process.
func TestWebhookRejectsABadURL(t *testing.T) {
	for name, u := range map[string]string{
		"NotAURL":     "\x7f://bad",
		"WrongScheme": "ftp://example.com/hook",
		"NoHost":      "http://",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eventsink.Webhook(eventsink.WebhookConfig{URL: u}); !errors.Is(err, eventsink.ErrWebhookConfig) {
				t.Fatalf("err = %v, want ErrWebhookConfig", err)
			}
		})
	}
}
