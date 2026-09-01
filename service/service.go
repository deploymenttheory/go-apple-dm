// Package service implements the MDM server behaviour behind the check-in
// and command endpoints: enrollment lifecycle, identity pinning, command
// delivery, and the events and hooks that let integrators observe or veto
// every step (decision records 0004 to 0006).
//
// Apple documentation:
//   - https://developer.apple.com/documentation/devicemanagement/check-in
//   - https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
//   - https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
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
	var se *Error
	if errors.As(err, &se) {
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

// DMHandler serves declarative management check-in messages.
type DMHandler func(ctx context.Context, r *mdm.Request, m *checkin.DeclarativeManagement) (DMResponse, error)

// GetTokenHandler serves GetToken requests.
type GetTokenHandler func(ctx context.Context, r *mdm.Request, m *checkin.GetToken) (*checkin.GetTokenResponse, error)

// UserAuthenticateHandler serves UserAuthenticate. Returning a response
// with an empty DigestChallenge accepts the user without authentication,
// which is the default behaviour.
type UserAuthenticateHandler func(ctx context.Context, r *mdm.Request, m *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error)

// ReenrollPolicy decides whether an Authenticate from an enrollment whose
// pinned certificate differs from the one presented is accepted.
type ReenrollPolicy func(ctx context.Context, r *mdm.Request, existing *storage.Enrollment) error

// Call describes one service operation for hooks.
type Call struct {
	// Op is "checkin:<MessageType>", "connect", or "enqueue".
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
	store    storage.Store
	bus      *event.Bus
	clock    clock.Clock
	hooks    []Hook
	log      *slog.Logger
	pinning  PinMode
	reenroll ReenrollPolicy
	dm       DMHandler
	getToken GetTokenHandler
	userAuth UserAuthenticateHandler
}

// New validates the configuration and builds a Core.
func New(cfg Config) (*Core, error) {
	if cfg.Store == nil {
		return nil, errors.New("service: Config.Store is required")
	}
	c := &Core{
		store: cfg.Store, bus: cfg.Bus, clock: cfg.Clock, hooks: cfg.Hooks, log: cfg.Logger,
		pinning: cfg.Pinning, reenroll: cfg.Reenroll, dm: cfg.DeclarativeManagement, getToken: cfg.GetToken, userAuth: cfg.UserAuthenticate,
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
		for i := len(c.hooks) - 1; i >= 0; i-- {
			c.hooks[i].After(ctx, call, err)
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
	res, err := c.store.Enqueue(ctx, ids, cmd, o)
	if err != nil {
		err = wrapCode(codeForStorage(err), err)
		after(err)
		return res, err
	}
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
	}
	return CodeInternal
}
