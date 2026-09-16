package lifecycle

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// IssueHTTPS creates an explicit private-lab HTTPS certificate. Public server
// deployments should use ACME or import a certificate from their chosen issuer.
func (m *Manager) IssueHTTPS(ctx context.Context, id, rev, issuerID string) (Identity, error) {
	r, err := read(ctx, m.Store, id)
	if err != nil {
		return Identity{}, err
	}
	if r.Kind != HTTPS || r.Pending != rev {
		return Identity{}, ErrConflict
	}
	v, err := revision(&r, rev)
	if err != nil {
		return Identity{}, err
	}
	if v.Phase == "ready" {
		return m.Get(ctx, id)
	}
	ca, err := m.LoadMaterial(ctx, issuerID, "")
	if err != nil {
		return Identity{}, err
	}
	pair, err := tls.X509KeyPair(ca.Certificate, ca.Key)
	if err != nil {
		return Identity{}, err
	}
	if !pair.Leaf.IsCA {
		return Identity{}, ErrInvalid
	}
	b, _ := pem.Decode(v.CSR)
	if b == nil {
		return Identity{}, ErrInvalid
	}
	csr, err := x509.ParseCertificateRequest(b.Bytes)
	if err != nil {
		return Identity{}, err
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return Identity{}, ErrInvalid
	}
	var cert []byte
	k, _ := key(id)
	err = m.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return err
		}
		end := tx.Now().Add(365 * 24 * time.Hour)
		if pair.Leaf.NotAfter.Before(end) {
			end = pair.Leaf.NotAfter
		}
		template := &x509.Certificate{SerialNumber: serial, Subject: csr.Subject, DNSNames: csr.DNSNames, IPAddresses: csr.IPAddresses, NotBefore: tx.Now().Add(-5 * time.Minute), NotAfter: end, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, err := x509.CreateCertificate(rand.Reader, template, pair.Leaf, csr.PublicKey, signer)
		if err != nil {
			return err
		}
		cert = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca.Certificate...)
		return nil
	})
	if err != nil {
		return Identity{}, err
	}
	copy := *m
	copy.Trust.HTTPSRoots = x509.NewCertPool()
	copy.Trust.HTTPSRoots.AddCert(pair.Leaf)
	return copy.Import(ctx, id, rev, cert)
}
