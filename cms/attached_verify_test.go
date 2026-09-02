package cms_test

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-mdm/cms"
)

// The structures below mirror RFC 5652 SignedData so tests can build
// vectors the library will not: a signer whose certificate is missing, a
// wrong messageDigest, a contentType that is not data.
type (
	tContentInfo struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}
	tIssuerAndSerial struct {
		IssuerName   asn1.RawValue
		SerialNumber *big.Int
	}
	tAttr struct {
		Type  asn1.ObjectIdentifier
		Value asn1.RawValue `asn1:"set"`
	}
	tSignerInfo struct {
		Version                   int
		IssuerAndSerialNumber     tIssuerAndSerial
		DigestAlgorithm           pkix.AlgorithmIdentifier
		AuthenticatedAttributes   []tAttr `asn1:"optional,omitempty,tag:0"`
		DigestEncryptionAlgorithm pkix.AlgorithmIdentifier
		EncryptedDigest           []byte
	}
	tSignedData struct {
		Version          int
		DigestAlgorithms []pkix.AlgorithmIdentifier `asn1:"set"`
		ContentInfo      tContentInfo
		Certificates     asn1.RawValue `asn1:"optional,tag:0"`
		SignerInfos      []tSignerInfo `asn1:"set"`
	}
)

type attrMode int

const (
	attrsNone attrMode = iota
	attrsGood
	attrsBadDigest
	attrsWrongContentType
	attrsNoDigest
	attrsNoContentType
	attrsDuplicateDigest
	attrsDuplicateContentType
	attrsDigestNotOctets
	attrsContentTypeNotOID
)

type vector struct {
	content   []byte
	signer    identity
	bundle    []*x509.Certificate // certificates carried; nil means the signer alone
	omitCert  bool                // leave the signer certificate out
	attrs     attrMode
	hash      crypto.Hash
	digestOID asn1.ObjectIdentifier
	sigOID    asn1.ObjectIdentifier
}

func attr(t *testing.T, oid asn1.ObjectIdentifier, v any) tAttr {
	t.Helper()
	b, err := asn1.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return tAttr{Type: oid, Value: asn1.RawValue{Tag: 17, IsCompound: true, Bytes: b}}
}

func build(t *testing.T, v vector) []byte {
	t.Helper()
	if v.hash == 0 {
		v.hash = crypto.SHA1
	}
	if v.digestOID == nil {
		v.digestOID = pkcs7.OIDDigestAlgorithmSHA1
	}
	if v.sigOID == nil {
		v.sigOID = pkcs7.OIDEncryptionAlgorithmRSA
	}
	h := v.hash.New()
	h.Write(v.content)
	digest := h.Sum(nil)
	var attrs []tAttr
	switch v.attrs {
	case attrsNone:
	case attrsGood:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeContentType, pkcs7.OIDData), attr(t, pkcs7.OIDAttributeMessageDigest, digest)}
	case attrsBadDigest:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeContentType, pkcs7.OIDData), attr(t, pkcs7.OIDAttributeMessageDigest, []byte("nope"))}
	case attrsWrongContentType:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeContentType, pkcs7.OIDSignedData), attr(t, pkcs7.OIDAttributeMessageDigest, digest)}
	case attrsNoDigest:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeContentType, pkcs7.OIDData)}
	case attrsNoContentType:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeMessageDigest, digest)}
	case attrsDuplicateDigest:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeContentType, pkcs7.OIDData), attr(t, pkcs7.OIDAttributeMessageDigest, digest), attr(t, pkcs7.OIDAttributeMessageDigest, digest)}
	case attrsDuplicateContentType:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeContentType, pkcs7.OIDData), attr(t, pkcs7.OIDAttributeContentType, pkcs7.OIDData), attr(t, pkcs7.OIDAttributeMessageDigest, digest)}
	case attrsDigestNotOctets:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeContentType, pkcs7.OIDData), attr(t, pkcs7.OIDAttributeMessageDigest, 42)}
	case attrsContentTypeNotOID:
		attrs = []tAttr{attr(t, pkcs7.OIDAttributeContentType, "data"), attr(t, pkcs7.OIDAttributeMessageDigest, digest)}
	}
	signed := v.content
	if attrs != nil {
		set, err := asn1.MarshalWithParams(attrs, "set")
		if err != nil {
			t.Fatal(err)
		}
		signed = set
	}
	h = v.hash.New()
	h.Write(signed)
	sig, err := v.signer.key.Sign(rand.Reader, h.Sum(nil), v.hash)
	if err != nil {
		t.Fatal(err)
	}
	var certs []byte
	if !v.omitCert {
		certs = append(certs, v.signer.cert.Raw...)
	}
	for _, c := range v.bundle {
		certs = append(certs, c.Raw...)
	}
	octets, err := asn1.Marshal(v.content)
	if err != nil {
		t.Fatal(err)
	}
	sd := tSignedData{
		Version:          1,
		DigestAlgorithms: []pkix.AlgorithmIdentifier{{Algorithm: v.digestOID}},
		ContentInfo:      tContentInfo{ContentType: pkcs7.OIDData, Content: asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: octets}},
		SignerInfos: []tSignerInfo{{
			Version:                   1,
			IssuerAndSerialNumber:     tIssuerAndSerial{IssuerName: asn1.RawValue{FullBytes: v.signer.cert.RawIssuer}, SerialNumber: v.signer.cert.SerialNumber},
			DigestAlgorithm:           pkix.AlgorithmIdentifier{Algorithm: v.digestOID},
			AuthenticatedAttributes:   attrs,
			DigestEncryptionAlgorithm: pkix.AlgorithmIdentifier{Algorithm: v.sigOID},
			EncryptedDigest:           sig,
		}},
	}
	if certs != nil {
		sd.Certificates = asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: certs}
	}
	inner, err := asn1.Marshal(sd)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := asn1.Marshal(tContentInfo{ContentType: pkcs7.OIDSignedData, Content: asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: inner}})
	if err != nil {
		t.Fatal(err)
	}
	return outer
}

// appleLikeChain issues a leaf under an intermediate that expired years
// ago and signs with SHA-1, the shape of Apple's device identity chain.
func appleLikeChain(t *testing.T) (root, inter, leaf identity) {
	t.Helper()
	root = newCA(t)
	ikey := rsaKey(t)
	itmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "Test iPhone Device CA"},
		NotBefore: time.Date(2007, 4, 16, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2014, 4, 16, 0, 0, 0, 0, time.UTC),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, SignatureAlgorithm: x509.SHA1WithRSA,
	}
	ider, err := x509.CreateCertificate(rand.Reader, itmpl, root.cert, ikey.Public(), root.key)
	if err != nil {
		t.Fatal(err)
	}
	icert, _ := x509.ParseCertificate(ider)
	inter = identity{cert: icert, key: ikey}
	lkey := rsaKey(t)
	ltmpl := &x509.Certificate{
		SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "00008030-000000000000"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, SignatureAlgorithm: x509.SHA1WithRSA,
	}
	lder, err := x509.CreateCertificate(rand.Reader, ltmpl, icert, lkey.Public(), ikey)
	if err != nil {
		t.Fatal(err)
	}
	lcert, _ := x509.ParseCertificate(lder)
	leaf = identity{cert: lcert, key: lkey}
	return root, inter, leaf
}

func TestVerifyAttached(t *testing.T) {
	t.Parallel()
	ca := newCA(t)
	leaf := newLeaf(t, ca, rsaKey(t), time.Now().Add(-time.Minute))
	content := []byte("<plist><dict><key>SERIAL</key><string>C02</string></dict></plist>")
	anchors := cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: []*x509.Certificate{ca.cert}}
	none := cms.VerifyAttachedOptions{}

	t.Run("SignedAttributes", func(t *testing.T) {
		t.Parallel()
		der := build(t, vector{content: content, signer: leaf, attrs: attrsGood})
		got, signer, err := cms.VerifyAttachedWith(der, anchors)
		if err != nil || string(got) != string(content) || !signer.Equal(leaf.cert) {
			t.Fatalf("%v", err)
		}
		// The library's own signer (contentType, messageDigest, signingTime).
		lib, _ := cms.SignAttached(content, leaf.cert, leaf.key)
		if _, _, err := cms.VerifyAttachedWith(lib, anchors); err != nil {
			t.Fatalf("library vector: %v", err)
		}
	})
	t.Run("ContentOnly", func(t *testing.T) {
		t.Parallel()
		der := build(t, vector{content: content, signer: leaf, attrs: attrsNone})
		got, _, err := cms.VerifyAttachedWith(der, anchors)
		if err != nil || string(got) != string(content) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("TamperedContent", func(t *testing.T) {
		t.Parallel()
		for _, mode := range []attrMode{attrsNone, attrsGood} {
			der := build(t, vector{content: content, signer: leaf, attrs: mode})
			// Flip a byte inside the embedded content.
			idx := indexOf(der, []byte("C02"))
			der[idx] = 'X'
			if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrSignature) {
				t.Fatalf("mode %d: %v", mode, err)
			}
		}
	})
	t.Run("TwoSigners", func(t *testing.T) {
		t.Parallel()
		other := newLeaf(t, ca, rsaKey(t), time.Now().Add(-time.Minute))
		sd, _ := pkcs7.NewSignedData(content)
		_ = sd.AddSigner(leaf.cert, leaf.key, pkcs7.SignerInfoConfig{})
		_ = sd.AddSigner(other.cert, other.key, pkcs7.SignerInfoConfig{})
		der, _ := sd.Finish()
		if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrMultipleSigners) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("SignerNotInBundle", func(t *testing.T) {
		t.Parallel()
		der := build(t, vector{content: content, signer: leaf, attrs: attrsGood, omitCert: true, bundle: []*x509.Certificate{ca.cert}})
		if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrNoSigner) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("BadMessageDigest", func(t *testing.T) {
		t.Parallel()
		der := build(t, vector{content: content, signer: leaf, attrs: attrsBadDigest})
		if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrSignature) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("WrongContentType", func(t *testing.T) {
		t.Parallel()
		der := build(t, vector{content: content, signer: leaf, attrs: attrsWrongContentType})
		if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrSignature) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("MissingOrDuplicateAttributes", func(t *testing.T) {
		t.Parallel()
		for _, mode := range []attrMode{attrsNoDigest, attrsNoContentType, attrsDuplicateDigest, attrsDuplicateContentType, attrsDigestNotOctets, attrsContentTypeNotOID} {
			der := build(t, vector{content: content, signer: leaf, attrs: mode})
			if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrSignature) {
				t.Fatalf("mode %d: %v", mode, err)
			}
		}
	})
	t.Run("SHA256", func(t *testing.T) {
		t.Parallel()
		for _, sig := range []asn1.ObjectIdentifier{pkcs7.OIDEncryptionAlgorithmRSA, pkcs7.OIDEncryptionAlgorithmRSASHA256} {
			der := build(t, vector{content: content, signer: leaf, attrs: attrsGood, hash: crypto.SHA256, digestOID: pkcs7.OIDDigestAlgorithmSHA256, sigOID: sig})
			if _, _, err := cms.VerifyAttachedWith(der, anchors); err != nil {
				t.Fatalf("%v: %v", sig, err)
			}
		}
		// A SHA-256 digest with a signature made over SHA-1 does not verify.
		der := build(t, vector{content: content, signer: leaf, attrs: attrsGood, hash: crypto.SHA1, digestOID: pkcs7.OIDDigestAlgorithmSHA256})
		if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrSignature) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("ECDSA", func(t *testing.T) {
		t.Parallel()
		ec := newLeaf(t, ca, ecKey(t), time.Now().Add(-time.Minute))
		for _, sig := range []asn1.ObjectIdentifier{pkcs7.OIDEncryptionAlgorithmECDSAP256, pkcs7.OIDDigestAlgorithmECDSASHA256} {
			der := build(t, vector{content: content, signer: ec, attrs: attrsGood, hash: crypto.SHA256, digestOID: pkcs7.OIDDigestAlgorithmSHA256, sigOID: sig})
			if _, _, err := cms.VerifyAttachedWith(der, anchors); err != nil {
				t.Fatalf("%v: %v", sig, err)
			}
		}
		der := build(t, vector{content: content, signer: ec, attrs: attrsNone, hash: crypto.SHA1, digestOID: pkcs7.OIDDigestAlgorithmSHA1, sigOID: pkcs7.OIDDigestAlgorithmECDSASHA1})
		if _, _, err := cms.VerifyAttachedWith(der, none); err != nil {
			t.Fatalf("ecdsa sha1: %v", err)
		}
	})
	t.Run("UnsupportedAlgorithms", func(t *testing.T) {
		t.Parallel()
		der := build(t, vector{content: content, signer: leaf, attrs: attrsNone, digestOID: pkcs7.OIDDigestAlgorithmSHA224})
		if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrAlgorithm) {
			t.Fatalf("digest: %v", err)
		}
		der = build(t, vector{content: content, signer: leaf, attrs: attrsNone, sigOID: pkcs7.OIDEncryptionAlgorithmRSAESOAEP})
		if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrAlgorithm) {
			t.Fatalf("signature: %v", err)
		}
	})
	t.Run("Malformed", func(t *testing.T) {
		t.Parallel()
		if _, _, err := cms.VerifyAttachedWith([]byte("garbage"), none); !errors.Is(err, cms.ErrParse) {
			t.Fatalf("garbage: %v", err)
		}
		detached, _ := cms.Sign(content, leaf.cert, leaf.key)
		if _, _, err := cms.VerifyAttachedWith(detached, none); !errors.Is(err, cms.ErrParse) {
			t.Fatalf("detached: %v", err)
		}
		// A certificate-only SignedData carries content but no signer.
		degenerate, _ := pkcs7.DegenerateCertificate(leaf.cert.Raw)
		if _, _, err := cms.VerifyAttachedWith(degenerate, none); !errors.Is(err, cms.ErrParse) && !errors.Is(err, cms.ErrNoSigner) {
			t.Fatalf("degenerate: %v", err)
		}
		sd, _ := pkcs7.NewSignedData(content)
		der, _ := sd.Finish()
		if _, _, err := cms.VerifyAttachedWith(der, none); !errors.Is(err, cms.ErrNoSigner) {
			t.Fatalf("no signers: %v", err)
		}
	})
	t.Run("ChainIgnoresValidity", func(t *testing.T) {
		t.Parallel()
		root, inter, dev := appleLikeChain(t)
		der := build(t, vector{content: content, signer: dev, attrs: attrsGood, bundle: []*x509.Certificate{inter.cert}})
		for name, a := range map[string][]*x509.Certificate{"intermediate": {inter.cert}, "root": {root.cert}, "both": {inter.cert, root.cert}} {
			if _, _, err := cms.VerifyAttachedWith(der, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: a}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		// The same chain fails under validity rules: the intermediate expired.
		if _, _, err := cms.VerifyAttachedWith(der, cms.VerifyAttachedOptions{Anchors: []*x509.Certificate{root.cert}}); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("validity enforced: %v", err)
		}
		// A stranger anchor is rejected; the intermediate missing from the bundle breaks the path to the root.
		if _, _, err := cms.VerifyAttachedWith(der, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: []*x509.Certificate{ca.cert}}); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("stranger: %v", err)
		}
		alone := build(t, vector{content: content, signer: dev, attrs: attrsGood})
		if _, _, err := cms.VerifyAttachedWith(alone, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: []*x509.Certificate{root.cert}}); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("no intermediate: %v", err)
		}
		// No anchors: signature only.
		if _, _, err := cms.VerifyAttachedWith(alone, cms.VerifyAttachedOptions{IgnoreValidity: true}); err != nil {
			t.Fatalf("no anchors: %v", err)
		}
	})
	t.Run("AnchorsJoinRoots", func(t *testing.T) {
		t.Parallel()
		der := build(t, vector{content: content, signer: leaf, attrs: attrsGood})
		other := newCA(t)
		if _, _, err := cms.VerifyAttachedWith(der, cms.VerifyAttachedOptions{VerifyOptions: cms.VerifyOptions{Roots: pool(other)}, Anchors: []*x509.Certificate{ca.cert}}); err != nil {
			t.Fatalf("anchor added to roots: %v", err)
		}
		if _, _, err := cms.VerifyAttachedWith(der, cms.VerifyAttachedOptions{Anchors: []*x509.Certificate{other.cert}}); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("wrong anchor: %v", err)
		}
		if _, _, err := cms.VerifyAttachedWith(der, cms.VerifyAttachedOptions{VerifyOptions: cms.VerifyOptions{Roots: pool(ca)}}); err != nil {
			t.Fatalf("roots only: %v", err)
		}
	})
	t.Run("PathSkipsNonCA", func(t *testing.T) {
		t.Parallel()
		// A leaf "issued" by another leaf: the issuer is not a CA, so no path.
		mid := newLeaf(t, ca, rsaKey(t), time.Now().Add(-time.Minute))
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(5), Subject: pkix.Name{CommonName: "child"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
		key := rsaKey(t)
		der, err := x509.CreateCertificate(rand.Reader, tmpl, mid.cert, key.Public(), mid.key)
		if err != nil {
			t.Fatal(err)
		}
		child, _ := x509.ParseCertificate(der)
		blob := build(t, vector{content: content, signer: identity{cert: child, key: key}, attrs: attrsGood, bundle: []*x509.Certificate{mid.cert}})
		if _, _, err := cms.VerifyAttachedWith(blob, anchors); !errors.Is(err, cms.ErrChain) {
			t.Fatalf("%v", err)
		}
	})
}

func TestVerifyAttachedPathCycle(t *testing.T) {
	t.Parallel()
	// Two CAs that certify each other and a leaf under one of them: the
	// walk never reaches a stranger anchor and must stop.
	ka, kb := rsaKey(t), rsaKey(t)
	mk := func(subject, issuer string, key, signer crypto.Signer) *x509.Certificate {
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: subject},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		}
		parent := &x509.Certificate{Subject: pkix.Name{CommonName: issuer}}
		parent.RawSubject, _ = asn1.Marshal(parent.Subject.ToRDNSequence())
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), signer)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := x509.ParseCertificate(der)
		return c
	}
	a := mk("A", "B", ka, kb)
	b := mk("B", "A", kb, ka)
	lkey := rsaKey(t)
	ltmpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "leaf"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	lder, err := x509.CreateCertificate(rand.Reader, ltmpl, a, lkey.Public(), ka)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(lder)
	blob := build(t, vector{content: []byte("x"), signer: identity{cert: leaf, key: lkey}, attrs: attrsGood, bundle: []*x509.Certificate{a, b}})
	stranger := newCA(t)
	if _, _, err := cms.VerifyAttachedWith(blob, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: []*x509.Certificate{stranger.cert}}); !errors.Is(err, cms.ErrChain) {
		t.Fatalf("%v", err)
	}
}

func indexOf(hay, needle []byte) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}

func FuzzVerifyAttached(f *testing.F) {
	ca := newCA(&testing.T{})
	leaf := newLeaf(&testing.T{}, ca, rsaKey(&testing.T{}), time.Now().Add(-time.Minute))
	attached, _ := cms.SignAttached([]byte("seed"), leaf.cert, leaf.key)
	f.Add(attached)
	f.Add([]byte("garbage"))
	f.Fuzz(func(t *testing.T, der []byte) {
		_, _, _ = cms.VerifyAttachedWith(der, cms.VerifyAttachedOptions{IgnoreValidity: true, Anchors: []*x509.Certificate{ca.cert}})
		_, _, _ = cms.VerifyAttachedWith(der, cms.VerifyAttachedOptions{VerifyOptions: cms.VerifyOptions{Roots: pool(ca)}})
	})
}
