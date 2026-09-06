package dep

import (
	"context"
	"maps"
	"time"

	"github.com/deploymenttheory/go-apple-dm/paging"
)

// AccountState records why an account cannot be used until an
// administrator acts. Both flags are set by a definitive failure of
// /session and cleared only by a definitive success.
type AccountState struct {
	// TermsExpired is set after 403 T_C_NOT_SIGNED: updated terms await
	// acceptance in Apple Business Manager or Apple School Manager.
	TermsExpired bool
	// TokenInvalid is set after 401 from /session: the tokens were revoked
	// or a new token was generated in the portal.
	TokenInvalid bool
}

// Account is one DEP MDM server: the OAuth 1.0a tokens (sealed at rest
// through storage/crypt), what GET /account reported, the profile the
// assigner targets, and the account state.
type Account struct {
	Name string
	// ConsumerKey identifies the token; the three secrets are sealed.
	ConsumerKey    string
	ConsumerSecret string
	AccessToken    string
	AccessSecret   string
	// AccessTokenExpiry is the token file's access_token_expiry; nil when
	// the file did not carry one.
	AccessTokenExpiry *time.Time
	// ProtocolVersion overrides DefaultProtocolVersion when positive.
	ProtocolVersion int
	// Recorded from GET /account when the token was stored.
	OrgName    string
	OrgID      string
	ServerName string
	ServerUUID string
	AdminID    string
	// Limits maps endpoint URI to its page limit from the account detail.
	Limits map[string]Limit
	// ProfileUUID is the profile the assigner keeps every device on; empty
	// disables assignment.
	ProfileUUID string
	State       AccountState
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Tokens returns the OAuth credentials.
func (a *Account) Tokens() Tokens {
	return Tokens{ConsumerKey: a.ConsumerKey, ConsumerSecret: a.ConsumerSecret, AccessToken: a.AccessToken, AccessSecret: a.AccessSecret, AccessTokenExpiry: a.AccessTokenExpiry}
}

// SetTokens replaces the OAuth credentials.
func (a *Account) SetTokens(t Tokens) {
	a.ConsumerKey, a.ConsumerSecret, a.AccessToken, a.AccessSecret, a.AccessTokenExpiry = t.ConsumerKey, t.ConsumerSecret, t.AccessToken, t.AccessSecret, t.AccessTokenExpiry
}

// HasTokens reports whether every credential is present.
func (a *Account) HasTokens() bool { return a.Tokens().Validate() == nil }

// Protocol returns the X-Server-Protocol-Version to send.
func (a *Account) Protocol() int {
	if a.ProtocolVersion > 0 {
		return a.ProtocolVersion
	}
	return DefaultProtocolVersion
}

// Limit returns the page limit for uri: the account detail's maximum,
// else its default, else fallback.
func (a *Account) Limit(uri string, fallback int) int {
	if l, ok := a.Limits[uri]; ok {
		if l.Maximum > 0 {
			return l.Maximum
		}
		if l.Default > 0 {
			return l.Default
		}
	}
	return fallback
}

// Clone returns a deep copy.
func (a *Account) Clone() *Account {
	out := *a
	out.Limits = maps.Clone(a.Limits)
	if a.AccessTokenExpiry != nil {
		t := *a.AccessTokenExpiry
		out.AccessTokenExpiry = &t
	}
	return &out
}

// Phase is where the syncer is: fetching the full list or syncing
// changes since a cursor.
type Phase string

// Phases of the fetch-then-sync state machine.
const (
	PhaseFetch Phase = "fetch"
	PhaseSync  Phase = "sync"
)

// Cursor is the persisted position of one account's device sync. Value is
// Apple's opaque cursor; UpdatedAt is when it was received, which is what
// the 7-day expiry is measured from; Phase says whether the next call is
// a fetch or a sync.
type Cursor struct {
	Value        string
	Phase        Phase
	FetchedUntil *time.Time
	UpdatedAt    time.Time
}

// IsZero reports whether no cursor is stored.
func (c Cursor) IsZero() bool { return c.Value == "" && c.Phase == "" && c.UpdatedAt.IsZero() }

// StoredDevice is a device as the store keeps it: the last wire record,
// whether it is tombstoned by a deleted op, and when it was written.
type StoredDevice struct {
	Account string
	Device
	// Deleted is set by an op_type of deleted and cleared when the serial
	// is seen again.
	Deleted   bool
	FirstSeen time.Time
	UpdatedAt time.Time
}

// DeviceQuery filters ListDevices.
type DeviceQuery struct {
	// IncludeDeleted lists tombstoned devices too.
	IncludeDeleted bool
	// ProfileUUID limits the list to devices carrying that profile.
	ProfileUUID string
}

// Assignment is the recorded outcome of the last profile assignment
// attempt for one serial.
type Assignment struct {
	Account      string
	SerialNumber string
	ProfileUUID  string
	// Status is SUCCESS, FAILED, NOT_ACCESSIBLE, or THROTTLED.
	Status    string
	Attempts  int
	LastError string
	// AttemptedAt is the last attempt; NextAttemptAt is zero once the
	// assignment succeeded.
	AttemptedAt   time.Time
	NextAttemptAt time.Time
}

// AssignmentQuery filters ListAssignments.
type AssignmentQuery struct {
	// Status limits the list to one outcome.
	Status string
}

// Stage names one of the two token PKI keypair slots.
type Stage string

// Keypair stages: the staged pair whose certificate was uploaded to the
// portal and the current pair whose token is in use.
const (
	StageStaged  Stage = "staged"
	StageCurrent Stage = "current"
)

// Keypair is a token PKI keypair as PEM; KeyPEM is sealed at rest.
type Keypair struct {
	CertPEM   []byte
	KeyPEM    []byte
	CreatedAt time.Time
}

// Clone returns a deep copy.
func (k *Keypair) Clone() *Keypair {
	return &Keypair{CertPEM: append([]byte(nil), k.CertPEM...), KeyPEM: append([]byte(nil), k.KeyPEM...), CreatedAt: k.CreatedAt}
}

// AccountStore persists accounts, their state, and their token PKI
// keypairs.
type AccountStore interface {
	// PutAccount upserts by Name. CreatedAt is kept on update; UpdatedAt is
	// taken from the value given.
	PutAccount(ctx context.Context, a *Account) error
	// GetAccount returns ErrNotFound for an unknown name.
	GetAccount(ctx context.Context, name string) (*Account, error)
	// DeleteAccount removes the account with its session, cursor,
	// keypairs, devices, profiles, and assignments. ErrNotFound when absent.
	DeleteAccount(ctx context.Context, name string) error
	// ListAccounts pages by name.
	ListAccounts(ctx context.Context, p paging.Page) (paging.Result[Account], error)
	// SetAccountState replaces the state flags. ErrNotFound when absent.
	SetAccountState(ctx context.Context, name string, s AccountState) error
	// PutKeypair stores the keypair in the stage, replacing any there. The
	// account need not exist yet: the staged pair precedes the token.
	PutKeypair(ctx context.Context, name string, stage Stage, kp *Keypair) error
	// Keypair returns the pair in the stage or ErrNotFound.
	Keypair(ctx context.Context, name string, stage Stage) (*Keypair, error)
	// UpstageKeypair moves the staged pair to current, replacing the current
	// pair, and clears the staged slot, all at once. ErrNotFound when
	// nothing is staged.
	UpstageKeypair(ctx context.Context, name string) error
}

// SessionStore persists the X-ADM-Auth-Session token per account so
// every process shares one session.
type SessionStore interface {
	// Session returns the stored token or "" when none.
	Session(ctx context.Context, name string) (string, error)
	// SetSession replaces the token; "" clears it.
	SetSession(ctx context.Context, name, token string) error
}

// CursorStore persists the sync cursor per account.
type CursorStore interface {
	// Cursor returns the zero Cursor when none is stored.
	Cursor(ctx context.Context, name string) (Cursor, error)
	// SetCursor replaces the cursor; the zero Cursor clears it.
	SetCursor(ctx context.Context, name string, c Cursor) error
}

// DeviceStore persists what the fetch and sync pages report.
type DeviceStore interface {
	// PutDevices upserts every device by serial: a deleted op tombstones
	// the row, any other op or a fetch record clears the tombstone.
	// FirstSeen is kept on update; UpdatedAt is set to at.
	PutDevices(ctx context.Context, account string, devs []Device, at time.Time) error
	// GetDevice returns the row, tombstoned or not, or ErrNotFound.
	GetDevice(ctx context.Context, account, serial string) (*StoredDevice, error)
	// ListDevices pages by serial, excluding tombstones unless asked.
	ListDevices(ctx context.Context, account string, q DeviceQuery, p paging.Page) (paging.Result[StoredDevice], error)
}

// ProfileStore keeps the profiles defined through an account.
type ProfileStore interface {
	// PutProfile upserts by ProfileUUID, which must be set.
	PutProfile(ctx context.Context, account string, p *Profile) error
	GetProfile(ctx context.Context, account, uuid string) (*Profile, error)
	DeleteProfile(ctx context.Context, account, uuid string) error
	// ListProfiles pages by UUID.
	ListProfiles(ctx context.Context, account string, p paging.Page) (paging.Result[Profile], error)
}

// AssignmentStore records assignment outcomes per serial.
type AssignmentStore interface {
	// PutAssignment upserts by (Account, SerialNumber).
	PutAssignment(ctx context.Context, a *Assignment) error
	GetAssignment(ctx context.Context, account, serial string) (*Assignment, error)
	// ListAssignments pages by serial.
	ListAssignments(ctx context.Context, account string, q AssignmentQuery, p paging.Page) (paging.Result[Assignment], error)
}

// Tx is the view every store exposes inside Update.
type Tx interface {
	AccountStore
	SessionStore
	CursorStore
	DeviceStore
	ProfileStore
	AssignmentStore
}

// Store is one backend. Methods called outside Update commit on their own.
type Store interface {
	Tx
	// Update runs fn in one transaction; an error rolls everything back.
	Update(ctx context.Context, fn func(tx Tx) error) error
}
