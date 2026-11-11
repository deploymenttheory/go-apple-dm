package axm

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Notification types differ from the upper-case subscription event selectors.
const (
	NotificationDeviceChanged     = "orgDeviceAttributeChanged"
	NotificationMigrationChanged  = "orgDeviceAttributeMigrationDetailsChanged"
	NotificationAssignmentChanged = "orgDeviceRelationshipAssignedServerChanged"
	NotificationWarrantyChanged   = "orgDeviceRelationshipWarrantyChanged"
	NotificationPing              = "webhookPingCreated"
	MaxWebhookNotificationBytes   = 1 << 20
)

var (
	// ErrWebhookSignature reports a missing, ambiguous or invalid callback signature.
	ErrWebhookSignature = errors.New("axm: invalid webhook signature")
	// ErrWebhookNotification reports a malformed or oversized callback.
	ErrWebhookNotification = errors.New("axm: invalid webhook notification")
)

// WebhookNotification preserves an authenticated provider payload. Unknown types
// and fields remain available in Raw; they do not imply supported refresh work.
// Signatures authenticate the body, not freshness. Hosts must persist deduplication
// scoped to the configured subscription/account before acknowledging receipt.
type WebhookNotification struct {
	Raw  json.RawMessage `json:"-"`
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Version    int    `json:"version"`
		Attributes struct {
			OrgDeviceSerialNumber string    `json:"orgDeviceSerialNumber,omitempty"`
			NewMDMServerID        string    `json:"newMdmServerId,omitempty"`
			AppleCareCoverageID   string    `json:"appleCareCoverageId,omitempty"`
			CreatedDateTime       time.Time `json:"createdDateTime,omitzero"`
		} `json:"attributes"`
		Relationships map[string]Relationship `json:"relationships,omitempty"`
	} `json:"data"`
}

// DecodeWebhookNotification verifies a single x-apple-signature header against
// the exact original body before decoding JSON. It reads at most 1 MiB plus one
// byte, does not close the body and performs no persistence or network requests.
// Cancellation and read errors take precedence over payload classification.
func DecodeWebhookNotification(r *http.Request, secret []byte) (WebhookNotification, error) {
	var n WebhookNotification
	if r == nil || r.Method != http.MethodPost || len(secret) == 0 {
		return n, ErrWebhookSignature
	}
	if err := r.Context().Err(); err != nil {
		return n, err
	}
	headers := r.Header.Values("x-apple-signature")
	if len(headers) != 1 {
		return n, ErrWebhookSignature
	}
	signature, ok := strings.CutPrefix(headers[0], "hmacsha256=")
	digest, err := hex.DecodeString(signature)
	if !ok || err != nil || len(digest) != sha256.Size {
		return n, ErrWebhookSignature
	}
	if r.Body == nil {
		return n, ErrWebhookNotification
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxWebhookNotificationBytes+1))
	if err != nil {
		return n, errors.Join(ErrWebhookNotification, err)
	}
	if err := r.Context().Err(); err != nil {
		return n, err
	}
	if len(raw) > MaxWebhookNotificationBytes {
		return n, ErrWebhookNotification
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(raw)
	if !hmac.Equal(digest, mac.Sum(nil)) {
		return n, ErrWebhookSignature
	}
	if json.Unmarshal(raw, &n) != nil || n.Data.ID == "" || n.Data.Type == "" || n.Data.Version < 1 {
		return WebhookNotification{}, ErrWebhookNotification
	}
	n.Raw = append(json.RawMessage(nil), raw...)
	return n, nil
}

// DeviceResourceID reads the actual orgDevices linkage. It never substitutes a
// serial number or follows an untrusted callback URL.
func (n WebhookNotification) DeviceResourceID() string {
	var link Linkage
	if json.Unmarshal(n.Data.Relationships["instance"].Data, &link) != nil || link.Type != TypeOrgDevices {
		return ""
	}
	return link.ID
}
