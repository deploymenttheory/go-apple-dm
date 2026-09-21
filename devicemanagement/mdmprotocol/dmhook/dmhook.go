package dmhook

import (
	"context"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

// Call describes one service operation for hooks.
type Call struct {
	// Op is "checkin:<MessageType>", "connect", "enqueue", "export", or
	// "import".
	Op       string
	Request  *mdm.Request
	Checkin  *mdm.Checkin
	Response *mdm.Response
	Command  *mdm.Command
	// Authenticate is populated after successful ordinary authentication. Known
	// means storage classified the transition under its write lock. Otherwise
	// Reset is a pre-read fallback for legacy stores, which requires external
	// serialization to classify concurrent requests reliably.
	Authenticate *AuthenticateResult
}

// AuthenticateResult tells completion hooks whether authentication reset the
// enrollment or preserved it as an idempotent retry. It describes the current
// operation, not the eventual commit of an enclosing transaction.
type AuthenticateResult struct {
	Known bool
	Reset bool
}

// Hook observes and may veto operations. Before runs before storage is
// touched; an error aborts the operation with CodeForbidden. After runs
// with the operation's result.
type Hook interface {
	// Before runs before the wrapped protocol operation and may replace its context or
	// reject it.
	Before(ctx context.Context, c *Call) (context.Context, error)
	// After observes the wrapped operation's result after processing.
	After(ctx context.Context, c *Call, err error)
}

// Completer finishes a successful check-in's required authorization and lifecycle
// work before the transport reports success. An error is returned to the device; implementations
// must permit an idempotent retry after a partially completed service operation.
type Completer interface {
	// Complete finishes required state changes after a successful check-in but before
	// the transport reports success. A failure must permit an idempotent retry.
	Complete(context.Context, *Call) error
}
