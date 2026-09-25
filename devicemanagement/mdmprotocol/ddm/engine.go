package ddm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/metric"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/telemetry"
)

// Resolver adds declarations to an enrollment's manifest dynamically, for
// example by device attribute or by an external group. Errors fail closed:
// serving that enrollment returns ErrResolver rather than a partial manifest.
type Resolver interface {
	// Resolve supplies the declaration references dynamically assigned to an enrollment.
	Resolve(ctx context.Context, id mdm.EnrollmentID) ([]string, error)
}

// Expander may rewrite a declaration's canonical bytes for one enrollment
// (variable substitution). Returning nil, or bytes equal to d.Canonical,
// means unchanged. The bytes returned must be a JSON object; they are
// canonicalised and the served token is derived from them.
type Expander interface {
	// Expand optionally supplies enrollment-specific declaration content; (nil, nil) leaves
	// the stored content unchanged.
	Expand(ctx context.Context, id mdm.EnrollmentID, d *Declaration) ([]byte, error)
}

// Subscriptions configures the synthesised status-subscriptions declaration and
// its unconditional companion activation (decision record 0021).
type Subscriptions struct {
	Enabled bool
	// Baseline is used until a device reports its capabilities; nil means
	// DefaultSubscriptionBaseline.
	Baseline []string
	// Exclude drops reported status items with these prefixes; nil means
	// DefaultSubscriptionExclude.
	Exclude []string
}

// Config builds an Engine.
type Config struct {
	// ObserveStatus projects a validated report in the enclosing event transaction.
	ObserveStatus func(context.Context, mdm.EnrollmentID, StatusUpdate) error
	Store         Store
	Resolvers     []Resolver
	Expander      Expander
	Bus           event.Publisher
	Clock         clock.Clock
	Logger        *slog.Logger
	// Telemetry supplies the meter the engine counts status reasons with. The zero
	// value counts nothing.
	Telemetry telemetry.Config
	// Target supplies the validation target for uploads; nil validates for
	// any OS.
	Target func(ctx context.Context) support.Target
	// EnrollmentTarget enables per-device compatibility checks at delivery.
	// Resolve outside store transactions. Nil preserves caller-managed routing.
	// Unknown inventory is a zero Target; lookup failures must return an error.
	EnrollmentTarget func(context.Context, mdm.EnrollmentID) (support.Target, error)
	// MaxStatusBytes bounds a status report; default 1 MiB.
	MaxStatusBytes int
	// Structural limits apply before decoding or storing a status report.
	MaxStatusDepth     int
	MaxStatusPathBytes int
	MaxStatusItems     int
	// KeepReports bounds raw status reports kept per enrollment; default 10.
	KeepReports   int
	Subscriptions Subscriptions
}

// Defaults.
const (
	DefaultMaxStatusBytes = 1 << 20
	DefaultKeepReports    = 10
)

// ErrNoStore is returned by New when Config.Store is nil.
var ErrNoStore = fault.NewOperator(fault.Internal, "a declaration store is required")

// Engine serves declarative management for enrollments.
type Engine struct {
	observeStatus                                 func(context.Context, mdm.EnrollmentID, StatusUpdate) error
	store                                         Store
	resolvers                                     []Resolver
	expander                                      Expander
	bus                                           event.Publisher
	clock                                         clock.Clock
	log                                           *slog.Logger
	target                                        func(ctx context.Context) support.Target
	enrollmentTarget                              func(context.Context, mdm.EnrollmentID) (support.Target, error)
	maxStatus                                     int
	maxStatusDepth, maxStatusPath, maxStatusItems int
	keep                                          int
	subs                                          Subscriptions
	reasons                                       metric.Int64Counter
}

// MetricStatusReasons counts the reasons devices report in status Errors, by the
// reason Code Apple defines.
const MetricStatusReasons = "apple_dm.ddm.status.reasons"

// AttrReasonCode is the metric attribute carrying a status reason's Code.
const AttrReasonCode = "ddm.reason_code"

// reasonCodes bounds the reason attribute to the codes Apple's schema defines.
var reasonCodes = telemetry.NewVocabulary(AttrReasonCode, schemaddm.ReasonCodes())

// New validates cfg and returns an Engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Store == nil {
		return nil, ErrNoStore
	}
	e := &Engine{
		observeStatus: cfg.ObserveStatus, store: cfg.Store, resolvers: cfg.Resolvers, expander: cfg.Expander, bus: cfg.Bus,
		clock: cfg.Clock, log: cfg.Logger, target: cfg.Target,
		enrollmentTarget: cfg.EnrollmentTarget,
		maxStatus:        cfg.MaxStatusBytes, keep: cfg.KeepReports, subs: cfg.Subscriptions,
	}
	if e.clock == nil {
		e.clock = clock.Real{}
	}
	if e.log == nil {
		e.log = slog.New(slog.DiscardHandler)
	}
	e.log = e.log.With(fault.Component("ddm"))
	reasons, err := cfg.Telemetry.Meter("ddm").Int64Counter(MetricStatusReasons,
		metric.WithDescription("Status report error reasons received from devices, by reason code."))
	if err != nil {
		return nil, fmt.Errorf("create the status reasons counter: %w", err)
	}
	e.reasons = reasons
	if e.target == nil {
		e.target = func(context.Context) support.Target { return support.Target{} }
	}
	if e.maxStatus <= 0 {
		e.maxStatus = DefaultMaxStatusBytes
	}
	e.maxStatusDepth, e.maxStatusPath, e.maxStatusItems = cfg.MaxStatusDepth, cfg.MaxStatusPathBytes, cfg.MaxStatusItems
	if e.maxStatusDepth <= 0 {
		e.maxStatusDepth = 64
	}
	if e.maxStatusPath <= 0 {
		e.maxStatusPath = 1024
	}
	if e.maxStatusItems <= 0 {
		e.maxStatusItems = 4096
	}
	if e.keep <= 0 {
		e.keep = DefaultKeepReports
	}
	if e.subs.Baseline == nil {
		e.subs.Baseline = DefaultSubscriptionBaseline
	}
	if e.subs.Exclude == nil {
		e.subs.Exclude = DefaultSubscriptionExclude
	}
	return e, nil
}

// Store exposes the backend for tests and administrative tooling.
func (e *Engine) Store() Store { return e.store }

// Logger returns the engine's logger, never nil, so a component built
// around an engine can inherit its logging rather than invent one.
func (e *Engine) Logger() *slog.Logger { return e.log }

// publish sends a DDM event through the configured bus and logs delivery failures other
// than a full queue.
func (e *Engine) publish(ctx context.Context, t event.Type, id mdm.EnrollmentID, data any) {
	if e.bus == nil {
		return
	}
	if err := e.bus.Publish(
		ctx,
		event.Event{Type: t, At: e.clock.Now(), Enrollment: id, Actor: "ddm", Data: data},
	); err != nil &&
		!errors.Is(err, event.ErrQueueFull) {
		e.log.WarnContext(ctx, "publish", "type", string(t), fault.Attr(err))
	}
}

// EnrollmentIdentity resolves the identity used by assignments and snapshots.
func (e *Engine) EnrollmentIdentity(ctx context.Context, rawID string) (mdm.EnrollmentID, error) {
	return e.store.EnrollmentIdentity(ctx, rawID)
}
