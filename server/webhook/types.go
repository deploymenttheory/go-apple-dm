package webhook

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

const (
	SchemaVersion            = "1"
	InlineLimit              = 256 << 10
	DefaultMaxBody           = 16 << 20
	DefaultPayloadRetention  = 7 * 24 * time.Hour
	DefaultMetadataRetention = 30 * 24 * time.Hour
	PayloadPath              = "/webhooks/v1/payloads/"
)

var (
	ErrInvalid   = errors.New("webhook: invalid argument")
	ErrNotFound  = errors.New("webhook: not found")
	ErrForbidden = errors.New("webhook: root or receiver authority required")
	ErrConflict  = errors.New("webhook: conflicting revision or delivery state")
	ErrExpired   = errors.New("webhook: payload expired")
)

// Subject identifies a verified resource. Unverified device claims belong in Data.
type Subject struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Channel  string `json:"channel,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
}

// Payload is either inline, referenced, or explicitly unavailable. SHA256 and
// Size describe the representation fetched from Href (raw bytes or JSON).
type Payload struct {
	ContentType  string          `json:"content_type"`
	Encoding     string          `json:"encoding"`
	Availability string          `json:"availability"`
	Size         int             `json:"size"`
	SHA256       string          `json:"sha256,omitempty"`
	Value        json.RawMessage `json:"value,omitempty"`
	Href         string          `json:"href,omitempty"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
}

// Event is the immutable occurrence carried by one or more deliveries.
type Event struct {
	SchemaVersion string             `json:"schema_version"`
	EventID       string             `json:"event_id"`
	Type          string             `json:"type"`
	OccurredAt    time.Time          `json:"occurred_at"`
	Source        string             `json:"source"`
	Subject       *Subject           `json:"subject,omitempty"`
	CorrelationID string             `json:"correlation_id,omitempty"`
	Data          map[string]any     `json:"data"`
	Payloads      map[string]Payload `json:"payloads,omitempty"`
}

// PayloadPolicy is a disclosure ceiling, including when replaying older data.
type PayloadPolicy struct {
	FullJSON    bool `json:"full_json"`
	RawRequest  bool `json:"raw_request"`
	RawResponse bool `json:"raw_response"`
}

// Sensitive reports whether the policy permits complete decoded bodies or original request
// or response bytes.
func (p PayloadPolicy) Sensitive() bool { return p.FullJSON || p.RawRequest || p.RawResponse }

// allows checks whether the disclosure policy permits the named captured representation.
func (p PayloadPolicy) allows(part string) bool {
	switch part {
	case "request_raw":
		return p.RawRequest
	case "response_raw":
		return p.RawResponse
	default:
		return strings.HasSuffix(part, "_json") && p.FullJSON
	}
}

// Filters use OR within a field and AND between fields. Subject filters never
// match unverified claims or exchanges without a verified subject.
type Filters struct {
	Operations   []string `json:"operations,omitempty"`
	Outcomes     []string `json:"outcomes,omitempty"`
	Channels     []string `json:"channels,omitempty"`
	Subjects     []string `json:"subjects,omitempty"`
	CommandTypes []string `json:"command_types,omitempty"`
}

// Spec is the writable subscription configuration. URLs may contain secrets and
// are only exposed to webhook administrators, never through delivery diagnostics.
type Spec struct {
	Name    string        `json:"name"`
	URL     string        `json:"url"`
	Events  []string      `json:"events"`
	Filters Filters       `json:"filters"`
	Payload PayloadPolicy `json:"payload"`
}

// Subscription is the persisted public configuration and lifecycle state of a webhook
// destination; it excludes receiver credentials.
type Subscription struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
	Spec
	Enabled   bool      `json:"enabled"`
	Paused    bool      `json:"paused"`
	Deleted   bool      `json:"deleted"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Credentials are returned only when created or rotated; ordinary reads omit them.
type Credentials struct {
	SigningSecret string `json:"signing_secret"`
	PayloadToken  string `json:"payload_token"`
}

// Change returns a subscription mutation and, only when created or rotated, the newly
// issued receiver credentials.
type Change struct {
	Subscription Subscription `json:"subscription"`
	Credentials  *Credentials `json:"credentials,omitempty"`
}

// CatalogueEntry describes one native webhook event type and the payload representations
// available for capture.
type CatalogueEntry struct {
	Type          string   `json:"type"`
	Operations    []string `json:"operations,omitempty"`
	SummaryFields []string `json:"summary_fields"`
	FullJSON      bool     `json:"full_json"`
	Raw           bool     `json:"raw"`
}

// The list is deliberately explicit: new internal events require contract review.
var outcomes = strings.Fields(`enrollment-denied identity-rejected certificate-status-rejected private-hop-rejected enrolled reenrolled token-updated checked-out cert-rotated command-queued command-sent command-rejected command-result bootstrap-token-set push-token-invalid push-rejected ddm-changed ddm-status-received cert-reuse-denied enrollment-imported enrollment-link-redeemed user-authenticated user-auth-failed acme-challenge-valid acme-issued certificate-revoked attestation-rejected admin-action admin-denied dep-device-added dep-device-modified dep-device-deleted dep-device-assigned dep-token-expiring`)

// OutcomeType maps a reviewed internal event name to the native server event vocabulary.
// Unknown internal events return an empty string and are not captured.
func OutcomeType(internal string) string {
	if !slices.Contains(outcomes, internal) {
		return ""
	}
	return "server." + strings.ReplaceAll(internal, "-", ".")
}

// Catalogue returns a fresh copy of the native vocabulary.
func Catalogue() []CatalogueEntry {
	out := []CatalogueEntry{}
	for _, name := range outcomes {
		out = append(out, CatalogueEntry{Type: OutcomeType(name), SummaryFields: []string{"actor", "fields"}, FullJSON: true})
	}
	for family, ops := range map[string][]string{
		"mdm":        {"Authenticate", "TokenUpdate", "CheckOut", "SetBootstrapToken", "GetBootstrapToken", "GetToken", "UserAuthenticate", "ReturnToService", "connect", "unknown"},
		"ddm":        {"tokens", "declaration-items", "declaration", "status", "unknown"},
		"enrollment": {"discovery", "ade", "account-driven", "authentication", "ota"},
		"profile":    {"download"}, "scep": {"exchange"}, "acme": {"exchange"},
		"certificate_status": {"exchange"}, "content_cache": {"report"},
	} {
		out = append(out, CatalogueEntry{Type: "protocol." + family + ".exchange", Operations: ops, SummaryFields: []string{"operation", "outcome", "http", "command_uuid", "command_type", "device_status", "claimed_id"}, FullJSON: true, Raw: true})
	}
	out = append(out, CatalogueEntry{Type: "server.worker.state", SummaryFields: []string{"worker", "running"}}, CatalogueEntry{Type: "server.certificate.lifecycle", SummaryFields: []string{"operation", "certificate_id", "version", "outcome"}}, CatalogueEntry{Type: "webhook.test", SummaryFields: []string{"synthetic"}})
	slices.SortFunc(out, func(a, b CatalogueEntry) int { return strings.Compare(a.Type, b.Type) })
	return out
}

// known reports whether an event belongs to the reviewed native webhook catalogue.
func known(t string) bool {
	for _, e := range Catalogue() {
		if e.Type == t {
			return true
		}
	}
	return false
}

// eventMatch matches an event name against an exact name, a family wildcard, or the global
// wildcard.
func eventMatch(pattern, name string) bool {
	return pattern == "*" || pattern == name || strings.HasSuffix(pattern, ".*") && strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
}

// matches applies event selection and subject filters using OR within a field and AND
// between fields.
func (s Spec) matches(e Event) bool {
	matched := false
	for _, t := range s.Events {
		matched = matched || eventMatch(t, e.Type)
	}
	if !matched {
		return false
	}
	match := func(values []string, v string) bool { return len(values) == 0 || v != "" && slices.Contains(values, v) }
	value := func(key string) string { v, _ := e.Data[key].(string); return v }
	var channel, subject string
	if e.Subject != nil {
		channel, subject = e.Subject.Channel, e.Subject.ID
	}
	return match(s.Filters.Operations, value("operation")) && match(s.Filters.Outcomes, value("outcome")) && match(s.Filters.CommandTypes, value("command_type")) && match(s.Filters.Channels, channel) && match(s.Filters.Subjects, subject)
}
