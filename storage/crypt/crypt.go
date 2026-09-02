// Package crypt seals the per-device secrets a storage backend must retain
// on Apple's behalf, such as the UnlockToken and the bootstrap token that
// the check-in protocol hands to the server
// (https://developer.apple.com/documentation/devicemanagement/check-in),
// so a copy of the database alone does not expose them (decision record
// 0013). Values are sealed with AES-256-GCM under a named key derived from
// a secrets.Provider. The key name travels in the ciphertext header, which
// lets an operator rotate by naming a new active key while keeping the
// retired key in the accepted list until every stored value has been
// rewrapped.
package crypt

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/deploymenttheory/go-apple-mdm/secrets"
)

// Sentinel errors returned by this package. Callers should test them with
// errors.Is because the returned values may carry extra context.
var (
	// ErrNoKeyring is returned when Seal or Open is called on a nil Keyring.
	ErrNoKeyring = errors.New("crypt: no keyring configured")
	// ErrUnknownKey is returned when a header names a key the ring does not hold.
	ErrUnknownKey = errors.New("crypt: ciphertext uses an unknown key")
	// ErrTampered is returned when authentication fails, including a wrong AAD.
	ErrTampered = errors.New("crypt: authentication failed")
	// ErrUnsealed is returned by strict callers that meet a value without the header.
	ErrUnsealed = errors.New("crypt: value is not sealed")
	// ErrWeakKey is returned when a provider supplies fewer than 16 bytes of key material.
	ErrWeakKey = errors.New("crypt: key material shorter than 16 bytes")
	// ErrBadFormat is returned for input that is not a well formed sealed value.
	ErrBadFormat = errors.New("crypt: malformed ciphertext")
	// ErrNoActive is returned when Keys.Active is empty.
	ErrNoActive = errors.New("crypt: no active key name")
)

const (
	// formatVersion is the last byte of the magic and identifies this layout.
	formatVersion = 0x01
	// magicLen is the length of the fixed header prefix.
	magicLen = 6
	// nonceLen is the AES-GCM nonce length in bytes.
	nonceLen = 12
	// tagLen is the AES-GCM authentication tag length in bytes.
	tagLen = 16
	// keyLen is the derived AES key length in bytes (AES-256).
	keyLen = 32
	// minMaterial is the shortest provider value accepted as key material.
	minMaterial = 16
	// maxNameLen is the longest key name that fits in the one byte length field.
	maxNameLen = 255
	// hkdfInfo binds derived keys to this package and layout version.
	hkdfInfo = "go-apple-mdm/storage/crypt/v1"
	// headerLen is the fixed part of the header, before the key name.
	headerLen = magicLen + 1
)

// magic starts every sealed value. The leading NUL keeps it apart from
// plist and PEM text, and the final byte is the layout version.
var magic = [magicLen]byte{0x00, 'g', 'a', 'm', 'c', formatVersion}

// randReader supplies nonces. It is a variable so tests can make it fail.
var randReader io.Reader = rand.Reader

// Keys names the active key and the retired keys still accepted on read.
type Keys struct {
	// Active is the key name every Seal uses.
	Active string
	// Accepted lists retired key names Open still honours.
	Accepted []string
	// Strict makes callers refuse unsealed values (set once Rewrap has run everywhere).
	Strict bool
}

// Options configures NewKeyring.
type Options struct {
	// Keys names the keys to fetch.
	Keys Keys
	// Provider supplies the raw key material for each name.
	Provider secrets.Provider
}

// Keyring holds the derived AEAD for each key name. It is safe for
// concurrent use once constructed.
type Keyring struct {
	active string
	strict bool
	keys   map[string]cipher.AEAD
}

// NewKeyring fetches every named key from the provider once, so a provider
// failure is a construction error rather than a surprise at the first
// Seal. Provider bytes are normalised to a 32 byte AES key with HKDF over
// SHA-256, salted with the key name, so two names backed by the same
// material still yield distinct keys.
func NewKeyring(ctx context.Context, o Options) (*Keyring, error) {
	if o.Keys.Active == "" {
		return nil, ErrNoActive
	}
	if o.Provider == nil {
		return nil, fmt.Errorf("%w: nil provider", ErrNoKeyring)
	}
	k := &Keyring{
		active: o.Keys.Active,
		strict: o.Keys.Strict,
		keys:   make(map[string]cipher.AEAD, 1+len(o.Keys.Accepted)),
	}
	names := append([]string{o.Keys.Active}, o.Keys.Accepted...)
	for _, name := range names {
		if _, done := k.keys[name]; done {
			continue
		}
		aead, err := load(ctx, o.Provider, name)
		if err != nil {
			return nil, err
		}
		k.keys[name] = aead
	}
	return k, nil
}

// load fetches one key from the provider and derives its AEAD.
func load(ctx context.Context, p secrets.Provider, name string) (cipher.AEAD, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	s, err := p.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("crypt: key %q: %w", name, err)
	}
	material := s.Bytes()
	if len(material) < minMaterial {
		return nil, fmt.Errorf("%w: key %q", ErrWeakKey, name)
	}
	key, err := hkdf.Key(sha256.New, material, []byte(name), hkdfInfo, keyLen)
	if err != nil {
		return nil, fmt.Errorf("crypt: key %q: derive: %w", name, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypt: key %q: cipher: %w", name, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypt: key %q: gcm: %w", name, err)
	}
	return aead, nil
}

// checkName enforces the header constraints on a key name.
func checkName(name string) error {
	if name == "" || len(name) > maxNameLen {
		return fmt.Errorf("%w: key name %q must be 1 to %d bytes", ErrBadFormat, name, maxNameLen)
	}
	if bytes.IndexByte([]byte(name), 0) >= 0 {
		return fmt.Errorf("%w: key name %q contains NUL", ErrBadFormat, name)
	}
	return nil
}

// Active returns the name of the key Seal uses.
func (k *Keyring) Active() string {
	if k == nil {
		return ""
	}
	return k.active
}

// Strict reports whether callers should refuse unsealed values.
func (k *Keyring) Strict() bool {
	if k == nil {
		return false
	}
	return k.strict
}

// Seal encrypts plaintext under the active key and returns the sealed
// value: magic, key name length, key name, nonce, then the AES-GCM output
// including its tag. Empty plaintext is allowed and yields a sealed empty
// value. The aad is authenticated but not stored; Open must be given the
// same bytes.
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, error) {
	if k == nil {
		return nil, ErrNoKeyring
	}
	aead := k.keys[k.active]
	name := k.active
	out := make([]byte, 0, headerLen+len(name)+nonceLen+len(plaintext)+tagLen)
	out = append(out, magic[:]...)
	out = append(out, byte(len(name))) // #nosec G115 -- NewKeyring rejects names longer than 255 bytes
	out = append(out, name...)
	nonceStart := len(out)
	out = out[:nonceStart+nonceLen]
	if _, err := io.ReadFull(randReader, out[nonceStart:]); err != nil {
		return nil, fmt.Errorf("crypt: nonce: %w", err)
	}
	return aead.Seal(out, out[nonceStart:], plaintext, aad), nil
}

// Open decrypts a sealed value and reports which key name it used. It
// returns ErrBadFormat for input that is not sealed or is too short,
// ErrUnknownKey when the header names a key the ring does not hold, and
// ErrTampered when authentication fails, which includes a wrong aad.
func (k *Keyring) Open(ciphertext, aad []byte) (plaintext []byte, keyName string, err error) {
	if k == nil {
		return nil, "", ErrNoKeyring
	}
	name, body, err := split(ciphertext)
	if err != nil {
		return nil, "", err
	}
	aead, ok := k.keys[name]
	if !ok {
		return nil, "", fmt.Errorf("%w: %q", ErrUnknownKey, name)
	}
	nonce, sealed := body[:nonceLen], body[nonceLen:]
	plaintext, err = aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		return nil, "", fmt.Errorf("%w: key %q", ErrTampered, name)
	}
	return plaintext, name, nil
}

// split validates the header and returns the key name and the bytes that
// follow it (nonce plus sealed body).
func split(b []byte) (string, []byte, error) {
	name, ok := KeyName(b)
	if !ok {
		return "", nil, ErrBadFormat
	}
	rest := b[headerLen+len(name):]
	if len(rest) < nonceLen+tagLen {
		return "", nil, fmt.Errorf("%w: %d bytes after key name, need at least %d", ErrBadFormat, len(rest), nonceLen+tagLen)
	}
	return name, rest, nil
}

// IsSealed reports whether b starts with the sealed value header.
func IsSealed(b []byte) bool {
	return len(b) >= magicLen && bytes.Equal(b[:magicLen], magic[:])
}

// KeyName returns the key name in a sealed header without decrypting. It
// reports ok as false when b is not sealed or the header is malformed.
func KeyName(b []byte) (string, bool) {
	if !IsSealed(b) || len(b) < headerLen {
		return "", false
	}
	n := int(b[magicLen])
	if n == 0 || len(b) < headerLen+n {
		return "", false
	}
	return string(b[headerLen : headerLen+n]), true
}

// AAD builds the associated data that binds a value to its table column
// and row, as purpose, a NUL byte, then rowID. Moving a sealed value to
// another column or row therefore fails to open.
func AAD(purpose, rowID string) []byte {
	out := make([]byte, 0, len(purpose)+1+len(rowID))
	out = append(out, purpose...)
	out = append(out, 0)
	return append(out, rowID...)
}
