package axmstore

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/axm"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

const (
	testClientID = "BUSINESSAPI.c75c0a8a-a026-4dae-99aa-89ea1e1103e5"
	testKeyID    = "e339d085-a821-438a-a527-d044edacf50a"
)

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func sec1PEM(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func TestCredentials(t *testing.T) {
	t.Parallel()
	t.Run("SealedAtRest", func(t *testing.T) {
		t.Parallel()
		ring, err := crypt.NewKeyring(context.Background(), crypt.Options{Keys: crypt.Keys{Active: "v1"}, Provider: secrets.Static{"v1": bytes.Repeat([]byte{7}, 32)}})
		if err != nil {
			t.Fatal(err)
		}
		store, err := New(ring)
		if err != nil {
			t.Fatal(err)
		}
		key := newKey(t)
		pemBytes := sec1PEM(t, key)
		creds := axm.Credentials{ClientID: testClientID, KeyID: testKeyID, PrivateKeyPEM: secrets.New(pemBytes)}
		if err := store.Put(context.Background(), "prod", creds); err != nil {
			t.Fatal(err)
		}
		sealed := store.Sealed("prod")
		if !crypt.IsSealed(sealed) {
			t.Fatal("record is not sealed")
		}
		if bytes.Contains(sealed, pemBytes) || bytes.Contains(sealed, []byte("EC PRIVATE KEY")) || bytes.Contains(sealed, []byte(testClientID)) {
			t.Fatal("plaintext leaked into the sealed record")
		}
		got, err := store.Get(context.Background(), "prod")
		if err != nil || got.ClientID != testClientID || got.KeyID != testKeyID || !got.PrivateKeyPEM.Equal(creds.PrivateKeyPEM) {
			t.Fatalf("Get: %+v, %v", got, err)
		}
		if s := got.PrivateKeyPEM.String(); s != secrets.Redacted {
			t.Fatalf("PEM prints as %q", s)
		}
		cfg, err := axm.ConfigFrom(context.Background(), store, "prod")
		if err != nil || !cfg.PrivateKey.Equal(key) || cfg.ClientID != testClientID {
			t.Fatalf("axm.ConfigFrom: %v", err)
		}
		if _, err := axm.New(context.Background(), cfg); err != nil {
			t.Fatalf("New from stored credentials: %v", err)
		}
		if err := store.Delete(context.Background(), "prod"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(context.Background(), "prod"); !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("after delete: %v", err)
		}
		if store.Sealed("prod") != nil {
			t.Fatal("Sealed after delete")
		}
	})
	t.Run("Failing", func(t *testing.T) {
		t.Parallel()
		if _, err := New(nil); !errors.Is(err, ErrKeyring) {
			t.Fatalf("nil keyring: %v", err)
		}
		ring, err := crypt.NewKeyring(context.Background(), crypt.Options{Keys: crypt.Keys{Active: "v1"}, Provider: secrets.Static{"v1": bytes.Repeat([]byte{7}, 32)}})
		if err != nil {
			t.Fatal(err)
		}
		store, _ := New(ring)
		ctx := context.Background()
		if err := store.Put(ctx, "", axm.Credentials{}); !errors.Is(err, ErrKeyring) {
			t.Fatalf("empty: %v", err)
		}
		if err := store.Put(ctx, "x", axm.Credentials{ClientID: "c", KeyID: "k", PrivateKeyPEM: secrets.New([]byte("junk"))}); !errors.Is(err, axm.ErrKey) {
			t.Fatalf("junk key: %v", err)
		}
		if _, err := axm.ConfigFrom(ctx, nil, "x"); !errors.Is(err, axm.ErrStore) {
			t.Fatalf("nil store: %v", err)
		}
		if _, err := axm.ConfigFrom(ctx, store, "missing"); !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		// A record sealed under another key ring cannot be opened.
		other, _ := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "v1"}, Provider: secrets.Static{"v1": bytes.Repeat([]byte{9}, 32)}})
		otherStore, _ := New(other)
		if err := otherStore.Put(ctx, "x", axm.Credentials{ClientID: "c", KeyID: "k", PrivateKeyPEM: secrets.New(sec1PEM(t, newKey(t)))}); err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		store.data["x"] = otherStore.Sealed("x")
		store.mu.Unlock()
		if _, err := store.Get(ctx, "x"); !errors.Is(err, ErrKeyring) || !errors.Is(err, crypt.ErrTampered) {
			t.Fatalf("tampered: %v", err)
		}
		// A sealed record that is not JSON.
		garbage, _ := ring.Seal([]byte("{"), crypt.AAD(credentialAAD, "g"))
		store.mu.Lock()
		store.data["g"] = garbage
		store.mu.Unlock()
		if _, err := store.Get(ctx, "g"); !errors.Is(err, ErrKeyring) {
			t.Fatalf("garbage: %v", err)
		}
		// Stored credentials whose key no longer parses.
		bad, _ := json.Marshal(sealedRecord{ClientID: "c", KeyID: "k", PrivateKeyPEM: []byte("junk")})
		sealedBad, _ := ring.Seal(bad, crypt.AAD(credentialAAD, "b"))
		store.mu.Lock()
		store.data["b"] = sealedBad
		store.mu.Unlock()
		if _, err := axm.ConfigFrom(ctx, store, "b"); !errors.Is(err, axm.ErrKey) {
			t.Fatalf("bad stored key: %v", err)
		}
	})
}
