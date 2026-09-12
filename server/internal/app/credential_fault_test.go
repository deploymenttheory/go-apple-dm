package app

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func TestSCEPFinalizationRejectsInvalidStateAndSigner(t *testing.T) {
	a, id := replacementSecurityApp(t)
	e := a.enroll
	ctx := t.Context()
	backend := e.state
	csr := hardeningCSR(t, id.ID)
	password, err := e.issueSCEPGrant(
		ctx,
		acme.Binding{CommonName: id.ID, UDID: id.ID},
		AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []state.Store{issuanceStateFault{Store: backend, txReadErr: state.ErrNotFound}, issuanceStateFault{Store: backend, txValue: []byte("corrupt")}} {
		e.state = st
		if err = e.verifySCEPGrant(ctx, password, csr); err == nil {
			t.Fatal("inconsistent transactional grant accepted")
		}
	}
	e.state = backend
	if _, err = e.issueSCEP(ctx, csr, ca.Policy{}, password, false); err == nil {
		t.Fatal("unreserved grant issued certificate")
	}
	if err = e.verifySCEPGrant(ctx, password, csr); err != nil {
		t.Fatal(err)
	}
	invalid := *csr
	invalid.Signature = []byte("invalid")
	if _, err = e.issueSCEP(ctx, &invalid, ca.Policy{}, password, false); err == nil {
		t.Fatal("invalid CSR signature accepted")
	}
	originalCA := e.caCert
	badCA := *originalCA
	badCA.IsCA = false
	e.caCert = &badCA
	if _, err = e.issueSCEP(ctx, csr, ca.Policy{}, password, false); err == nil {
		t.Fatal("non-authority issued certificate")
	}
	e.caCert = originalCA
	for _, subject := range []string{replacementSubjectPrefix + "invalid", replacementSubject(mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "unknown"}, "attempt")} {
		csr.Subject.CommonName = subject
		if _, err = e.issueSCEP(ctx, csr, ca.Policy{}, password, false); err == nil {
			t.Fatal("unbound replacement issued certificate")
		}
	}
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: id.ID}, Raw: []byte("unregistered")}
	call := &service.Call{
		Op:      "checkin:Authenticate",
		Request: &mdm.Request{ID: id, Certificate: cert},
	}
	if _, err = (issuanceAdmission{app: a}).Before(ctx, call); err == nil {
		t.Fatal("missing provenance admitted")
	}
}

func TestACMECredentialRejectsMissingIdentityFailedLookupAndDisable(t *testing.T) {
	a, id := replacementSecurityApp(t)
	ctx := t.Context()
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: id.ID}, Raw: []byte("existing device")}
	backing := inmem.New()
	if err := backing.Import(
		ctx,
		storage.EnrollmentExport{
			Enrollment: storage.Enrollment{ID: id, Enabled: true, CertHash: cms.Fingerprint(cert)},
		},
	); err != nil {
		t.Fatal(err)
	}
	st := &storagetest.Failing{Store: backing, Fail: map[string]error{}}
	a.Store = st
	handler := a.enroll.acme.credentialHandler()
	for _, mode := range []string{"missing", "lookup failed", "disabled"} {
		r := httptest.NewRequest("GET", "https://mdm.example/credential", nil)
		want := http.StatusUnauthorized
		if mode != "missing" {
			r = r.WithContext(httpapi.WithCert(ctx, cert))
		}
		if mode == "lookup failed" {
			st.Fail["Get"] = errors.New("unavailable")
			want = http.StatusInternalServerError
		}
		if mode == "disabled" {
			delete(st.Fail, "Get")
			if err := backing.Disable(ctx, id, time.Now()); err != nil {
				t.Fatal(err)
			}
			want = http.StatusForbidden
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(mode, w.Code, w.Body.String())
		}
	}
}

func TestAdminQueueRejectsMalformedTargetsAndLookupFailure(t *testing.T) {
	a, id := replacementSecurityApp(t)
	for _, handler := range []http.HandlerFunc{a.enqueueCommand, a.pushEnrollment} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", "https://mdm.example/admin", nil))
		if w.Code != http.StatusBadRequest {
			t.Fatal(w.Code)
		}
	}
	st := &storagetest.Failing{
		Store: a.Store,
		Fail:  map[string]error{"Get": errors.New("unavailable")},
	}
	core, err := service.New(service.Config{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	a.Core = core
	command, err := mdm.NewCommand(&commands.DeviceInformation{}, mdm.WithUUID("blocked"))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://mdm.example/admin", bytes.NewReader(command.Raw))
	r.SetPathValue("id", id.ID)
	r.SetPathValue("channel", "device")
	w := httptest.NewRecorder()
	a.enqueueCommand(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code, w.Body.String())
	}
	queued, err := a.Store.Next(t.Context(), id, false, time.Now())
	if err != nil || queued != nil {
		t.Fatal("failed enqueue changed queue", queued, err)
	}
}
