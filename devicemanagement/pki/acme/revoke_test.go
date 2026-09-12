package acme_test

import (
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme/jose"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func TestRevokeCertificateAuthorizations(t *testing.T) {
	for _, mode := range []string{"account", "certificate-key", "authorized-identifiers"} {
		t.Run(mode, func(t *testing.T) {
			var registry *revocation.Registry
			f := newFixture(t, func(c *acme.Config) {
				root, err := sharedCA()
				if err != nil {
					t.Fatal(err)
				}
				registry, err = revocation.New(state.NewMemory(), revocation.Issuer{Certificate: root.cert, Signer: root.key, CRLTTL: time.Hour, CRLRefresh: time.Minute, OCSPTTL: time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				c.Revocations = registry
			})
			flow := f.begin(testIdentifier).pass()
			requireStatus(t, flow.finalizeWith(flow.key, pkix.Name{CommonName: "device"}), 200)
			chain := flow.acct.post(flow.order().Certificate, nil)
			requireStatus(t, chain, 200)
			cert := leafOf(t, chain.body)
			body := map[string]any{"certificate": base64.RawURLEncoding.EncodeToString(cert.Raw), "reason": 1}
			target := f.url("/revoke-cert")
			outsider := f.register()
			requireStatus(t, outsider.post(target, body), http.StatusUnauthorized)
			wrongKey := newKey(t)
			jwk, _ := jose.JWKFromPublic(wrongKey.Public())
			requireStatus(t, f.signed(target, wrongKey, jose.Header{JWK: jwk}, mustJSON(t, body)), 401)
			badReason := map[string]any{"certificate": body["certificate"], "reason": 6}
			requireStatus(t, flow.acct.post(target, badReason), 400)
			if err := registry.Check(t.Context(), cert); err != nil {
				t.Fatal("invalid revoke changed status", err)
			}
			var res *response
			switch mode {
			case "account":
				res = flow.acct.post(target, body)
			case "certificate-key":
				jwk, _ := jose.JWKFromPublic(flow.key.Public())
				res = f.signed(target, flow.key, jose.Header{JWK: jwk}, mustJSON(t, body))
			case "authorized-identifiers":
				// Seed another account's currently valid authorization, as an embedding
				// server supporting reusable identifiers may do. Its account did not
				// issue this certificate, so only RFC 8555's third mode can authorize it.
				orders, _ := f.store.ListOrders(t.Context(), flow.acct.id, paging.Page{})
				o := orders.Items[0]
				authorization, _ := f.store.GetAuthorization(t.Context(), o.AuthzID)
				if err := f.store.Update(t.Context(), func(tx acme.Tx) error {
					o.ID = "authorized-order"
					o.AccountID = outsider.id
					o.AuthzID = "authorized-authz"
					authorization.ID = o.AuthzID
					authorization.AccountID = outsider.id
					authorization.OrderID = o.ID
					if err := tx.PutOrder(t.Context(), &o); err != nil {
						return err
					}
					return tx.PutAuthorization(t.Context(), authorization)
				}); err != nil {
					t.Fatal(err)
				}
				res = outsider.post(target, body)
			}
			requireStatus(t, res, 200)
			if err := registry.Check(t.Context(), cert); !errors.Is(err, revocation.ErrRevoked) {
				t.Fatal(err)
			}
			requireStatus(t, flow.acct.post(target, body), 400)
		})
	}
}
