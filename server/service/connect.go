package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// Connect handles one request on the server URL: it records the device's
// response (unless Idle) and returns the next command to deliver, or nil
// when the queue is empty. A NotNow response skips other NotNow commands
// for this connection, as Apple recommends.
func (c *Core) Connect(
	ctx context.Context,
	r *mdm.Request,
	resp *mdm.Response,
) (*mdm.Command, error) {
	if r == nil || resp == nil {
		return nil, wrapCode(
			CodeBadRequest,
			fmt.Errorf("%w: nil request or response", ErrInvalidMessage),
		)
	}
	r.ID = resp.ID
	if c.certificateStatus != nil {
		if err := c.certificateStatus(ctx, r.Certificate); err != nil {
			c.publish(ctx, event.CertificateStatusRejected, r.ID, "device", nil)
			return nil, wrapCode(CodeForbidden, err)
		}
	}
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

func (c *Core) connect(
	ctx context.Context,
	r *mdm.Request,
	resp *mdm.Response,
) (*mdm.Command, error) {
	if cmd, handled, err := c.replacementConnect(ctx, r, resp); handled || err != nil {
		return cmd, err
	}
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
			c.log.InfoContext(
				ctx,
				"result for unknown command",
				"enrollment",
				r.ID.ID,
				"command",
				resp.CommandUUID,
				"status",
				resp.Status,
			)
		default:
			return nil, wrapCode(codeForStorage(err), err)
		}
		c.publish(ctx, event.CommandResult, r.ID, "device", resp)
	}
	cmd, err := c.nextEligible(ctx, r.ID, resp.Status == mdm.StatusNotNow, now)
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

// Recheck queued work using the latest inventory: an OS upgrade can make a
// previously eligible command unavailable before the device asks for it.
// Clear only that command, retain its audit row, then continue draining.
func (c *Core) nextEligible(
	ctx context.Context,
	id mdm.EnrollmentID,
	skipNotNow bool,
	now time.Time,
) (*mdm.Command, error) {
	for {
		cmd, err := c.store.Next(ctx, id, skipNotNow, now)
		if err != nil || cmd == nil {
			return cmd, err
		}
		decoded, invalid := validatedCommand(cmd)
		reason := "invalid-command"
		if invalid == nil {
			_, skipped, checkErr := c.checkTargets(ctx, []mdm.EnrollmentID{id}, decoded)
			if checkErr != nil {
				return nil, checkErr
			}
			invalid = skipped[id]
			reason = "unsupported-target"
		}
		if invalid == nil {
			return cmd, nil
		}
		clearer, ok := c.store.(storage.CommandClearer)
		if !ok {
			return nil, fmt.Errorf(
				"%w: store needs CommandClearer to discard ineligible queued work",
				ErrUnsupportedTarget,
			)
		}
		if _, err := clearer.ClearCommand(ctx, id, cmd.UUID); err != nil {
			return nil, err
		}
		c.publish(
			ctx,
			event.CommandRejected,
			id,
			"server",
			map[string]any{
				"command_uuid": cmd.UUID,
				"request_type": cmd.RequestType,
				"reason":       reason,
			},
		)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}
