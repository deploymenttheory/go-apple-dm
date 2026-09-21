package lifecycle

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"howett.net/plist"
)

var vendorPurpose = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 4, 12}

// vendorFixture creates a vendor identity fixture with a trusted root, intermediate, and activated
// vendor certificate.
func vendorFixture(t *testing.T) (*Manager, *faultRepository, Material, Material, []byte) {
	t.Helper()
	m, s, now := testManager(t)
	root := rootIdentity(t, m, "authority")
	cs, err := certificates(root.Certificate)
	requireError(t, err, nil)
	intermediateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	requireError(t, err, nil)
	intermediateCert := issueCertificate(t, root, intermediateKey.Public(), &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "vendor intermediate",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(2 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	})
	der, err := x509.MarshalPKCS8PrivateKey(intermediateKey)
	requireError(t, err, nil)
	intermediate := Material{
		Certificate: append(intermediateCert, root.Certificate...),
		Key:         pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
	}
	intermediateCerts, err := certificates(intermediateCert)
	requireError(t, err, nil)
	m.Trust.Apple = append(cs, intermediateCerts...)
	v := pending(t, m, "vendor", Vendor)
	mat, err := m.LoadMaterial(t.Context(), v.ID, v.Pending)
	requireError(t, err, nil)
	leaf := issueCertificate(t, intermediate, privateSigner(t, mat.Key).Public(), &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "vendor",
		},
		NotBefore:       now.Add(-time.Minute),
		NotAfter:        now.Add(365 * 24 * time.Hour),
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtraExtensions: []pkix.Extension{{Id: vendorPurpose, Value: []byte{5, 0}}},
	})
	_, err = m.Import(t.Context(), v.ID, v.Pending, leaf)
	requireError(t, err, nil)
	_, err = m.Activate(t.Context(), v.ID, v.Pending)
	requireError(t, err, nil)
	mat, err = m.LoadMaterial(t.Context(), v.ID, "")
	requireError(t, err, nil)
	return m, s, root, mat, leaf
}

// TestVendorAndCustomerCSRExchangeValidatesEveryBoundary checks that vendor and customer CSR
// exchange validates every boundary.
func TestVendorAndCustomerCSRExchangeValidatesEveryBoundary(t *testing.T) {
	m, _, root, vendor, leaf := vendorFixture(t)
	ctx := t.Context()
	push := pending(t, m, "customer", Push)
	csr, err := m.Export(ctx, push.ID, "", "csr")
	requireError(t, err, nil)
	signed, err := m.Sign(ctx, "vendor", csr)
	requireError(t, err, nil)
	attached, err := m.AttachSignature(ctx, push.ID, push.Pending, signed)
	requireError(t, err, nil)
	if !strings.Contains(attached.NextAction, "identity.apple.com") {
		t.Fatal(attached.NextAction)
	}
	exported, err := m.Export(ctx, push.ID, "", "signed-request")
	requireError(t, err, nil)
	if !bytes.Equal(exported, signed) {
		t.Fatal("portal envelope changed")
	}
	// A separate process can adopt the downloaded DER identity and sign using
	// the original key. Repeating adoption cannot introduce a second revision.
	cs, err := certificates(leaf)
	requireError(t, err, nil)
	for range 2 {
		adopted, err := m.Adopt(
			ctx,
			Request{ID: "adopted-vendor", Kind: Vendor},
			cs[0].Raw,
			vendor.Key,
		)
		requireError(t, err, nil)
		if adopted.Active != "1" || len(adopted.Revisions) != 1 {
			t.Fatal(adopted)
		}
	}
	_, err = m.Sign(ctx, "adopted-vendor", csr)
	requireError(t, err, nil)
	_, err = m.Sign(ctx, "authority", csr)
	requireError(t, err, ErrInvalid)
	_, err = m.Sign(ctx, "missing", csr)
	requireError(t, err, ErrNotFound)
	pending(t, m, "unsigned-vendor", Vendor)
	_, err = m.Sign(ctx, "unsigned-vendor", csr)
	requireError(t, err, ErrNotFound)
	if _, err = m.Sign(ctx, "vendor", []byte("invalid CSR")); err == nil {
		t.Fatal("invalid customer CSR signed")
	}
	_, err = m.AttachSignature(ctx, "authority", "1", signed)
	requireError(t, err, ErrConflict)
	_, err = m.Adopt(ctx, Request{ID: "adopted-vendor", Kind: Issuer}, root.Certificate, root.Key)
	requireError(t, err, ErrConflict)
	_, err = m.Adopt(ctx, Request{ID: "adopted-vendor", Kind: Vendor}, root.Certificate, root.Key)
	requireError(t, err, ErrConflict)
	_, err = m.Adopt(ctx, Request{ID: "unsigned-vendor", Kind: Vendor}, leaf, vendor.Key)
	requireError(t, err, ErrConflict)
	for _, req := range []Request{{ID: "../vendor", Kind: Vendor}, {ID: "vendor", Kind: "bad"}} {
		_, err = m.Adopt(ctx, req, leaf, vendor.Key)
		requireError(t, err, ErrInvalid)
	}
	_, err = m.Adopt(ctx, Request{ID: "bad-pair", Kind: Vendor}, []byte("invalid"), vendor.Key)
	requireError(t, err, ErrInvalid)
	_, err = m.Adopt(ctx, Request{ID: "wrong-key", Kind: Vendor}, leaf, root.Key)
	requireError(t, err, ErrInvalid)
	_, err = m.Import(ctx, "vendor", "1", leaf)
	requireError(t, err, nil)
	_, err = m.Import(ctx, "vendor", "1", root.Certificate)
	requireError(t, err, ErrConflict)
	_, err = m.Import(ctx, "vendor", "missing", leaf)
	requireError(t, err, ErrNotFound)
	_, err = m.Activate(ctx, "vendor", "1")
	requireError(t, err, nil)
	_, err = m.Cancel(ctx, "vendor", "1")
	requireError(t, err, ErrConflict)
	cert, err := m.Export(ctx, "vendor", "", "certificate")
	requireError(t, err, nil)
	if !bytes.Equal(cert, vendor.Certificate) {
		t.Fatal("active certificate export changed")
	}
}

type portalEnvelope struct {
	CSR       string `plist:"PushCertRequestCSR"`
	Chain     string `plist:"PushCertCertificateChain"`
	Signature string `plist:"PushCertSignature"`
}

// TestReturnedVendorEnvelopeRejectsSubstitutionAndInvalidSignatures checks that returned vendor
// envelope rejects substitution and invalid signatures.
func TestReturnedVendorEnvelopeRejectsSubstitutionAndInvalidSignatures(t *testing.T) {
	m, _, root, _, _ := vendorFixture(t)
	ctx := t.Context()
	v := pending(t, m, "customer", Push)
	csr, err := m.Export(ctx, v.ID, "", "csr")
	requireError(t, err, nil)
	signed, err := m.Sign(ctx, "vendor", csr)
	requireError(t, err, nil)
	xml, err := base64.StdEncoding.DecodeString(string(signed))
	requireError(t, err, nil)
	var valid portalEnvelope
	_, err = plist.Unmarshal(xml, &valid)
	requireError(t, err, nil)
	_, nowErr := m.Get(ctx, v.ID)
	requireError(t, nowErr, nil)
	now := time.Now()
	for _, tc := range []struct {
		name string
		edit func(*portalEnvelope)
		want error
	}{
		{"CSR encoding", func(e *portalEnvelope) { e.CSR = "%%%" }, ErrInvalid},
		{"different CSR", func(e *portalEnvelope) { e.CSR = base64.StdEncoding.EncodeToString([]byte("different")) }, ErrConflict},
		{"chain encoding", func(e *portalEnvelope) { e.Chain = "invalid" }, ErrInvalid},
		{"CA used as vendor", func(e *portalEnvelope) { e.Chain = string(root.Certificate) }, ErrInvalid},
		{"signature encoding", func(e *portalEnvelope) { e.Signature = "%%%" }, ErrInvalid},
		{"signature substitution", func(e *portalEnvelope) { e.Signature = base64.StdEncoding.EncodeToString([]byte("invalid")) }, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := valid
			tc.edit(&envelope)
			b, err := plist.Marshal(envelope, plist.XMLFormat)
			requireError(t, err, nil)
			_, err = m.AttachSignature(
				ctx,
				v.ID,
				v.Pending,
				[]byte(base64.StdEncoding.EncodeToString(b)),
			)
			requireError(t, err, tc.want)
			if _, err = m.Export(
				ctx,
				v.ID,
				v.Pending,
				"signed-request",
			); !errors.Is(
				err,
				ErrInvalid,
			) {
				t.Fatal("invalid artifact persisted", err)
			}
		})
	}
	for _, bad := range [][]byte{[]byte("%%%"), []byte(base64.StdEncoding.EncodeToString([]byte("not a plist")))} {
		requireError(t, m.verifySigned(bad, csr, now), ErrInvalid)
	}
	requireError(t, m.verifySigned(signed, []byte("invalid CSR"), now), ErrConflict)
	unknown := *m
	unknown.Trust.Apple = []*x509.Certificate{}
	requireError(t, unknown.verifySigned(signed, csr, now), ErrInvalid)
	requireError(t, m.verifySigned(signed, csr, now.Add(366*24*time.Hour)), ErrInvalid)
	// A valid chain and vendor purpose alone cannot make an ECDSA leaf suitable
	// for the portal's RSA signature format.
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	requireError(t, err, nil)
	for _, usage := range []x509.KeyUsage{x509.KeyUsageDigitalSignature, x509.KeyUsageKeyEncipherment} {
		leaf := issueCertificate(
			t,
			root,
			ec.Public(),
			&x509.Certificate{
				Subject:         pkix.Name{CommonName: "EC vendor"},
				NotBefore:       now.Add(-time.Hour),
				NotAfter:        now.Add(time.Hour),
				KeyUsage:        usage,
				ExtraExtensions: []pkix.Extension{{Id: vendorPurpose, Value: []byte{5, 0}}},
			},
		)
		envelope := valid
		envelope.Chain = string(append(leaf, root.Certificate...))
		b, err := plist.Marshal(envelope, plist.XMLFormat)
		requireError(t, err, nil)
		requireError(
			t,
			m.verifySigned([]byte(base64.StdEncoding.EncodeToString(b)), csr, now),
			ErrInvalid,
		)
	}
}

// TestImportRejectsUntrustedExpiredWrongHostAndWrongPurpose checks that import rejects untrusted
// expired wrong host and wrong purpose.
func TestImportRejectsUntrustedExpiredWrongHostAndWrongPurpose(t *testing.T) {
	m, _, now := testManager(t)
	root := rootIdentity(t, m, "root")
	rootCerts, err := certificates(root.Certificate)
	requireError(t, err, nil)
	m.Trust.Apple = rootCerts
	m.Trust.HTTPSRoots = x509.NewCertPool()
	m.Trust.HTTPSRoots.AddCert(rootCerts[0])
	for _, kind := range []Kind{HTTPS, Vendor, Push, Issuer} {
		id := string(kind)
		v := pending(t, m, id, kind)
		material, err := m.LoadMaterial(t.Context(), id, v.Pending)
		requireError(t, err, nil)
		template := &x509.Certificate{
			Subject:     pkix.Name{CommonName: id},
			NotBefore:   now.Add(-time.Hour),
			NotAfter:    now.Add(time.Hour),
			KeyUsage:    x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		leaf := issueCertificate(t, root, privateSigner(t, material.Key).Public(), template)
		if _, err = m.Import(t.Context(), id, v.Pending, leaf); err == nil {
			t.Fatalf("%s accepted invalid purpose or hosts", kind)
		}
	}
	v, err := m.Get(t.Context(), "https")
	requireError(t, err, nil)
	material, err := m.LoadMaterial(t.Context(), v.ID, v.Pending)
	requireError(t, err, nil)
	for _, tc := range []struct {
		name string
		edit func(*x509.Certificate)
	}{
		{"future", func(c *x509.Certificate) { c.NotBefore = now.Add(time.Hour) }},
		{"expired", func(c *x509.Certificate) { c.NotAfter = now.Add(-time.Minute) }},
		{"wrong DNS", func(c *x509.Certificate) { c.DNSNames = []string{"other.example"} }},
		{"client only", func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &x509.Certificate{
				Subject:     v.Subject,
				DNSNames:    []string{"mdm.example"},
				IPAddresses: rootCerts[0].IPAddresses,
				NotBefore:   now.Add(-time.Hour),
				NotAfter:    now.Add(24 * time.Hour),
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}
			// Remove the IP request for these independent trust and validity cases.
			changeRecord(t, m, v.ID, func(r *record) { r.DNSNames = []string{"mdm.example"} })
			tc.edit(c)
			leaf := issueCertificate(t, root, privateSigner(t, material.Key).Public(), c)
			_, err := m.Import(t.Context(), v.ID, v.Pending, leaf)
			requireError(t, err, ErrInvalid)
		})
	}
	changeRecord(t, m, v.ID, func(r *record) { r.DNSNames = nil })
	leaf := issueCertificate(
		t,
		root,
		privateSigner(t, material.Key).Public(),
		&x509.Certificate{
			Subject:   v.Subject,
			NotBefore: now.Add(-time.Hour),
			NotAfter:  now.Add(time.Hour),
		},
	)
	_, err = m.Import(t.Context(), v.ID, v.Pending, leaf)
	requireError(t, err, ErrInvalid)
	_, err = m.Import(t.Context(), v.ID, v.Pending, root.Certificate)
	requireError(t, err, ErrInvalid)
	for _, bad := range [][]byte{nil, []byte("invalid DER"), []byte("-----BEGIN CERTIFICATE-----\ninvalid"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}), append(append([]byte(nil), root.Certificate...), []byte("trailing garbage")...)} {
		_, err = certificates(bad)
		requireError(t, err, ErrInvalid)
		if !bytes.Equal(certificatePEMOrDER(bad), bad) {
			t.Fatal("invalid input unexpectedly rewritten")
		}
	}
	authorities, err := AppleAuthorities()
	requireError(t, err, nil)
	if len(authorities) != 3 {
		t.Fatal("incomplete bundled Apple trust")
	}
	_, _, err = (Trust{}).apple()
	requireError(t, err, nil)
	_, err = completeChain(rootCerts, nil)
	requireError(t, err, nil)
	cs, err := certificates(leaf)
	requireError(t, err, nil)
	_, err = completeChain(cs, nil)
	requireError(t, err, ErrInvalid)
	_, err = completeChain(
		append(append([]*x509.Certificate{}, cs...), make([]*x509.Certificate, 9)...),
		nil,
	)
	requireError(t, err, ErrInvalid)
}

// TestIssuerAndHTTPSCreationRetriesAndCorruptMaterial checks issuer and HTTPS creation retries and
// corrupt material.
func TestIssuerAndHTTPSCreationRetriesAndCorruptMaterial(t *testing.T) {
	m, _, now := testManager(t)
	ctx := t.Context()
	root := rootIdentity(t, m, "root")
	cs, err := certificates(root.Certificate)
	requireError(t, err, nil)
	m.Trust.HTTPSRoots = x509.NewCertPool()
	m.Trust.HTTPSRoots.AddCert(cs[0])
	v := pending(t, m, "https", HTTPS)
	_, err = m.CreateIssuer(ctx, "root", "1", -time.Hour)
	requireError(t, err, ErrInvalid)
	_, err = m.CreateIssuer(ctx, v.ID, v.Pending, time.Hour)
	requireError(t, err, ErrConflict)
	_, err = m.IssueHTTPS(ctx, "root", "1", "root")
	requireError(t, err, ErrConflict)
	_, err = m.IssueHTTPS(ctx, "missing", "1", "root")
	requireError(t, err, ErrNotFound)
	_, err = m.IssueHTTPS(ctx, v.ID, v.Pending, "missing")
	requireError(t, err, ErrNotFound)
	_, err = m.IssueHTTPS(ctx, v.ID, v.Pending, "root")
	requireError(t, err, nil)
	first, err := m.LoadMaterial(ctx, v.ID, v.Pending)
	requireError(t, err, nil)
	_, err = m.IssueHTTPS(ctx, v.ID, v.Pending, "root")
	requireError(t, err, nil)
	again, err := m.LoadMaterial(ctx, v.ID, v.Pending)
	requireError(t, err, nil)
	if !bytes.Equal(first.Certificate, again.Certificate) {
		t.Fatal("ready HTTPS cert was regenerated")
	}
	_, err = m.Activate(ctx, v.ID, v.Pending)
	requireError(t, err, nil)
	adopted, err := m.Adopt(
		ctx,
		Request{ID: "adopted-https", Kind: HTTPS},
		first.Certificate,
		first.Key,
	)
	requireError(t, err, nil)
	if len(adopted.DNSNames) != 2 {
		t.Fatal("adoption lost SANs")
	}
	_, err = m.Begin(ctx, requestFor("root", Issuer))
	requireError(t, err, nil)
	_, err = m.CreateIssuer(ctx, "root", "2", time.Hour)
	requireError(t, err, ErrInvalid)
	*now = now.Add(time.Hour)
	_, err = m.CreateIssuer(ctx, "root", "2", 0)
	requireError(t, err, nil)
	_, err = m.CreateIssuer(ctx, "root", "2", 0)
	requireError(t, err, nil)
	_, err = m.Begin(ctx, v.Request)
	requireError(t, err, nil)
	_, err = m.Import(ctx, v.ID, "2", first.Certificate)
	requireError(t, err, ErrInvalid)
	// Corruption must be returned before an invalid key or CSR reaches issuance.
	for _, data := range [][]byte{[]byte("invalid"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}})} {
		changeRecord(
			t,
			m,
			"root",
			func(r *record) { r.Revisions[1].Phase = "awaiting-certificate"; r.Revisions[1].Key = data },
		)
		if _, err = m.CreateIssuer(ctx, "root", "2", 0); err == nil {
			t.Fatal("corrupt root key accepted")
		}
	}
	for _, data := range [][]byte{[]byte("invalid"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte{1}})} {
		changeRecord(t, m, v.ID, func(r *record) { r.Revisions[1].CSR = data })
		if _, err = m.IssueHTTPS(ctx, v.ID, "2", "root"); err == nil {
			t.Fatal("corrupt HTTPS CSR accepted")
		}
	}
	_, err = m.IssueHTTPS(ctx, v.ID, "2", "adopted-https")
	requireError(t, err, ErrInvalid)
	changeRecord(t, m, "root", func(r *record) { r.Revisions[0].Key = []byte("invalid") })
	if _, err = m.IssueHTTPS(ctx, v.ID, "2", "root"); err == nil {
		t.Fatal("corrupt issuer key accepted")
	}
}

// TestAppleChainCycleRejected checks apple chain cycle rejected.
func TestAppleChainCycleRejected(t *testing.T) {
	m, _, now := testManager(t)
	root := rootIdentity(t, m, "root")
	signer := privateSigner(t, root.Key)
	a := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "A"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	b := *a
	b.Subject.CommonName = "B"
	b.SerialNumber = big.NewInt(2)
	der, err := x509.CreateCertificate(rand.Reader, a, &b, signer.Public(), signer)
	requireError(t, err, nil)
	ca, err := x509.ParseCertificate(der)
	requireError(t, err, nil)
	der, err = x509.CreateCertificate(rand.Reader, &b, a, signer.Public(), signer)
	requireError(t, err, nil)
	cb, err := x509.ParseCertificate(der)
	requireError(t, err, nil)
	_, err = completeChain([]*x509.Certificate{ca}, []*x509.Certificate{ca, cb})
	requireError(t, err, ErrInvalid)
}
