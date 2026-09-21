package lifecycle_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

// issuePush issues a push certificate for the CSR's key with the supplied topic and validity
// window.
func issuePush(t *testing.T, ca *testpki.CA, csrPEM []byte, topic string, start, end time.Time) []byte {
	t.Helper()
	block, _ := pem.Decode(csrPEM)
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "MDM push", ExtraNames: []pkix.AttributeTypeAndValue{{Type: asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}, Value: topic}}}, NotBefore: start.Add(-time.Minute), NotAfter: end, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestAnnualPushRenewalSurvivesRestartAndOriginalExpiry checks that annual push renewal survives
// restart and original expiry.
func TestAnnualPushRenewalSurvivesRestartAndOriginalExpiry(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	initial := now
	store := state.NewMemory()
	store.Now = func() time.Time { return now }
	ca, err := testpki.NewCA("test Apple authority")
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := *ca.Cert
	rootTemplate.NotAfter = now.Add(5 * 365 * 24 * time.Hour)
	rootDER, err := x509.CreateCertificate(rand.Reader, &rootTemplate, &rootTemplate, ca.Key.Public(), ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	ca.Cert, err = x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	m := &lifecycle.Manager{Store: store, Trust: lifecycle.Trust{Apple: []*x509.Certificate{ca.Cert}}}
	req := lifecycle.Request{ID: "customer", Kind: lifecycle.Push, Subject: pkix.Name{CommonName: "customer"}}
	first, err := m.Begin(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := m.Export(ctx, req.ID, first.Pending, "csr")
	if err != nil {
		t.Fatal(err)
	}
	topic := "com.apple.mgmt.External.test"
	cert := issuePush(t, ca, csr, topic, now, now.Add(365*24*time.Hour))
	if _, err = m.Import(ctx, req.ID, "1", cert); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Activate(ctx, req.ID, "1"); err != nil {
		t.Fatal(err)
	}
	now = initial.Add(310 * 24 * time.Hour)
	status, err := m.Get(ctx, req.ID)
	if err != nil || status.Severity == "" {
		t.Fatal(status, err)
	}
	next, err := m.Begin(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if next.Active != "1" || next.Pending != "2" {
		t.Fatal(next)
	}
	material, err := m.LoadMaterial(ctx, req.ID, "2")
	if err != nil {
		t.Fatal(err)
	}
	// A new manager sees the same pending private key and CSR after restart.
	m = &lifecycle.Manager{Store: store, Trust: m.Trust}
	again, err := m.Begin(ctx, req)
	if err != nil || again.Pending != "2" {
		t.Fatal(again, err)
	}
	resumed, err := m.LoadMaterial(ctx, req.ID, "2")
	if err != nil || !bytes.Equal(material.Key, resumed.Key) || !bytes.Equal(material.CSR, resumed.CSR) {
		t.Fatal("renewal regenerated pending material", err)
	}
	wrong := issuePush(t, ca, material.CSR, "com.apple.mgmt.External.other", now, now.Add(365*24*time.Hour))
	if _, err = m.Import(ctx, req.ID, "2", wrong); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatal("wrong topic accepted", err)
	}
	cert = issuePush(t, ca, material.CSR, topic, now, now.Add(365*24*time.Hour))
	if _, err = m.Import(ctx, req.ID, "2", cert); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("publication failed")
	m.Publish = func(context.Context, state.Tx, lifecycle.Identity, lifecycle.Material) error { return failure }
	if _, err = m.Activate(ctx, req.ID, "2"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	status, _ = m.Get(ctx, req.ID)
	if status.Active != "1" || status.Pending != "2" {
		t.Fatal("failed publication changed active identity")
	}
	m.Publish = nil
	if _, err = m.Activate(ctx, req.ID, "2"); err != nil {
		t.Fatal(err)
	}
	// No device migration or acknowledgment is needed to activate the successor.
	now = initial.Add(366 * 24 * time.Hour)
	status, err = m.Get(ctx, req.ID)
	if err != nil || status.Active != "2" || status.Topic != topic || status.Severity == "expired" {
		t.Fatal(status, err)
	}
	b, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"PRIVATE KEY", "SignedRequest", "Certificate", "CSR", "Key"} {
		if bytes.Contains(b, []byte(forbidden)) {
			t.Fatalf("metadata contains %s", forbidden)
		}
	}
	if _, err = m.Export(ctx, req.ID, "2", "key"); !errors.Is(err, lifecycle.ErrInvalid) {
		t.Fatal("private key exported", err)
	}
}

// TestConcurrentRequestsCreateOnePendingRevision checks concurrent requests create one pending
// revision.
func TestConcurrentRequestsCreateOnePendingRevision(t *testing.T) {
	m := &lifecycle.Manager{Store: state.NewMemory()}
	req := lifecycle.Request{ID: "vendor", Kind: lifecycle.Vendor, Subject: pkix.Name{CommonName: "vendor"}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := m.Begin(t.Context(), req)
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	item, err := m.Get(t.Context(), req.ID)
	if err != nil || len(item.Revisions) != 1 || item.Pending != "1" {
		t.Fatal(item, err)
	}
}

// TestLabHTTPSHasSeparateCAAndValidHostnames checks lab HTTPS has separate CA and valid hostnames.
func TestLabHTTPSHasSeparateCAAndValidHostnames(t *testing.T) {
	ctx := t.Context()
	m := &lifecycle.Manager{Store: state.NewMemory()}
	caReq := lifecycle.Request{ID: "lab-ca", Kind: lifecycle.Issuer, Subject: pkix.Name{CommonName: "lab HTTPS CA"}}
	item, err := m.Begin(ctx, caReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateIssuer(ctx, caReq.ID, item.Pending, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Activate(ctx, caReq.ID, item.Pending); err != nil {
		t.Fatal(err)
	}
	req := lifecycle.Request{ID: "https", Kind: lifecycle.HTTPS, Subject: pkix.Name{CommonName: "lab"}, DNSNames: []string{"localhost", "127.0.0.1"}}
	item, err = m.Begin(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.IssueHTTPS(ctx, req.ID, item.Pending, caReq.ID); err != nil {
		t.Fatal(err)
	}
	material, err := m.LoadMaterial(ctx, caReq.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	m.Trust.HTTPSRoots = x509.NewCertPool()
	m.Trust.HTTPSRoots.AppendCertsFromPEM(material.Certificate)
	if _, err = m.Activate(ctx, req.ID, item.Pending); err != nil {
		t.Fatal(err)
	}
	leaf, err := m.LoadMaterial(ctx, req.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(material.Key, leaf.Key) {
		t.Fatal("CA and HTTPS share a private key")
	}
}
