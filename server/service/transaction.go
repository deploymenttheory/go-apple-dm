package service

import (
	"context"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// Enqueue validates and queues commands together with their required events.
func (c *Core) Enqueue(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, o storage.EnqueueOptions) (storage.EnqueueResult, error) {
	var out storage.EnqueueResult
	err := event.Run(ctx, c.bus, func(ctx context.Context) error {
		var err error
		out, err = c.enqueue(ctx, ids, cmd, o)
		return err
	})
	if err != nil {
		return storage.EnqueueResult{}, err
	}
	return out, nil
}

// Checkin processes local enrollment mutations in the event transaction. DDM
// adapters and token/return-to-service integrations own their own boundaries;
// they may call remote services and must not hold this database transaction.
func (c *Core) Checkin(ctx context.Context, r *mdm.Request, ck *mdm.Checkin) (*CheckinResult, error) {
	if ck != nil && (ck.Type == "DeclarativeManagement" || ck.Type == "GetToken" || ck.Type == "ReturnToService") {
		return c.checkin(ctx, r, ck)
	}
	var out *CheckinResult
	err := event.Run(ctx, c.bus, func(ctx context.Context) error {
		var err error
		out, err = c.checkin(ctx, r, ck)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Connect commits the response, next-command transition and required events
// before the transport can deliver that command to the device.
func (c *Core) Connect(ctx context.Context, r *mdm.Request, resp *mdm.Response) (*mdm.Command, error) {
	var out *mdm.Command
	err := event.Run(ctx, c.bus, func(ctx context.Context) error {
		var err error
		out, err = c.handleConnect(ctx, r, resp)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
