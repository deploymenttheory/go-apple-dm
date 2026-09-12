package attesttest

import (
	"crypto"
	"crypto/x509"
	"encoding/json"
	"errors"
)

// MarshalPrivate exports a simulated authority for a private persistent bench.
// It contains the intermediate signing key and must never appear in reports.
func (c *CA) MarshalPrivate() ([]byte, error) {
	key, err := x509.MarshalPKCS8PrivateKey(c.interKey)
	if err != nil {
		return nil, err
	}
	return json.Marshal(
		struct{ Root, Intermediate, Key []byte }{c.Root.Raw, c.Intermediate.Raw, key},
	)
}

// ParsePrivate restores a fixture issuer and validates its key and chain.
func ParsePrivate(data []byte) (*CA, error) {
	var v struct{ Root, Intermediate, Key []byte }
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	root, err := x509.ParseCertificate(v.Root)
	if err != nil {
		return nil, err
	}
	inter, err := x509.ParseCertificate(v.Intermediate)
	if err != nil {
		return nil, err
	}
	if err = inter.CheckSignatureFrom(root); err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(v.Key)
	if err != nil {
		return nil, err
	}
	signer, ok := k.(crypto.Signer)
	if !ok {
		return nil, errors.New("fixture key is not a signer")
	}
	pub, ok := inter.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !pub.Equal(signer.Public()) {
		return nil, errors.New("fixture key mismatch")
	}
	return &CA{Root: root, Intermediate: inter, interKey: signer}, nil
}
