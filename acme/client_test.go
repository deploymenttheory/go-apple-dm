package acme_test

import (
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"testing"
	"time"

	xacme "golang.org/x/crypto/acme"

	"github.com/deploymenttheory/go-apple-mdm/acme"
	"github.com/deploymenttheory/go-apple-mdm/ca"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// TestEnrollmentWithAnIndependentClient drives the whole exchange with
// golang.org/x/crypto/acme rather than with anything from this repository.
// A server tested only against its own client proves the two agree, not
// that the server implements RFC 8555, so the only part of this that is
// ours is the attestation, which is Apple's extension rather than the
// protocol.
func TestEnrollmentWithAnIndependentClient(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	client := &xacme.Client{
		Key:          newKey(t),
		DirectoryURL: f.server.DirectoryURL(),
		HTTPClient:   f.ts.Client(),
		UserAgent:    "go-apple-mdm acme tests",
	}
	if _, err := client.Register(ctx, &xacme.Account{}, xacme.AcceptTOS); err != nil {
		t.Fatalf("register: %v", err)
	}

	order, err := client.AuthorizeOrder(ctx, []xacme.AuthzID{
		{Type: acme.IdentifierPermanent, Value: testIdentifier},
	})
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if len(order.AuthzURLs) != 1 {
		t.Fatalf("%d authorizations, want one", len(order.AuthzURLs))
	}
	authz, err := client.GetAuthorization(ctx, order.AuthzURLs[0])
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	var challenge *xacme.Challenge
	for _, c := range authz.Challenges {
		if c.Type == acme.ChallengeDeviceAttest {
			challenge = c
		}
	}
	if challenge == nil {
		t.Fatalf("no %s challenge was offered", acme.ChallengeDeviceAttest)
	}

	// The device key is the one the attestation covers and the one the
	// certificate request asks for; the account key above is a different
	// key entirely, which is why the freshness code alone cannot bind them.
	deviceKey := newKey(t)
	props := deviceProperties()
	object, err := f.attest.ObjectForToken(challenge.Token, props, deviceKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	challenge.Payload = mustJSON(t, map[string]string{
		"attObj": base64.RawURLEncoding.EncodeToString(object),
	})
	if _, err := client.Accept(ctx, challenge); err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if _, err := client.WaitOrder(ctx, order.URI); err != nil {
		t.Fatalf("order did not become ready: %v", err)
	}

	// The subject in the request is one the device chose for itself, and is
	// not evidence of anything: Apple states the server may override it.
	csr := csrDER(t, deviceKey, pkix.Name{
		CommonName:   "a name the device chose",
		Organization: []string{"An Organisation The Device Named"},
	})
	chain, certURL, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("the chain has %d certificates, want the leaf and the issuer", len(chain))
	}
	if certURL == "" {
		t.Error("no certificate URL was returned")
	}

	leaf := leafOf(t, pemChain(t, chain))
	if got := leaf.Subject.CommonName; got != testCommonName {
		t.Errorf("subject common name = %q, want the binding's %q", got, testCommonName)
	}
	if len(leaf.Subject.Organization) != 1 || leaf.Subject.Organization[0] != "Deployment Theory" {
		t.Errorf("subject organization = %v, want the binding's", leaf.Subject.Organization)
	}

	// The identifier is carried as the RFC 4043 permanent identifier, which
	// is the name Apple's payload asked to be certified.
	value, ok, err := ca.ParsePermanentIdentifier(leaf)
	if err != nil {
		t.Fatalf("permanent identifier: %v", err)
	}
	if !ok {
		t.Fatal("the issued certificate carries no permanent identifier")
	}
	if value != testIdentifier {
		t.Errorf("permanent identifier = %q, want %q", value, testIdentifier)
	}

	// Keeping the attested facts alongside the certificate is what lets an
	// operator ask later which hardware holds a given identity.
	certs, err := f.store.ListCertificates(ctx, acme.CertificateQuery{}, storage.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if len(certs.Items) != 1 {
		t.Fatalf("%d certificates were recorded, want one", len(certs.Items))
	}
	record := certs.Items[0]
	if record.Device.SerialNumber != testSerial || record.Device.UDID != testUDID {
		t.Errorf("recorded device = %+v, want the attested serial and UDID", record.Device)
	}
	if record.Device.SecureBoot != props.SecureBoot {
		t.Errorf("recorded secure boot = %q, want %q", record.Device.SecureBoot, props.SecureBoot)
	}
	if record.Device.SIPEnabled == nil || !*record.Device.SIPEnabled {
		t.Error("the recorded device does not report System Integrity Protection")
	}
	if record.Binding.Serial != testSerial {
		t.Errorf("recorded binding = %+v, want the one the order was made under", record.Binding)
	}
	if record.Serial != leaf.SerialNumber.String() {
		t.Errorf("recorded serial = %q, want the certificate's %q", record.Serial, leaf.SerialNumber)
	}
	if !record.NotAfter.Equal(leaf.NotAfter) {
		t.Errorf("recorded expiry = %v, want the certificate's %v", record.NotAfter, leaf.NotAfter)
	}

	for _, kind := range []event.Type{event.ACMEChallengeValid, event.ACMEIssued} {
		if n := f.events.count(kind); n != 1 {
			t.Errorf("published %d %s events, want 1", n, kind)
		}
	}
}

// TestBindingCapsTheCertificateLifetime: a binding that names an expiry
// shortens the certificate, so an identity cannot outlive the enrollment it
// was issued for.
func TestBindingCapsTheCertificateLifetime(t *testing.T) {
	const identifier = "an-identifier-with-a-short-life"
	f := newFixture(t)
	f.ids[identifier] = acme.Binding{
		Serial: testSerial, UDID: testUDID,
		NotAfter: f.clock.Now().Add(2 * time.Hour),
	}
	fl := f.begin(identifier)
	requireStatus(t, fl.answer(fl.attestation(deviceProperties())), http.StatusOK)
	res := fl.finalizeWith(fl.key, pkix.Name{})
	requireStatus(t, res, http.StatusOK)

	certs, err := f.store.ListCertificates(t.Context(), acme.CertificateQuery{}, storage.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if len(certs.Items) != 1 {
		t.Fatalf("%d certificates, want one", len(certs.Items))
	}
	if got := certs.Items[0].NotAfter.Sub(f.clock.Now()); got > 2*time.Hour {
		t.Fatalf("the certificate lives %v, want no more than the binding's two hours", got)
	}
}

// TestSubjectFallsBackToTheSerial: a binding with no common name is still
// named by the server, never by the device.
func TestSubjectFallsBackToTheSerial(t *testing.T) {
	cases := map[string]struct {
		binding acme.Binding
		want    string
	}{
		"Serial": {acme.Binding{Serial: testSerial, UDID: testUDID}, testSerial},
		// With neither a name nor a serial, the identifier itself is the
		// only thing the server knows about the device.
		"Identifier": {acme.Binding{AllowUnidentified: true}, "an-identifier-with-no-binding"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			identifier := "an-identifier-with-no-binding"
			f := newFixture(t)
			f.ids[identifier] = c.binding
			fl := f.begin(identifier)
			props := deviceProperties()
			if c.binding.AllowUnidentified {
				props.SerialNumber, props.UDID = "", ""
			}
			requireStatus(t, fl.answer(fl.attestation(props)), http.StatusOK)
			requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)

			certURL := fl.order().Certificate
			leaf := leafOf(t, fl.acct.post(certURL, nil).body)
			if got := leaf.Subject.CommonName; got != c.want {
				t.Fatalf("common name = %q, want %q", got, c.want)
			}
		})
	}
}

// pemChain re-encodes the DER chain the client handed back, so the same
// reader is used for it as for a download straight off the wire.
func pemChain(t *testing.T, chain [][]byte) []byte {
	t.Helper()
	var out []byte
	for _, der := range chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return out
}

// TestBindingDeadlineInThePast: a binding whose certificate deadline has
// already passed would produce a certificate that is expired the moment it
// is issued, so the order is refused rather than satisfied uselessly.
func TestBindingDeadlineInThePast(t *testing.T) {
	const identifier = "an-identifier-whose-deadline-passed"
	f := newFixture(t)
	f.ids[identifier] = acme.Binding{
		Serial: testSerial, UDID: testUDID,
		NotAfter: f.clock.Now().Add(-time.Minute),
	}
	fl := f.begin(identifier)
	requireStatus(t, fl.answer(fl.attestation(deviceProperties())), http.StatusOK)
	requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemRejectedIdentifier)
}
