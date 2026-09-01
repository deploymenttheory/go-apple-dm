// Package mdm is the protocol core of the Apple MDM check-in and command
// channels: enrollment identity, request context, check-in message
// decoding, command envelopes, and command response decoding.
//
// Apple documentation:
//   - https://developer.apple.com/documentation/devicemanagement/check-in
//   - https://developer.apple.com/documentation/devicemanagement/commands-and-queries
//
// Wire types come from the generated schema packages; this package adds the
// envelope structure Apple documents in prose rather than YAML.
package mdm

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// Channel identifies which MDM channel an enrollment identity belongs to.
type Channel uint8

// Channels. Apple runs a device channel and, on macOS and shared iPad, a
// user channel; User Enrollment (BYOD) uses EnrollmentID instead of UDID.
const (
	ChannelUnknown Channel = iota
	ChannelDevice
	ChannelUser
	ChannelSharedIPadUser
	ChannelUserEnrollmentDevice
	ChannelUserEnrollmentUser
)

// String implements fmt.Stringer.
func (c Channel) String() string {
	switch c {
	case ChannelDevice:
		return "device"
	case ChannelUser:
		return "user"
	case ChannelSharedIPadUser:
		return "shared-ipad-user"
	case ChannelUserEnrollmentDevice:
		return "user-enrollment-device"
	case ChannelUserEnrollmentUser:
		return "user-enrollment-user"
	case ChannelUnknown:
	}
	return "unknown"
}

// IsUser reports whether the channel is a user channel.
func (c Channel) IsUser() bool {
	return c == ChannelUser || c == ChannelSharedIPadUser || c == ChannelUserEnrollmentUser
}

// Valid reports whether the channel is one of the defined values.
func (c Channel) Valid() bool { return c > ChannelUnknown && c <= ChannelUserEnrollmentUser }

// SharedIPadUserID is the sentinel UserID Apple sends on the shared iPad
// user channel; the real user is in UserShortName.
const SharedIPadUserID = "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"

// Enrollment carries the identity keys present on every check-in message
// and command response. Which keys are populated depends on the channel.
type Enrollment struct {
	UDID             string `plist:"UDID,omitempty" json:"UDID,omitempty"`
	UserID           string `plist:"UserID,omitempty" json:"UserID,omitempty"`
	UserShortName    string `plist:"UserShortName,omitempty" json:"UserShortName,omitempty"`
	UserLongName     string `plist:"UserLongName,omitempty" json:"UserLongName,omitempty"`
	EnrollmentID     string `plist:"EnrollmentID,omitempty" json:"EnrollmentID,omitempty"`
	EnrollmentUserID string `plist:"EnrollmentUserID,omitempty" json:"EnrollmentUserID,omitempty"`
}

// EnrollmentID is the normalised identity of one channel of one enrollment.
// ID is the UDID or EnrollmentID for device channels and "<device>:<user>"
// for user channels; ParentID is the device channel id for user channels.
type EnrollmentID struct {
	Channel  Channel
	ID       string
	ParentID string
}

// Errors returned when resolving identities.
var (
	ErrNoEnrollmentID     = errors.New("mdm: message carries neither UDID nor EnrollmentID")
	ErrInvalidEnrollment  = errors.New("mdm: invalid enrollment identity")
	ErrSharedIPadNoUser   = errors.New("mdm: shared iPad user channel without UserShortName")
	ErrUnknownMessageType = errors.New("mdm: unknown check-in MessageType")
	ErrInvalidCommand     = errors.New("mdm: invalid command")
	ErrInvalidResponse    = errors.New("mdm: invalid command response")
)

// String returns the id.
func (e EnrollmentID) String() string { return e.ID }

// Device returns the device-channel identity this id belongs to (itself for
// device channels).
func (e EnrollmentID) Device() EnrollmentID {
	if !e.Channel.IsUser() {
		return e
	}
	ch := ChannelDevice
	if e.Channel == ChannelUserEnrollmentUser {
		ch = ChannelUserEnrollmentDevice
	}
	return EnrollmentID{Channel: ch, ID: e.ParentID}
}

// Validate checks the id is well formed.
func (e EnrollmentID) Validate() error {
	switch {
	case !e.Channel.Valid():
		return fmt.Errorf("%w: channel %d", ErrInvalidEnrollment, e.Channel)
	case e.ID == "":
		return fmt.Errorf("%w: empty id", ErrInvalidEnrollment)
	case e.Channel.IsUser() && e.ParentID == "":
		return fmt.Errorf("%w: user channel without parent", ErrInvalidEnrollment)
	case !e.Channel.IsUser() && e.ParentID != "":
		return fmt.Errorf("%w: device channel with parent", ErrInvalidEnrollment)
	}
	return nil
}

// Resolve derives the EnrollmentID from the identity keys, following the
// rules in Apple's check-in documentation:
//
//   - UDID present: device channel keyed by UDID; with UserID it is the user
//     channel "UDID:UserID", or the shared iPad user channel
//     "UDID:UserShortName" when UserID is the shared iPad sentinel.
//   - EnrollmentID present (User Enrollment): device channel keyed by
//     EnrollmentID; with EnrollmentUserID it is "EnrollmentID:EnrollmentUserID".
func (en Enrollment) Resolve() (EnrollmentID, error) {
	switch {
	case en.UDID != "":
		if en.UserID == "" {
			return EnrollmentID{Channel: ChannelDevice, ID: en.UDID}, nil
		}
		if en.UserID == SharedIPadUserID {
			if en.UserShortName == "" {
				return EnrollmentID{}, ErrSharedIPadNoUser
			}
			return EnrollmentID{Channel: ChannelSharedIPadUser, ID: en.UDID + ":" + en.UserShortName, ParentID: en.UDID}, nil
		}
		return EnrollmentID{Channel: ChannelUser, ID: en.UDID + ":" + en.UserID, ParentID: en.UDID}, nil
	case en.EnrollmentID != "":
		if en.EnrollmentUserID == "" {
			return EnrollmentID{Channel: ChannelUserEnrollmentDevice, ID: en.EnrollmentID}, nil
		}
		return EnrollmentID{Channel: ChannelUserEnrollmentUser, ID: en.EnrollmentID + ":" + en.EnrollmentUserID, ParentID: en.EnrollmentID}, nil
	}
	return EnrollmentID{}, ErrNoEnrollmentID
}

// Push is what the server needs to wake a device: from TokenUpdate.
type Push struct {
	Topic string
	Token []byte
	Magic string
}

// Valid reports whether all three parts are present.
func (p Push) Valid() bool { return p.Topic != "" && len(p.Token) > 0 && p.Magic != "" }

// PeerInfo describes the transport peer of a request.
type PeerInfo struct {
	RemoteAddr string
	UserAgent  string
}

// Request is the context of one device request handed to the service layer.
// It never carries a context.Context: every service method takes one.
type Request struct {
	ID          EnrollmentID
	Enrollment  Enrollment
	Certificate *x509.Certificate
	Params      map[string]string
	Peer        PeerInfo
	ReceivedAt  time.Time
}

// ParseError wraps a decode failure with the offending content so callers
// can log it. Content is bounded by the plist size limits.
type ParseError struct {
	Err     error
	Content []byte
}

// Error implements error.
func (e *ParseError) Error() string { return "mdm: parse: " + e.Err.Error() }

// Unwrap implements errors.Unwrap.
func (e *ParseError) Unwrap() error { return e.Err }
