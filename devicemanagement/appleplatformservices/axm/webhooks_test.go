package axm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestWebhookManagementWire checks all eight management operations against independent HTTPS responses.
func TestWebhookManagementWire(t *testing.T) {
	ctx := context.Background()
	enabled := false
	for _, tc := range []struct {
		name, method, path string
		call               func(*Client) error
		check              func(*testing.T, *http.Request, map[string]any)
		response           string
		status             int
	}{
		{name: "list", method: "GET", path: "/v1/webhooks", call: func(c *Client) error {
			p, e := c.ListWebhooks(ctx, ListOptions{Fields: []string{"enabled"}, Limit: 1})
			if e == nil && (len(p.Items) != 1 || len(p.Items[0].Raw) == 0) {
				return errors.New("lost resource")
			}
			return e
		}, response: `{"data":[{"id":"w1","type":"webhooks","attributes":{"enabled":false,"future":42}}]}`},
		{name: "read", method: "GET", path: "/v1/webhooks/w1", call: func(c *Client) error { _, e := c.GetWebhook(ctx, "w1", GetOptions{}); return e }, response: `{"data":{"id":"w1","type":"webhooks"}}`},
		{name: "create", method: "POST", path: "/v1/webhooks", call: func(c *Client) error {
			_, e := c.CreateWebhook(ctx, WebhookCreateAttributes{Name: "lab", URL: "https://example.com/callback", Secret: "secret", EventTypes: []string{EventOrgDeviceAttributeChanged}})
			return e
		}, check: func(t *testing.T, _ *http.Request, m map[string]any) {
			t.Helper()
			if dig(t, m, "data", "attributes", "enabled") != false {
				t.Fatal("false omitted")
			}
		}, response: `{"data":{"id":"w1","type":"webhooks"}}`, status: 201},
		{name: "update", method: "PATCH", path: "/v1/webhooks/w1", call: func(c *Client) error {
			_, e := c.UpdateWebhook(ctx, "w1", WebhookUpdateAttributes{Enabled: &enabled})
			return e
		}, check: func(t *testing.T, _ *http.Request, m map[string]any) {
			t.Helper()
			if dig(t, m, "data", "attributes", "enabled") != false {
				t.Fatal("false omitted")
			}
		}, response: `{"data":{"id":"w1","type":"webhooks"}}`},
		{name: "delete", method: "DELETE", path: "/v1/webhooks/w1", call: func(c *Client) error { return c.DeleteWebhook(ctx, "w1") }, status: 204},
		{name: "deliveries", method: "GET", path: "/v1/webhooks/w1/deliveries", call: func(c *Client) error {
			_, e := c.ListWebhookDeliveries(ctx, "w1", WebhookDeliveryQuery{Start: time.Unix(1, 0), End: time.Unix(2, 0), IncludeEvent: true, EventFields: []string{"payload"}, States: []string{"SUCCEEDED"}})
			return e
		}, check: func(t *testing.T, r *http.Request, _ map[string]any) {
			t.Helper()
			q := r.URL.Query()
			if q.Get("include") != "event" || q.Get("filter[createdDateTimeAtOrAfter]") == "" || q.Get("filter[createdDateTimeAtBefore]") == "" {
				t.Fatal("missing delivery query")
			}
		}, response: `{"data":[],"included":[]}`},
		{name: "redeliver", method: "POST", path: "/v1/webhookDeliveries", call: func(c *Client) error { _, e := c.CreateWebhookDelivery(ctx, "d1"); return e }, check: func(t *testing.T, _ *http.Request, m map[string]any) {
			t.Helper()
			if dig(t, m, "data", "relationships", "template", "data", "id") != "d1" {
				t.Fatal("wrong template")
			}
		}, response: `{"data":{"id":"d2","type":"webhookDeliveries","attributes":{"redelivery":true}}}`, status: 201},
		{name: "ping", method: "POST", path: "/v1/webhookPings", call: func(c *Client) error { _, e := c.CreateWebhookPing(ctx, "w1"); return e }, response: `{"data":{"id":"p1","type":"webhookPings"}}`, status: 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				var body map[string]any
				if len(raw) > 0 {
					if err := json.Unmarshal(raw, &body); err != nil {
						t.Error(err)
					}
				}
				if tc.check != nil {
					tc.check(t, r, body)
				}
				w.Header().Set("Content-Type", "application/json")
				status := tc.status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, tc.response)
			})
			if err := tc.call(stubClient(t, srv, nil)); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("unexpected request count", calls)
			}
		})
	}
}

// TestWebhookInvalidArguments rejects unusable webhook requests before network I/O.
func TestWebhookInvalidArguments(t *testing.T) {
	c := newFixture(t).client(t, nil)
	ctx := context.Background()
	calls := []func() error{
		func() error { _, e := c.GetWebhook(ctx, "", GetOptions{}); return e },
		func() error { _, e := c.CreateWebhook(ctx, WebhookCreateAttributes{}); return e },
		func() error {
			_, e := c.CreateWebhook(ctx, WebhookCreateAttributes{Name: "n", Secret: "s", EventTypes: []string{"e"}, URL: "http://bad"})
			return e
		},
		func() error { _, e := c.UpdateWebhook(ctx, "", WebhookUpdateAttributes{}); return e },
		func() error { _, e := c.UpdateWebhook(ctx, "w", WebhookUpdateAttributes{}); return e },
		func() error { return c.DeleteWebhook(ctx, "") },
		func() error { _, e := c.ListWebhookDeliveries(ctx, "", WebhookDeliveryQuery{}); return e },
		func() error { _, e := c.ListWebhookDeliveries(ctx, "w", WebhookDeliveryQuery{}); return e },
		func() error { _, e := c.CreateWebhookDelivery(ctx, ""); return e },
		func() error { _, e := c.CreateWebhookPing(ctx, ""); return e },
	}
	for i, call := range calls {
		if err := call(); !errors.Is(err, ErrArgument) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

// TestWebhookFakeSignedLifecycle verifies signed ping and replay through the independent Apple fake.
func TestWebhookFakeSignedLifecycle(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, nil)
	ctx := t.Context()
	const secret = "fixture-secret"
	received := 0
	receiver := stub(t, func(w http.ResponseWriter, r *http.Request) {
		n, err := DecodeWebhookNotification(r, []byte(secret))
		if err != nil || n.Data.Type != NotificationPing {
			t.Errorf("invalid signed ping: %v", err)
			w.WriteHeader(401)
			return
		}
		received++
		w.WriteHeader(204)
	})
	created, err := c.CreateWebhook(ctx, WebhookCreateAttributes{Name: "lab", Enabled: true, URL: receiver.URL, Secret: secret, EventTypes: []string{EventOrgDeviceAttributeChanged}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(created.Raw, []byte(secret)) {
		t.Fatal("write-only secret returned")
	}
	ping, err := c.CreateWebhookPing(ctx, created.ID)
	if err != nil || ping.ID == "" {
		t.Fatal(err)
	}
	query := WebhookDeliveryQuery{Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour), IncludeEvent: true}
	deliveries, err := c.ListWebhookDeliveries(ctx, created.ID, query)
	if err != nil || len(deliveries.Items) != 1 || len(deliveries.Included) != 1 {
		t.Fatal(deliveries, err)
	}
	original := deliveries.Items[0]
	if err := f.srv.DeliverWebhook(ctx, original.ID, receiver.Client()); err != nil {
		t.Fatal(err)
	}
	replay, err := c.CreateWebhookDelivery(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.srv.DeliverWebhook(ctx, replay.ID, receiver.Client()); err != nil {
		t.Fatal(err)
	}
	if received != 2 {
		t.Fatal("signed receipts", received)
	}
	deliveries, err = c.ListWebhookDeliveries(ctx, created.ID, query)
	if err != nil || len(deliveries.Items) != 2 {
		t.Fatal(err)
	}
	for _, delivery := range deliveries.Items {
		if delivery.Attributes.DeliveryState != "SUCCEEDED" {
			t.Fatal("delivery failed")
		}
	}
	if !deliveries.Items[1].Attributes.Redelivery {
		t.Fatal("missing replay flag")
	}
	if err := c.DeleteWebhook(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetWebhook(ctx, created.ID, GetOptions{}); !IsNotFound(err) {
		t.Fatal("cleanup unconfirmed", err)
	}
}

// TestWebhookFakePagination checks shared field projection and explicit page traversal.
func TestWebhookFakePagination(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, nil)
	for _, name := range []string{"one", "two"} {
		if _, err := c.CreateWebhook(t.Context(), WebhookCreateAttributes{Name: name, URL: "https://example.com/hook", Secret: "fixture", EventTypes: []string{EventOrgDeviceAttributeChanged}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := c.ListWebhooks(t.Context(), ListOptions{Limit: 1, Fields: []string{"enabled"}})
	if err != nil || len(first.Items) != 1 || !first.HasNext() {
		t.Fatal(first, err)
	}
	all, err := All(t.Context(), c, first)
	if err != nil || len(all) != 2 {
		t.Fatal(len(all), err)
	}
	for _, item := range all {
		if bytes.Contains(item.Raw, []byte("fixture")) || bytes.Contains(item.Raw, []byte(`"name"`)) {
			t.Fatal("field projection or secret isolation failed")
		}
	}
	if _, err := c.ListWebhooks(t.Context(), ListOptions{Fields: []string{"unknown"}}); err == nil {
		t.Fatal("invalid projection accepted")
	}
}
