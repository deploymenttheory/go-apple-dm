package dep

import (
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
)

// Event types this package publishes on the event bus. They live here
// rather than in package event so the DEP feature owns its vocabulary.
const (
	// EventDeviceAdded carries a DeviceEvent for a device new to the
	// account: an added op on a sync page, or a fetch record for a serial
	// the store did not hold.
	EventDeviceAdded event.Type = "dep-device-added"
	// EventDeviceModified carries a DeviceEvent for a modified op, or a
	// fetch record for a serial already stored.
	EventDeviceModified event.Type = "dep-device-modified"
	// EventDeviceDeleted carries a DeviceEvent for a deleted op.
	EventDeviceDeleted event.Type = "dep-device-deleted"
	// EventDeviceAssigned carries an AssignmentEvent after Apple answered
	// SUCCESS for a serial.
	EventDeviceAssigned event.Type = "dep-device-assigned"
	// EventTokenExpiring carries a TokenExpiringEvent when a session is
	// requested inside the configured warning window before
	// access_token_expiry.
	EventTokenExpiring event.Type = "dep-token-expiring" // #nosec G101 -- an event name, not a credential
)

// Actor is the event actor this package uses.
const Actor = "dep"

// DeviceEvent is the Data of the device events.
type DeviceEvent struct {
	Account string
	Device  Device
	// Phase is the sync phase the record arrived in.
	Phase Phase
}

// AssignmentEvent is the Data of EventDeviceAssigned.
type AssignmentEvent struct {
	Account    string
	Assignment Assignment
}

// TokenExpiringEvent is the Data of EventTokenExpiring.
type TokenExpiringEvent struct {
	Account string
	Expiry  time.Time
}
