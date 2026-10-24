package source

import (
	"crypto/sha256"
	"encoding/hex"
)

// Reference identifies a resource within an independently authenticated source.
// Kind describes the protocol evidence, AccountID scopes a remote account and
// ResourceID retains its opaque resource or enrollment/channel identifier.
type Reference struct {
	Kind       string `json:"kind"`
	AccountID  string `json:"account_id,omitempty"`
	ResourceID string `json:"resource_id"`
}

// Key preserves the inventory storage key format. It is not an Apple identifier,
// authentication credential or alternative physical-device correlation key.
func (r Reference) Key() string {
	h := sha256.Sum256([]byte(r.Kind + "\x00" + r.AccountID + "\x00" + r.ResourceID))
	return hex.EncodeToString(h[:])
}
