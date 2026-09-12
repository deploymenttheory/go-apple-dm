package scep_test

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smallstep/pkcs7"
	smallscep "github.com/smallstep/scep"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/scep"
)

func assertSecureSCEPWire(t *testing.T, raw []byte) {
	t.Helper()
	p7, err := pkcs7.Parse(raw)
	if err != nil {
		t.Error(err)
		return
	}
	if err := p7.Verify(); err != nil {
		t.Error(err)
		return
	}
	if len(p7.Signers) != 1 || !p7.Signers[0].DigestAlgorithm.Algorithm.Equal(pkcs7.OIDDigestAlgorithmSHA256) {
		t.Error("SCEP message did not use SHA-256")
	}
	var outer struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue `asn1:"explicit,tag:0"`
	}
	if _, err := asn1.Unmarshal(p7.Content, &outer); err != nil {
		t.Error(err)
		return
	}
	var envelope struct {
		Version    int
		Recipients asn1.RawValue
		Content    struct {
			Type      asn1.ObjectIdentifier
			Algorithm pkix.AlgorithmIdentifier
			Bytes     asn1.RawValue `asn1:"tag:0,optional"`
		}
	}
	if _, err := asn1.Unmarshal(outer.Content.Bytes, &envelope); err != nil {
		t.Error(err)
		return
	}
	if !envelope.Content.Algorithm.Algorithm.Equal(pkcs7.OIDEncryptionAlgorithmAES128CBC) {
		t.Errorf("SCEP encryption is %s, want AES-128-CBC", envelope.Content.Algorithm.Algorithm)
	}
}

func TestSCEPUsesAESAndSHA256OnWire(t *testing.T) {
	f := newFixture(t)
	s, err := newTestServer(f.signer, f.caCert, f.caKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		assertSecureSCEPWire(t, raw)
		reply, err := s.PKIOperation(r.Context(), raw)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		assertSecureSCEPWire(t, reply)
		_, _ = w.Write(reply)
	}))
	defer server.Close()
	if _, err := scep.NewClient(server.URL, server.Client()).Enroll(t.Context(), rsaKey(t), scep.EnrollOptions{Recipients: []*x509.Certificate{f.caCert}}); err != nil {
		t.Fatal(err)
	}
}

func TestServerRejectsDESAndUnexpectedMessageType(t *testing.T) {
	f := newFixture(t)
	s, err := newTestServer(f.signer, f.caCert, f.caKey)
	if err != nil {
		t.Fatal(err)
	}
	key := rsaKey(t)
	self, err := scep.SelfSigned(key, pkix.Name{CommonName: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: self.Subject}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	// The independent upstream encoder still defaults to single DES.
	request, err := smallscep.NewCSRRequest(csr, &smallscep.PKIMessage{MessageType: smallscep.PKCSReq, SignerCert: self, SignerKey: key, Recipients: []*x509.Certificate{f.caCert}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PKIOperation(t.Context(), request.Raw); !errors.Is(err, scep.ErrCSR) {
		t.Fatalf("DES request accepted: %v", err)
	}
	reply, err := request.Fail(f.caCert, f.caKey, smallscep.BadRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PKIOperation(t.Context(), reply.Raw); !errors.Is(err, scep.ErrOperation) {
		t.Fatalf("CertRep accepted as request: %v", err)
	}
}
