package app

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func TestSCEPGrantRejectsInvalidBindingWithoutReservation(t *testing.T) {
	a, id := replacementSecurityApp(t)
	e := a.enroll
	csr := hardeningCSR(t, id.ID)
	for _, binding := range []json.RawMessage{json.RawMessage(`"invalid binding"`), json.RawMessage(`{"commonName":"other"}`)} {
		grants := e.scepGrants()
		password, err := grants.Issue(
			t.Context(),
			scep.Grant{Binding: binding, ExpiresAt: time.Now().Add(time.Hour)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := grants.Verify(t.Context(), password, csr); err == nil {
			t.Fatal("invalid binding authorized")
		}
		record, err := e.state.Get(t.Context(), scepGrantKey(password))
		if err != nil {
			t.Fatal(err)
		}
		var persisted scep.Grant
		if err := json.Unmarshal(record.Value, &persisted); err != nil || persisted.CSRHash != "" {
			t.Fatal("rejected binding reserved CSR", err)
		}
	}
}

func TestCredentialGrantExpiryAndPersistenceFailures(t *testing.T) {
	a, id := replacementSecurityApp(t)
	s := a.enroll.acme
	backend := a.protocol
	cert := &x509.Certificate{
		NotAfter: a.cfg.Clock.Now().Add(time.Minute),
		Raw:      []byte("invalid DER"),
	}
	if err := s.recordCredentialGrant(t.Context(), "limited", id, cert, false); err != nil {
		t.Fatal(err)
	}
	record, err := backend.Get(t.Context(), credentialGrantKey("limited"))
	if err != nil || !record.ExpiresAt.Equal(cert.NotAfter) {
		t.Fatal("grant outlives identity", record.ExpiresAt, err)
	}
	decision := &acme.Decision{Binding: acme.Binding{EnrollmentID: id.ID}}
	decision.Identifier.Value = "limited"
	if err := s.authorizeCredential(t.Context(), decision, false); err == nil {
		t.Fatal("invalid stored certificate authorized")
	}
	fault := errors.New("grant store unavailable")
	a.protocol = issuanceStateFault{Store: backend, writeErr: fault}
	if err := s.recordCredentialGrant(
		t.Context(),
		"failed",
		id,
		cert,
		true,
	); !errors.Is(
		err,
		fault,
	) {
		t.Fatal("persistence failure lost", err)
	}
	if _, err := backend.Get(
		t.Context(),
		credentialGrantKey("failed"),
	); !errors.Is(
		err,
		state.ErrNotFound,
	) {
		t.Fatal("failed grant persisted", err)
	}
	a.protocol = issuanceStateFault{Store: backend, readErr: fault}
	if err := s.authorizeCredential(t.Context(), decision, false); !errors.Is(err, fault) {
		t.Fatal("read failure lost", err)
	}
	a.protocol = backend
	key := credentialGrantKey("limited")
	if err := backend.Update(t.Context(), []string{key}, func(tx state.Tx) error {
		return tx.Put(t.Context(), state.Record{Key: key, Value: []byte("corrupt")})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.authorizeCredential(t.Context(), decision, false); err == nil {
		t.Fatal("corrupt grant authorized")
	}
	if err := s.authorizeCredential(
		t.Context(),
		&acme.Decision{},
		false,
	); !errors.Is(
		err,
		acme.ErrUnauthorized,
	) {
		t.Fatal("unbound identifier authorized", err)
	}
	cert.NotAfter = a.cfg.Clock.Now().Add(-time.Second)
	if err := s.recordCredentialGrant(
		t.Context(),
		"expired",
		id,
		cert,
		false,
	); !errors.Is(
		err,
		ErrBadACMERequest,
	) {
		t.Fatal("expired identity granted authorization", err)
	}
}

func TestAppleServiceClientsRejectUnavailablePrivateTrust(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	a := &App{
		cfg: Config{
			AxM: AxMConfig{KeyPEM: []byte("unused"), RootCAFile: missing},
			DEP: DEPConfig{RootCAFile: missing},
		},
	}
	for _, build := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := a.newAxM(ctx); return err },
		func(ctx context.Context) error { _, err := a.newDEP(ctx); return err },
	} {
		if err := build(t.Context()); err == nil {
			t.Fatal("missing private trust ignored")
		}
	}
}
