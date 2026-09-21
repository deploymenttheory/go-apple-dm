package storage

import (
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
)

// AuthenticateChange commits a policy-authorized Authenticate and its certificate
// pin together. ExpectedHash is the pin observed during policy evaluation; an
// empty value permits only an unpinned enrollment. AllowReuse permits historical
// reuse, never a certificate currently pinned by another device.
type AuthenticateChange struct {
	ExpectedHash string
	Hash         string
	AllowReuse   bool
	// AllowReenroll permits a disabled device to start a new enrollment after
	// policy approval. It requires a different, nonempty identity certificate;
	// replaying the old identity can never reactivate a disabled enrollment.
	AllowReenroll bool
	Message       *checkin.Authenticate
	Raw           []byte
	At            time.Time
	// Result optionally receives the authoritative reset/retry classification on
	// success. Use a fresh result for each call and ignore it when the call fails.
	// Older implementations may leave Known false. A successful operation inside
	// a caller's transaction remains subject to that transaction's final outcome.
	Result *AuthenticateResult
}

// AuthenticateResult describes the transition selected under the store's write
// lock. Known distinguishes an authoritative result from an older store that
// does not report one. Reset is false for an idempotent same-certificate retry.
// This result does not assert that an enclosing transaction has committed.
type AuthenticateResult struct {
	Known bool
	Reset bool
}

// CheckAuthenticate checks an enrollment under the backend's write lock. A true
// result denotes an idempotent retry whose existing state must be preserved.
func CheckAuthenticate(id mdm.EnrollmentID, e *Enrollment, c AuthenticateChange) (bool, error) {
	if err := id.Validate(); err != nil || id.Channel.IsUser() || c.At.IsZero() {
		return false, fmt.Errorf(
			"%w: authentication requires a device identity and timestamp",
			ErrInvalid,
		)
	}
	if e == nil {
		if c.ExpectedHash != "" {
			return false, ErrConflict
		}
		return false, nil
	}
	if e.ID != id {
		return false, ErrConflict
	}
	if !e.DisabledAt.IsZero() && (!c.AllowReenroll || c.Hash == "" || c.Hash == e.CertHash) {
		return false, ErrDisabled
	}
	if c.Hash != "" && e.CertHash == c.Hash {
		return true, nil
	}
	if e.CertHash != c.ExpectedHash {
		return false, ErrConflict
	}
	return false, nil
}
