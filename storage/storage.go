// Package storage defines the persistence interfaces the service layer uses,
// split by concern (decision record 0005). Backends live in sub-packages
// (inmem, sqlite, postgres, mysql) and must pass the storagetest suites.
//
// Apple documentation on what an enrollment must retain:
// https://developer.apple.com/documentation/devicemanagement/check-in
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
)

// Errors shared by every backend.
var (
	ErrNotFound = errors.New("storage: not found")
	ErrDisabled = errors.New("storage: enrollment disabled")
	ErrConflict = errors.New("storage: conflict")
	ErrInvalid  = errors.New("storage: invalid argument")
)

// DeviceInfo is the subset of Authenticate worth indexing.
type DeviceInfo struct {
	SerialNumber string
	Model        string
	ModelName    string
	DeviceName   string
	ProductName  string
	OSVersion    string
	BuildVersion string
	IMEI         string
	MEID         string
	Topic        string
}

// Enrollment is one channel of one enrollment as the server knows it.
type Enrollment struct {
	ID mdm.EnrollmentID
	// Enabled becomes true on TokenUpdate and false on CheckOut or a new
	// Authenticate; only enabled enrollments receive commands and pushes.
	Enabled bool
	Push    mdm.Push
	Device  DeviceInfo
	// User names are set on user channels.
	UserShortName string
	UserLongName  string
	// UnlockToken from TokenUpdate (macOS), if the device sent one.
	UnlockToken []byte
	// AuthenticateRaw is the last Authenticate plist as received.
	AuthenticateRaw []byte
	EnrolledAt      time.Time
	TokenUpdatedAt  time.Time
	LastSeenAt      time.Time
	DisabledAt      time.Time
	// CertHash is the pinned identity certificate fingerprint (device channels).
	CertHash string
}

// EnrollmentQuery filters List. Zero values mean "any".
type EnrollmentQuery struct {
	Channel  mdm.Channel
	Enabled  *bool
	ParentID string
}

// Page requests one page of results. An empty Cursor starts from the
// beginning; Limit <= 0 uses the backend default.
type Page struct {
	Cursor string
	Limit  int
}

// Result is one page of items with the cursor for the next page ("" at
// the end).
type Result[T any] struct {
	Items      []T
	NextCursor string
}

// DefaultPageSize applies when Page.Limit is not positive.
const DefaultPageSize = 100

// EnrollmentStore persists enrollment records.
type EnrollmentStore interface {
	// UpsertAuthenticate records an Authenticate message. It creates the
	// record or resets an existing one: push info, unlock token, bootstrap
	// token, certificate association, and the pending command queue are
	// cleared so a re-enrollment never inherits the previous identity's
	// state. The enrollment stays disabled until TokenUpdate.
	UpsertAuthenticate(ctx context.Context, id mdm.EnrollmentID, msg *checkin.Authenticate, raw []byte, at time.Time) error
	// StoreTokenUpdate records push info and enables the enrollment.
	StoreTokenUpdate(ctx context.Context, id mdm.EnrollmentID, push mdm.Push, msg *checkin.TokenUpdate, at time.Time) error
	// Disable marks the enrollment as checked out. Its record is kept.
	Disable(ctx context.Context, id mdm.EnrollmentID, at time.Time) error
	// Get returns the record or ErrNotFound.
	Get(ctx context.Context, id mdm.EnrollmentID) (*Enrollment, error)
	// List pages through enrollments ordered by id.
	List(ctx context.Context, q EnrollmentQuery, p Page) (Result[Enrollment], error)
	// TouchLastSeen records device activity.
	TouchLastSeen(ctx context.Context, id mdm.EnrollmentID, at time.Time) error
}

// State of a queued command.
type State string

// Command states.
const (
	StatePending      State = "pending"      // never delivered
	StateSent         State = "sent"         // delivered, awaiting a result
	StateNotNow       State = "not-now"      // device answered NotNow; retry after NotNowUntil
	StateAcknowledged State = "acknowledged" // terminal
	StateError        State = "error"        // terminal: Error or CommandFormatError
	StateCleared      State = "cleared"      // terminal: removed by Clear
)

// Terminal reports whether the state is final.
func (s State) Terminal() bool {
	return s == StateAcknowledged || s == StateError || s == StateCleared
}

// QueuedCommand is a command with its delivery state for one enrollment.
type QueuedCommand struct {
	Command     mdm.Command
	State       State
	DedupeKey   string
	EnqueuedAt  time.Time
	LastSentAt  time.Time
	NotNowUntil time.Time
	// Attempts counts deliveries; NotNowCount counts NotNow answers and
	// drives the backoff.
	Attempts    int
	NotNowCount int
	CompletedAt time.Time
	Result      *mdm.Response
}

// EnqueueOptions tune Enqueue.
type EnqueueOptions struct {
	// DedupeKey skips enrollments that already have a non-terminal command
	// with the same key (for example one DeclarativeManagement kick).
	DedupeKey string
	// Now stamps EnqueuedAt; zero means time.Now().
	Now time.Time
}

// EnqueueResult reports per-enrollment outcomes.
type EnqueueResult struct {
	Queued  []mdm.EnrollmentID
	Skipped map[mdm.EnrollmentID]error
}

// ClearFilter selects commands for Clear. Zero values mean "any".
type ClearFilter struct {
	States      []State // default: every non-terminal state
	RequestType string
	Before      time.Time // enqueued before this time
}

// CommandQuery filters Commands.
type CommandQuery struct {
	States      []State
	RequestType string
}

// NotNowBackoff is the default retry delay after the nth NotNow (1-based):
// 30s, 1m, 2m, 4m, ... capped at 1h.
func NotNowBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := 30 * time.Second
	for i := 1; i < attempt && d < time.Hour; i++ {
		d *= 2
	}
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// CommandQueue persists commands per enrollment.
type CommandQueue interface {
	// Enqueue queues cmd for each enrollment. Disabled or unknown
	// enrollments are reported in Skipped, not as an error.
	Enqueue(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, o EnqueueOptions) (EnqueueResult, error)
	// Next returns the next command to deliver, in enqueue order: pending
	// and sent commands, plus NotNow commands whose backoff elapsed unless
	// skipNotNow is set (the device just said NotNow). It marks the command
	// sent. nil, nil when the queue is empty.
	Next(ctx context.Context, id mdm.EnrollmentID, skipNotNow bool, now time.Time) (*mdm.Command, error)
	// StoreResult records the device's response for the command it names.
	// Unknown CommandUUIDs return ErrNotFound.
	StoreResult(ctx context.Context, id mdm.EnrollmentID, resp *mdm.Response, now time.Time) error
	// Commands pages through an enrollment's commands, newest first.
	Commands(ctx context.Context, id mdm.EnrollmentID, q CommandQuery, p Page) (Result[QueuedCommand], error)
	// Clear marks matching non-terminal commands cleared and returns how many.
	Clear(ctx context.Context, id mdm.EnrollmentID, f ClearFilter) (int64, error)
}

// PushStore returns what the push layer needs.
type PushStore interface {
	// PushInfo returns push details for the enabled enrollments among ids.
	PushInfo(ctx context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]mdm.Push, error)
}

// CertAuthStore pins identity certificates to device-channel enrollments.
type CertAuthStore interface {
	// AssociateCert pins hash to the device channel of id. ErrConflict when
	// the hash is pinned to a different enrollment.
	AssociateCert(ctx context.Context, id mdm.EnrollmentID, hash string, at time.Time) error
	// CertHash returns the pinned hash for the device channel of id, or
	// "" when none.
	CertHash(ctx context.Context, id mdm.EnrollmentID) (string, error)
	// EnrollmentByCertHash resolves a hash to its device-channel enrollment.
	EnrollmentByCertHash(ctx context.Context, hash string) (mdm.EnrollmentID, error)
}

// BootstrapTokenStore escrows macOS bootstrap tokens (device channel).
type BootstrapTokenStore interface {
	StoreBootstrapToken(ctx context.Context, id mdm.EnrollmentID, token []byte, at time.Time) error
	// BootstrapToken returns the token or ErrNotFound.
	BootstrapToken(ctx context.Context, id mdm.EnrollmentID) ([]byte, error)
}

// Store is everything the service layer needs from one backend.
type Store interface {
	EnrollmentStore
	CommandQueue
	PushStore
	CertAuthStore
	BootstrapTokenStore
}

// DeviceInfoFromAuthenticate extracts the indexed fields.
func DeviceInfoFromAuthenticate(m *checkin.Authenticate) DeviceInfo {
	if m == nil {
		return DeviceInfo{}
	}
	d := DeviceInfo{Topic: m.Topic, Model: m.Model, ModelName: m.ModelName, DeviceName: m.DeviceName}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&d.SerialNumber, m.SerialNumber)
	set(&d.ProductName, m.ProductName)
	set(&d.OSVersion, m.OSVersion)
	set(&d.BuildVersion, m.BuildVersion)
	set(&d.IMEI, m.IMEI)
	set(&d.MEID, m.MEID)
	return d
}
