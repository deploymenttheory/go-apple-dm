package appsbooks

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Notification preserves the original payload and deduplication identity.
// Persist (UID, ID) with the applied change before acknowledging with HTTP 2xx.
type Notification struct {
	ID      string         `json:"notificationId"`
	Type    string         `json:"notificationType"`
	UID     string         `json:"uId"`
	Payload jsontext.Value `json:"notification"`
}

// AssetManagementNotification may be only one batch of an event. Check Result
// and each failure; one SUCCESS notification does not complete the whole event.
type AssetManagementNotification struct {
	EventID     string       `json:"eventId"`
	Result      string       `json:"result"`
	Type        string       `json:"type"`
	Assignments []Assignment `json:"assignments"`
	Failures    []Failure    `json:"failures,omitempty"`
}

// UserManagementNotification carries creation, update or retirement outcomes.
type UserManagementNotification struct {
	EventID  string    `json:"eventId"`
	Result   string    `json:"result"`
	Type     string    `json:"type"`
	Users    []User    `json:"users"`
	Failures []Failure `json:"failures,omitempty"`
}

// UserAssociatedNotification carries users who accepted their invitations.
type UserAssociatedNotification struct {
	Users []User `json:"associatedUsers"`
}

// AssetCountNotification is a delta, not a complete inventory count.
type AssetCountNotification struct {
	Asset
	CountDelta int64 `json:"countDelta"`
}

// DecodeNotification authenticates the bearer token and expected library before
// returning data, including TEST_NOTIFICATION. Unknown types remain raw for
// forward compatibility. It reads at most 32 MiB and does not close the body.
// Use a dedicated token per location; expectedUID and token must both be set.
func DecodeNotification(r *http.Request, token, expectedUID string) (Notification, error) {
	var out Notification
	if r == nil || token == "" || expectedUID == "" || r.Method != http.MethodPost {
		return out, ErrNotificationAuth
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return out, ErrNotificationAuth
	}
	scheme, provided, ok := strings.Cut(values[0], " ")
	wantHash, gotHash := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(provided))
	if !ok || !strings.EqualFold(scheme, "Bearer") ||
		subtle.ConstantTimeCompare(wantHash[:], gotHash[:]) != 1 {
		return out, ErrNotificationAuth
	}
	if r.Body == nil {
		return out, ErrProtocol
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return out, fmt.Errorf("appsbooks: read notification: %w", err)
	}
	if len(data) > maxBody || json.Unmarshal(data, &out) != nil || out.ID == "" || out.Type == "" ||
		len(out.Payload) == 0 {
		return Notification{}, ErrProtocol
	}
	if out.UID != expectedUID {
		return Notification{}, ErrLocation
	}
	return out, nil
}

// Decode decodes an authenticated notification's payload into one of the typed
// notification bodies above. The caller selects the type using Notification.Type.
func (n Notification) Decode(out any) error {
	if err := json.Unmarshal(n.Payload, out); err != nil {
		return fmt.Errorf("%w: invalid notification payload", ErrProtocol)
	}
	return nil
}
