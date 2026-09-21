package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

// Counted failures cover each repository boundary of operations that perform
// several reads or commits. The same fault must remain visible to the caller.
type setupStateFault struct {
	state.Store
	fail        func(operation, key string) error
	afterUpdate func()
}

// Get runs the injected state-read fault before delegating to the store.
func (s *setupStateFault) Get(ctx context.Context, key string) (state.Record, error) {
	if err := s.fail("read", key); err != nil {
		return state.Record{}, err
	}
	return s.Store.Get(ctx, key)
}

// List runs the injected state-list fault before delegating to the store.
func (s *setupStateFault) List(
	ctx context.Context,
	prefix, after string,
	limit int,
) ([]state.Record, error) {
	if err := s.fail("list", prefix); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, prefix, after, limit)
}

// Update wraps a state transaction with faults and invokes the post-update hook after success.
func (s *setupStateFault) Update(ctx context.Context, keys []string, f func(state.Tx) error) error {
	err := s.Store.Update(
		ctx,
		keys,
		func(tx state.Tx) error { return f(setupFaultTx{Tx: tx, fail: s.fail}) },
	)
	if err == nil && s.afterUpdate != nil {
		s.afterUpdate()
	}
	return err
}

type setupFaultTx struct {
	state.Tx
	fail func(string, string) error
}

// Get runs the injected state-read fault before delegating to the transaction.
func (tx setupFaultTx) Get(ctx context.Context, key string) (state.Record, error) {
	if err := tx.fail("read", key); err != nil {
		return state.Record{}, err
	}
	return tx.Tx.Get(ctx, key)
}

// Put runs the injected state-write fault before delegating to the transaction.
func (tx setupFaultTx) Put(ctx context.Context, record state.Record) error {
	if err := tx.fail("write", record.Key); err != nil {
		return err
	}
	return tx.Tx.Put(ctx, record)
}

// List runs the injected state-list fault before delegating to the transaction.
func (tx setupFaultTx) List(
	ctx context.Context,
	prefix, after string,
	limit int,
) ([]state.Record, error) {
	if err := tx.fail("list", prefix); err != nil {
		return nil, err
	}
	return tx.Tx.List(ctx, prefix, after, limit)
}

// TestManagedServicesPropagateEveryCertificateReadFailure checks managed services propagate every
// certificate read failure.
func TestManagedServicesPropagateEveryCertificateReadFailure(t *testing.T) {
	for _, name := range []string{"pairs", "issuer", "registry", "profile trust", "service config", "status", "retire HTTPS"} {
		t.Run(name, func(t *testing.T) {
			a, _, _, _ := renewalFixture(t)
			v := setupExecute(t, a, lifecycle.HTTPS, "lab", setupRequest("https", lifecycle.HTTPS))
			setupExecute(
				t,
				a,
				lifecycle.HTTPS,
				"activate",
				SetupRequest{Revision: v.Identity.Pending},
			)
			seen, failAt := 0, 0
			fault := &setupStateFault{Store: a.protocol, fail: func(op, _ string) error {
				if op == "read" {
					seen++
					if seen == failAt {
						return io.ErrUnexpectedEOF
					}
				}
				return nil
			}}
			a.protocol, a.Certificates.Store = fault, fault
			operation := func() error {
				a.issuerServices = nil
				switch name {
				case "pairs":
					_, err := a.certificatePairs(t.Context(), "issuer", false)
					return err
				case "issuer":
					_, err := a.managedIssuer(t.Context(), "2")
					return err
				case "registry":
					_, err := a.managedRegistry(t.Context())
					return err
				case "profile trust":
					_, err := a.managedProfileTrust(t.Context())
					return err
				case "service config":
					return a.wireServiceConfig(t.Context(), a.enroll, http.NewServeMux())
				case "status":
					_, err := a.ExecuteSetup(
						t.Context(),
						lifecycle.Issuer,
						"status",
						SetupRequest{},
					)
					return err
				default:
					_, err := a.retireHTTPSTrust(t.Context(), "https-ca", "1")
					return err
				}
			}
			_ = operation()
			reads := seen
			for n := 1; n <= reads; n++ {
				seen, failAt = 0, n
				setupRequire(t, operation(), io.ErrUnexpectedEOF)
			}
		})
	}
}

// TestMigrationOperationsPropagateEnrollmentAndRepositoryFailures checks migration operations
// propagate enrollment and repository failures.
func TestMigrationOperationsPropagateEnrollmentAndRepositoryFailures(t *testing.T) {
	a, e, target, job := renewalFixture(t)
	ctx := t.Context()
	backing, protocol := a.Store, a.protocol
	a.Store = &storagetest.Failing{
		Store: backing,
		Fail:  map[string]error{"List": io.ErrUnexpectedEOF},
	}
	for _, op := range []func() error{
		func() error { _, err := a.startIssuerRollover(ctx, "issuer", "2"); return err },
		func() error { _, err := a.startHTTPSTrustRollover(ctx, "https-ca", "2"); return err },
		func() error { return a.advanceRollover(ctx, job) },
		func() error { return a.advanceHTTPSTrust(ctx, lifecycle.Rollover{IssuerID: "https-ca", To: "2"}) },
		func() error { _, err := a.retireManagedIssuer(ctx, "issuer", "1"); return err },
	} {
		setupRequire(t, op(), io.ErrUnexpectedEOF)
	}
	a.Store = &storagetest.Failing{
		Store: backing,
		Fail:  map[string]error{"Commands": io.ErrUnexpectedEOF},
	}
	setupRequire(
		t,
		a.progressMigration(ctx, target, e, &lifecycle.Migration{Device: e.ID.ID}, true),
		io.ErrUnexpectedEOF,
	)
	a.Store = backing
	for _, kind := range []string{"list", "read", "write"} {
		t.Run(kind, func(t *testing.T) {
			fault := &setupStateFault{Store: protocol, fail: func(op, _ string) error {
				if op == kind {
					return io.ErrUnexpectedEOF
				}
				return nil
			}}
			a.protocol, a.Certificates.Store = fault, fault
			for _, op := range []func() error{
				func() error { return a.renewOneIdentity(ctx, *e) },
				func() error { return a.retryMigration(ctx, "issuer", "", e.ID.ID) },
				func() error { return a.retryMigration(ctx, "issuer", "2", e.ID.ID) },
				func() error { return a.advanceRollover(ctx, job) },
				func() error { return a.advanceHTTPSTrust(ctx, lifecycle.Rollover{IssuerID: "https-ca", To: "2"}) },
				func() error { return a.reconcileDeviceRenewals(ctx) },
				func() error { return a.identityRenewalPass(ctx) },
				func() error { return a.certificateRenewalPass(ctx) },
			} {
				err := op()
				if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) &&
					!errors.Is(err, lifecycle.ErrNotFound) &&
					!errors.Is(err, lifecycle.ErrConflict) {
					t.Fatal("unexpected workflow failure", err)
				}
			}
		})
	}
	a.protocol, a.Certificates.Store = protocol, protocol
}

// TestDeviceRenewalFinalCommitFencesStaleWorkers checks device renewal final commit fences stale
// workers.
func TestDeviceRenewalFinalCommitFencesStaleWorkers(t *testing.T) {
	for _, mode := range []string{"read", "corrupt", "generation", "write"} {
		t.Run(mode, func(t *testing.T) {
			a, e, _, _ := renewalFixture(t)
			e.LastSeenAt = time.Time{}
			k := "pki/lifecycle/device-renewal/" + digest([]byte(e.ID.ID))
			reads, writes := 0, 0
			fault := &setupStateFault{Store: a.protocol, fail: func(op, key string) error {
				if key != k {
					return nil
				}
				if op == "read" {
					reads++
					if reads == 2 && mode == "read" {
						return io.ErrUnexpectedEOF
					}
				}
				if op == "write" {
					writes++
					if writes == 2 && mode == "write" {
						return io.ErrUnexpectedEOF
					}
				}
				return nil
			}}
			if mode == "corrupt" || mode == "generation" {
				// Competing progress appears after the first claim, before final save.
				fault.afterUpdate = func() {
					fault.afterUpdate = nil
					row, err := fault.Store.Get(t.Context(), k)
					setupRequire(t, err, nil)
					if mode == "corrupt" {
						row.Value = []byte("corrupt")
					} else {
						var job deviceRenewal
						setupRequire(t, json.Unmarshal(row.Value, &job), nil)
						job.Generation++
						row.Value, err = json.Marshal(job)
						setupRequire(t, err, nil)
					}
					setupRequire(
						t,
						fault.Store.Update(
							t.Context(),
							[]string{k},
							func(tx state.Tx) error { return tx.Put(t.Context(), row) },
						),
						nil,
					)
				}
			}
			a.protocol, a.Certificates.Store = fault, fault
			if err := a.renewOneIdentity(t.Context(), *e); err == nil {
				t.Fatal("failed or stale final progress accepted")
			}
		})
	}
}

// TestReconciliationPagesTerminalAndMissingDevices checks reconciliation pages terminal and
// missing devices.
func TestReconciliationPagesTerminalAndMissingDevices(t *testing.T) {
	a, _, _, _ := renewalFixture(t)
	ctx := t.Context()
	for i := range 101 {
		device := fmt.Sprintf("missing-%03d", i)
		setupJSON(
			t,
			a.protocol,
			"pki/lifecycle/device-renewal/"+digest([]byte(device)),
			deviceRenewal{Migration: lifecycle.Migration{Device: device, Phase: "queued"}},
		)
	}
	setupRequire(t, a.reconcileDeviceRenewals(ctx), nil)
	a.Store = &storagetest.Failing{
		Store: a.Store,
		Fail:  map[string]error{"Get": io.ErrUnexpectedEOF},
	}
	setupRequire(t, a.reconcileDeviceRenewals(ctx), io.ErrUnexpectedEOF)
}
