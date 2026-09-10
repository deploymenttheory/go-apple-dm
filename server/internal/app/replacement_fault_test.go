package app

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/storagetest"
)

type unavailableReplacementStore struct {
	storage.Store
	fault error
}

func (s unavailableReplacementStore) TransitionReplacement(
	context.Context,
	mdm.EnrollmentID,
	storage.ReplacementChange,
) (*storage.Replacement, error) {
	return nil, s.fault
}

func TestReplacementBoundaryRejectsUnavailableAndUnboundState(t *testing.T) {
	a, id := replacementSecurityApp(t)
	backing := a.Store
	ctx := t.Context()
	cert := &x509.Certificate{
		Subject: pkix.Name{CommonName: replacementSubject(id, "attempt")},
		Raw:     []byte("unregistered candidate"),
	}
	csr := &x509.CertificateRequest{Subject: cert.Subject}
	call := &service.Call{
		Op:      "checkin:Authenticate",
		Request: &mdm.Request{ID: id, Certificate: cert},
	}
	hook := replacementAdmission{app: a}
	for _, store := range []storage.Store{
		backing,
		struct{ storage.Store }{backing},
		&storagetest.Failing{Store: backing, Fail: map[string]error{"CertHash": errors.New("pin unavailable")}},
		unavailableReplacementStore{Store: backing, fault: errors.New("replacement unavailable")},
	} {
		a.Store = store
		if _, err := hook.Before(ctx, call); err == nil {
			t.Fatal("unbound candidate admitted")
		}
		if err := a.replacementIssuance(ctx, cert); err == nil {
			t.Fatal("unbound candidate registered")
		}
		if err := a.replacementChallenge(ctx, "unregistered", csr); err == nil {
			t.Fatal("unbound challenge claimed")
		}
		r := httptest.NewRequest("DELETE", "https://mdm.example/replacement", nil)
		r.SetPathValue("channel", "device")
		r.SetPathValue("id", id.ID)
		r.SetPathValue("attempt", "attempt")
		w := httptest.NewRecorder()
		a.replaceEnrollment(w, r)
		if w.Code < 400 {
			t.Fatal("unavailable replacement operation succeeded", w.Code)
		}
	}
	a.Store = backing
	for _, subject := range []string{replacementSubjectPrefix + "malformed", replacementSubject(mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "another"}, "attempt")} {
		cert.Subject.CommonName = subject
		csr.Subject = cert.Subject
		if _, err := hook.Before(ctx, call); err == nil {
			t.Fatal("foreign/malformed subject admitted")
		}
		if err := a.replacementIssuance(ctx, cert); err == nil {
			t.Fatal("foreign/malformed subject registered")
		}
		if err := a.replacementChallenge(ctx, "secret", csr); err == nil {
			t.Fatal("foreign/malformed challenge claimed")
		}
	}
	w := httptest.NewRecorder()
	a.replaceEnrollment(w, httptest.NewRequest("GET", "https://mdm.example/replacement", nil))
	if w.Code != 400 {
		t.Fatal("invalid replacement target", w.Code)
	}
	stored, err := backing.Get(ctx, id)
	if err != nil || stored.CertHash != "original-pin" {
		t.Fatal("failed boundary checks changed pin", stored, err)
	}
}
