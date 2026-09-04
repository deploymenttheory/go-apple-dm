package adminauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"strings"
)

// Token format. A token is a type prefix, a random body, and a checksum:
//
//	mdmt_<30 base62 characters><6 base62 checksum characters>
//
// The prefix makes the credential recognisable to a secret scanner, and the
// checksum lets the server reject a mistyped or truncated value before it
// touches the database. The body carries 30*log2(62) is about 178 bits of
// entropy, so the stored digest needs no salt or key derivation: there is no
// dictionary to attack. Zentral's ztlu_/ztls_ tokens are the model here
// (record 0034); Fleet, by contrast, stores its bearer tokens in plaintext.
const (
	// Prefix precedes every admin token.
	Prefix = "mdmt_"
	// bodyLen is the random portion, in base62 characters.
	bodyLen = 30
	// checksumLen is the trailing CRC32, in base62 characters.
	checksumLen = 6
	// TokenLen is the total length of a well-formed token.
	TokenLen = len(Prefix) + bodyLen + checksumLen

	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	// rejectAbove is the largest multiple of 62 that fits in a byte (62*4).
	// Discarding bytes at or above it keeps the mapping to alphabet uniform.
	rejectAbove = 248
)

// Token is a plaintext admin API token. It exists only between minting and
// handing it to the operator: the store keeps Digest(t) and never the value.
type Token string

// Digest is the SHA-256 hex digest a store persists in place of the token.
// The token is high-entropy random, so a plain hash is the right primitive;
// a password KDF would only add cost.
func Digest(t Token) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// Mint returns a new token. The caller stores Digest(token) and shows the
// token to the operator once.
func Mint() (Token, error) {
	body, err := randomBase62(bodyLen)
	if err != nil {
		return "", err
	}
	head := Prefix + body
	return Token(head + checksum(head)), nil
}

// Valid reports whether t is well formed: the right prefix, the right length,
// only alphabet characters, and a checksum that matches the body. It says
// nothing about whether the token was ever issued.
//
// Checking this before a store lookup means a malformed value never becomes a
// database round trip, which is the point of carrying a checksum at all.
func Valid(t Token) bool {
	s := string(t)
	if len(s) != TokenLen || !strings.HasPrefix(s, Prefix) {
		return false
	}
	for i := len(Prefix); i < len(s); i++ {
		if !strings.ContainsRune(alphabet, rune(s[i])) {
			return false
		}
	}
	head := s[:len(Prefix)+bodyLen]
	// Constant time so a checksum probe cannot be timed, even though the
	// value it protects is not secret on its own.
	return subtle.ConstantTimeCompare([]byte(checksum(head)), []byte(s[len(Prefix)+bodyLen:])) == 1
}

// Redact renders a token for a log line or an error: the prefix, the first
// four body characters, and an ellipsis. Never log a whole token.
func Redact(t Token) string {
	s := string(t)
	if len(s) < len(Prefix)+4 {
		return Prefix + "..."
	}
	return s[:len(Prefix)+4] + "..."
}

// checksum is the CRC32 of head, base62 encoded and left-padded to
// checksumLen characters.
func checksum(head string) string {
	n := crc32.ChecksumIEEE([]byte(head))
	out := make([]byte, checksumLen)
	for i := checksumLen - 1; i >= 0; i-- {
		out[i] = alphabet[n%62]
		n /= 62
	}
	return string(out)
}

// randomBase62 returns n characters drawn uniformly from alphabet.
func randomBase62(n int) (string, error) {
	out := make([]byte, 0, n)
	// A quarter extra covers the bytes rejection sampling discards; the loop
	// refills if a draw is unlucky.
	buf := make([]byte, n+n/4+8)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("adminauth: read random: %w", err)
		}
		for _, b := range buf {
			if b >= rejectAbove {
				continue
			}
			out = append(out, alphabet[b%62])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}
