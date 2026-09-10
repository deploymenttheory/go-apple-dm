package storage

import (
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
)

// AuthenticateChange commits a policy-authorized Authenticate and its certificate
// pin together. ExpectedHash is the pin observed during policy evaluation; an
// empty value permits only an unpinned enrollment. AllowReuse permits historical
// reuse, never a certificate currently pinned by another device.
type AuthenticateChange struct {
	ExpectedHash string
	Hash         string
	AllowReuse   bool
	Message      *checkin.Authenticate
	Raw          []byte
	At           time.Time
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
	if !e.DisabledAt.IsZero() {
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
