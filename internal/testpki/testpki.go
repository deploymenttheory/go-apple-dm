// Package testpki generates throwaway certificate authorities and device
// identities for tests and the device simulator.
package testpki

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"
)

// ErrNilKey is returned when no key is supplied.
var ErrNilKey = errors.New("testpki: nil key")

// Identity is a certificate with its private key.
type Identity struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

// CA is a test certificate authority.
type CA struct {
	Identity
	serial atomic.Int64
}

// NewCA creates a self-signed RSA CA valid for one day.
func NewCA(name string) (*CA, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("testpki: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("testpki: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("testpki: %w", err)
	}
	ca := &CA{Identity: Identity{Cert: cert, Key: key}}
	ca.serial.Store(1)
	return ca, nil
}

// Pool returns a pool containing only this CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

// Issue signs a device identity (ECDSA P-256) with the given common name,
// valid from notBefore for one day.
func (ca *CA) Issue(commonName string, notBefore time.Time) (*Identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("testpki: %w", err)
	}
	return ca.IssueWithKey(commonName, notBefore, key)
}

// IssueWithKey signs a device identity for an existing key.
func (ca *CA) IssueWithKey(
	commonName string,
	notBefore time.Time,
	key crypto.Signer,
) (*Identity, error) {
	if key == nil {
		return nil, ErrNilKey
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(ca.serial.Add(1)),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    notBefore,
		NotAfter:     notBefore.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, key.Public(), ca.Key)
	if err != nil {
		return nil, fmt.Errorf("testpki: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("testpki: %w", err)
	}
	return &Identity{Cert: cert, Key: key}, nil
}
