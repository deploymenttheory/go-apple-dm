package axm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/httpsurl"
)

// JSON:API webhook resource types.
const (
	TypeWebhooks                          = "webhooks"
	TypeWebhookDeliveries                 = "webhookDeliveries"
	TypeWebhookEvents                     = "webhookEvents"
	TypeWebhookPings                      = "webhookPings"
	EventOrgDeviceAttributeChanged        = "ORG_DEVICE_ATTRIBUTE_CHANGED"
	EventOrgDeviceMigrationDetailsChanged = "ORG_DEVICE_ATTRIBUTE_MIGRATION_DETAILS_CHANGED"
	EventOrgDeviceAssignedServerChanged   = "ORG_DEVICE_RELATIONSHIP_ASSIGNED_SERVER_CHANGED"
	EventOrgDeviceWarrantyChanged         = "ORG_DEVICE_RELATIONSHIP_WARRANTY_CHANGED"
)

// Webhook is an Apple subscription. Raw preserves the complete resource.
// Apple never returns the signing secret.
type Webhook struct {
	Raw           json.RawMessage         `json:"-"`
	ID            string                  `json:"id"`
	Type          string                  `json:"type"`
	Attributes    WebhookAttributes       `json:"attributes"`
	Relationships map[string]Relationship `json:"relationships,omitempty"`
	Links         Links                   `json:"links,omitempty"`
}

// WebhookAttributes contains subscription metadata, without credentials.
type WebhookAttributes struct {
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	EventTypes      []string  `json:"eventTypes"`
	URL             string    `json:"url"`
	CreatedDateTime time.Time `json:"createdDateTime,omitzero"`
	UpdatedDateTime time.Time `json:"updatedDateTime,omitzero"`
}

// WebhookCreateAttributes includes Enabled even when false. Secret is write-only.
type WebhookCreateAttributes struct {
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	EventTypes []string `json:"eventTypes"`
	URL        string   `json:"url"`
	Secret     string   `json:"secret"`
}

// WebhookUpdateAttributes distinguishes omission from explicit false or empty values.
type WebhookUpdateAttributes struct {
	Name       *string   `json:"name,omitempty"`
	Enabled    *bool     `json:"enabled,omitempty"`
	EventTypes *[]string `json:"eventTypes,omitempty"`
	URL        *string   `json:"url,omitempty"`
	Secret     *string   `json:"secret,omitempty"`
}

// WebhookDelivery is a send attempt; event linkage identifies its included event.
type WebhookDelivery struct {
	Raw           json.RawMessage           `json:"-"`
	ID            string                    `json:"id"`
	Type          string                    `json:"type"`
	Attributes    WebhookDeliveryAttributes `json:"attributes"`
	Relationships map[string]Relationship   `json:"relationships,omitempty"`
	Included      []IncludedResource        `json:"-"`
}

// WebhookDeliveryAttributes contains delivery outcome and diagnostic data.
// Response bodies and URLs may be sensitive; callers control their disclosure.
type WebhookDeliveryAttributes struct {
	CreatedDateTime time.Time       `json:"createdDateTime,omitzero"`
	SentDateTime    time.Time       `json:"sentDateTime,omitzero"`
	DeliveryState   string          `json:"deliveryState"`
	Redelivery      bool            `json:"redelivery"`
	ErrorMessage    string          `json:"errorMessage,omitempty"`
	Request         json.RawMessage `json:"request,omitempty"`
	Response        json.RawMessage `json:"response,omitempty"`
}

// WebhookEvent is an included event. Payload is JSON encoded inside a string.
type WebhookEvent struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Attributes WebhookEventAttributes `json:"attributes"`
}

// WebhookEventAttributes preserves Apple's stored payload template separately from callbacks.
type WebhookEventAttributes struct {
	EventType       string    `json:"eventType"`
	Payload         string    `json:"payload"`
	IsPingEvent     bool      `json:"isPingEvent"`
	CreatedDateTime time.Time `json:"createdDateTime,omitzero"`
}

// WebhookPing identifies a requested test notification.
type WebhookPing struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// UnmarshalJSON retains the original subscription resource.
func (w *Webhook) UnmarshalJSON(b []byte) error {
	type plain Webhook
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*w = Webhook(v)
	w.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// UnmarshalJSON retains the original delivery resource.
func (w *WebhookDelivery) UnmarshalJSON(b []byte) error {
	type plain WebhookDelivery
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*w = WebhookDelivery(v)
	w.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// ListWebhooks returns one page; Pages or NextPage traverses subsequent pages.
func (c *Client) ListWebhooks(ctx context.Context, o ListOptions) (Page[Webhook], error) {
	q, err := o.query(TypeWebhooks)
	if err != nil {
		return Page[Webhook]{}, err
	}
	return list[Webhook](ctx, c, "/v1/webhooks", q)
}

// GetWebhook reads a subscription by opaque ID.
func (c *Client) GetWebhook(ctx context.Context, id string, o GetOptions) (*Webhook, error) {
	if err := requireID("webhook id", id); err != nil {
		return nil, err
	}
	return get[Webhook](ctx, c, "/v1/webhooks/"+url.PathEscape(id), o.query(TypeWebhooks))
}

// CreateWebhook registers a subscription. An uncertain POST is not retried by default.
func (c *Client) CreateWebhook(ctx context.Context, a WebhookCreateAttributes, opts ...RequestOption) (*Webhook, error) {
	if a.Name == "" || a.Secret == "" || len(a.EventTypes) == 0 {
		return nil, fmt.Errorf("%w: name, secret and eventTypes required", ErrArgument)
	}
	if _, err := httpsurl.Parse(a.URL); err != nil {
		return nil, fmt.Errorf("%w: webhook HTTPS URL", ErrArgument)
	}
	body := map[string]any{"data": map[string]any{"type": TypeWebhooks, "attributes": a}}
	var doc single[Webhook]
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v1/webhooks", body: body, opts: options(opts)}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// UpdateWebhook changes only supplied attributes, preserving an explicit false.
func (c *Client) UpdateWebhook(ctx context.Context, id string, a WebhookUpdateAttributes) (*Webhook, error) {
	if err := requireID("webhook id", id); err != nil {
		return nil, err
	}
	if a.Name == nil && a.Enabled == nil && a.EventTypes == nil && a.URL == nil && a.Secret == nil {
		return nil, fmt.Errorf("%w: webhook update attributes required", ErrArgument)
	}
	if a.URL != nil {
		if _, err := httpsurl.Parse(*a.URL); err != nil {
			return nil, fmt.Errorf("%w: webhook HTTPS URL", ErrArgument)
		}
	}
	body := map[string]any{"data": map[string]any{"type": TypeWebhooks, "id": id, "attributes": a}}
	var doc single[Webhook]
	if err := c.do(ctx, request{method: http.MethodPatch, path: "/v1/webhooks/" + url.PathEscape(id), body: body}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// DeleteWebhook removes a subscription (204).
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	if err := requireID("webhook id", id); err != nil {
		return err
	}
	return c.do(ctx, request{method: http.MethodDelete, path: "/v1/webhooks/" + url.PathEscape(id)}, nil)
}

// WebhookDeliveryQuery selects one delivery page. Both date bounds are required
// by Apple's live service. IncludeEvent requests event linkage and included data.
type WebhookDeliveryQuery struct {
	ListOptions
	Start, End   time.Time
	States       []string
	EventFields  []string
	IncludeEvent bool
}

// ListWebhookDeliveries returns one page of attempts and optional included events.
func (c *Client) ListWebhookDeliveries(ctx context.Context, id string, o WebhookDeliveryQuery) (Page[WebhookDelivery], error) {
	if err := requireID("webhook id", id); err != nil {
		return Page[WebhookDelivery]{}, err
	}
	if o.Start.IsZero() || o.End.IsZero() || !o.End.After(o.Start) {
		return Page[WebhookDelivery]{}, fmt.Errorf("%w: ordered delivery date bounds required", ErrArgument)
	}
	q, err := o.ListOptions.query(TypeWebhookDeliveries)
	if err != nil {
		return Page[WebhookDelivery]{}, err
	}
	q.Set("filter[createdDateTimeAtOrAfter]", o.Start.UTC().Format(time.RFC3339Nano))
	q.Set("filter[createdDateTimeAtBefore]", o.End.UTC().Format(time.RFC3339Nano))
	if len(o.States) > 0 {
		q.Set("filter[deliveryState]", strings.Join(o.States, ","))
	}
	if len(o.EventFields) > 0 {
		q.Set("fields[webhookEvents]", strings.Join(o.EventFields, ","))
	}
	if o.IncludeEvent {
		q.Set("include", "event")
	}
	return list[WebhookDelivery](ctx, c, "/v1/webhooks/"+url.PathEscape(id)+"/deliveries", q)
}

// CreateWebhookDelivery replays an existing delivery identified by templateID.
func (c *Client) CreateWebhookDelivery(ctx context.Context, templateID string, opts ...RequestOption) (*WebhookDelivery, error) {
	if err := requireID("template delivery id", templateID); err != nil {
		return nil, err
	}
	var doc single[WebhookDelivery]
	body := webhookRelationRequest(TypeWebhookDeliveries, "template", TypeWebhookDeliveries, templateID)
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v1/webhookDeliveries", body: body, opts: options(opts)}, &doc); err != nil {
		return nil, err
	}
	doc.Data.Included = doc.Included
	return &doc.Data, nil
}

// CreateWebhookPing requests a signed connectivity notification for a subscription.
func (c *Client) CreateWebhookPing(ctx context.Context, id string, opts ...RequestOption) (*WebhookPing, error) {
	if err := requireID("webhook id", id); err != nil {
		return nil, err
	}
	var doc single[WebhookPing]
	body := webhookRelationRequest(TypeWebhookPings, "webhook", TypeWebhooks, id)
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v1/webhookPings", body: body, opts: options(opts)}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// webhookRelationRequest builds the provider's resource-linkage envelope.
func webhookRelationRequest(typ, rel, target, id string) any {
	return map[string]any{"data": map[string]any{"type": typ, "relationships": map[string]any{rel: map[string]any{"data": Linkage{Type: target, ID: id}}}}}
}
