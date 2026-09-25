package activationlock

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

const alphabet = "0123456789ACDEFGHJKLMNPQRTUVWXYZ"

// ErrCode means the input is not a canonical Apple server bypass code.
var ErrCode = fault.ActivationLockBypassCodeInvalid

// Generate returns a fresh server code and its uppercase hexadecimal hash.
// Retain the code before enabling Activation Lock with the hash. The hash
// cannot be used to recover the code needed for unlocking.
// https://developer.apple.com/documentation/devicemanagement/creating-and-using-bypass-codes
func Generate() (code, hash string) {
	var raw [16]byte
	rand.Read(raw[:])
	code = encode(raw)
	return code, hashBytes(raw[:])
}

// Hash derives the lock hash from a canonical server code. It rejects malformed
// separators, non-Apple symbols and noncanonical final bits without echoing input.
func Hash(code string) (string, error) {
	if len(code) != 31 {
		return "", ErrCode
	}
	var raw [16]byte
	bits := 0
	for i, char := range []byte(code) {
		if i == 5 || i == 11 || i == 16 || i == 21 || i == 26 {
			if char != '-' {
				return "", ErrCode
			}
			continue
		}
		value := strings.IndexByte(alphabet, char)
		width := 5
		if bits == 125 {
			width = 3
		}
		if value < 0 || value >= 1<<width {
			return "", ErrCode
		}
		for bit := width - 1; bit >= 0; bit-- {
			raw[bits/8] |= byte((value>>bit)&1) << (7 - bits%8)
			bits++
		}
	}
	return hashBytes(raw[:]), nil
}

// encode renders the bypass value in the representation expected by the Activation Lock
// protocol.
func encode(raw [16]byte) string {
	var out strings.Builder
	for bits, symbol := 0, 0; bits < 128; symbol++ {
		if symbol == 5 || symbol == 10 || symbol == 14 || symbol == 18 || symbol == 22 {
			out.WriteByte('-')
		}
		width := min(5, 128-bits)
		var value byte
		for range width {
			value = value<<1 | (raw[bits/8]>>(7-bits%8))&1
			bits++
		}
		out.WriteByte(alphabet[value])
	}
	return out.String()
}

// hashBytes derives the activation-lock hash from the supplied secret bytes.
func hashBytes(raw []byte) string {
	// All parameters are fixed, valid PBKDF2 parameters from Apple's algorithm.
	derived, _ := pbkdf2.Key(sha256.New, string(raw), []byte{0, 0, 0, 0}, 50000, 32)
	return strings.ToUpper(hex.EncodeToString(derived))
}
