package acme_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/acme/acmetest"
)

func TestChallengeStorageFailurePreservesRetry(t *testing.T) {
	for _, method := range []string{"GetOrder", "GetAuthorization", "GetChallenge", "PutOrder"} {
		t.Run(method, func(t *testing.T) {
			var broken *acmetest.Failing
			after := 2
			if method == "PutOrder" {
				after = 1
			}
			f := newFixture(t, func(c *acme.Config) {
				broken = &acmetest.Failing{Store: c.Store, After: map[string]int{method: after}}
				c.Store = broken
			})
			fl := f.begin(testIdentifier)
			object := fl.attestation(deviceProperties())
			broken.SetFail(map[string]error{method: errStore})
			requireProblem(t, fl.answer(object), acme.ProblemServerInternal)
			o, err := f.store.GetOrder(t.Context(), idOf(fl.orderURL))
			if err != nil || o.Status != acme.StatusPending {
				t.Fatalf("failed challenge transaction changed the order: %+v %v", o, err)
			}
			broken.SetFail(nil)
			if fl.challenge().Status != acme.StatusPending {
				t.Fatal("challenge escaped the rolled-back transaction")
			}
			requireStatus(t, fl.answer(object), http.StatusOK)
			requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
		})
	}
}

func TestFinalizeStorageReadsFailClosed(t *testing.T) {
	for _, method := range []string{"GetOrder", "GetAuthorization", "GetChallenge"} {
		for _, after := range []int{2, 3} {
			t.Run(fmt.Sprintf("%s-%d", method, after), func(t *testing.T) {
				var broken *acmetest.Failing
				f := newFixture(t, func(c *acme.Config) {
					broken = &acmetest.Failing{Store: c.Store, After: map[string]int{method: after}}
					c.Store = broken
				})
				fl := f.begin(testIdentifier).pass()
				broken.SetFail(map[string]error{method: errStore})
				requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemServerInternal)
				o, err := f.store.GetOrder(t.Context(), idOf(fl.orderURL))
				if err != nil {
					t.Fatal(err)
				}
				if o.Status == acme.StatusValid {
					t.Fatal("failed read completed issuance")
				}
				broken.SetFail(nil)
				if o.CertificateID != "" {
					requireProblem(
						t,
						fl.acct.post(f.url("/cert/"+o.CertificateID), nil),
						acme.ProblemOrderNotReady,
					)
					requireStatus(t, fl.acct.post(fl.orderURL, nil), http.StatusOK)
				} else {
					requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
				}
			})
		}
	}
}

// Policy may do a slow inventory lookup. The transaction must observe changes
// committed while that lookup was running, instead of trusting its old snapshot.
func TestFinalizeRechecksStateAfterPolicy(t *testing.T) {
	for _, changed := range []string{"binding", "order", "authorization"} {
		t.Run(changed, func(t *testing.T) {
			var f *fixture
			var armed atomic.Bool
			var orderID string
			f = newFixture(t, func(c *acme.Config) {
				c.Authorize = acme.PolicyFunc(func(ctx context.Context, _ *acme.Decision) error {
					if !armed.CompareAndSwap(true, false) {
						return nil
					}
					return f.store.UpdateOrder(ctx, orderID, func(tx acme.Tx) error {
						o, err := tx.GetOrder(ctx, orderID)
						if err != nil {
							return err
						}
						switch changed {
						case "binding":
							o.Binding.CommonName = "changed-policy-subject"
						case "order":
							o.Status = acme.StatusInvalid
						case "authorization":
							a, err := tx.GetAuthorization(ctx, o.AuthzID)
							if err != nil {
								return err
							}
							a.Status = acme.StatusInvalid
							return tx.PutAuthorization(ctx, a)
						}
						return tx.PutOrder(ctx, o)
					})
				})
			})
			fl := f.begin(testIdentifier).pass()
			orderID = idOf(fl.orderURL)
			armed.Store(true)
			res := fl.finalizeWith(fl.key, pkix.Name{})
			if res.status == http.StatusOK {
				t.Fatal("stale authorization issued a certificate")
			}
			o, err := f.store.GetOrder(t.Context(), orderID)
			if err != nil || o.CertificateID != "" {
				t.Fatalf("stale snapshot created a receipt: %+v %v", o, err)
			}
		})
	}
}

func TestReceiptCommitFailureDoesNotExposeCertificate(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(
			map[int]string{1: "before-receipt", 2: "after-registration"}[failAt],
			func(t *testing.T) {
				var broken *acmetest.Failing
				var signer *countingSigner
				var registered []byte
				var registrations atomic.Int32
				f := newFixture(t, func(c *acme.Config) {
					broken = &acmetest.Failing{
						Store: c.Store,
						After: map[string]int{"PutOrder": failAt},
					}
					c.Store = broken
					signer = &countingSigner{Signer: c.Signer}
					c.Signer = signer
					c.Register = func(_ context.Context, cert *x509.Certificate) error {
						registrations.Add(1)
						if registered != nil && !bytes.Equal(registered, cert.Raw) {
							t.Error("registration changed DER after a failed completion commit")
						}
						registered = append([]byte(nil), cert.Raw...)
						return nil
					}
				})
				fl := f.begin(testIdentifier).pass()
				broken.SetFail(map[string]error{"PutOrder": errStore})
				requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemServerInternal)
				o, err := f.store.GetOrder(t.Context(), idOf(fl.orderURL))
				if err != nil {
					t.Fatal(err)
				}
				if failAt == 1 {
					if o.CertificateID != "" || o.Status != acme.StatusReady ||
						registrations.Load() != 0 {
						t.Fatalf("uncommitted certificate escaped: %+v", o)
					}
				} else {
					requireProblem(
						t,
						fl.acct.post(f.url("/cert/"+o.CertificateID), nil),
						acme.ProblemOrderNotReady,
					)
				}
				broken.SetFail(nil)
				if failAt == 1 {
					requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
					if signer.calls.Load() != 2 {
						t.Fatal("rolled-back signing was not retried")
					}
				} else {
					requireStatus(t, fl.acct.post(fl.orderURL, nil), http.StatusOK)
					if signer.calls.Load() != 1 {
						t.Fatal("completion retry signed again")
					}
				}
			},
		)
	}
}

func TestReceiptPollingFailsClosed(t *testing.T) {
	for _, fault := range []string{"policy", "expired", "malformed-pem", "malformed-der", "wrong-order", "missing-receipt"} {
		t.Run(fault, func(t *testing.T) {
			var denied atomic.Bool
			var registrations atomic.Int32
			f := newFixture(t, func(c *acme.Config) {
				c.Authorize = acme.PolicyFunc(func(context.Context, *acme.Decision) error {
					if denied.Load() {
						return acme.ErrUnauthorized
					}
					return nil
				})
				c.Register = func(context.Context, *x509.Certificate) error {
					registrations.Add(1)
					return errStore
				}
			})
			fl := f.begin(testIdentifier).pass()
			requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemServerInternal)
			o, err := f.store.GetOrder(t.Context(), idOf(fl.orderURL))
			if err != nil {
				t.Fatal(err)
			}
			want := acme.ProblemServerInternal
			switch fault {
			case "policy":
				denied.Store(true)
				want = acme.ProblemUnauthorized
			case "expired":
				f.clock.Advance(acme.DefaultOrderTTL + time.Minute)
				want = acme.ProblemOrderNotReady
			default:
				if err := f.store.UpdateOrder(t.Context(), o.ID, func(tx acme.Tx) error {
					c, err := tx.GetCertificate(t.Context(), o.CertificateID)
					if err != nil {
						return err
					}
					switch fault {
					case "malformed-pem":
						c.ChainPEM = []byte("invalid")
					case "malformed-der":
						c.ChainPEM = pem.EncodeToMemory(
							&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")},
						)
					case "wrong-order":
						c.OrderID = "another-order"
					case "missing-receipt":
						o.CertificateID = "missing"
						return tx.PutOrder(t.Context(), o)
					}
					return tx.PutCertificate(t.Context(), c)
				}); err != nil {
					t.Fatal(err)
				}
			}
			requireProblem(t, fl.acct.post(fl.orderURL, nil), want)
			if registrations.Load() != 1 {
				t.Fatal("invalid receipt or authorization reached registration")
			}
		})
	}
}

func TestReceiptExpiresDuringRegistration(t *testing.T) {
	var f *fixture
	f = newFixture(t, func(c *acme.Config) {
		c.Register = func(context.Context, *x509.Certificate) error {
			f.clock.Advance(acme.DefaultOrderTTL + time.Minute)
			return nil
		}
	})
	fl := f.begin(testIdentifier).pass()
	requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemOrderNotReady)
	o, err := f.store.GetOrder(t.Context(), idOf(fl.orderURL))
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != acme.StatusProcessing {
		t.Fatalf("expired registration completed: %+v", o)
	}
	requireProblem(t, fl.acct.post(f.url("/cert/"+o.CertificateID), nil), acme.ProblemOrderNotReady)
}
