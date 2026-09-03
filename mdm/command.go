package mdm

import (
	"fmt"
	"strings"
	"uuid"

	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
)

// Command is a queued MDM command: the envelope Apple documents as
// {CommandUUID, Command: {RequestType, ...}} plus the typed payload.
type Command struct {
	UUID        string
	RequestType string
	// Payload is the typed command when known; nil after decoding a command
	// whose RequestType is not in the schema registry.
	Payload commands.Command
	// Raw is the complete command plist sent to the device.
	Raw []byte
}

// CommandOption configures NewCommand.
type CommandOption func(*Command)

// WithUUID sets an explicit CommandUUID instead of a generated UUIDv7.
func WithUUID(id string) CommandOption {
	return func(c *Command) { c.UUID = id }
}

// commandEnvelope is the wire shape.
type commandEnvelope struct {
	CommandUUID string         `plist:"CommandUUID"`
	Command     map[string]any `plist:"Command"`
}

// NewCommand builds the wire plist for a typed command, injecting RequestType
// and a time-ordered UUID.
func NewCommand(payload commands.Command, opts ...CommandOption) (*Command, error) {
	if payload == nil {
		return nil, fmt.Errorf("%w: nil payload", ErrInvalidCommand)
	}
	c := &Command{RequestType: payload.RequestTypeName(), Payload: payload}
	for _, o := range opts {
		o(c)
	}
	if c.UUID == "" {
		// Apple renders CommandUUID values in upper case in its documentation.
		c.UUID = strings.ToUpper(uuid.NewV7().String())
	}
	body, err := plist.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	var fields map[string]any
	if err := plist.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["RequestType"] = c.RequestType
	raw, err := plist.Marshal(commandEnvelope{CommandUUID: c.UUID, Command: fields})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	c.Raw = raw
	return c, nil
}

// DecodeCommand parses a command plist, resolving the typed payload through
// the schema registry when the RequestType is known.
func DecodeCommand(raw []byte, opts ...DecodeOption) (*Command, error) {
	o := applyDecodeOptions(opts)
	var head struct {
		CommandUUID string `plist:"CommandUUID"`
		Command     struct {
			RequestType string `plist:"RequestType"`
		} `plist:"Command"`
	}
	if err := o.dec.Unmarshal(raw, &head); err != nil {
		return nil, &ParseError{Err: err, Content: raw}
	}
	if head.CommandUUID == "" || head.Command.RequestType == "" {
		return nil, &ParseError{Err: fmt.Errorf("%w: missing CommandUUID or RequestType", ErrInvalidCommand), Content: raw}
	}
	c := &Command{UUID: head.CommandUUID, RequestType: head.Command.RequestType, Raw: raw}
	if entries := commands.ByID(head.Command.RequestType); len(entries) > 0 {
		payload := entries[0].New()
		var body struct {
			Command *decodeInto `plist:"Command"`
		}
		body.Command = &decodeInto{v: payload}
		if err := o.dec.Unmarshal(raw, &body); err != nil {
			return nil, &ParseError{Err: err, Content: raw}
		}
		c.Payload = payload
	}
	return c, nil
}

// decodeInto routes a nested dictionary into an existing typed value; a
// plain any field would be replaced by a map.
type decodeInto struct{ v any }

// UnmarshalPlist implements plist.Unmarshaler.
func (d *decodeInto) UnmarshalPlist(f func(any) error) error { return f(d.v) }
