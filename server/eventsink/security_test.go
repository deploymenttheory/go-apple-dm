package eventsink_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
)

type failingTransport struct{ err error }

func (t failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

func TestWebhookTransportProtectsEndpointSecrets(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.com/secret", "http://127.0.0.1/secret",
		"https://user:secret@example.com", "https://example.com/#secret",
		"https://example.com:secret", "https://secret%",
	} {
		_, err := eventsink.Webhook(eventsink.WebhookConfig{URL: endpoint})
		if !errors.Is(err, eventsink.ErrWebhookConfig) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid endpoint accepted or leaked: %v", err)
		}
	}
	var logs bytes.Buffer
	failure := errors.New("transport disclosed secret-query-value")
	h, err := eventsink.Webhook(eventsink.WebhookConfig{
		URL: "https://receiver.example/hook?token=secret-query-value", Retries: -1,
		Client: &http.Client{Transport: failingTransport{err: failure}},
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = h(t.Context(), event.Event{Type: event.Enrolled})
	if !errors.Is(err, failure) {
		t.Fatalf("transport error identity lost: %v", err)
	}
	if strings.Contains(err.Error()+logs.String(), "secret-query-value") {
		t.Fatal("webhook endpoint leaked through diagnostics")
	}
}
