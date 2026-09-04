package axm

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/v3/secrets"
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
