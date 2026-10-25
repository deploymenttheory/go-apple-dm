package managedcontent

import (
	"context"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

var (
	ErrInput          = errors.New("managedcontent: invalid request")
	ErrTarget         = errors.New("managedcontent: authenticated target unavailable or ambiguous")
	ErrUnsupported    = errors.New("managedcontent: unsupported target or delivery method")
	ErrLicense        = errors.New("managedcontent: applicable licensing is not confirmed")
	ErrBusy           = errors.New("managedcontent: worker lease is active")
	ErrFence          = errors.New("managedcontent: stale worker")
	ErrReconciliation = errors.New("managedcontent: dispatch requires reconciliation")
	ErrConflict       = errors.New("managedcontent: immutable intent or management ownership conflict")
	ErrProtocol       = errors.New("managedcontent: invalid adapter or persisted evidence")
)

// Method is explicitly selected by the caller; unsupported DDM never falls back
// to an MDM command. Switching an existing app's management path is separate work.
type Method string

const (
	MDM Method = "mdm"
	DDM Method = "ddm"
)

// Content selects one native Apps and Books asset and its expected product type.
// No URL, package bytes, subscription or inferred catalog metadata is accepted.
type Content struct {
	Asset appsbooks.Asset `json:"asset"`
	Kind  string          `json:"kind"` // Apple's App or Book.
}

// Selection contains an explicit enrollment locator and either a physical serial
// for device licensing or Apple's clientUserId for user licensing. User licensing
// does not require hardware disclosure. A supplied serial must always match.
type Selection struct {
	Enrollment   mdm.EnrollmentID `json:"enrollment"`
	Serial       string           `json:"serial"`
	ClientUserID string           `json:"clientUserId"`
}

// Target is a caller-owned authenticated enrollment observation, not an inventory
// model. The resolver must bind ClientUserID to this authenticated principal,
// report current channel/platform/version and app-installation authorization.
type Target struct {
	Enrollment   mdm.EnrollmentID `json:"enrollment"`
	Serial       string           `json:"serial"`
	ClientUserID string           `json:"clientUserId"`
	// ScopeID is the native user installation principal, not a licensing alias.
	ScopeID         string          `json:"scopeId"`
	Authenticated   bool            `json:"authenticated"`
	AllowAppInstall bool            `json:"allowAppInstall"`
	DDMEnabled      bool            `json:"ddmEnabled"`
	Support         support.Target  `json:"-"`
	Context         PlatformContext `json:"context"`
}

// PlatformContext is the saved, tagged native execution context. Resolver.Support
// is always read afresh for decisions; this snapshot does not authorize later work.
type PlatformContext struct {
	OS             support.OS      `json:"os"`
	Version        string          `json:"version"`
	Channel        support.Channel `json:"channel"`
	Supervised     bool            `json:"supervised"`
	SharedIPad     bool            `json:"sharedIPad"`
	UserEnrollment bool            `json:"userEnrollment"`
	ADE            bool            `json:"ade"`
	UserApproved   bool            `json:"userApproved"`
}

// Resolver returns candidates without selecting one. Exactly one candidate must
// match Selection.Enrollment; no ordering or most-recent enrollment heuristic is
// used. Implementations can resolve their native protocol records directly.
type Resolver interface {
	Resolve(context.Context, Selection) ([]Target, error)
}

// Licensing is directly implemented by appsbooks.Workflow. Reads remain scoped
// by the location's immutable Apple UID and MDM identity across token renewal.
type Licensing interface {
	Load(context.Context, string) (appsbooks.Location, error)
	ReadAsset(context.Context, string, appsbooks.Asset, int) (appsbooks.AssetEvidence, error)
	CreateJob(context.Context, string, uint64, string, appsbooks.JobRequest) (appsbooks.LicensingJob, error)
	LoadJob(context.Context, string, string) (appsbooks.LicensingJob, error)
	SubmitJob(context.Context, string, string, string, time.Duration) (appsbooks.LicensingJob, error)
	ConfirmAssignment(context.Context, string, appsbooks.Asset, string, string, int) (appsbooks.AssignmentEvidence, error)
}

// Document is one stable DDM declaration, including its derived server token.
type Document struct {
	Identifier  string `json:"identifier"`
	ServerToken string `json:"serverToken"`
	Raw         []byte `json:"raw"`
}

// Dispatch is immutable. MDM contains a complete typed-command plist; DDM has a
// configuration and simple activation. The adapter must atomically bind the same
// identity to the same digest and target, reject mismatches and conflicting
// management paths, and validate current authenticated target ownership again.
type Dispatch struct {
	Predecessor  *DispatchReference `json:"predecessor,omitempty"`
	ID           string             `json:"id"`
	Enrollment   mdm.EnrollmentID   `json:"enrollment"`
	Serial       string             `json:"serial"`
	ClientUserID string             `json:"clientUserId"`
	Content      Content            `json:"content"`
	Method       Method             `json:"method"`
	MDM          []byte             `json:"mdm"`
	Declarations []Document         `json:"declarations"`
	Digest       string             `json:"digest"`
}

// Observation separates queue acceptance, command acknowledgment, declaration
// publication and device-reported installation. Raw is privileged adapter/device
// evidence. Installed and Terminal require device-processing evidence, never an
// acknowledgment/publication inference. Terminal describes this operation, while
// Installed and AppVersion describe the device observation at ObservedAt.
//
// Authoritative and OwnsIdentity attest a complete read and the adapter's local
// ownership boundary. RetrySafe additionally attests atomically idempotent submit
// of this exact identity/digest/target even if an older call arrives later. A
// not-found read by itself never permits replay after uncertainty.
type Observation struct {
	Terminal             bool             `json:"terminal"`
	Found                bool             `json:"found"`
	Authoritative        bool             `json:"authoritative"`
	OwnsIdentity         bool             `json:"ownsIdentity"`
	RetrySafe            bool             `json:"retrySafe"`
	ID                   string           `json:"id"`
	Enrollment           mdm.EnrollmentID `json:"enrollment"`
	Method               Method           `json:"method"`
	Digest               string           `json:"digest"`
	ManagementMethod     Method           `json:"managementMethod"`
	CommandAcknowledged  bool             `json:"commandAcknowledged"`
	DeclarationPublished bool             `json:"declarationPublished"`
	Installed            bool             `json:"installed"`
	AppVersion           string           `json:"appVersion"`
	ObservedAt           time.Time        `json:"observedAt"`
	Raw                  []byte           `json:"raw"`
}

// Delivery owns the application's durable command queue/declaration publication.
// Submit is atomically idempotent for Dispatch.ID and its immutable digest/target;
// every same-ID mismatch must fail. Inspect must not mutate, submit, install or
// silently switch management methods. No background retry loop is imposed.
type Delivery interface {
	Submit(context.Context, Dispatch) (Observation, error)
	Inspect(context.Context, Dispatch) (Observation, error)
}

// Plan selects one location, asset, authenticated target and delivery method.
// Assign uses an empty Method and LicensingJobID. Install can name an existing
// successful job, or use fresh assignment readback alone for an existing license.
// AssignAndInstall creates its own stable licensing job. PredecessorID explicitly
// selects completed same-method work for a reinstall/update in the same native
// scope; fresh terminal readback and atomic ownership handoff remain required.
// MaxPages bounds each read.
type Plan struct {
	LocationID     string    `json:"locationId"`
	Content        Content   `json:"content"`
	Target         Selection `json:"target"`
	Method         Method    `json:"method"`
	LicensingJobID string    `json:"licensingJobId"`
	PredecessorID  string    `json:"predecessorId"`
	MaxPages       int       `json:"maxPages"`
}

// Mode selects licensing, existing-license installation, or their explicit composition.
type Mode string

const (
	AssignOnly       Mode = "assign"
	InstallOnly      Mode = "install"
	AssignAndInstall Mode = "assign-and-install"
)

// Phase describes durable workflow progress, independently of device installation.
type Phase string

const (
	Queued         Phase = "queued"
	WaitingLicense Phase = "waiting-license"
	Prepared       Phase = "prepared"
	Submitting     Phase = "submitting"
	Uncertain      Phase = "uncertain"
	Dispatched     Phase = "dispatched"
	Licensed       Phase = "licensed"
	Cancelled      Phase = "cancelled"
)

// Operation is privileged durable intent and evidence. Records never expire.
// Dispatched is not installed; observe the separate device evidence. Lease/fence
// describe local workers, not remote idempotency or authorization credentials.
type Operation struct {
	ID             string          `json:"id"`
	Mode           Mode            `json:"mode"`
	Plan           Plan            `json:"plan"`
	Phase          Phase           `json:"phase"`
	LocationUID    string          `json:"locationUid"`
	MDMID          string          `json:"mdmId"`
	LicensingJobID string          `json:"licensingJobId"`
	Target         Target          `json:"target"`
	License        *LicenseStatus  `json:"license,omitempty"`
	Dispatch       *DispatchStatus `json:"dispatch,omitempty"`
	Observation    *DeliveryStatus `json:"observation,omitempty"`
	Worker         string          `json:"worker"`
	Fence          uint64          `json:"fence"`
	LeaseUntil     time.Time       `json:"leaseUntil"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	RecoveryNote   string          `json:"recoveryNote"`
	SupersededBy   string          `json:"supersededBy"`
	Blocked        string          `json:"blocked"`
}

// LicenseStatus is a historical observation, not a promise that Apple cannot
// subsequently revoke a license. Source pages require LoadEvidence.
type LicenseStatus struct {
	Confirmed  bool      `json:"confirmed"`
	ObservedAt time.Time `json:"observedAt"`
}

// DispatchStatus identifies immutable work without exposing its raw wire payload.
type DispatchStatus struct {
	ID     string `json:"id"`
	Method Method `json:"method"`
	Digest string `json:"digest"`
}

// DeliveryStatus carries device evidence independently of licensing completion.
// Raw device/adapter data and arbitrary error strings are never included.
type DeliveryStatus struct {
	Terminal             bool      `json:"terminal"`
	CommandAcknowledged  bool      `json:"commandAcknowledged"`
	DeclarationPublished bool      `json:"declarationPublished"`
	Installed            bool      `json:"installed"`
	AppVersion           string    `json:"appVersion"`
	ObservedAt           time.Time `json:"observedAt"`
}

// Evidence is explicitly privileged: exact source pages, command/declaration
// payloads and raw adapter/device data can contain identifiers and credentials.
// Loading an ordinary Operation never implicitly returns these records.
type Evidence struct {
	Handoff       *HandoffEvidence              `json:"handoff,omitempty"`
	DeclarationID string                        `json:"declarationId"`
	PhysicalLane  string                        `json:"physicalLane"`
	Asset         *appsbooks.AssetEvidence      `json:"asset,omitempty"`
	Assignment    *appsbooks.AssignmentEvidence `json:"assignment,omitempty"`
	Dispatch      *Dispatch                     `json:"dispatch,omitempty"`
	Observation   *Observation                  `json:"observation,omitempty"`
}

// HandoffEvidence retains the exact owned predecessor and fresh terminal device
// proof used to promote an explicitly named same-method successor.
type HandoffEvidence struct {
	ID           string      `json:"id"`
	PhysicalLane string      `json:"physicalLane"`
	Dispatch     Dispatch    `json:"dispatch"`
	Observation  Observation `json:"observation"`
}

// DispatchReference authorizes an atomic same-method native ownership handoff.
// Adapters recheck this exact predecessor identity/digest/target and terminal
// processing before accepting a successor; it is not permission to switch methods.
type DispatchReference struct {
	ID         string           `json:"id"`
	Digest     string           `json:"digest"`
	Enrollment mdm.EnrollmentID `json:"enrollment"`
}
