package axmcreds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/v3/secrets"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/crypt"
)

// ErrKeyring reports a missing or unusable keyring.
var ErrKeyring = errors.New("axmcreds: keyring")

// credentialAAD is the purpose bound into every sealed record.
const credentialAAD = "axm/credentials"

// sealedRecord is the plaintext that gets sealed.
//
//nolint:tagliatelle // internal record; the tags are storage keys
type sealedRecord struct {
	ClientID      string `json:"client_id"`
	KeyID         string `json:"key_id"`
	PrivateKeyPEM []byte `json:"private_key_pem"`
}

// Store keeps credentials in memory, sealed under a storage/crypt
// keyring with the record name as associated data, so the PEM never sits
// in plaintext at rest. Sealed returns the ciphertext for a persistence
// layer to write elsewhere.
type Store struct {
	ring *crypt.Keyring
	mu   sync.RWMutex
	data map[string][]byte
}

// New wraps a keyring.
func New(ring *crypt.Keyring) (*Store, error) {
	if ring == nil {
		return nil, fmt.Errorf("%w: nil keyring", ErrKeyring)
	}
	return &Store{ring: ring, data: map[string][]byte{}}, nil
}

// Put implements axm.CredentialStore.
func (s *Store) Put(_ context.Context, name string, creds axm.Credentials) error {
	if name == "" || creds.ClientID == "" || creds.KeyID == "" || creds.PrivateKeyPEM.IsZero() {
		return fmt.Errorf("%w: name, client id, key id, and private key are required", ErrKeyring)
	}
	if _, err := axm.ParseKey(creds.PrivateKeyPEM.Bytes()); err != nil {
		return err
	}
	plain, err := json.Marshal(sealedRecord{ClientID: creds.ClientID, KeyID: creds.KeyID, PrivateKeyPEM: creds.PrivateKeyPEM.Bytes()})
	if err != nil {
		return fmt.Errorf("%w: encode: %w", ErrKeyring, err)
	}
	sealed, err := s.ring.Seal(plain, crypt.AAD(credentialAAD, name))
	if err != nil {
		return fmt.Errorf("%w: seal: %w", ErrKeyring, err)
	}
	s.mu.Lock()
	s.data[name] = sealed
	s.mu.Unlock()
	return nil
}

// Get implements axm.CredentialStore.
func (s *Store) Get(_ context.Context, name string) (axm.Credentials, error) {
	s.mu.RLock()
	sealed, ok := s.data[name]
	s.mu.RUnlock()
	if !ok {
		return axm.Credentials{}, fmt.Errorf("%w: %s", secrets.ErrNotFound, name)
	}
	plain, _, err := s.ring.Open(sealed, crypt.AAD(credentialAAD, name))
	if err != nil {
		return axm.Credentials{}, fmt.Errorf("%w: open %s: %w", ErrKeyring, name, err)
	}
	var rec sealedRecord
	if err := json.Unmarshal(plain, &rec); err != nil {
		return axm.Credentials{}, fmt.Errorf("%w: decode %s: %w", ErrKeyring, name, err)
	}
	return axm.Credentials{ClientID: rec.ClientID, KeyID: rec.KeyID, PrivateKeyPEM: secrets.New(rec.PrivateKeyPEM)}, nil
}

// Delete implements axm.CredentialStore.
func (s *Store) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	delete(s.data, name)
	s.mu.Unlock()
	return nil
}

// Sealed returns the ciphertext stored under name, or nil.
func (s *Store) Sealed(name string) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.data[name]
	if !ok {
		return nil
	}
	return append([]byte(nil), b...)
}
