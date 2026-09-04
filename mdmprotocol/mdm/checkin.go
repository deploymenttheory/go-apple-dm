package mdm

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
)

// Checkin is a decoded check-in message: the typed message from the
// generated schema, the identity keys, the resolved enrollment id, and the
// raw bytes as received.
type Checkin struct {
	// Type is the MessageType wire value (Authenticate, TokenUpdate, ...).
	Type string
	// Message is the typed message, one of the schema/checkin types.
	Message checkin.Message
	// Enrollment holds the identity keys as sent.
	Enrollment Enrollment
	// ID is the resolved enrollment identity.
	ID EnrollmentID
	// Raw is the plist exactly as received.
	Raw []byte
}

// DecodeOption configures decoding.
type DecodeOption func(*decodeOptions)

type decodeOptions struct {
	dec plist.Decoder
}

// WithLimits overrides the plist size and depth limits.
func WithLimits(d plist.Decoder) DecodeOption {
	return func(o *decodeOptions) { o.dec = d }
}

func applyDecodeOptions(opts []DecodeOption) decodeOptions {
	var o decodeOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// checkinEnvelope dispatches on MessageType inside a single decode pass:
// the decoder calls UnmarshalPlist with a function that can populate any
// value from the already-parsed document.
type checkinEnvelope struct {
	typ        string
	msg        checkin.Message
	enrollment Enrollment
}

// UnmarshalPlist implements plist.Unmarshaler.
func (e *checkinEnvelope) UnmarshalPlist(f func(any) error) error {
	var head struct {
		MessageType string `plist:"MessageType"`
	}
	if err := f(&head); err != nil {
		return err
	}
	e.typ = head.MessageType
	entries := checkin.ByID(head.MessageType)
	if len(entries) == 0 {
		return fmt.Errorf("%w: %q", ErrUnknownMessageType, head.MessageType)
	}
	e.msg = entries[0].New()
	if err := f(e.msg); err != nil {
		return err
	}
	return f(&e.enrollment)
}

// DecodeCheckin parses a check-in request body.
func DecodeCheckin(raw []byte, opts ...DecodeOption) (*Checkin, error) {
	o := applyDecodeOptions(opts)
	var env checkinEnvelope
	if err := o.dec.Unmarshal(raw, &env); err != nil {
		return nil, &ParseError{Err: err, Content: raw}
	}
	id, err := env.enrollment.Resolve()
	if err != nil {
		return nil, &ParseError{Err: err, Content: raw}
	}
	return &Checkin{Type: env.typ, Message: env.msg, Enrollment: env.enrollment, ID: id, Raw: raw}, nil
}

// PushFromTokenUpdate extracts push information from a TokenUpdate.
func PushFromTokenUpdate(m *checkin.TokenUpdate) (Push, error) {
	if m == nil {
		return Push{}, fmt.Errorf("%w: nil TokenUpdate", ErrInvalidEnrollment)
	}
	p := Push{Topic: m.Topic, Token: m.Token, Magic: m.PushMagic}
	if !p.Valid() {
		return Push{}, fmt.Errorf("%w: TokenUpdate missing Topic, Token, or PushMagic", ErrInvalidEnrollment)
	}
	return p, nil
}

// UserAuthenticateResponse is the server's reply to UserAuthenticate, which
// Apple documents in prose rather than in the schema: the first reply
// carries DigestChallenge (empty to skip authentication), the second
// carries AuthToken (empty to reject the password).
type UserAuthenticateResponse struct {
	DigestChallenge *string `plist:"DigestChallenge,omitempty" json:"DigestChallenge,omitempty"`
	AuthToken       *string `plist:"AuthToken,omitempty" json:"AuthToken,omitempty"`
}
