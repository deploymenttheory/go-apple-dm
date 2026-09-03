package axm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

// Credentials are one API account's secrets. PrivateKeyPEM is a Secret so
// it never prints.
type Credentials struct {
	ClientID      string
	KeyID         string
	PrivateKeyPEM secrets.Secret
}

// CredentialStore persists credentials by name.
type CredentialStore interface {
	// Put stores creds under name, replacing any existing record.
	Put(ctx context.Context, name string, creds Credentials) error
	// Get returns the credentials under name or secrets.ErrNotFound.
	Get(ctx context.Context, name string) (Credentials, error)
	// Delete removes the record; a missing name is not an error.
	Delete(ctx context.Context, name string) error
}

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

// SealedStore keeps credentials in memory, sealed under a storage/crypt
// keyring with the record name as associated data, so the PEM never sits
// in plaintext at rest. Sealed returns the ciphertext for a persistence
// layer to write elsewhere.
type SealedStore struct {
	ring *crypt.Keyring
	mu   sync.RWMutex
	data map[string][]byte
}

// NewSealedStore wraps a keyring.
func NewSealedStore(ring *crypt.Keyring) (*SealedStore, error) {
	if ring == nil {
		return nil, fmt.Errorf("%w: nil keyring", ErrStore)
	}
	return &SealedStore{ring: ring, data: map[string][]byte{}}, nil
}

// Put implements CredentialStore.
func (s *SealedStore) Put(_ context.Context, name string, creds Credentials) error {
	if name == "" || creds.ClientID == "" || creds.KeyID == "" || creds.PrivateKeyPEM.IsZero() {
		return fmt.Errorf("%w: name, client id, key id, and private key are required", ErrStore)
	}
	if _, err := ParseKey(creds.PrivateKeyPEM.Bytes()); err != nil {
		return err
	}
	plain, err := json.Marshal(sealedRecord{ClientID: creds.ClientID, KeyID: creds.KeyID, PrivateKeyPEM: creds.PrivateKeyPEM.Bytes()})
	if err != nil {
		return fmt.Errorf("%w: encode: %w", ErrStore, err)
	}
	sealed, err := s.ring.Seal(plain, crypt.AAD(credentialAAD, name))
	if err != nil {
		return fmt.Errorf("%w: seal: %w", ErrStore, err)
	}
	s.mu.Lock()
	s.data[name] = sealed
	s.mu.Unlock()
	return nil
}

// Get implements CredentialStore.
func (s *SealedStore) Get(_ context.Context, name string) (Credentials, error) {
	s.mu.RLock()
	sealed, ok := s.data[name]
	s.mu.RUnlock()
	if !ok {
		return Credentials{}, fmt.Errorf("%w: %s", secrets.ErrNotFound, name)
	}
	plain, _, err := s.ring.Open(sealed, crypt.AAD(credentialAAD, name))
	if err != nil {
		return Credentials{}, fmt.Errorf("%w: open %s: %w", ErrStore, name, err)
	}
	var rec sealedRecord
	if err := json.Unmarshal(plain, &rec); err != nil {
		return Credentials{}, fmt.Errorf("%w: decode %s: %w", ErrStore, name, err)
	}
	return Credentials{ClientID: rec.ClientID, KeyID: rec.KeyID, PrivateKeyPEM: secrets.New(rec.PrivateKeyPEM)}, nil
}

// Delete implements CredentialStore.
func (s *SealedStore) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	delete(s.data, name)
	s.mu.Unlock()
	return nil
}

// Sealed returns the ciphertext stored under name, or nil.
func (s *SealedStore) Sealed(name string) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.data[name]
	if !ok {
		return nil
	}
	return append([]byte(nil), b...)
}

// ConfigFrom loads the named credentials from store and returns a Config
// with ClientID, KeyID, and PrivateKey set; the caller fills in the rest.
func ConfigFrom(ctx context.Context, store CredentialStore, name string) (Config, error) {
	if store == nil {
		return Config{}, fmt.Errorf("%w: nil store", ErrStore)
	}
	creds, err := store.Get(ctx, name)
	if err != nil {
		return Config{}, err
	}
	key, err := ParseKey(creds.PrivateKeyPEM.Bytes())
	if err != nil {
		return Config{}, err
	}
	return Config{ClientID: creds.ClientID, KeyID: creds.KeyID, PrivateKey: key}, nil
}
