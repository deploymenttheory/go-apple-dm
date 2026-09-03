package ca_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ca"
)

// sanOID is the subjectAltName extension identifier, repeated here because
// the package keeps its copy unexported.
var sanOID = asn1.ObjectIdentifier{2, 5, 29, 17}

// emptySubjectCSR builds a request that names nothing, the shape an ACME
// client sends when the identifier lives entirely in the otherName.
func emptySubjectCSR(t *testing.T, key any) *x509.CertificateRequest {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	req, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// sanCert wraps a raw subjectAltName value in the smallest certificate the
// parse functions will look at.
func sanCert(value []byte) *x509.Certificate {
	return &x509.Certificate{Extensions: []pkix.Extension{{Id: sanOID, Value: value}}}
}

// testSigner returns a signer over a throwaway CA.
func testSigner(t *testing.T) *ca.Local {
	t.Helper()
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ca.NewLocal(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// sanExtension returns the certificate's single subjectAltName extension,
// failing if there is not exactly one: a second one would be a certificate
// no client can parse.
func sanExtension(t *testing.T, cert *x509.Certificate) pkix.Extension {
	t.Helper()
	var found []pkix.Extension
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(sanOID) {
			found = append(found, ext)
		}
	}
	if len(found) != 1 {
		t.Fatalf("certificate has %d subjectAltName extensions, want 1", len(found))
	}
	return found[0]
}

func TestPermanentIdentifierRoundTrip(t *testing.T) {
	t.Parallel()
	signer := testSigner(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ca.PermanentIdentifier("C02XX1234567")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := signer.Sign(context.Background(), csr(t, key, "device", false), ca.Policy{OtherNames: []ca.OtherName{other}})
	if err != nil {
		t.Fatal(err)
	}
	if ext := sanExtension(t, issued); ext.Critical {
		t.Error("subjectAltName should not be critical when the subject names the device")
	}
	value, ok, err := ca.ParsePermanentIdentifier(issued)
	if err != nil || !ok || value != "C02XX1234567" {
		t.Fatalf("permanent identifier %q, present %v, err %v", value, ok, err)
	}
	names, err := ca.ParseOtherNames(issued)
	if err != nil || len(names) != 1 || !names[0].ID.Equal(ca.OIDPermanentIdentifier) {
		t.Fatalf("other names %+v: %v", names, err)
	}
	// A certificate with no otherName at all is not an error, it simply has
	// nothing to report.
	plain, err := signer.Sign(context.Background(), csr(t, key, "plain", false), ca.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if names, err := ca.ParseOtherNames(plain); err != nil || names != nil {
		t.Fatalf("plain certificate other names %+v: %v", names, err)
	}
	if _, ok, err := ca.ParsePermanentIdentifier(plain); err != nil || ok {
		t.Fatalf("plain certificate permanent identifier present %v: %v", ok, err)
	}
}

func TestSANCriticalWhenSubjectEmpty(t *testing.T) {
	t.Parallel()
	signer := testSigner(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ca.PermanentIdentifier("empty-subject")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := signer.Sign(context.Background(), emptySubjectCSR(t, key), ca.Policy{OtherNames: []ca.OtherName{other}})
	if err != nil {
		t.Fatal(err)
	}
	if !sanExtension(t, issued).Critical {
		t.Error("RFC 5280 requires a critical subjectAltName when the subject is empty")
	}
	value, ok, err := ca.ParsePermanentIdentifier(issued)
	if err != nil || !ok || value != "empty-subject" {
		t.Fatalf("permanent identifier %q, present %v, err %v", value, ok, err)
	}
}

func TestPolicySubjectOverridesRequest(t *testing.T) {
	t.Parallel()
	signer := testSigner(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	subject := pkix.Name{CommonName: "server says so", Organization: []string{"acme"}}
	issued, err := signer.Sign(context.Background(), csr(t, key, "device claims this", false), ca.Policy{Subject: &subject})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Subject.CommonName != "server says so" || issued.Subject.Organization[0] != "acme" {
		t.Fatalf("subject %+v", issued.Subject)
	}
}

func TestOtherNamesAndConventionalSANsShareOneExtension(t *testing.T) {
	t.Parallel()
	signer := testSigner(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	hardware, err := ca.HardwareModuleName(asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 2, 1}, []byte("serial"))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := ca.NTPrincipalName("device@example")
	if err != nil {
		t.Fatal(err)
	}
	permanent, err := ca.PermanentIdentifier("both")
	if err != nil {
		t.Fatal(err)
	}
	policy := ca.Policy{AllowSANs: true, OtherNames: []ca.OtherName{permanent, hardware, principal}}
	issued, err := signer.Sign(context.Background(), csr(t, key, "both", true), policy)
	if err != nil {
		t.Fatal(err)
	}
	sanExtension(t, issued)
	if len(issued.DNSNames) != 1 || issued.DNSNames[0] != "dev.example" {
		t.Errorf("DNS names %v", issued.DNSNames)
	}
	if len(issued.EmailAddresses) != 1 || len(issued.IPAddresses) != 1 || len(issued.URIs) != 1 {
		t.Errorf("conventional names %v %v %v", issued.EmailAddresses, issued.IPAddresses, issued.URIs)
	}
	names, err := ca.ParseOtherNames(issued)
	if err != nil || len(names) != 3 {
		t.Fatalf("other names %+v: %v", names, err)
	}
	if !names[1].ID.Equal(ca.OIDHardwareModuleName) || !names[2].ID.Equal(ca.OIDNTPrincipalName) {
		t.Errorf("other name identifiers %v %v", names[1].ID, names[2].ID)
	}
	value, ok, err := ca.ParsePermanentIdentifier(issued)
	if err != nil || !ok || value != "both" {
		t.Fatalf("permanent identifier %q, present %v, err %v", value, ok, err)
	}
	// The otherNames come from the server, so they survive a policy that
	// refuses the request's own names.
	strict, err := signer.Sign(context.Background(), csr(t, key, "strict", false), ca.Policy{OtherNames: []ca.OtherName{permanent}})
	if err != nil {
		t.Fatal(err)
	}
	if len(strict.DNSNames) != 0 {
		t.Errorf("DNS names %v leaked into a certificate issued without AllowSANs", strict.DNSNames)
	}
}

func TestSANExtensionBuildsEveryNameForm(t *testing.T) {
	t.Parallel()
	if _, ok, err := ca.SANExtension(ca.SANs{}, false); ok || err != nil {
		t.Fatalf("empty names built an extension: %v %v", ok, err)
	}
	other, err := ca.PermanentIdentifier("x")
	if err != nil {
		t.Fatal(err)
	}
	names := ca.SANs{
		OtherNames:     []ca.OtherName{other},
		DNSNames:       []string{"a.example"},
		EmailAddresses: []string{"a@example"},
		IPAddresses:    []net.IP{net.IPv4(10, 0, 0, 1), net.ParseIP("2001:db8::1")},
		URIs:           []*url.URL{{Scheme: "urn", Opaque: "x"}},
	}
	ext, ok, err := ca.SANExtension(names, true)
	if err != nil || !ok || !ext.Critical || !ext.Id.Equal(sanOID) {
		t.Fatalf("extension %+v ok %v err %v", ext, ok, err)
	}
	parsed, err := ca.ParseOtherNames(sanCert(ext.Value))
	if err != nil || len(parsed) != 1 {
		t.Fatalf("round trip %+v: %v", parsed, err)
	}
}

func TestSANExtensionRejectsUnencodableNames(t *testing.T) {
	t.Parallel()
	good, err := ca.PermanentIdentifier("x")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]ca.SANs{
		"no type identifier": {OtherNames: []ca.OtherName{{Value: good.Value}}},
		"no value":           {OtherNames: []ca.OtherName{{ID: ca.OIDPermanentIdentifier}}},
		"unencodable type":   {OtherNames: []ca.OtherName{{ID: asn1.ObjectIdentifier{9}, Value: good.Value}}},
		"non-IA5 DNS name":   {DNSNames: []string{"dëv.example"}},
		"non-IA5 email":      {EmailAddresses: []string{"dëv@example"}},
		"non-IA5 URI":        {URIs: []*url.URL{{Scheme: "urn", Opaque: "dëv"}}},
		"short IP address":   {IPAddresses: []net.IP{{10, 0}}},
		"nil URI":            {URIs: []*url.URL{nil}},
	}
	for name, names := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, ok, err := ca.SANExtension(names, false); err == nil || ok {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

// TestSANExtensionMatchesCryptoX509 checks the hand-written DER against
// the encoder in the standard library. The names are long enough to force
// the long definite length form, which is where a hand-written encoder is
// most likely to be wrong.
func TestSANExtensionMatchesCryptoX509(t *testing.T) {
	t.Parallel()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	repeat := func(n int) string { return strings.Repeat("a", n) + ".example" }
	// One set per length form: under 128 bytes, one length octet, and two.
	sets := map[string]ca.SANs{
		"short form": {
			DNSNames:       []string{"b.example"},
			EmailAddresses: []string{"a@example"},
			IPAddresses:    []net.IP{net.IPv4(10, 0, 0, 1)},
			URIs:           []*url.URL{{Scheme: "urn", Opaque: "x"}},
		},
		"one length octet": {
			DNSNames:    []string{repeat(120), "b.example"},
			IPAddresses: []net.IP{net.ParseIP("2001:db8::1")},
		},
		"two length octets": {
			DNSNames: []string{repeat(200), repeat(200)},
		},
	}
	for name, names := range sets {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tmpl := &x509.Certificate{
				SerialNumber: big.NewInt(1),
				Subject:      pkix.Name{CommonName: "compare"},
				NotBefore:    time.Now(), NotAfter: time.Now().Add(time.Hour),
				DNSNames: names.DNSNames, EmailAddresses: names.EmailAddresses,
				IPAddresses: names.IPAddresses, URIs: names.URIs,
			}
			der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			reference, err := x509.ParseCertificate(der)
			if err != nil {
				t.Fatal(err)
			}
			ext, ok, err := ca.SANExtension(names, false)
			if err != nil || !ok {
				t.Fatalf("build extension: %v %v", ok, err)
			}
			if want := sanExtension(t, reference); !bytes.Equal(ext.Value, want.Value) {
				t.Fatalf("subjectAltName differs from crypto/x509:\n got %x\nwant %x", ext.Value, want.Value)
			}
		})
	}
}

func TestOtherNameConstructorsRejectEmptyInput(t *testing.T) {
	t.Parallel()
	if _, err := ca.PermanentIdentifier(""); err == nil {
		t.Error("empty permanent identifier accepted")
	}
	if _, err := ca.NTPrincipalName(""); err == nil {
		t.Error("empty NT principal name accepted")
	}
	if _, err := ca.HardwareModuleName(nil, []byte("s")); err == nil {
		t.Error("empty hardware module type accepted")
	}
	if _, err := ca.HardwareModuleName(ca.OIDHardwareModuleName, nil); err == nil {
		t.Error("empty hardware module serial accepted")
	}
	if _, err := ca.HardwareModuleName(asn1.ObjectIdentifier{9}, []byte("s")); err == nil {
		t.Error("unencodable hardware module type accepted")
	}
}

// otherNameSAN wraps an already-encoded otherName body in a subjectAltName
// sequence, so a test can hand the parser a shape no builder produces.
func otherNameSAN(t *testing.T, body []byte) []byte {
	t.Helper()
	name, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: body})
	if err != nil {
		t.Fatal(err)
	}
	value, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: name})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestParseOtherNamesRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	if _, err := ca.ParseOtherNames(nil); err == nil {
		t.Error("nil certificate accepted")
	}
	oid, err := asn1.Marshal(ca.OIDPermanentIdentifier)
	if err != nil {
		t.Fatal(err)
	}
	wrongTag, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: []byte{0x05, 0x00}})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: []byte{0x05, 0x00}})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"garbage":                   {0xff},
		"trailing data":             {0x30, 0x00, 0x05, 0x00},
		"not a sequence":            {0x04, 0x00},
		"truncated general name":    {0x30, 0x02, 0xa0, 0x05},
		"otherName without type":    otherNameSAN(t, nil),
		"otherName without value":   otherNameSAN(t, oid),
		"otherName value not [0]":   otherNameSAN(t, append(append([]byte(nil), oid...), wrongTag...)),
		"trailing after the value":  otherNameSAN(t, append(append(append([]byte(nil), oid...), explicit...), explicit...)),
		"otherName type not an OID": otherNameSAN(t, append([]byte{0x05, 0x00}, explicit...)),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ca.ParseOtherNames(sanCert(value)); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func TestParsePermanentIdentifierRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	if _, _, err := ca.ParsePermanentIdentifier(nil); err == nil {
		t.Error("nil certificate accepted")
	}
	if _, _, err := ca.ParsePermanentIdentifier(sanCert([]byte{0xff})); err == nil {
		t.Error("malformed subjectAltName accepted")
	}
	// A permanent identifier whose value is not the RFC 4043 sequence, and
	// one that carries a second value after it.
	notASequence := ca.OtherName{ID: ca.OIDPermanentIdentifier, Value: []byte{0x05, 0x00}}
	good, err := ca.PermanentIdentifier("x")
	if err != nil {
		t.Fatal(err)
	}
	doubled := ca.OtherName{ID: ca.OIDPermanentIdentifier, Value: append(append([]byte(nil), good.Value...), good.Value...)}
	for name, other := range map[string]ca.OtherName{"not a sequence": notASequence, "doubled value": doubled} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ext, ok, err := ca.SANExtension(ca.SANs{OtherNames: []ca.OtherName{other}}, false)
			if err != nil || !ok {
				t.Fatalf("build %s: %v", name, err)
			}
			if _, _, err := ca.ParsePermanentIdentifier(sanCert(ext.Value)); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
	// An otherName of another form is skipped rather than misread.
	principal, err := ca.NTPrincipalName("someone@example")
	if err != nil {
		t.Fatal(err)
	}
	ext, _, err := ca.SANExtension(ca.SANs{OtherNames: []ca.OtherName{principal}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ca.ParsePermanentIdentifier(sanCert(ext.Value)); err != nil || ok {
		t.Fatalf("NT principal name read as a permanent identifier: %v %v", ok, err)
	}
}

func TestSignRejectsUncertifiableRequests(t *testing.T) {
	t.Parallel()
	signer := testSigner(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// An otherName the policy cannot encode is a policy fault, not a fault
	// in the request, but the caller still learns the certificate was not
	// issued for policy reasons.
	broken := ca.Policy{OtherNames: []ca.OtherName{{ID: ca.OIDPermanentIdentifier}}}
	if _, err := signer.Sign(context.Background(), csr(t, key, "broken", false), broken); !errors.Is(err, ca.ErrPolicy) {
		t.Fatalf("unencodable otherName: %v", err)
	}
	// Ed25519 is a key type the CA has no policy for, so it is refused
	// rather than certified under RSA or ECDSA rules.
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Sign(context.Background(), csr(t, edKey, "ed25519", false), ca.Policy{}); !errors.Is(err, ca.ErrPolicy) {
		t.Fatalf("Ed25519 key: %v", err)
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	// The RSA moduli and EC curves are synthetic because KindOf looks only
	// at the size, and generating a 4096-bit key to learn that would cost
	// seconds.
	rsaKey := func(bits int) *rsa.PublicKey {
		return &rsa.PublicKey{N: new(big.Int).Lsh(big.NewInt(1), uint(bits-1)), E: 65537}
	}
	cases := []struct {
		key  any
		want ca.KeyKind
	}{
		{rsaKey(2048), ca.KeyRSA2048},
		{rsaKey(3072), ca.KeyRSA3072},
		{rsaKey(4096), ca.KeyRSA4096},
		{&ecdsa.PublicKey{Curve: elliptic.P256()}, ca.KeyECP256},
		{&ecdsa.PublicKey{Curve: elliptic.P384()}, ca.KeyECP384},
		{&ecdsa.PublicKey{Curve: elliptic.P521()}, ca.KeyECP521},
	}
	for _, c := range cases {
		if got, ok := ca.KindOf(c.key); !ok || got != c.want {
			t.Errorf("KindOf(%T) = %q, %v; want %q", c.key, got, ok, c.want)
		}
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unsupported := []any{pub, nil, rsaKey(1024), &ecdsa.PublicKey{Curve: elliptic.P224()}}
	for _, key := range unsupported {
		if got, ok := ca.KindOf(key); ok {
			t.Errorf("KindOf(%T) = %q, true; want no kind", key, got)
		}
	}
}

func TestAllowedKeys(t *testing.T) {
	t.Parallel()
	signer := testSigner(t)
	p256, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p521, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	policy := ca.Policy{AllowedKeys: []ca.KeyKind{ca.KeyECP256}}
	if _, err := signer.Sign(context.Background(), csr(t, p256, "allowed", false), policy); err != nil {
		t.Fatalf("P-256 key refused: %v", err)
	}
	if _, err := signer.Sign(context.Background(), csr(t, p521, "refused", false), policy); !errors.Is(err, ca.ErrPolicy) {
		t.Fatalf("P-521 key accepted where only P-256 is allowed: %v", err)
	}
	// An RSA key of an unusual size has no kind at all, so a policy that
	// lists kinds rejects it even though it clears MinRSABits.
	odd, err := rsa.GenerateKey(rand.Reader, 2056)
	if err != nil {
		t.Fatal(err)
	}
	oddPolicy := ca.Policy{AllowedKeys: []ca.KeyKind{ca.KeyRSA2048}, MinRSABits: 1024}
	if _, err := signer.Sign(context.Background(), csr(t, odd, "odd", false), oddPolicy); !errors.Is(err, ca.ErrPolicy) {
		t.Fatalf("2056-bit RSA key accepted: %v", err)
	}
}
