// Package uuid generates RFC 9562 version 7 UUIDs (time-ordered) for
// command identifiers, and validates UUID strings.
package uuid

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrInvalid is returned by Parse for malformed input.
var ErrInvalid = errors.New("uuid: invalid format")

// UUID is a 128-bit identifier.
type UUID [16]byte

// NewV7 returns a version 7 UUID using the current time.
func NewV7() UUID { return NewV7At(time.Now()) }

// NewV7At returns a version 7 UUID for the given time.
func NewV7At(t time.Time) UUID {
	var u UUID
	var ts [8]byte
	binary.BigEndian.PutUint64(
		ts[:],
		uint64(t.UnixMilli()),
	) //nolint:gosec // UnixMilli is non-negative for any realistic time
	copy(
		u[0:6],
		ts[2:8],
	) // low 48 bits of the millisecond timestamp
	if _, err := rand.Read(u[6:]); err != nil {
		panic("uuid: crypto/rand failed: " + err.Error())
	}
	u[6] = (u[6] & 0x0f) | 0x70 // version 7
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 9562 variant
	return u
}

// String formats the UUID in canonical upper-case form, which is how Apple
// renders CommandUUID values in its documentation.
func (u UUID) String() string {
	var buf [36]byte
	hex.Encode(buf[0:8], u[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], u[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], u[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], u[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], u[10:16])
	for i, c := range buf {
		if c >= 'a' && c <= 'f' {
			buf[i] = c - 'a' + 'A'
		}
	}
	return string(buf[:])
}

// Version returns the UUID version nibble.
func (u UUID) Version() int { return int(u[6] >> 4) }

// Parse accepts the canonical 8-4-4-4-12 form in either case.
func Parse(s string) (UUID, error) {
	var u UUID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return u, fmt.Errorf("%w: %q", ErrInvalid, s)
	}
	clean := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(u[:], []byte(clean)); err != nil {
		return u, fmt.Errorf("%w: %q", ErrInvalid, s)
	}
	return u, nil
}
