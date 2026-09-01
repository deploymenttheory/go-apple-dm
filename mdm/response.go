package mdm

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
)

// Status is the Status key of a command response.
type Status string

// Statuses Apple documents for command responses.
const (
	StatusAcknowledged       Status = "Acknowledged"
	StatusError              Status = "Error"
	StatusCommandFormatError Status = "CommandFormatError"
	StatusIdle               Status = "Idle"
	StatusNotNow             Status = "NotNow"
)

// Valid reports whether the status is one Apple defines.
func (s Status) Valid() bool {
	switch s {
	case StatusAcknowledged, StatusError, StatusCommandFormatError, StatusIdle, StatusNotNow:
		return true
	}
	return false
}

// ErrorChainItem is one entry of the ErrorChain array on an Error response.
type ErrorChainItem struct {
	ErrorCode            int64  `plist:"ErrorCode" json:"ErrorCode"`
	ErrorDomain          string `plist:"ErrorDomain" json:"ErrorDomain"`
	LocalizedDescription string `plist:"LocalizedDescription,omitempty" json:"LocalizedDescription,omitempty"`
	USEnglishDescription string `plist:"USEnglishDescription,omitempty" json:"USEnglishDescription,omitempty"`
}

// Response is a decoded command response (or an Idle poll).
type Response struct {
	Enrollment  Enrollment
	ID          EnrollmentID
	CommandUUID string
	Status      Status
	ErrorChain  []ErrorChainItem
	// Payload is the typed response when the caller supplied the RequestType
	// of the command being answered and the status is Acknowledged.
	Payload commands.Response
	Raw     []byte
}

// IsIdle reports whether the message is an Idle poll rather than a result.
func (r *Response) IsIdle() bool { return r.Status == StatusIdle }

type responseHead struct {
	Enrollment
	CommandUUID string           `plist:"CommandUUID,omitempty"`
	Status      Status           `plist:"Status"`
	ErrorChain  []ErrorChainItem `plist:"ErrorChain,omitempty"`
}

// DecodeResponse parses a command response body. requestType may be empty;
// when set and the registry knows it, Payload is populated for Acknowledged
// responses.
func DecodeResponse(raw []byte, requestType string, opts ...DecodeOption) (*Response, error) {
	o := applyDecodeOptions(opts)
	var head responseHead
	if err := o.dec.Unmarshal(raw, &head); err != nil {
		return nil, &ParseError{Err: err, Content: raw}
	}
	if head.Status == "" {
		return nil, &ParseError{Err: fmt.Errorf("%w: missing Status", ErrInvalidResponse), Content: raw}
	}
	if !head.Status.Valid() {
		return nil, &ParseError{Err: fmt.Errorf("%w: unknown Status %q", ErrInvalidResponse, head.Status), Content: raw}
	}
	if head.Status != StatusIdle && head.CommandUUID == "" {
		return nil, &ParseError{Err: fmt.Errorf("%w: %s response without CommandUUID", ErrInvalidResponse, head.Status), Content: raw}
	}
	id, err := head.Enrollment.Resolve()
	if err != nil {
		return nil, &ParseError{Err: err, Content: raw}
	}
	r := &Response{Enrollment: head.Enrollment, ID: id, CommandUUID: head.CommandUUID, Status: head.Status, ErrorChain: head.ErrorChain, Raw: raw}
	if requestType != "" && head.Status == StatusAcknowledged {
		if entries := commands.ByID(requestType); len(entries) > 0 {
			payload := entries[0].NewResponse()
			if err := o.dec.Unmarshal(raw, payload); err != nil {
				return nil, &ParseError{Err: err, Content: raw}
			}
			r.Payload = payload
		}
	}
	return r, nil
}
