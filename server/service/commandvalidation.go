package service

import (
	"bytes"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// validatedCommand uses the bytes the device will receive as the source of
// truth. Payload is a convenience view and may have changed since NewCommand.
// Returning a decoded copy keeps validation, target checks and stored bytes in
// agreement without re-encoding or dropping unknown extension fields.
func validatedCommand(cmd *mdm.Command) (*mdm.Command, error) {
	if cmd == nil {
		return nil, fmt.Errorf("%w: nil command", storage.ErrInvalid)
	}
	decoded, err := mdm.DecodeCommand(bytes.Clone(cmd.Raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", storage.ErrInvalid, err)
	}
	if decoded.UUID != cmd.UUID || decoded.RequestType != cmd.RequestType {
		return nil, fmt.Errorf("%w: command identifiers differ from the wire envelope", storage.ErrInvalid)
	}
	if decoded.Payload != nil {
		if err := decoded.Payload.Validate(support.Target{}); err != nil {
			return nil, fmt.Errorf("%w: %w", storage.ErrInvalid, err)
		}
	}
	return decoded, nil
}
