package hook

import (
	"context"

	"github.com/deploymenttheory/go-apple-dm/mdm"
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
}

// Hook observes and may veto operations. Before runs before storage is
// touched; an error aborts the operation with CodeForbidden. After runs
// with the operation's result.
type Hook interface {
	Before(ctx context.Context, c *Call) (context.Context, error)
	After(ctx context.Context, c *Call, err error)
}
