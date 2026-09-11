package eventsink_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
)

func TestWebhookRedirectDoesNotForwardSignedBody(t *testing.T) {
	var calls atomic.Int64
	dest := httptest.NewTLSServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }),
	)
	defer dest.Close()
	source := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dest.URL, 307) },
		),
	)
	defer source.Close()
	h, err := eventsink.Webhook(
		eventsink.WebhookConfig{URL: source.URL, Client: source.Client(), HMACKey: []byte("test-key"), Retries: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = h(t.Context(), event.Event{Type: event.Enrolled}); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("signed body forwarded")
	}
}
