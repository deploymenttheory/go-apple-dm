package acme_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme/jose"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
)

type countingSigner struct {
	ca.Signer
	calls atomic.Int32
}

func TestIssuanceReceiptSurvivesRegistrationFailureAndRestart(t *testing.T) {
	for _, recovery := range []string{"finalize", "poll"} {
		t.Run(recovery, func(t *testing.T) { testReceiptRecovery(t, recovery) })
	}
}

func testReceiptRecovery(t *testing.T, recovery string) {
	var signer *countingSigner
	var seen []byte
	f := newFixture(t, func(c *acme.Config) {
		signer = &countingSigner{Signer: c.Signer}
		c.Signer = signer
		c.Register = func(_ context.Context, cert *x509.Certificate) error {
			seen = append([]byte(nil), cert.Raw...)
			return errStore
		}
	})
	fl := f.begin(testIdentifier).pass()
	payload := map[string]string{
		"csr": base64.RawURLEncoding.EncodeToString(csrDER(t, fl.key, pkix.Name{})),
	}
	requireProblem(t, fl.acct.post(fl.finalize, payload), acme.ProblemServerInternal)
	requireProblem(t, fl.acct.post(fl.orderURL, nil), acme.ProblemServerInternal)
	stored, err := f.store.GetOrder(t.Context(), idOf(fl.orderURL))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != acme.StatusProcessing || stored.CertificateID == "" {
		t.Fatalf("missing durable processing receipt: %+v", stored)
	}
	requireProblem(
		t,
		fl.acct.post(f.url("/cert/"+stored.CertificateID), nil),
		acme.ProblemOrderNotReady,
	)

	// Another instance recovers the durable receipt. Registration must never
	// need to sign again, and it receives exactly the DER from the first try.
	g := newFixture(t, func(c *acme.Config) {
		c.Store, c.Signer, c.Anchors, c.Clock = f.store, signer, f.attest.Anchors(), f.clock
		c.Register = func(_ context.Context, cert *x509.Certificate) error {
			if !bytes.Equal(cert.Raw, seen) {
				return errors.New("registration retried with another certificate")
			}
			return nil
		}
	})
	target := g.url("/order/" + stored.ID + "/finalize")
	header := jose.Header{KeyID: g.url("/account/" + fl.acct.id)}
	changed := map[string]string{
		"csr": base64.RawURLEncoding.EncodeToString(
			csrDER(t, fl.key, pkix.Name{CommonName: "changed"}),
		),
	}
	requireProblem(
		t,
		g.signed(target, fl.acct.key, header, mustJSON(t, changed)),
		acme.ProblemBadCSR,
	)
	if recovery == "poll" {
		requireStatus(
			t,
			g.signed(g.url("/order/"+stored.ID), fl.acct.key, header, nil),
			http.StatusOK,
		)
	} else {
		requireStatus(t, g.signed(target, fl.acct.key, header, mustJSON(t, payload)), http.StatusOK)
	}
	cert := fl.acct.post(f.url("/cert/"+stored.CertificateID), nil)
	requireStatus(t, cert, http.StatusOK)
	if signer.calls.Load() != 1 {
		t.Fatal("receipt recovery signed another certificate")
	}
}

func TestDelayedChallengeCannotReopenCompletedOrder(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rejection"}[denied], func(t *testing.T) {
			arrived, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			f := newFixture(t, func(c *acme.Config) {
				c.Authorize = acme.PolicyFunc(func(ctx context.Context, _ *acme.Decision) error {
					if calls.Add(1) == 1 {
						close(arrived)
						select {
						case <-release:
						case <-ctx.Done():
							return ctx.Err()
						}
						if denied {
							return acme.ErrUnauthorized
						}
					}
					return nil
				})
			})
			fl := f.begin(testIdentifier)
			props := deviceProperties()
			props.Freshness = attest.FreshnessForToken(fl.token)
			object, err := f.attest.Object(
				attesttest.LeafOptions{Properties: props, PublicKey: fl.key.Public()},
			)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan *response, 1)
			go func() { done <- fl.answer(object) }()
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("challenge did not reach policy")
			}
			requireStatus(t, fl.answer(object), http.StatusOK)
			requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
			before := fl.order()
			close(release)
			<-done
			after := fl.order()
			if after.Status != acme.StatusValid || after.Certificate != before.Certificate {
				t.Fatalf("delayed challenge changed a completed order: %+v", after)
			}
		})
	}
}

func (s *countingSigner) Sign(
	ctx context.Context,
	csr *x509.CertificateRequest,
	p ca.Policy,
) (*x509.Certificate, error) {
	s.calls.Add(1)
	return s.Signer.Sign(ctx, csr, p)
}

// Both requests have read the ready order before either can finish policy
// evaluation. Distinct JWS nonces make these legitimate concurrent retries.
func TestConcurrentFinalizeIssuesOneCertificate(t *testing.T) {
	var armed atomic.Bool
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var signer *countingSigner
	f := newFixture(t, func(c *acme.Config) {
		signer = &countingSigner{Signer: c.Signer}
		c.Signer = signer
		c.Authorize = acme.PolicyFunc(func(ctx context.Context, _ *acme.Decision) error {
			if armed.Load() {
				arrived <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
	})
	fl := f.begin(testIdentifier).pass()
	payload := map[string]string{
		"csr": base64.RawURLEncoding.EncodeToString(csrDER(t, fl.key, pkix.Name{})),
	}
	armed.Store(true)
	responses := make(chan *response, 2)
	for range 2 {
		go func() { responses <- fl.acct.post(fl.finalize, payload) }()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("finalization did not reach the policy barrier")
		}
	}
	armed.Store(false)
	close(release)
	var certificate string
	for range 2 {
		res := <-responses
		requireStatus(t, res, http.StatusOK)
		body := decode[orderJSON](t, res)
		if certificate != "" && certificate != body.Certificate {
			t.Errorf("concurrent finalizations returned different certificates")
		}
		certificate = body.Certificate
	}
	if n := signer.calls.Load(); n != 1 {
		t.Fatalf("signed %d certificates for one order, want 1", n)
	}
}
