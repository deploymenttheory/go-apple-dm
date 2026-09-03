package ca

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/clock"
)

// Errors returned by this package.
var (
	ErrCSR      = errors.New("ca: invalid certificate signing request")
	ErrPolicy   = errors.New("ca: request violates policy")
	ErrNotFound = errors.New("ca: certificate not found")
)

// Policy constrains what Sign issues. Zero values take the defaults.
type Policy struct {
	// Validity of issued certificates; default one year.
	Validity time.Duration
	// NotAfter caps the expiry absolutely, whatever Validity says. A
	// deadline is a moment, not a duration: a caller that turned one into a
	// duration would have the certificate outlive it by however long
	// issuing took, which is the kind of margin that goes unnoticed until
	// it matters. Zero means Validity alone decides.
	NotAfter time.Time
	// Backdate NotBefore by this much to absorb clock skew; default 5 minutes.
	Backdate time.Duration
	// KeyUsage default: digital signature (plus key encipherment for RSA).
	KeyUsage x509.KeyUsage
	// ExtKeyUsage default: client authentication.
	ExtKeyUsage []x509.ExtKeyUsage
	// AllowSANs permits subject alternative names from the CSR. Off by
	// default: an MDM identity is named by its subject only.
	AllowSANs bool
	// MinRSABits rejects small RSA keys; default 2048.
	MinRSABits int
	// Subject replaces the certificate request's subject when set. An ACME
	// server uses this because Apple's documentation says the server may
	// override the Subject the profile asked for: the subject is the server's
	// statement about the device, not the device's claim about itself.
	Subject *pkix.Name
	// OtherNames are subjectAltName otherName entries added to the issued
	// certificate. They come from the server, not from the request, so they
	// are not subject to AllowSANs.
	OtherNames []OtherName
	// AllowedKeys restricts the key types the CA will certify. Empty means
	// the existing behaviour: any RSA key of at least MinRSABits, and any
	// ECDSA key.
	AllowedKeys []KeyKind
}

// KeyKind names a public key type and size that the CA can be told to
// accept.
type KeyKind string

// The key kinds this package can name. A deployment lists the ones it
// approves in Policy.AllowedKeys so that a device cannot pick a key the
// deployment has not sanctioned.
const (
	KeyRSA2048 KeyKind = "rsa-2048"
	KeyRSA3072 KeyKind = "rsa-3072"
	KeyRSA4096 KeyKind = "rsa-4096"
	KeyECP256  KeyKind = "ec-p256"
	KeyECP384  KeyKind = "ec-p384"
	KeyECP521  KeyKind = "ec-p521"
)

// KindOf reports the KeyKind of a public key, and false for a key this
// package cannot describe. An RSA key of an unusual size gets no kind
// rather than an approximate one, so that AllowedKeys admits exactly the
// sizes it lists.
func KindOf(pub crypto.PublicKey) (KeyKind, bool) {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		switch key.N.BitLen() {
		case 2048:
			return KeyRSA2048, true
		case 3072:
			return KeyRSA3072, true
		case 4096:
			return KeyRSA4096, true
		}
	case *ecdsa.PublicKey:
		switch key.Curve {
		case elliptic.P256():
			return KeyECP256, true
		case elliptic.P384():
			return KeyECP384, true
		case elliptic.P521():
			return KeyECP521, true
		}
	}
	return "", false
}

func (p Policy) withDefaults() Policy {
	if p.Validity == 0 {
		p.Validity = 365 * 24 * time.Hour
	}
	if p.Backdate == 0 {
		p.Backdate = 5 * time.Minute
	}
	if len(p.ExtKeyUsage) == 0 {
		p.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	if p.MinRSABits == 0 {
		p.MinRSABits = 2048
	}
	return p
}

// Signer issues certificates.
type Signer interface {
	// Sign validates the CSR against the policy and returns the issued
	// certificate.
	Sign(ctx context.Context, csr *x509.CertificateRequest, p Policy) (*x509.Certificate, error)
	// Certificate returns the CA (or RA) certificate that signs.
	Certificate() *x509.Certificate
	// Chain returns the certificate followed by any intermediates up to but
	// not including the root, for clients that need the full chain.
	Chain() []*x509.Certificate
}

// Depot records issued certificates.
type Depot interface {
	Put(ctx context.Context, cert *x509.Certificate) error
	// Get returns the certificate with the serial or ErrNotFound.
	Get(ctx context.Context, serial *big.Int) (*x509.Certificate, error)
}

// Local is a Signer holding its key in memory.
type Local struct {
	cert   *x509.Certificate
	chain  []*x509.Certificate
	key    crypto.Signer
	depot  Depot
	clock  clock.Clock
	random io.Reader
}

// Option configures Local.
type Option func(*Local)

// WithDepot records issued certificates.
func WithDepot(d Depot) Option { return func(l *Local) { l.depot = d } }

// WithClock sets the clock (tests).
func WithClock(c clock.Clock) Option { return func(l *Local) { l.clock = c } }

// WithChain sets intermediates returned by Chain.
func WithChain(chain ...*x509.Certificate) Option { return func(l *Local) { l.chain = chain } }

// WithRandom sets the entropy source for serials (tests).
func WithRandom(r io.Reader) Option { return func(l *Local) { l.random = r } }

// NewLocal creates a signer from a CA certificate and its key.
func NewLocal(cert *x509.Certificate, key crypto.Signer, opts ...Option) (*Local, error) {
	if cert == nil || key == nil {
		return nil, errors.New("ca: certificate and key are required")
	}
	if !cert.IsCA {
		return nil, errors.New("ca: certificate is not a CA")
	}
	l := &Local{cert: cert, key: key, clock: clock.Real{}, random: rand.Reader}
	for _, o := range opts {
		o(l)
	}
	return l, nil
}

// Certificate implements Signer.
func (l *Local) Certificate() *x509.Certificate { return l.cert }

// Chain implements Signer.
func (l *Local) Chain() []*x509.Certificate {
	return append([]*x509.Certificate{l.cert}, l.chain...)
}

// Sign implements Signer.
func (l *Local) Sign(ctx context.Context, csr *x509.CertificateRequest, p Policy) (*x509.Certificate, error) {
	if csr == nil {
		return nil, fmt.Errorf("%w: nil", ErrCSR)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCSR, err)
	}
	p = p.withDefaults()
	keyUsage := p.KeyUsage
	switch pub := csr.PublicKey.(type) {
	case *rsa.PublicKey:
		if pub.N.BitLen() < p.MinRSABits {
			return nil, fmt.Errorf("%w: RSA key is %d bits, minimum %d", ErrPolicy, pub.N.BitLen(), p.MinRSABits)
		}
		if keyUsage == 0 {
			keyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		}
	case *ecdsa.PublicKey:
		if keyUsage == 0 {
			keyUsage = x509.KeyUsageDigitalSignature
		}
	default:
		return nil, fmt.Errorf("%w: unsupported key type %T", ErrPolicy, csr.PublicKey)
	}
	if len(p.AllowedKeys) > 0 {
		kind, known := KindOf(csr.PublicKey)
		if !known || !slices.Contains(p.AllowedKeys, kind) {
			return nil, fmt.Errorf("%w: key %q is not among the allowed kinds %v", ErrPolicy, kind, p.AllowedKeys)
		}
	}
	if !p.AllowSANs && (len(csr.DNSNames) > 0 || len(csr.EmailAddresses) > 0 || len(csr.IPAddresses) > 0 || len(csr.URIs) > 0) {
		return nil, fmt.Errorf("%w: subject alternative names not allowed", ErrPolicy)
	}
	serial, err := SerialFrom(l.random)
	if err != nil {
		return nil, err
	}
	now := l.clock.Now()
	subject := csr.Subject
	if p.Subject != nil {
		subject = *p.Subject
	}
	notAfter := now.Add(p.Validity)
	if !p.NotAfter.IsZero() && p.NotAfter.Before(notAfter) {
		notAfter = p.NotAfter
	}
	if !notAfter.After(now) {
		return nil, fmt.Errorf("%w: NotAfter %s has already passed", ErrPolicy, p.NotAfter.UTC())
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             now.Add(-p.Backdate),
		NotAfter:              notAfter,
		KeyUsage:              keyUsage,
		ExtKeyUsage:           p.ExtKeyUsage,
		BasicConstraintsValid: true,
	}
	switch {
	case len(p.OtherNames) > 0:
		// A certificate holds at most one subjectAltName extension and
		// x509.CreateCertificate cannot encode the otherName form, so every
		// name has to go into an extension this package builds. The template
		// fields stay empty or CreateCertificate would refuse the duplicate.
		names := SANs{OtherNames: p.OtherNames}
		if p.AllowSANs {
			names.DNSNames = csr.DNSNames
			names.EmailAddresses = csr.EmailAddresses
			names.IPAddresses = csr.IPAddresses
			names.URIs = csr.URIs
		}
		ext, ok, err := SANExtension(names, len(subject.ToRDNSequence()) == 0)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrPolicy, err)
		}
		if ok {
			tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, ext)
		}
	case p.AllowSANs:
		tmpl.DNSNames = csr.DNSNames
		tmpl.EmailAddresses = csr.EmailAddresses
		tmpl.IPAddresses = append([]net.IP(nil), csr.IPAddresses...)
		tmpl.URIs = append([]*url.URL(nil), csr.URIs...)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, l.cert, csr.PublicKey, l.key)
	if err != nil {
		return nil, fmt.Errorf("ca: sign: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("ca: parse issued certificate: %w", err)
	}
	if l.depot != nil {
		if err := l.depot.Put(ctx, cert); err != nil {
			return nil, fmt.Errorf("ca: depot: %w", err)
		}
	}
	return cert, nil
}

// Serial returns a random 127-bit positive serial number, as CA/Browser
// Forum guidance recommends over counters.
func Serial() (*big.Int, error) { return SerialFrom(rand.Reader) }

// SerialFrom draws a serial from r.
func SerialFrom(r io.Reader) (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 127)
	n, err := rand.Int(r, limit)
	if err != nil {
		return nil, fmt.Errorf("ca: serial: %w", err)
	}
	return n.Add(n, big.NewInt(1)), nil
}

// MemoryDepot keeps issued certificates in memory.
type MemoryDepot struct {
	mu    sync.RWMutex
	certs map[string]*x509.Certificate
}

// NewMemoryDepot creates an empty depot.
func NewMemoryDepot() *MemoryDepot { return &MemoryDepot{certs: map[string]*x509.Certificate{}} }

// Put implements Depot.
func (d *MemoryDepot) Put(_ context.Context, cert *x509.Certificate) error {
	if cert == nil {
		return errors.New("ca: nil certificate")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.certs[cert.SerialNumber.String()] = cert
	return nil
}

// Get implements Depot.
func (d *MemoryDepot) Get(_ context.Context, serial *big.Int) (*x509.Certificate, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	c, ok := d.certs[serial.String()]
	if !ok {
		return nil, fmt.Errorf("%w: serial %s", ErrNotFound, serial)
	}
	return c, nil
}

// Len returns how many certificates are stored.
func (d *MemoryDepot) Len() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.certs)
}

// SelfSignedOptions configure NewSelfSigned.
type SelfSignedOptions struct {
	Subject  pkix.Name
	Validity time.Duration // default 10 years
	// RSABits default 2048. SCEP requires an RSA CA because devices
	// encrypt the PKCS #7 envelope to it.
	RSABits int
	// Random defaults to crypto/rand.
	Random io.Reader
}

// NewSelfSigned generates an RSA CA certificate and key, suitable as a
// SCEP RA/CA for a self-contained deployment.
func NewSelfSigned(o SelfSignedOptions) (*x509.Certificate, *rsa.PrivateKey, error) {
	if o.Validity == 0 {
		o.Validity = 10 * 365 * 24 * time.Hour
	}
	if o.RSABits == 0 {
		o.RSABits = 2048
	}
	if o.Subject.CommonName == "" {
		o.Subject.CommonName = "go-apple-dm CA"
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	key, err := rsa.GenerateKey(o.Random, o.RSABits)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: generate key: %w", err)
	}
	serial, err := SerialFrom(o.Random)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: o.Subject,
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(o.Validity),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: create certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: parse certificate: %w", err)
	}
	return cert, key, nil
}
