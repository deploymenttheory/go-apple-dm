package ade

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/other"
)

// ErrPasswordHash indicates invalid derivation parameters.
var ErrPasswordHash = fault.NewOperator(fault.Internal, "the password hash parameters are not valid")

// PasswordHash creates plist data for AccountConfiguration.PasswordHash or
// SetAutoAdminPassword.PasswordHash. Iterations must be positive and selected
// by the caller for its deployment; there is deliberately no default.
// The salt is 32 random bytes. The 128-byte derived value follows Apple's
// AccountConfiguration example, rather than a normative entropy-length rule.
// XML is a valid inner plist: the command embeds these bytes as plist data.
//
// https://developer.apple.com/documentation/devicemanagement/passwordhash/salted-sha512-pbkdf2-data.dictionary
func PasswordHash(password []byte, iterations int) ([]byte, error) {
	if iterations <= 0 {
		return nil, ErrPasswordHash
	}
	salt := make([]byte, 32)
	rand.Read(salt)
	return passwordHash(password, salt, iterations)
}

// passwordHash derives the password representation required by the enrollment account
// configuration.
func passwordHash(password, salt []byte, iterations int) ([]byte, error) {
	entropy, err := pbkdf2.Key(sha512.New, string(password), salt, iterations, 128)
	if err != nil {
		return nil, fmt.Errorf("%w: derivation failed", ErrPasswordHash)
	}
	data, err := plist.Marshal(
		&other.PasswordHash{SALTEDSHA512PBKDF2: other.PasswordHashSALTEDSHA512PBKDF2{
			Entropy: entropy, Iterations: int64(iterations), Salt: salt,
		}},
	)
	if err != nil {
		return nil, fmt.Errorf("encode password hash: %w", err)
	}
	return data, nil
}
