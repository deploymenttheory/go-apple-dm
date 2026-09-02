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
	// TokenUpdateRaw is the last TokenUpdate plist as received, kept so an
	// enrollment can be replayed into another server (decision record 0017).
	TokenUpdateRaw []byte
	EnrolledAt     time.Time
	TokenUpdatedAt time.Time
	LastSeenAt     time.Time
	DisabledAt     time.Time
	// CertHash is the pinned identity certificate fingerprint (device channels).
	CertHash string
	// CertHashAt is when CertHash was pinned (zero when none).
	CertHashAt time.Time
	// BootstrapTokenAt is when the escrowed bootstrap token was stored
	// (zero when none). The token itself is read through BootstrapTokenStore.
	BootstrapTokenAt time.Time
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
	// StoreTokenUpdate records push info, the raw plist, and enables the
	// enrollment. An unlock token in msg replaces the stored one; a missing
	// one keeps it.
	StoreTokenUpdate(ctx context.Context, id mdm.EnrollmentID, push mdm.Push, msg *checkin.TokenUpdate, raw []byte, at time.Time) error
	// Disable marks the enrollment as checked out. Disabling a device
	// channel also disables the user channels whose parent it is, because a
	// checked-out device cannot carry a user channel. Records are kept.
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
	// Clear marks matching non-terminal commands cleared and returns how
	// many. Backends may apply it in batches without one enclosing
	// transaction: on error the count is what was applied so far and the
	// caller may simply retry.
	Clear(ctx context.Context, id mdm.EnrollmentID, f ClearFilter) (int64, error)
}

// PushStore returns what the push layer needs.
type PushStore interface {
	// PushInfo returns push details for the enabled enrollments among ids.
	PushInfo(ctx context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]mdm.Push, error)
}

// CertAssociation is one row of the append-only pin history: a device
// channel pinned a certificate hash at a time (decision record 0014).
type CertAssociation struct {
	ID   mdm.EnrollmentID
	Hash string
	At   time.Time
}

// CertAuthStore pins identity certificates to device-channel enrollments
// and keeps the history of every pin.
type CertAuthStore interface {
	// AssociateCert pins hash to the device channel of id at the given time
	// and appends the pair to the history. ErrConflict when the hash is
	// currently pinned to a different enrollment, including when two
	// callers race to pin the same hash.
	AssociateCert(ctx context.Context, id mdm.EnrollmentID, hash string, at time.Time) error
	// CertHash returns the pinned hash for the device channel of id, or
	// "" when none.
	CertHash(ctx context.Context, id mdm.EnrollmentID) (string, error)
	// EnrollmentByCertHash resolves a hash to the device-channel enrollment
	// that currently pins it.
	EnrollmentByCertHash(ctx context.Context, hash string) (mdm.EnrollmentID, error)
	// CertHistory returns every hash ever pinned to the device channel of
	// id, oldest first. It is empty, not ErrNotFound, for an enrollment
	// that never pinned; ErrNotFound for an unknown enrollment.
	CertHistory(ctx context.Context, id mdm.EnrollmentID) ([]CertAssociation, error)
	// CertHashHistory returns every enrollment that ever pinned hash,
	// oldest first; empty when the hash was never seen.
	CertHashHistory(ctx context.Context, hash string) ([]CertAssociation, error)
}

// BootstrapTokenStore escrows macOS bootstrap tokens (device channel).
type BootstrapTokenStore interface {
	// StoreBootstrapToken escrows token for the device channel of id and
	// records at as Enrollment.BootstrapTokenAt.
	StoreBootstrapToken(ctx context.Context, id mdm.EnrollmentID, token []byte, at time.Time) error
	// BootstrapToken returns the token or ErrNotFound.
	BootstrapToken(ctx context.Context, id mdm.EnrollmentID) ([]byte, error)
}

// PushCert is a stored APNs push certificate for one topic (decision
// record 0015). KeyPEM is empty in listings.
type PushCert struct {
	Topic    string
	CertPEM  []byte
	KeyPEM   []byte
	NotAfter time.Time
	// Version increments on every StorePushCert for the topic, so caches
	// can detect a renewal with one cheap read.
	Version   int64
	UpdatedAt time.Time
}

// PushCertStore keeps push certificates and their private keys.
type PushCertStore interface {
	// StorePushCert validates the PEM pair (key matches certificate, topic
	// in the subject UID, not expired at the given time) and upserts it.
	// An empty topic accepts the certificate's own topic; otherwise the two
	// must match. ErrInvalid for anything that fails validation. The
	// returned record carries the new Version and no KeyPEM.
	StorePushCert(ctx context.Context, topic string, certPEM, keyPEM []byte, at time.Time) (PushCert, error)
	// PushCert returns the certificate and key for topic, or ErrNotFound.
	PushCert(ctx context.Context, topic string) (*PushCert, error)
	// PushCerts lists every stored certificate by topic, without keys.
	PushCerts(ctx context.Context) ([]PushCert, error)
	// PushCertVersion returns the current Version for topic, or ErrNotFound.
	PushCertVersion(ctx context.Context, topic string) (int64, error)
}

// UserAuthState is the UserAuthenticate handshake state of one user
// channel (decision record 0016). The user's own enrollment row may not
// exist yet: the handshake precedes the user channel's TokenUpdate.
type UserAuthState struct {
	ID mdm.EnrollmentID
	// Challenge is the outstanding DigestChallenge, "" once answered or
	// cleared.
	Challenge   string
	ChallengeAt time.Time
	// AuthToken is the issued token, "" until the digest was accepted.
	AuthToken string
	TokenAt   time.Time
	// AuthenticateRaw is the first UserAuthenticate plist; DigestRaw the
	// second one carrying DigestResponse.
	AuthenticateRaw []byte
	DigestRaw       []byte
}

// UserAuthStore persists UserAuthenticate challenges and tokens per user
// channel. Every method returns ErrInvalid for a device channel and
// ErrNotFound when the parent device enrollment does not exist. The state
// is removed when the device re-enrolls.
type UserAuthStore interface {
	// StoreUserAuthChallenge records a new challenge and clears any token.
	StoreUserAuthChallenge(ctx context.Context, id mdm.EnrollmentID, challenge string, raw []byte, at time.Time) error
	// StoreUserAuthToken records the issued token and clears the challenge.
	// ErrNotFound when no challenge was issued for the user.
	StoreUserAuthToken(ctx context.Context, id mdm.EnrollmentID, token string, raw []byte, at time.Time) error
	// UserAuth returns the state or ErrNotFound.
	UserAuth(ctx context.Context, id mdm.EnrollmentID) (*UserAuthState, error)
	// ClearUserAuth removes the state; absent state is not an error.
	ClearUserAuth(ctx context.Context, id mdm.EnrollmentID) error
}

// EnrollmentExport is everything one enrollment channel needs to move to
// another backend (decision record 0017). Empty byte fields are nil.
type EnrollmentExport struct {
	Enrollment
	BootstrapToken []byte
	CertHistory    []CertAssociation
}

// MigrationStore exports and imports enrollment records between backends.
type MigrationStore interface {
	// Export pages through every enrollment with device channels before the
	// user channels that belong to them.
	Export(ctx context.Context, p Page) (Result[EnrollmentExport], error)
	// Import writes rec exactly as given (Enabled, timestamps, pin, tokens,
	// history) in one transaction, upserting by id. ErrInvalid for a user
	// channel whose parent is absent or for history rows naming another
	// enrollment; ErrConflict when CertHash is currently pinned elsewhere.
	// The command queue is not touched.
	Import(ctx context.Context, rec EnrollmentExport) error
}

// Store is everything the service layer needs from one backend.
type Store interface {
	EnrollmentStore
	CommandQueue
	PushStore
	CertAuthStore
	BootstrapTokenStore
	PushCertStore
	UserAuthStore
	MigrationStore
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
