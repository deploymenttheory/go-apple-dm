package lifecycle

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"

	"howett.net/plist"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

//go:embed apple/*.pem
var appleFiles embed.FS

// Trust separates Apple's signing authorities from HTTPS trust.
// Nil Apple selects the bundled Apple authorities. HTTPSRoots nil uses system roots.
type Trust struct {
	Apple      []*x509.Certificate
	HTTPSRoots *x509.CertPool
}

// AppleAuthorities returns the public authorities bundled with this library.
func AppleAuthorities() ([]*x509.Certificate, error) {
	files, err := appleFiles.ReadDir("apple")
	if err != nil {
		return nil, err
	}
	var out []*x509.Certificate
	for _, f := range files {
		b, err := appleFiles.ReadFile("apple/" + f.Name())
		if err != nil {
			return nil, err
		}
		cs, err := certificates(b)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
	}
	return out, nil
}

func (t Trust) apple() ([]*x509.Certificate, *x509.CertPool, error) {
	certs := t.Apple
	var err error
	if certs == nil {
		certs, err = AppleAuthorities()
		if err != nil {
			return nil, nil, err
		}
	}
	roots := x509.NewCertPool()
	for _, c := range certs {
		if bytes.Equal(c.RawIssuer, c.RawSubject) {
			roots.AddCert(c)
		}
	}
	return certs, roots, nil
}

func generate(req Request) (Material, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Material{}, err
	}
	template := &x509.CertificateRequest{Subject: req.Subject, SignatureAlgorithm: x509.SHA256WithRSA}
	for _, name := range req.DNSNames {
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return Material{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Material{}, err
	}
	return Material{Key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), CSR: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})}, nil
}

func certificates(data []byte) ([]*x509.Certificate, error) {
	if !bytes.Contains(data, []byte("-----BEGIN")) {
		c, err := x509.ParseCertificate(data)
		if err != nil {
			return nil, fmt.Errorf("%w: certificate encoding", ErrInvalid)
		}
		return []*x509.Certificate{c}, nil
	}
	var out []*x509.Certificate
	for len(bytes.TrimSpace(data)) > 0 {
		b, rest := pem.Decode(data)
		if b == nil || b.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: expected certificate PEM", ErrInvalid)
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: certificate encoding", ErrInvalid)
		}
		out = append(out, c)
		data = rest
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: empty certificate", ErrInvalid)
	}
	return out, nil
}

func certificatePEM(certs []*x509.Certificate) []byte {
	var out []byte
	for _, c := range certs {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return out
}

func completeChain(chain, authorities []*x509.Certificate) ([]*x509.Certificate, error) {
	for len(chain) < 10 {
		last := chain[len(chain)-1]
		if bytes.Equal(last.RawIssuer, last.RawSubject) {
			return chain, nil
		}
		var parent *x509.Certificate
		for _, candidate := range authorities {
			if bytes.Equal(last.RawIssuer, candidate.RawSubject) && last.CheckSignatureFrom(candidate) == nil {
				parent = candidate
				break
			}
		}
		if parent == nil {
			return nil, fmt.Errorf("%w: missing Apple issuer", ErrInvalid)
		}
		for _, c := range chain {
			if bytes.Equal(c.Raw, parent.Raw) {
				return nil, fmt.Errorf("%w: chain cycle", ErrInvalid)
			}
		}
		chain = append(chain, parent)
	}
	return nil, fmt.Errorf("%w: certificate chain too long", ErrInvalid)
}

func (m *Manager) validate(r record, material Material, at time.Time) (Material, *x509.Certificate, string, error) {
	chain, err := certificates(material.Certificate)
	if err != nil {
		return material, nil, "", err
	}
	leaf := chain[0]
	if at.Before(leaf.NotBefore) || !at.Before(leaf.NotAfter) {
		return material, nil, "", fmt.Errorf("%w: certificate outside validity interval", ErrInvalid)
	}
	var roots *x509.CertPool
	if r.Kind == Vendor || r.Kind == Push {
		authorities, pool, err := m.Trust.apple()
		if err != nil {
			return material, nil, "", err
		}
		roots = pool
		chain, err = completeChain(chain, authorities)
		if err != nil {
			return material, nil, "", err
		}
	}
	material.Certificate = certificatePEM(chain)
	pair, err := tls.X509KeyPair(material.Certificate, material.Key)
	if err != nil {
		return material, nil, "", fmt.Errorf("%w: certificate and key do not match", ErrInvalid)
	}
	var topic string
	switch r.Kind {
	case Vendor:
		purpose := asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 4, 12}
		found := false
		for _, ext := range leaf.Extensions {
			if ext.Id.Equal(purpose) {
				found = true
			}
		}
		if !found {
			return material, nil, "", fmt.Errorf("%w: not an Apple MDM Vendor CSR signing certificate", ErrInvalid)
		}
		// SignCSR performs the same ordered-chain validation used for portal submissions.
		if _, err = pushcert.SignCSR(material.CSR, material.Certificate, material.Key, roots, at); err != nil {
			return material, nil, "", err
		}
	case Push:
		p, err := pushcert.Parse(material.Certificate, material.Key)
		if err != nil {
			return material, nil, "", err
		}
		topic = p.Topic
		if r.Topic != "" && topic != r.Topic {
			return material, nil, "", fmt.Errorf("%w: renewal must retain topic %s", ErrConflict, r.Topic)
		}
		if err = pushcert.Validate(pair, topic, true, at); err != nil {
			return material, nil, "", err
		}
	case HTTPS:
		roots = m.Trust.HTTPSRoots
		if len(r.DNSNames) == 0 {
			return material, nil, "", fmt.Errorf("%w: HTTPS requires hostnames", ErrInvalid)
		}
		for _, name := range r.DNSNames {
			if err = leaf.VerifyHostname(name); err != nil {
				return material, nil, "", fmt.Errorf("%w: HTTPS hostname", ErrInvalid)
			}
		}
	case Issuer:
		if !leaf.IsCA || !leaf.BasicConstraintsValid || leaf.KeyUsage&x509.KeyUsageCertSign == 0 || leaf.KeyUsage&x509.KeyUsageCRLSign == 0 || len(leaf.SubjectKeyId) == 0 {
			return material, nil, "", fmt.Errorf("%w: issuer requires CA constraints, certificate and CRL signing usage, and a subject key identifier", ErrInvalid)
		}
		// Imported issuer keys are an explicit operator trust decision. Verify a
		// supplied chain, or the self-signature for a standalone root.
		roots = x509.NewCertPool()
		roots.AddCert(chain[len(chain)-1])
		if len(chain) == 1 && leaf.CheckSignatureFrom(leaf) != nil {
			return material, nil, "", fmt.Errorf("%w: issuer requires its certificate chain", ErrInvalid)
		}
	}
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	usage := x509.ExtKeyUsageAny
	if r.Kind == HTTPS {
		usage = x509.ExtKeyUsageServerAuth
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
		return material, nil, "", fmt.Errorf("%w: untrusted certificate chain: %w", ErrInvalid, err)
	}
	return material, leaf, topic, nil
}

// Import validates a returned certificate against the existing pending key.
func (m *Manager) Import(ctx context.Context, id, rev string, certificate []byte) (Identity, error) {
	return m.change(ctx, id, func(tx state.Tx, r *record) error {
		v, err := revision(r, rev)
		if err != nil {
			return err
		}
		if r.Pending != rev {
			cs, err := certificates(certificate)
			if err == nil && v.Fingerprint == fingerprint(cs[0].Raw) && r.Active == rev {
				return nil
			}
			return ErrConflict
		}
		material := v.Material
		material.Certificate = certificate
		material, leaf, topic, err := m.validate(*r, material, tx.Now())
		if err != nil {
			return err
		}
		if r.Active != "" {
			active, _ := revision(r, r.Active)
			if !leaf.NotAfter.After(active.NotAfter) {
				return fmt.Errorf("%w: renewal must extend validity", ErrInvalid)
			}
		}
		v.Material = material
		v.Fingerprint = fingerprint(leaf.Raw)
		v.NotBefore = leaf.NotBefore
		v.NotAfter = leaf.NotAfter
		v.Phase = "ready"
		if topic != "" {
			r.Topic = topic
		}
		return nil
	})
}

// Activate publishes a validated revision with its workflow state atomically.
func (m *Manager) Activate(ctx context.Context, id, rev string) (Identity, error) {
	return m.activate(ctx, id, rev, nil, nil)
}

func (m *Manager) activate(ctx context.Context, id, rev string, locks []string, guard func(state.Tx) error) (Identity, error) {
	return m.changeLocked(ctx, id, locks, func(tx state.Tx, r *record) error {
		if guard != nil {
			if err := guard(tx); err != nil {
				return err
			}
		}
		if r.Active == rev {
			return nil
		}
		v, err := revision(r, rev)
		if err != nil {
			return err
		}
		if r.Pending != rev || v.Phase != "ready" {
			return ErrConflict
		}
		if _, _, _, err = m.validate(*r, v.Material, tx.Now()); err != nil {
			return err
		}
		if r.Active != "" {
			old, _ := revision(r, r.Active)
			old.Phase = "retained"
		}
		r.Active = rev
		r.Pending = ""
		v.Phase = "active"
		v.ActivatedAt = tx.Now()
		if m.Publish != nil {
			return m.Publish(ctx, tx, view(*r, tx.Now()), v.Material)
		}
		return nil
	})
}

// Adopt imports existing material without replacing its key or topic.
func (m *Manager) Adopt(ctx context.Context, req Request, certificate, keyPEM []byte) (Identity, error) {
	if req.Kind != Vendor && req.Kind != Push && req.Kind != HTTPS && req.Kind != Issuer {
		return Identity{}, fmt.Errorf("%w: certificate kind", ErrInvalid)
	}
	k, err := key(req.ID)
	if err != nil {
		return Identity{}, err
	}
	pair, err := tls.X509KeyPair(certificatePEMOrDER(certificate), keyPEM)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: certificate/key pair", ErrInvalid)
	}
	if req.Subject.CommonName == "" {
		req.Subject = pair.Leaf.Subject
		// Names is a decoded representation, not an additional requested RDN.
		req.Subject.Names = nil
	}
	if req.Kind == HTTPS && len(req.DNSNames) == 0 {
		req.DNSNames = append([]string(nil), pair.Leaf.DNSNames...)
		for _, ip := range pair.Leaf.IPAddresses {
			req.DNSNames = append(req.DNSNames, ip.String())
		}
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return Identity{}, ErrInvalid
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: req.Subject}, signer)
	if err != nil {
		return Identity{}, err
	}
	var out Identity
	err = m.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, e := read(ctx, tx, req.ID)
		if e == nil {
			if r.Kind != req.Kind {
				return ErrConflict
			}
			if r.Active != "" {
				v, _ := revision(&r, r.Active)
				cs, _ := certificates(certificate)
				if len(cs) > 0 && v.Fingerprint == fingerprint(cs[0].Raw) {
					out = view(r, tx.Now())
					return nil
				}
			}
			return ErrConflict
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		r = record{Request: req, Active: "1"}
		mat, leaf, topic, err := m.validate(r, Material{Key: keyPEM, Certificate: certificate, CSR: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})}, tx.Now())
		if err != nil {
			return err
		}
		r.Topic = topic
		r.Revisions = []storedRevision{{Revision: Revision{ID: "1", Phase: "active", CreatedAt: tx.Now(), ActivatedAt: tx.Now(), Fingerprint: fingerprint(leaf.Raw), NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter}, Material: mat}}
		if m.Publish != nil {
			if err = m.Publish(ctx, tx, view(r, tx.Now()), mat); err != nil {
				return err
			}
		}
		if err = write(ctx, tx, r); err != nil {
			return err
		}
		r.Generation++
		out = view(r, tx.Now())
		return nil
	})
	return out, err
}

func certificatePEMOrDER(b []byte) []byte {
	cs, err := certificates(b)
	if err != nil {
		return b
	}
	return certificatePEM(cs)
}

// Sign signs a public customer CSR. Customer private keys are never needed.
func (m *Manager) Sign(ctx context.Context, vendor string, csr []byte) ([]byte, error) {
	r, err := read(ctx, m.Store, vendor)
	if err != nil {
		return nil, err
	}
	if r.Kind != Vendor {
		return nil, ErrInvalid
	}
	v, err := revision(&r, r.Active)
	if err != nil {
		return nil, err
	}
	_, roots, err := m.Trust.apple()
	if err != nil {
		return nil, err
	}
	var signed []byte
	k, _ := key(vendor)
	err = m.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		// Re-read under the lock so a concurrent vendor renewal cannot sign with
		// an identity that has been retired between lookup and use.
		current, err := read(ctx, tx, vendor)
		if err != nil {
			return err
		}
		v, err = revision(&current, current.Active)
		if err != nil {
			return err
		}
		signed, err = pushcert.SignCSR(csr, v.Certificate, v.Key, roots, tx.Now())
		return err
	})
	return signed, err
}

// AttachSignature binds a signed portal artifact to the pending customer CSR.
func (m *Manager) AttachSignature(ctx context.Context, id, rev string, signed []byte) (Identity, error) {
	return m.change(ctx, id, func(tx state.Tx, r *record) error {
		if r.Kind != Push || r.Pending != rev {
			return ErrConflict
		}
		v, err := revision(r, rev)
		if err != nil {
			return err
		}
		if err = m.verifySigned(signed, v.CSR, tx.Now()); err != nil {
			return err
		}
		v.SignedRequest = append([]byte(nil), signed...)
		return nil
	})
}

// CreateIssuer issues a pending root CA. Its private key was generated by Begin.
func (m *Manager) CreateIssuer(ctx context.Context, id, rev string, validity time.Duration) (Identity, error) {
	if validity == 0 {
		validity = 10 * 365 * 24 * time.Hour
	}
	if validity <= 0 {
		return Identity{}, ErrInvalid
	}
	return m.change(ctx, id, func(tx state.Tx, r *record) error {
		if r.Kind != Issuer || r.Pending != rev {
			return ErrConflict
		}
		v, err := revision(r, rev)
		if err != nil || v.Phase == "ready" {
			return err
		}
		cert, err := selfSign(*r, v.Material, tx.Now(), validity)
		if err != nil {
			return err
		}
		material := v.Material
		material.Certificate = cert
		material, leaf, _, err := m.validate(*r, material, tx.Now())
		if err != nil {
			return err
		}
		if r.Active != "" {
			active, _ := revision(r, r.Active)
			if !leaf.NotAfter.After(active.NotAfter) {
				return fmt.Errorf("%w: renewal must extend validity", ErrInvalid)
			}
		}
		v.Material = material
		v.Fingerprint = fingerprint(leaf.Raw)
		v.NotBefore, v.NotAfter = leaf.NotBefore, leaf.NotAfter
		v.Phase = "ready"
		return nil
	})
}

func selfSign(r record, material Material, now time.Time, validity time.Duration) ([]byte, error) {
	b, _ := pem.Decode(material.Key)
	if b == nil {
		return nil, ErrInvalid
	}
	key, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, ErrInvalid
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: r.Subject, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(validity), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func (m *Manager) verifySigned(signed, csrPEM []byte, at time.Time) error {
	xml, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(signed)))
	if err != nil {
		return fmt.Errorf("%w: signed request encoding", ErrInvalid)
	}
	var envelope struct {
		CSR       string `plist:"PushCertRequestCSR"`
		Chain     string `plist:"PushCertCertificateChain"`
		Signature string `plist:"PushCertSignature"`
	}
	if _, err = plist.Unmarshal(xml, &envelope); err != nil {
		return fmt.Errorf("%w: signed request plist", ErrInvalid)
	}
	csr, err := base64.StdEncoding.DecodeString(envelope.CSR)
	if err != nil {
		return ErrInvalid
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil || !bytes.Equal(csr, block.Bytes) {
		return fmt.Errorf("%w: signature belongs to another CSR", ErrConflict)
	}
	chain, err := certificates([]byte(envelope.Chain))
	if err != nil {
		return err
	}
	_, roots, err := m.Trust.apple()
	if err != nil {
		return err
	}
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	leaf := chain[0]
	purpose := false
	for _, e := range leaf.Extensions {
		if e.Id.Equal(asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 4, 12}) {
			purpose = true
		}
	}
	if !purpose || leaf.IsCA || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return fmt.Errorf("%w: vendor signing purpose", ErrInvalid)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		return fmt.Errorf("%w: signed request chain: %w", ErrInvalid, err)
	}
	pub, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return ErrInvalid
	}
	sig, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		return ErrInvalid
	}
	hash := crypto.SHA256.New()
	_, _ = hash.Write(csr)
	if err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, hash.Sum(nil), sig); err != nil {
		return fmt.Errorf("%w: vendor signature", ErrInvalid)
	}
	return nil
}
