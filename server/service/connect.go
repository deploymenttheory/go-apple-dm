package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage"
)

// Connect handles one request on the server URL: it records the device's
// response (unless Idle) and returns the next command to deliver, or nil
// when the queue is empty. A NotNow response skips other NotNow commands
// for this connection, as Apple recommends.
func (c *Core) Connect(ctx context.Context, r *mdm.Request, resp *mdm.Response) (*mdm.Command, error) {
	if r == nil || resp == nil {
		return nil, wrapCode(CodeBadRequest, fmt.Errorf("%w: nil request or response", ErrInvalidMessage))
	}
	r.ID = resp.ID
	r.Enrollment = resp.Enrollment
	call := &Call{Op: "connect", Request: r, Response: resp}
	ctx, after, err := c.runHooks(ctx, call)
	if err != nil {
		return nil, err
	}
	cmd, err := c.connect(ctx, r, resp)
	call.Command = cmd
	after(err)
	return cmd, err
}

func (c *Core) connect(ctx context.Context, r *mdm.Request, resp *mdm.Response) (*mdm.Command, error) {
	if err := c.authorize(ctx, r); err != nil {
		return nil, err
	}
	now := c.clock.Now()
	if !resp.IsIdle() {
		err := c.store.StoreResult(ctx, r.ID, resp, now)
		switch {
		case err == nil:
		case errors.Is(err, storage.ErrNotFound):
			// A result for a command this server no longer tracks (cleared,
			// migrated, or duplicate): log and carry on so the device is not
			// stuck.
			c.log.InfoContext(ctx, "result for unknown command", "enrollment", r.ID.ID, "command", resp.CommandUUID, "status", resp.Status)
		default:
			return nil, wrapCode(codeForStorage(err), err)
		}
		c.publish(ctx, event.CommandResult, r.ID, "device", resp)
	}
	cmd, err := c.store.Next(ctx, r.ID, resp.Status == mdm.StatusNotNow, now)
	if err != nil {
		return nil, wrapCode(codeForStorage(err), err)
	}
	if err := c.store.TouchLastSeen(ctx, r.ID, now); err != nil {
		return nil, wrapCode(codeForStorage(err), err)
	}
	if cmd != nil {
		c.publish(ctx, event.CommandSent, r.ID, "server", cmd)
	}
	return cmd, nil
}
