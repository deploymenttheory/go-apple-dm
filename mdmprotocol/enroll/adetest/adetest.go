package adetest

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
)

// Identity is a certificate with its key.
type Identity struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

// Chain is a device identity chain: the leaf is issued by the
// intermediate, which is issued by the root.
type Chain struct {
	Root, Intermediate, Leaf Identity
}

// NewChain issues a fresh chain shaped like Apple's: an RSA root valid
// now, a "test iPhone Device CA" that expired in 2014 and is SHA-1
// signed, and a SHA-1 signed leaf valid now.
func NewChain(tb testing.TB) *Chain {
	tb.Helper()
	root := selfSigned(tb, "test Apple Root CA")
	inter := issue(tb, root, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test iPhone Device CA"},
		NotBefore: time.Date(2007, 4, 16, 22, 54, 46, 0, time.UTC), NotAfter: time.Date(2014, 4, 16, 22, 54, 46, 0, time.UTC),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		SignatureAlgorithm: x509.SHA1WithRSA,
	})
	leaf := issue(tb, inter, &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "00008030-001E2D3A0C0B802E", Organization: []string{"Apple Inc."}, OrganizationalUnit: []string{"iPhone"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		SignatureAlgorithm: x509.SHA1WithRSA,
	})
	return &Chain{Root: root, Intermediate: inter, Leaf: leaf}
}

// Anchors returns the intermediate and the root, the anchors to verify
// blobs from this chain with.
func (c *Chain) Anchors() []*x509.Certificate {
	return []*x509.Certificate{c.Intermediate.Cert, c.Root.Cert}
}

// Options returns ParseOptions anchored on this chain.
func (c *Chain) Options() ade.ParseOptions {
	return ade.ParseOptions{Anchors: c.Anchors()}
}

func selfSigned(tb testing.TB, name string) Identity {
	tb.Helper()
	key := rsaKey(tb)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		tb.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatal(err)
	}
	return Identity{Cert: cert, Key: key}
}

func issue(tb testing.TB, parent Identity, tmpl *x509.Certificate) Identity {
	tb.Helper()
	key := rsaKey(tb)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.Cert, key.Public(), parent.Key)
	if err != nil {
		tb.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatal(err)
	}
	return Identity{Cert: cert, Key: key}
}

func rsaKey(tb testing.TB) crypto.Signer {
	tb.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		tb.Fatal(err)
	}
	return key
}

// Info returns a MachineInfo for an iPhone with the given serial, with
// the keys an iOS 17 device sends.
func Info(serial string) ade.MachineInfo {
	return ade.MachineInfo{
		UDID: "00008030-001E2D3A0C0B802E", SERIAL: serial, PRODUCT: "iPhone15,2", VERSION: "21F90", OSVERSION: "17.5.1",
		LANGUAGE: new("en"), MDMCANREQUESTSOFTWAREUPDATE: new(true), SOFTWAREUPDATEDEVICEID: new("iPhone15,2"),
	}
}

// Digest selects the CMS digest algorithm.
type Digest int

// Digests. SHA1 is what Apple's device identities use.
const (
	SHA1 Digest = iota
	SHA256
)

// SignOptions control Sign.
type SignOptions struct {
	// SignedAttributes wraps the plist with contentType, messageDigest,
	// and signingTime authenticated attributes; otherwise the signature
	// is over the content alone.
	SignedAttributes bool
	Digest           Digest
	// OmitIntermediate leaves the intermediate out of the bundle.
	OmitIntermediate bool
	// Content replaces the marshalled MachineInfo when set.
	Content []byte
}

// Sign returns the CMS SignedData a device would send for info.
func Sign(tb testing.TB, chain *Chain, info ade.MachineInfo, o SignOptions) []byte {
	tb.Helper()
	content := o.Content
	if content == nil {
		var err error
		if content, err = plist.Marshal(info); err != nil {
			tb.Fatal(err)
		}
	}
	sd, err := pkcs7.NewSignedData(content)
	if err != nil {
		tb.Fatal(err)
	}
	digest := pkcs7.OIDDigestAlgorithmSHA1
	if o.Digest == SHA256 {
		digest = pkcs7.OIDDigestAlgorithmSHA256
	}
	sd.SetDigestAlgorithm(digest)
	sd.SetEncryptionAlgorithm(pkcs7.OIDEncryptionAlgorithmRSA)
	if o.SignedAttributes {
		err = sd.AddSigner(chain.Leaf.Cert, chain.Leaf.Key, pkcs7.SignerInfoConfig{})
	} else {
		err = sd.SignWithoutAttr(chain.Leaf.Cert, chain.Leaf.Key, pkcs7.SignerInfoConfig{})
	}
	if err != nil {
		tb.Fatal(err)
	}
	if !o.OmitIntermediate {
		sd.AddCertificate(chain.Intermediate.Cert)
	}
	der, err := sd.Finish()
	if err != nil {
		tb.Fatal(err)
	}
	return der
}

// Header encodes a blob as the x-apple-aspen-deviceinfo header value.
func Header(blob []byte) string { return base64.StdEncoding.EncodeToString(blob) }

// Lane is a request form.
type Lane int

// Lanes.
const (
	// LaneHeader is the web view GET with the header.
	LaneHeader Lane = iota
	// LaneQuery is a GET with the deviceinfo query parameter (URL-safe base64).
	LaneQuery
	// LaneBody is the token-based POST with the blob as the body.
	LaneBody
)

// Request builds the request form for a blob.
func Request(tb testing.TB, target string, blob []byte, lane Lane) *http.Request {
	tb.Helper()
	ctx := context.Background()
	switch lane {
	case LaneHeader:
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
		if err != nil {
			tb.Fatal(err)
		}
		req.Header.Set(ade.HeaderName, Header(blob))
		return req
	case LaneQuery:
		u, err := url.Parse(target)
		if err != nil {
			tb.Fatal(err)
		}
		q := u.Query()
		q.Set(ade.QueryParam, base64.RawURLEncoding.EncodeToString(blob))
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
		if err != nil {
			tb.Fatal(err)
		}
		return req
	default:
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(blob))
		if err != nil {
			tb.Fatal(err)
		}
		req.Header.Set("Content-Type", ade.ContentTypePKCS7)
		return req
	}
}
