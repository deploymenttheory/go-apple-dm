package sink

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
)

// Webhook wire constants, chosen to match what MicroMDM and NanoMDM receivers
// already expect.
const (
	// ContentType is what both references send.
	ContentType = "application/json; charset=utf-8"
	// HMACHeader carries a base64 SHA-256 HMAC of the body, as NanoMDM's
	// WithHMACSecret does.
	HMACHeader = "X-Hmac-Signature"
	// DefaultMaxResponse bounds what is read back from the receiver. Neither
	// reference reads the body at all, so neither bounds it either; we read a
	// little to put the receiver's complaint in the log.
	DefaultMaxResponse = 4 << 10
	// DefaultTimeout bounds one delivery attempt.
	DefaultTimeout = 10 * time.Second
	// DefaultRetries is how many times a delivery is retried.
	DefaultRetries = 2
)

// ErrWebhookConfig reports an unusable webhook configuration.
var ErrWebhookConfig = errors.New("sink: invalid webhook configuration")

// errStatus reports a receiver that refused a delivery.
var errStatus = errors.New("sink: webhook rejected")

// WebhookConfig configures Webhook. URL is required.
type WebhookConfig struct {
	// URL receives the POST.
	URL string
	// Registry decides what each event may publish. Default applies when nil.
	Registry *Registry
	// Client sends the request; http.DefaultClient with DefaultTimeout when
	// nil.
	Client *http.Client
	// HMACKey signs the body into HMACHeader when set.
	HMACKey []byte
	// Retries is how many times a failed delivery is retried. Negative means
	// none; zero takes DefaultRetries.
	Retries int
	// Backoff is the pause before the first retry, doubling after that.
	Backoff time.Duration
	Clock   clock.Clock
	Logger  *slog.Logger
}

// envelope is the MicroMDM webhook event shape: topic, event id, timestamp,
// and one of two payload objects. Receivers written for MicroMDM or NanoMDM
// read this without modification.
type envelope struct {
	Topic       string     `json:"topic"`
	EventID     string     `json:"event_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Checkin     *checkinEv `json:"checkin_event,omitempty"`
	Acknowledge *ackEv     `json:"acknowledge_event,omitempty"`
}

// checkinEv and ackEv carry no raw_payload field, which is the one place this
// envelope departs from MicroMDM's. See Webhook.
type checkinEv struct {
	UDID         string         `json:"udid,omitempty"`
	EnrollmentID string         `json:"enrollment_id,omitempty"`
	Fields       map[string]any `json:"fields,omitempty"`
}

type ackEv struct {
	UDID        string         `json:"udid,omitempty"`
	CommandUUID string         `json:"command_uuid,omitempty"`
	Status      string         `json:"status,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}

// Webhook returns a handler that POSTs each event to a receiver.
//
// The envelope is MicroMDM's -- topic, event_id, created_at, and one of
// checkin_event or acknowledge_event -- so a receiver written for MicroMDM or
// NanoMDM reads the shape it expects. It departs from them in exactly one
// way, deliberately: there is no raw_payload.
//
// Both references set raw_payload to the base64 of the originating check-in
// body. For a TokenUpdate that body contains UnlockToken, the secret that
// clears a device passcode, along with the push token, PushMagic and the
// user's names, so every one of those reaches the receiver and every hop in
// front of it. This package publishes projected fields instead. There is no
// option to restore it: the events carry parsed messages rather than the
// bytes, and re-plumbing the raw body through the bus to offer the switch
// would be reintroducing the leak in order to make it configurable. A
// receiver that parses the plist itself needs the check-in handler, not a
// webhook.
//
// Delivery must not be on the bus's synchronous path. NanoMDM sends inside the
// check-in handler, so a slow receiver delays every device check-in; subscribe
// this to a bus built with event.WithAsync, which internal/app does.
func Webhook(cfg WebhookConfig) (event.Handler, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("%w: no URL", ErrWebhookConfig)
	}
	// Parsed here rather than at delivery, so an unusable URL fails the
	// build instead of failing every event quietly for the life of the
	// process.
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWebhookConfig, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme %q is not http or https", ErrWebhookConfig, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: no host in %q", ErrWebhookConfig, cfg.URL)
	}
	if cfg.Registry == nil {
		cfg.Registry = Default()
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: DefaultTimeout}
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Retries == 0 {
		cfg.Retries = DefaultRetries
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 100 * time.Millisecond
	}
	return func(ctx context.Context, e event.Event) error {
		body, err := json.Marshal(build(cfg, e))
		if err != nil {
			return fmt.Errorf("sink: encode webhook event: %w", err)
		}
		return deliver(ctx, cfg, body)
	}, nil
}

// build turns an event into the MicroMDM envelope. Command results become the
// acknowledge event, everything else the check-in event, which is the split
// both references make.
func build(cfg WebhookConfig, e event.Event) envelope {
	rec := cfg.Registry.Project(e)
	env := envelope{Topic: "mdm." + rec.Type, CreatedAt: rec.At}
	if env.CreatedAt.IsZero() {
		env.CreatedAt = cfg.Clock.Now()
	}
	if e.Type == event.CommandResult || e.Type == event.CommandSent || e.Type == event.CommandQueued {
		ack := &ackEv{UDID: rec.ID, Fields: rec.Fields}
		if v, ok := rec.Fields["command_uuid"].(string); ok {
			ack.CommandUUID = v
		}
		if v, ok := rec.Fields["status"].(string); ok {
			ack.Status = v
		}
		env.Acknowledge = ack
		return env
	}
	env.Checkin = &checkinEv{UDID: rec.ID, EnrollmentID: rec.Parent, Fields: rec.Fields}
	return env
}

func deliver(ctx context.Context, cfg WebhookConfig, body []byte) error {
	var last error
	wait := cfg.Backoff
	for attempt := 0; attempt <= cfg.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("sink: webhook: %w", ctx.Err())
			case <-cfg.Clock.After(wait):
			}
			wait *= 2
		}
		last = post(ctx, cfg, body)
		if last == nil {
			return nil
		}
		cfg.Logger.WarnContext(ctx, "sink: webhook delivery failed", "attempt", attempt+1, "error", last)
	}
	return last
}

func post(ctx context.Context, cfg WebhookConfig, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sink: webhook request: %w", err)
	}
	req.Header.Set("Content-Type", ContentType)
	if len(cfg.HMACKey) > 0 {
		mac := hmac.New(sha256.New, cfg.HMACKey)
		mac.Write(body)
		req.Header.Set(HMACHeader, base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	resp, err := cfg.Client.Do(req)
	if err != nil {
		return fmt.Errorf("sink: webhook post: %w", err)
	}
	defer resp.Body.Close()
	// Read a bounded amount so the receiver's complaint reaches the log
	// without letting it stream at us.
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, DefaultMaxResponse))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s: %s", errStatus, resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}
