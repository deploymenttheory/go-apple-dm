package inventory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Domain errors are shared by persistence implementations and callers.
var (
	ErrNotFound = errors.New("inventory: not found")
	ErrInvalid  = errors.New("inventory: invalid argument")
	ErrConflict = errors.New("inventory: conflicting identity or revision")
	ErrStopped  = errors.New("inventory: job stopped")
	ErrLease    = errors.New("inventory: worker lease lost")
)

// SourceReference identifies a resource within an independently authenticated source.
type SourceReference struct {
	Kind       string `json:"kind"`
	AccountID  string `json:"account_id,omitempty"`
	ResourceID string `json:"resource_id"`
}

// Key is a bounded, collision-resistant storage key, not an Apple identifier.
func (r SourceReference) Key() string {
	return digest(r.Kind + "\x00" + r.AccountID + "\x00" + r.ResourceID)
}

// Observation is the latest successful source snapshot, with separate attempt metadata.
type Observation struct {
	Source       SourceReference            `json:"source"`
	ObservedAt   time.Time                  `json:"observed_at"`
	AttemptedAt  time.Time                  `json:"attempted_at"`
	ExpiresAt    time.Time                  `json:"expires_at,omitempty"`
	Error        string                     `json:"error,omitempty"`
	Absent       bool                       `json:"absent,omitempty"`
	Disconnected bool                       `json:"disconnected,omitempty"`
	Generation   string                     `json:"generation,omitempty"`
	FieldTimes   map[string]time.Time       `json:"field_times,omitempty"`
	Fields       map[string]json.RawMessage `json:"fields"`
	Raw          json.RawMessage            `json:"raw,omitempty"`
}

// FieldValue records both the selected value and the evidence used to choose it.
type FieldValue struct {
	Value      json.RawMessage `json:"value"`
	Source     SourceReference `json:"source"`
	ObservedAt time.Time       `json:"observed_at"`
}

// CoveragePlan preserves every plan, including subscriptions with no expiry.
type CoveragePlan struct {
	ID         string                     `json:"id"`
	AccountID  string                     `json:"account_id"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

// CoverageSummary is derived at read time; plans and source freshness remain authoritative.
type CoverageSummary struct {
	State      string     `json:"state"`
	NextExpiry *time.Time `json:"next_expiry,omitempty"`
	Stale      bool       `json:"stale"`
}

// DeviceRecord is independent of any enrollment or Apple account membership.
type DeviceRecord struct {
	ID           string                 `json:"id"`
	SerialNumber string                 `json:"serial_number,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
	Fields       map[string]FieldValue  `json:"fields"`
	Sources      map[string]Observation `json:"sources"`
	Coverage     []CoveragePlan         `json:"coverage"`
	Conflicts    []string               `json:"conflicts,omitempty"`
}

// Condition compares a source-qualified or normalized field. Conditions are ANDed.
// Supported operators are eq, contains (array membership), exists, lt, lte, gt, gte.
type Condition struct {
	Field    string          `json:"field"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value,omitempty"`
}

// DeviceQuery is shared by lists, reports, and exports. Source is an observation
// kind; Focus is combined, apple, or managed. Cursor is an opaque device ID boundary.
type DeviceQuery struct {
	AccountID  string      `json:"account_id,omitempty"`
	Source     string      `json:"source,omitempty"`
	Focus      string      `json:"focus,omitempty"`
	Search     string      `json:"search,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
	Cursor     string      `json:"cursor,omitempty"`
	Limit      int         `json:"limit,omitempty"`
	AsOf       time.Time   `json:"as_of,omitempty"`
}

// DevicePage is a bounded result and continuation for the same query.
type DevicePage struct {
	Items      []DeviceRecord `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	AsOf       time.Time      `json:"as_of"`
}

// ID generates an opaque local identity.
func ID() string { return rand.Text() }

// digest returns a stable bounded key for externally supplied identifiers.
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// CanonicalSerial normalizes a serial for matching, without changing original source data.
func CanonicalSerial(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
