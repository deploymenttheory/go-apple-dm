package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
	"github.com/deploymenttheory/go-apple-mdm/schema/support"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// Code classifies service errors so transports can map them to responses
// without inspecting messages.
type Code int

// Error codes.
const (
	CodeInternal          Code = iota // storage or handler failure
	CodeBadRequest                    // malformed or inconsistent message
	CodeForbidden                     // identity mismatch or policy veto
	CodeUnknownEnrollment             // no enrollment for the identity presented
	CodeNotImplemented                // no handler configured for the message
	CodeGone                          // the server declines to manage this user or enrollment (HTTP 410)
)

// Error is the typed error every service method returns.
type Error struct {
	Code Code
	Err  error
}

// Error implements error.
func (e *Error) Error() string { return e.Err.Error() }

// Unwrap implements errors.Unwrap.
func (e *Error) Unwrap() error { return e.Err }

// CodeOf returns the Code of err, or CodeInternal for other errors.
func CodeOf(err error) Code {
	if se, ok := errors.AsType[*Error](err); ok {
		return se.Code
	}
	return CodeInternal
}

func wrapCode(code Code, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// Sentinel errors.
var (
	ErrUnknownEnrollment = errors.New("service: unknown enrollment")
	ErrCertRequired      = errors.New("service: identity certificate required")
	ErrCertMismatch      = errors.New("service: identity certificate does not match the enrollment")
	ErrReenrollDenied    = errors.New("service: re-enrollment with a new identity denied")
	ErrNoHandler         = errors.New("service: no handler configured")
	ErrInvalidMessage    = errors.New("service: invalid message")
	ErrHookVeto          = errors.New("service: rejected by hook")
	// ErrCertReused is returned when an identity certificate presented on
	// Authenticate, or on a retroactive pin, appears in another
	// enrollment's certificate history (decision record 0014).
	ErrCertReused = errors.New("service: identity certificate already used by another enrollment")
)

// PinMode controls identity certificate pinning.
type PinMode int

// Pin modes.
const (
	// PinEnforce rejects requests whose certificate does not match the
	// pinned one, and requires a certificate on every request.
	PinEnforce PinMode = iota
	// PinWarn logs mismatches but allows the request.
	PinWarn
	// PinOff disables pinning entirely.
	PinOff
)

// DMResponse is what a DeclarativeManagement handler returns to the device.
type DMResponse struct {
	Body        []byte
	ContentType string
	// Status overrides the HTTP status when non-zero (for example 404 for
	// an unknown declaration).
	Status int
}

// DMHandler serves declarative management check-in messages. ck is the
// check-in as received, including the raw plist bytes, so an adapter that
// forwards the message to another process can send it unchanged
// (decision record 0023); m is the typed message inside ck.
type DMHandler func(ctx context.Context, r *mdm.Request, ck *mdm.Checkin, m *checkin.DeclarativeManagement) (DMResponse, error)

// GetTokenHandler serves GetToken requests.
type GetTokenHandler func(ctx context.Context, r *mdm.Request, m *checkin.GetToken) (*checkin.GetTokenResponse, error)

// UserAuthenticateHandler serves UserAuthenticate. Returning a response
// with an empty DigestChallenge accepts the user without authentication,
// which is the default behaviour.
type UserAuthenticateHandler func(ctx context.Context, r *mdm.Request, m *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error)

// ReenrollPolicy decides whether an Authenticate from an enrollment whose
// pinned certificate differs from the one presented is accepted.
type ReenrollPolicy func(ctx context.Context, r *mdm.Request, existing *storage.Enrollment) error

// CertReusePolicy decides whether an Authenticate may use an identity
// certificate that another enrollment has pinned before. previous lists
// the other enrollments' history rows (never the requesting device).
type CertReusePolicy func(ctx context.Context, r *mdm.Request, previous []storage.CertAssociation) error

// DenyCertReuse rejects every certificate that another enrollment pinned
// before, with ErrCertReused.
func DenyCertReuse(context.Context, *mdm.Request, []storage.CertAssociation) error {
	return ErrCertReused
}

// AllowCertReuse accepts a certificate that appears only in other
// enrollments' history. It never overrides a live pin: a hash that another
// enrollment currently pins still fails with ErrCertMismatch.
func AllowCertReuse(context.Context, *mdm.Request, []storage.CertAssociation) error { return nil }

// Call describes one service operation for hooks.
type Call struct {
	// Op is "checkin:<MessageType>", "connect", "enqueue", "export", or
	// "import".
	Op       string
	Request  *mdm.Request
	Checkin  *mdm.Checkin
	Response *mdm.Response
	Command  *mdm.Command
}

// Hook observes and may veto operations. Before runs before storage is
// touched; an error aborts the operation with CodeForbidden. After runs
// with the operation's result.
type Hook interface {
	Before(ctx context.Context, c *Call) (context.Context, error)
	After(ctx context.Context, c *Call, err error)
}

// Config builds a Core.
type Config struct {
	Store storage.Store
	// Bus receives events; nil disables publishing.
	Bus *event.Bus
	// Clock defaults to the real clock.
	Clock clock.Clock
	Hooks []Hook
	// Logger defaults to slog.Default.
	Logger *slog.Logger
	// Pinning defaults to PinEnforce.
	Pinning PinMode
	// Reenroll defaults to AllowReenroll.
	Reenroll ReenrollPolicy
	// CertReuse defaults to DenyCertReuse. It is consulted when an
	// Authenticate presents a certificate whose hash appears in another
	// enrollment's history and is ignored under PinOff. AllowCertReuse only
	// permits certificates that are in history but not currently pinned: a
	// live pin held by another enrollment still yields ErrCertMismatch with
	// CodeForbidden, because the pin exists to stop a second device using
	// the identity.
	CertReuse CertReusePolicy
	// RequireUserAuth makes a user channel's TokenUpdate depend on a
	// completed UserAuthenticate session (a token issued by DigestUserAuth):
	// without one the TokenUpdate is CodeForbidden (decision record 0029).
	// Shared iPad and User Enrollment user channels are exempt because
	// Apple never sends UserAuthenticate for them.
	RequireUserAuth bool
	// ValidateTargets checks every Enqueue target against the request
	// type's support metadata (channel, Shared iPad, User Enrollment) from
	// schema/commands and reports unsupported targets in
	// EnqueueResult.Skipped instead of queuing them. Default true.
	ValidateTargets *bool
	// Optional message handlers.
	DeclarativeManagement DMHandler
	GetToken              GetTokenHandler
	UserAuthenticate      UserAuthenticateHandler
}

// AllowReenroll accepts every re-enrollment with a new identity.
func AllowReenroll(context.Context, *mdm.Request, *storage.Enrollment) error { return nil }

// DenyReenroll rejects re-enrollment with a new identity.
func DenyReenroll(context.Context, *mdm.Request, *storage.Enrollment) error { return ErrReenrollDenied }

// Core is the service implementation.
type Core struct {
	store           storage.Store
	bus             *event.Bus
	clock           clock.Clock
	hooks           []Hook
	log             *slog.Logger
	pinning         PinMode
	reenroll        ReenrollPolicy
	reuse           CertReusePolicy
	dm              DMHandler
	getToken        GetTokenHandler
	userAuth        UserAuthenticateHandler
	requireUserAuth bool
	validateTargets bool
}

// New validates the configuration and builds a Core.
func New(cfg Config) (*Core, error) {
	if cfg.Store == nil {
		return nil, errors.New("service: Config.Store is required")
	}
	c := &Core{
		store: cfg.Store, bus: cfg.Bus, clock: cfg.Clock, hooks: cfg.Hooks, log: cfg.Logger,
		pinning: cfg.Pinning, reenroll: cfg.Reenroll, reuse: cfg.CertReuse,
		dm: cfg.DeclarativeManagement, getToken: cfg.GetToken, userAuth: cfg.UserAuthenticate,
		requireUserAuth: cfg.RequireUserAuth, validateTargets: cfg.ValidateTargets == nil || *cfg.ValidateTargets,
	}
	if c.clock == nil {
		c.clock = clock.Real{}
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.reenroll == nil {
		c.reenroll = AllowReenroll
	}
	if c.reuse == nil {
		c.reuse = DenyCertReuse
	}
	if c.userAuth == nil {
		c.userAuth = acceptAllUsers
	}
	return c, nil
}

func acceptAllUsers(context.Context, *mdm.Request, *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error) {
	return &mdm.UserAuthenticateResponse{DigestChallenge: new("")}, nil
}

// publish sends an event when a bus is configured.
func (c *Core) publish(ctx context.Context, t event.Type, id mdm.EnrollmentID, actor string, data any) {
	if c.bus == nil {
		return
	}
	if err := c.bus.Publish(ctx, event.Event{Type: t, At: c.clock.Now(), Enrollment: id, Actor: actor, Data: data}); err != nil {
		c.log.WarnContext(ctx, "event handler failed", "type", t, "enrollment", id.ID, "err", err)
	}
}

// runHooks executes Before hooks and returns an After function.
func (c *Core) runHooks(ctx context.Context, call *Call) (context.Context, func(error), error) {
	for _, h := range c.hooks {
		next, err := h.Before(ctx, call)
		if err != nil {
			return ctx, func(error) {}, wrapCode(CodeForbidden, fmt.Errorf("%w: %w", ErrHookVeto, err))
		}
		if next != nil {
			ctx = next
		}
	}
	return ctx, func(err error) {
		for _, v := range slices.Backward(c.hooks) {
			v.After(ctx, call, err)
		}
	}, nil
}

// Enqueue queues a command for enrollments and publishes CommandQueued for
// each that accepted it.
func (c *Core) Enqueue(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, o storage.EnqueueOptions) (storage.EnqueueResult, error) {
	call := &Call{Op: "enqueue", Command: cmd}
	ctx, after, err := c.runHooks(ctx, call)
	if err != nil {
		return storage.EnqueueResult{}, err
	}
	if o.Now.IsZero() {
		o.Now = c.clock.Now()
	}
	ids, unsupported, err := c.checkTargets(ctx, ids, cmd)
	if err != nil {
		err = wrapCode(CodeInternal, err)
		after(err)
		return storage.EnqueueResult{}, err
	}
	var res storage.EnqueueResult
	if len(ids) > 0 || len(unsupported) == 0 {
		// The store validates the command itself; skip it only when every
		// target was filtered out above.
		res, err = c.store.Enqueue(ctx, ids, cmd, o)
		if err != nil {
			err = wrapCode(codeForStorage(err), err)
			after(err)
			return res, err
		}
	}
	if res.Skipped == nil {
		res.Skipped = map[mdm.EnrollmentID]error{}
	}
	maps.Copy(res.Skipped, unsupported)
	for _, id := range res.Queued {
		c.publish(ctx, event.CommandQueued, id, "admin", cmd)
	}
	after(nil)
	return res, nil
}

func codeForStorage(err error) Code {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return CodeUnknownEnrollment
	case errors.Is(err, storage.ErrInvalid):
		return CodeBadRequest
	case errors.Is(err, storage.ErrConflict):
		return CodeForbidden
	}
	return CodeInternal
}

// ErrUnsupportedTarget marks an Enqueue target the request type does not
// support per Apple's schema metadata.
var ErrUnsupportedTarget = errors.New("service: request type not supported on this enrollment")

// checkTargets splits ids into supported targets and unsupported ones with
// the reason (decision record 0029). Unknown enrollments pass through so
// the store reports them as it always has.
func (c *Core) checkTargets(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command) ([]mdm.EnrollmentID, map[mdm.EnrollmentID]error, error) {
	if !c.validateTargets || cmd == nil {
		return ids, nil, nil
	}
	entry := commands.Support(cmd.RequestType)
	if entry == nil {
		return ids, nil, nil
	}
	var keep []mdm.EnrollmentID
	unsupported := map[mdm.EnrollmentID]error{}
	for _, id := range ids {
		e, err := c.store.Get(ctx, id)
		if errors.Is(err, storage.ErrNotFound) {
			keep = append(keep, id)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		target := targetFor(ctx, c.store, e)
		if r := entry.Check(target); !r.Supported {
			unsupported[id] = fmt.Errorf("%w: %s", ErrUnsupportedTarget, r.Reason)
			continue
		}
		keep = append(keep, id)
	}
	return keep, unsupported, nil
}

// targetFor derives the support target of an enrollment: the OS family
// from the product, the version, the channel, and the Shared iPad and User
// Enrollment modes from the channel kind. A user channel's OS comes from
// its device.
func targetFor(ctx context.Context, st storage.EnrollmentStore, e *storage.Enrollment) support.Target {
	device := e
	if e.ID.Channel.IsUser() {
		if parent, err := st.Get(ctx, mdm.EnrollmentID{Channel: deviceChannelOf(e.ID.Channel), ID: e.ID.ParentID}); err == nil {
			device = parent
		}
	}
	// Supervision, DEP, and user-approved MDM are not tracked on the
	// enrollment record, so they are assumed rather than enforced; only the
	// channel and mode rules bite here.
	t := support.Target{OS: support.OSFromProduct(device.Device.ProductName), Channel: support.ChannelDevice, Supervised: true, DEP: true, UserApproved: true}
	if v, err := support.ParseVersion(device.Device.OSVersion); err == nil {
		t.Version = v
	}
	switch e.ID.Channel {
	case mdm.ChannelDevice:
		if t.OS == support.IOS {
			// A Shared iPad is recognised by its logged-in user channel.
			res, err := st.List(ctx, storage.EnrollmentQuery{ParentID: e.ID.ID, Channel: mdm.ChannelSharedIPadUser}, storage.Page{Limit: 1})
			t.SharedIPad = err == nil && len(res.Items) > 0
		}
	case mdm.ChannelUser:
		t.Channel = support.ChannelUser
	case mdm.ChannelSharedIPadUser:
		t.Channel, t.SharedIPad = support.ChannelUser, true
	case mdm.ChannelUserEnrollmentDevice:
		t.UserEnrollment = true
	case mdm.ChannelUserEnrollmentUser:
		t.Channel, t.UserEnrollment = support.ChannelUser, true
	}
	return t
}

func deviceChannelOf(c mdm.Channel) mdm.Channel {
	if c == mdm.ChannelUserEnrollmentUser {
		return mdm.ChannelUserEnrollmentDevice
	}
	return mdm.ChannelDevice
}
