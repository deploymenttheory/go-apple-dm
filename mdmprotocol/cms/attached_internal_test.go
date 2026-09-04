package cms

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"
)

func TestSignAttachedFailures(t *testing.T) {
	t.Parallel()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	cert := selfSigned(t, key, time.Now().Add(-time.Minute))
	if _, err := SignAttached([]byte("x"), cert, failingSigner{pub: struct{}{}}); !errors.Is(err, ErrAlgorithm) {
		t.Fatalf("unsupported key: %v", err)
	}
	if _, err := SignAttached([]byte("x"), cert, failingSigner{pub: &key.PublicKey}); !errors.Is(err, ErrSign) {
		t.Fatalf("signer failure: %v", err)
	}
}
