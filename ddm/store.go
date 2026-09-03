package ddm

import (
	"context"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// Declaration is a stored declaration: the canonical JSON of
// {Identifier, Payload, Type} and the token derived from it.
type Declaration struct {
	Identifier  string
	Type        string
	Kind        schemaddm.Kind
	ServerToken string
	Canonical   []byte
	CreatedAt   time.Time
	// UpdatedAt is the last time ServerToken changed.
	UpdatedAt time.Time
}

// DeclarationVersion is one revision of a declaration, kept so a fetch can
// serve the exact bytes a manifest advertised.
type DeclarationVersion struct {
	Identifier  string
	Type        string
	ServerToken string
	Canonical   []byte
	CreatedAt   time.Time
}

// DeclarationQuery filters ListDeclarations. Zero values mean "any".
type DeclarationQuery struct {
	Kind  schemaddm.Kind
	Type  string
	InSet string
}

// Set is a named group of declarations assigned to enrollments.
type Set struct {
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SnapshotItem is one manifest entry as advertised to an enrollment.
type SnapshotItem struct {
	DeclarationRef
	// BaseToken is the stored declaration's token; ServerToken differs only
	// when an Expander rewrote the bytes for this enrollment.
	BaseToken string
	// Expanded holds the per-enrollment canonical bytes when they differ
	// from the stored declaration; nil otherwise.
	Expanded []byte
}

// Snapshot is the manifest an enrollment was last served, with the token
// it was told. TokenChangedAt is the Timestamp in the tokens response.
type Snapshot struct {
	ID                mdm.EnrollmentID
	DeclarationsToken string
	Items             []SnapshotItem
	TokenChangedAt    time.Time
	RefreshedAt       time.Time
}

// DeclarationStatus is what a device last reported about one declaration.
type DeclarationStatus struct {
	Kind        schemaddm.Kind
	Identifier  string
	ServerToken string
	Active      bool
	// Valid is unknown, invalid, or valid as the device reported it.
	Valid string
	// Reasons is the raw JSON array of reasons, nil when none.
	Reasons   []byte
	FirstSeen time.Time
	LastSeen  time.Time
}

// EnrollmentDeclarationStatus is DeclarationStatus for one enrollment,
// returned by identifier-centred queries.
type EnrollmentDeclarationStatus struct {
	ID mdm.EnrollmentID
	DeclarationStatus
}

// StatusValue is one status item value as canonical JSON, keyed by its
// dotted path (array elements keep their index as a path segment).
type StatusValue struct {
	Path      string
	Value     []byte
	FirstSeen time.Time
	LastSeen  time.Time
}

// StatusValueQuery filters StatusValues by path prefix.
type StatusValueQuery struct {
	PathPrefix string
}

// StatusError is one entry of a report's Errors array.
type StatusError struct {
	Seq        int64
	StatusItem string
	Reasons    []byte
	ReceivedAt time.Time
}

// StatusReportRecord is one raw report as received.
type StatusReportRecord struct {
	Seq        int64
	FullReport bool
	Raw        []byte
	ReceivedAt time.Time
}

// StatusUpdate is a parsed report ready for PutStatus.
type StatusUpdate struct {
	Raw        []byte
	ReceivedAt time.Time
	FullReport bool
	// HasDeclarations is false when the report carried no
	// management.declarations item, in which case declaration rows are
	// left untouched even for a full report.
	HasDeclarations bool
	Declarations    []DeclarationStatus
	Values          []StatusValue
	Errors          []StatusError
	// KeepReports bounds the raw reports retained per enrollment.
	KeepReports int
}

// StatusOutcome reports what PutStatus changed.
type StatusOutcome struct {
	Seq           int64
	Removed       []DeclarationRef
	RemovedValues []string
	PrunedReports int64
}

// Change is a pending notification for one enrollment.
type Change struct {
	Seq           int64
	ID            mdm.EnrollmentID
	Reason        string
	CreatedAt     time.Time
	Attempts      int
	LastError     string
	NextAttemptAt time.Time
}

// DeclarationStore persists declarations and their revisions.
type DeclarationStore interface {
	// PutDeclaration upserts by Identifier. changed is false when the stored
	// ServerToken already equals d.ServerToken; nothing is written then.
	// ErrConflict when the identifier exists with a different Kind. Every
	// accepted change also records a DeclarationVersion.
	PutDeclaration(ctx context.Context, d *Declaration) (changed bool, err error)
	// GetDeclaration returns the current revision or ErrNotFound.
	GetDeclaration(ctx context.Context, identifier string) (*Declaration, error)
	// GetDeclarationVersion returns one revision or ErrNotFound.
	GetDeclarationVersion(ctx context.Context, identifier, serverToken string) (*DeclarationVersion, error)
	// DeleteDeclaration removes the declaration, its versions, set
	// memberships, and direct assignments. ErrNotFound when absent.
	DeleteDeclaration(ctx context.Context, identifier string) error
	// ListDeclarations pages by identifier.
	ListDeclarations(ctx context.Context, q DeclarationQuery, p storage.Page) (storage.Result[Declaration], error)
	// PruneVersions deletes revisions that are neither current nor named by
	// a snapshot item and returns how many.
	PruneVersions(ctx context.Context) (int64, error)
}

// SetStore persists sets and their membership.
type SetStore interface {
	// PutSet creates the set; created is false when it already existed.
	PutSet(ctx context.Context, name string, at time.Time) (created bool, err error)
	// DeleteSet removes the set, its memberships, and its assignments.
	DeleteSet(ctx context.Context, name string) error
	GetSet(ctx context.Context, name string) (*Set, error)
	ListSets(ctx context.Context, p storage.Page) (storage.Result[Set], error)
	// AddSetDeclaration returns ErrNotFound when either side is unknown.
	AddSetDeclaration(ctx context.Context, set, identifier string, at time.Time) (changed bool, err error)
	RemoveSetDeclaration(ctx context.Context, set, identifier string) (changed bool, err error)
	// SetDeclarations lists member identifiers, sorted; ErrNotFound for an
	// unknown set.
	SetDeclarations(ctx context.Context, set string) ([]string, error)
	// DeclarationSets lists the sets containing identifier, sorted.
	DeclarationSets(ctx context.Context, identifier string) ([]string, error)
}

// AssignmentStore binds enrollments to sets and to single declarations.
type AssignmentStore interface {
	AssignSet(ctx context.Context, id mdm.EnrollmentID, set string, at time.Time) (changed bool, err error)
	UnassignSet(ctx context.Context, id mdm.EnrollmentID, set string) (changed bool, err error)
	EnrollmentSets(ctx context.Context, id mdm.EnrollmentID) ([]string, error)
	SetEnrollments(ctx context.Context, set string, p storage.Page) (storage.Result[mdm.EnrollmentID], error)
	AssignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string, at time.Time) (changed bool, err error)
	UnassignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string) (changed bool, err error)
	// EnrollmentDeclarations lists direct assignments only, sorted.
	EnrollmentDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]string, error)
	// StaticDeclarations is the union of direct assignments and set members,
	// deduplicated and sorted by identifier. Empty for an unknown id.
	StaticDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]Declaration, error)
	// AffectedEnrollments lists every enrollment whose static membership
	// includes any of the identifiers or sets, deduplicated and sorted.
	AffectedEnrollments(ctx context.Context, identifiers, sets []string) ([]mdm.EnrollmentID, error)
}

// SnapshotStore keeps the manifest last served per enrollment.
type SnapshotStore interface {
	// PutSnapshot replaces the snapshot and its items atomically.
	PutSnapshot(ctx context.Context, s *Snapshot) error
	Snapshot(ctx context.Context, id mdm.EnrollmentID) (*Snapshot, error)
}

// StatusStore persists what devices report.
type StatusStore interface {
	// PutStatus applies one report atomically: appends the raw report,
	// upserts declaration rows and values (LastSeen bumped, FirstSeen kept),
	// appends errors, and for a full report deletes declaration rows and
	// values absent from the update. It prunes raw reports beyond
	// KeepReports, oldest first.
	PutStatus(ctx context.Context, id mdm.EnrollmentID, u StatusUpdate) (StatusOutcome, error)
	// DeclarationStatus lists rows sorted by (kind, identifier).
	DeclarationStatus(ctx context.Context, id mdm.EnrollmentID) ([]DeclarationStatus, error)
	DeclarationStatusByIdentifier(ctx context.Context, identifier string, p storage.Page) (storage.Result[EnrollmentDeclarationStatus], error)
	// StatusValues pages by path.
	StatusValues(ctx context.Context, id mdm.EnrollmentID, q StatusValueQuery, p storage.Page) (storage.Result[StatusValue], error)
	// StatusErrors pages newest first.
	StatusErrors(ctx context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[StatusError], error)
	// StatusReports pages newest first.
	StatusReports(ctx context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[StatusReportRecord], error)
}

// ChangeStore queues notifications.
type ChangeStore interface {
	// RecordChanges appends one row per id.
	RecordChanges(ctx context.Context, ids []mdm.EnrollmentID, reason string, at time.Time) error
	// PendingChanges returns rows due at or before now, oldest first.
	PendingChanges(ctx context.Context, now time.Time, limit int) ([]Change, error)
	CompleteChanges(ctx context.Context, seqs []int64) error
	// FailChanges records the error and the next attempt time; rows are
	// never deleted by a failure.
	FailChanges(ctx context.Context, seqs []int64, msg string, nextAttempt time.Time) error
	// ChangeStats counts rows due now and rows that have failed at least
	// once.
	ChangeStats(ctx context.Context, now time.Time) (pending, failed int64, err error)
}

// Tx is the view every store exposes inside Update.
type Tx interface {
	DeclarationStore
	SetStore
	AssignmentStore
	SnapshotStore
	StatusStore
	ChangeStore
	// ClearEnrollment deletes the enrollment's sets, assignments, snapshot,
	// status, and pending changes. Absent state is not an error.
	ClearEnrollment(ctx context.Context, id mdm.EnrollmentID) error
}

// Store is one backend. Methods called outside Update commit on their own.
type Store interface {
	Tx
	// Update runs fn in one transaction; an error rolls everything back.
	Update(ctx context.Context, fn func(tx Tx) error) error
}
