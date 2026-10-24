package inventory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// Domain errors are shared by persistence implementations and callers.
var (
	ErrNotFound = errors.New("inventory: not found")
	ErrInvalid  = errors.New("inventory: invalid argument")
	ErrConflict = errors.New("inventory: conflicting identity or revision")
	ErrStopped  = errors.New("inventory: job stopped")
	ErrLease    = errors.New("inventory: worker lease lost")
)

// Observation retains one successful original and its observation provenance.
type Observation struct {
	Source       source.Reference           `json:"source"`
	ObservedAt   time.Time                  `json:"observed_at"`
	ExpiresAt    time.Time                  `json:"expires_at,omitempty"`
	Absent       bool                       `json:"absent,omitempty"`
	Disconnected bool                       `json:"disconnected,omitempty"`
	Generation   string                     `json:"generation,omitempty"`
	FieldTimes   map[string]time.Time       `json:"field_times,omitempty"`
	Fields       map[string]json.RawMessage `json:"fields"`
	Native       *NativeIdentity            `json:"native,omitempty"`

	Raw json.RawMessage `json:"raw,omitempty"`
}

// NativeIdentity records the host's authenticated enrollment provenance. Unknown
// methods and unauthenticated reports can be retained but cannot create links.
type NativeIdentity struct {
	Serial        string             `json:"serial,omitempty"`
	Method        correlation.Method `json:"method,omitempty"`
	Authenticated bool               `json:"authenticated"`
}

// ResourceRecord retains one source identity independently of all other sources.
// Snapshot is the latest successful observation. Original successful history and
// failed attempts are stored separately; neither a refresh failure nor a different
// source can overwrite that evidence. Incomplete originals require refresh.
type ResourceRecord struct {
	ID         string           `json:"id"`
	Source     source.Reference `json:"source"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Snapshot   Observation      `json:"snapshot"`
	Incomplete bool             `json:"incomplete,omitempty"`
}

// Attempt is an independent unsuccessful refresh outcome.
type Attempt struct {
	Source source.Reference `json:"source"`
	At     time.Time        `json:"at"`
	Code   string           `json:"code"`
}

// SerialLink is an explicit, checked relationship between independent evidence.
// It never changes source identity or combines stored records.
type SerialLink struct {
	Apple    source.Reference `json:"apple"`
	Native   source.Reference `json:"native"`
	Serial   string           `json:"serial"`
	LinkedAt time.Time        `json:"linked_at"`
}

// Condition compares a data.<original path> or meta.<column> selector. Conditions
// are ANDed; unqualified paths are invalid.
// Supported operators are eq, contains (array membership), exists, lt, lte, gt, gte.
type Condition struct {
	Field    string          `json:"field"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value,omitempty"`
}

// ResourceQuery is shared by lists, reports, and exports. Source is an observation
// kind; Focus is combined, apple, or managed. Cursor is an opaque resource ID boundary.
type ResourceQuery struct {
	AccountID  string      `json:"account_id,omitempty"`
	Source     string      `json:"source,omitempty"`
	Focus      string      `json:"focus,omitempty"`
	Search     string      `json:"search,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
	Cursor     string      `json:"cursor,omitempty"`
	Limit      int         `json:"limit,omitempty"`
	AsOf       time.Time   `json:"as_of,omitempty"`
}

// ResourcePage is a bounded result and continuation for the same query.
type ResourcePage struct {
	Items      []ResourceRecord `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
	AsOf       time.Time        `json:"as_of"`
}

// ID generates an opaque local identity.
func ID() string { return rand.Text() }

// digest returns a stable bounded key for externally supplied identifiers.
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// CanonicalSerial normalizes a serial for matching, without changing original source data.
func CanonicalSerial(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// UnmarshalJSON restores byte-exact original source JSON after document decoding.
// Records predating byte retention still retain their available raw JSON.
func (o *Observation) UnmarshalJSON(raw []byte) error {
	type plain Observation
	var value struct {
		plain
		OriginalJSON []byte `json:"original_json"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if len(value.OriginalJSON) > 0 {
		if !json.Valid(value.OriginalJSON) {
			return ErrInvalid
		}
		value.Raw = append(json.RawMessage(nil), value.OriginalJSON...)
	}
	*o = Observation(value.plain)
	return nil
}

// UnmarshalJSON restores original source bytes from the internal document
// encoding. Duplicate byte bookkeeping is never part of exported API values.
func (d *ResourceRecord) UnmarshalJSON(raw []byte) error {
	type plain ResourceRecord
	var value struct {
		plain
		OriginalJSON []byte `json:"original_json"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if len(value.OriginalJSON) > 0 {
		if !json.Valid(value.OriginalJSON) {
			return ErrInvalid
		}
		value.Snapshot.Raw = append(json.RawMessage(nil), value.OriginalJSON...)
	}
	*d = ResourceRecord(value.plain)
	return nil
}
