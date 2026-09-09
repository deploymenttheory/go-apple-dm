package apns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/push"
)

// ErrRequest indicates an app request rejected locally, before contacting APNs.
var ErrRequest = errors.New("apns: invalid app notification")

// AppRequest sends an ordinary alert or background notification. Token is raw
// APNs token bytes, Payload is a JSON object containing aps. Zero Priority uses
// 10 for alert and 5 for background. Expiration is Unix seconds; zero discards
// the notification if APNs cannot deliver immediately.
type AppRequest struct {
	Token      []byte
	Topic      string
	PushType   string
	Payload    json.RawMessage
	Priority   int
	Expiration int64
}

// AppClient sends app notifications with certificate authentication. It shares
// transport behavior with Client but cannot send MDM wake-ups.
type AppClient struct{ client *Client }

// NewApp creates an app sender. Use a separate instance for each environment.
func NewApp(certs push.CertStore, opts ...Option) *AppClient {
	return &AppClient{client: New(certs, opts...)}
}

// Close releases the sender and its connections.
func (c *AppClient) Close() error { return c.client.Close() }

// Send validates and sends one request. Sent means accepted by APNs, not that
// the device received it; applications must record receipts independently.
func (c *AppClient) Send(ctx context.Context, r AppRequest) push.Result {
	n, err := r.notification()
	if err != nil {
		return push.Result{Outcome: push.OutcomeRejected, Err: err}
	}
	return c.client.send(ctx, n, false)
}

func (r AppRequest) notification() (notification, error) {
	n := notification{
		topic:      r.Topic,
		token:      r.Token,
		kind:       r.PushType,
		priority:   r.Priority,
		expiration: r.Expiration,
		body:       r.Payload,
	}
	if len(r.Token) == 0 || r.Topic == "" || r.Expiration < 0 || len(r.Payload) > 4096 {
		return n, fmt.Errorf(
			"%w: require token, topic, nonnegative expiration and at most 4096 payload bytes",
			ErrRequest,
		)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(r.Payload, &payload); err != nil {
		return n, fmt.Errorf("%w: JSON: %w", ErrRequest, err)
	}
	var aps map[string]json.RawMessage
	if err := json.Unmarshal(payload["aps"], &aps); err != nil || aps == nil {
		return n, fmt.Errorf("%w: aps must be an object", ErrRequest)
	}
	switch r.PushType {
	case "alert":
		if n.priority == 0 {
			n.priority = 10
		}
		if len(aps["alert"]) == 0 && len(aps["badge"]) == 0 && len(aps["sound"]) == 0 {
			return n, fmt.Errorf("%w: alert requires alert, badge or sound", ErrRequest)
		}
		if n.priority != 5 && n.priority != 10 {
			return n, fmt.Errorf("%w: alert priority must be 5 or 10", ErrRequest)
		}
		if err := validateAlert(aps); err != nil {
			return n, err
		}
	case "background":
		if n.priority == 0 {
			n.priority = 5
		}
		var available int
		if err := json.Unmarshal(
			aps["content-available"],
			&available,
		); err != nil || available != 1 ||
			n.priority != 5 {
			return n, fmt.Errorf(
				"%w: background requires content-available 1 and priority 5",
				ErrRequest,
			)
		}
		for _, key := range []string{"alert", "sound", "badge"} {
			if _, present := aps[key]; present {
				return n, fmt.Errorf("%w: background cannot contain %s", ErrRequest, key)
			}
		}
	default:
		return n, fmt.Errorf("%w: push type must be alert or background", ErrRequest)
	}
	return n, nil
}

func validateAlert(aps map[string]json.RawMessage) error {
	for _, name := range []string{"alert", "sound", "badge"} {
		raw, present := aps[name]
		if !present {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%w: %w", ErrRequest, err)
		}
		valid := false
		switch v := value.(type) {
		case string, map[string]any:
			valid = name != "badge"
		case float64:
			valid = name == "badge" && v >= 0 && math.Trunc(v) == v
		}
		if !valid {
			return fmt.Errorf("%w: invalid %s value", ErrRequest, name)
		}
	}
	return nil
}
