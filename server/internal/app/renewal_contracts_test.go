package app

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/server/pushnotify"
)

func renewalFixture(
	t *testing.T,
) (*App, *storage.Enrollment, *managedIssuerService, lifecycle.Rollover) {
	t.Helper()
	a, id := managedRolloverApp(t)
	a.Push = &pushnotify.Notifier{Store: a.Store, Pusher: renewalPusher{}, Clock: a.cfg.Clock}
	a.cfg.Setup = &SetupConfig{
		Role:      "combined",
		IssuerID:  "issuer",
		HTTPSCAID: "https-ca",
		HTTPSID:   "https",
		PushID:    "push",
		VendorID:  "vendor",
	}
	a.Certificates = &lifecycle.Manager{Store: a.protocol}
	key, err := x509.MarshalPKCS8PrivateKey(a.enroll.caKey)
	setupRequire(t, err, nil)
	for _, name := range []string{"issuer", "https-ca"} {
		_, err = a.Certificates.Adopt(
			t.Context(),
			lifecycle.Request{ID: name, Kind: lifecycle.Issuer},
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.enroll.caCert.Raw}),
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
		)
		setupRequire(t, err, nil)
	}
	binding := acme.Binding{MDMUDID: id.ID, CommonName: id.ID}
	profile, err := a.enroll.profile(t.Context(), binding)
	setupRequire(t, err, nil)
	profile.AccessRights = enroll.RightInstallProfiles
	setupRequire(t, a.enroll.recordProfile(t.Context(), binding, profile), nil)
	e, err := a.Store.Get(t.Context(), id)
	setupRequire(t, err, nil)
	e.LastSeenAt = a.cfg.Clock.Now()
	setupRequire(t, a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}), nil)
	setupJSON(
		t,
		a.protocol,
		"issued-identity:"+e.CertHash,
		identityEvidence{
			Issuer:   cms.Fingerprint(a.enroll.caCert),
			Method:   "scep",
			NotAfter: a.cfg.Clock.Now().Add(10 * 24 * time.Hour),
		},
	)
	v := setupExecute(t, a, lifecycle.Issuer, "renew", SetupRequest{Force: true})
	_, err = a.Certificates.CreateIssuer(
		t.Context(),
		"issuer",
		v.Identity.Pending,
		20*365*24*time.Hour,
	)
	setupRequire(t, err, nil)
	job, err := a.startIssuerRollover(t.Context(), "issuer", v.Identity.Pending)
	setupRequire(t, err, nil)
	target, err := a.managedIssuer(t.Context(), job.To)
	setupRequire(t, err, nil)
	return a, e, target, job
}

type renewalPusher struct{}

func (renewalPusher) Push(
	context.Context,
	[]push.Target,
) (map[mdm.EnrollmentID]push.Result, error) {
	return nil, io.ErrUnexpectedEOF
}

type renewalReplacementStore struct {
	storage.Store
	current           *storage.Replacement
	readErr, beginErr error
}

func (s renewalReplacementStore) TransitionReplacement(
	ctx context.Context,
	id mdm.EnrollmentID,
	change storage.ReplacementChange,
) (*storage.Replacement, error) {
	if change.Op == "read" {
		return s.current, s.readErr
	}
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	return s.Store.(storage.ReplacementStore).TransitionReplacement(ctx, id, change)
}

func queueRenewalTrust(
	t *testing.T,
	a *App,
	e *storage.Enrollment,
	uuid string,
	status storage.State,
) {
	t.Helper()
	cmd, err := mdm.NewCommand(
		&commands.InstallProfile{Payload: []byte("profile")},
		mdm.WithUUID(uuid),
	)
	setupRequire(t, err, nil)
	_, err = a.Store.Enqueue(
		t.Context(),
		[]mdm.EnrollmentID{e.ID},
		cmd,
		storage.EnqueueOptions{Now: a.cfg.Clock.Now()},
	)
	setupRequire(t, err, nil)
	if status == storage.StateCleared {
		_, err = a.Store.Clear(t.Context(), e.ID, storage.ClearFilter{})
		setupRequire(t, err, nil)
		return
	}
	if status == storage.StateAcknowledged || status == storage.StateError {
		_, err = a.Store.Next(t.Context(), e.ID, false, a.cfg.Clock.Now())
		setupRequire(t, err, nil)
		response := mdm.StatusAcknowledged
		if status == storage.StateError {
			response = mdm.StatusError
		}
		setupRequire(
			t,
			a.Store.StoreResult(
				t.Context(),
				e.ID,
				&mdm.Response{CommandUUID: uuid, Status: response},
				a.cfg.Clock.Now(),
			),
			nil,
		)
	}
}

func TestDeviceMigrationTrustAndReplacementOutcomes(t *testing.T) {
	for _, mode := range []string{"missing evidence", "already migrated", "offline", "queue trust", "trust pending", "trust failed", "trust cleared", "trust acknowledged", "unsupported method", "ineligible profile", "committed", "pending", "failed", "cancelled", "expired", "other replacement", "matching replacement", "replacement read failure", "replacement begin failure"} {
		t.Run(mode, func(t *testing.T) {
			a, e, target, _ := renewalFixture(t)
			m := lifecycle.Migration{Device: e.ID.ID, Phase: "queued"}
			trustRequired, want := false, "blocked"
			backing := a.Store
			switch mode {
			case "missing evidence":
				e.CertHash = "missing"
			case "already migrated":
				trustRequired, want = true, "confirmed"
				setupJSON(
					t,
					a.protocol,
					"issued-identity:"+e.CertHash,
					identityEvidence{
						Issuer: cms.Fingerprint(target.enrollment.caCert),
						Method: "scep",
					},
				)
			case "offline":
				e.LastSeenAt = time.Time{}
				want = "queued"
			case "queue trust",
				"trust pending",
				"trust failed",
				"trust cleared",
				"trust acknowledged":
				trustRequired, want = true, "trust-pending"
				if mode != "queue trust" {
					status := storage.StatePending
					if mode == "trust failed" {
						status, want = storage.StateError, "blocked"
					}
					if mode == "trust cleared" {
						status, want = storage.StateCleared, "blocked"
					}
					if mode == "trust acknowledged" {
						status, want = storage.StateAcknowledged, "identity-pending"
					}
					queueRenewalTrust(
						t,
						a,
						e,
						stableUUID("issuer-trust/issuer/2/"+e.ID.ID+"/0"),
						status,
					)
				}
			case "unsupported method":
				setupJSON(
					t,
					a.protocol,
					"issued-identity:"+e.CertHash,
					identityEvidence{Issuer: "old", Method: "manual"},
				)
			case "ineligible profile":
				e.ID.ID = "unknown-profile"
				e.CertHash = "unrecorded-pin"
				setupRequire(
					t,
					backing.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}),
					nil,
				)
				setupJSON(
					t,
					a.protocol,
					"issued-identity:"+e.CertHash,
					identityEvidence{Issuer: "old", Method: "scep"},
				)
			case "committed", "pending", "failed", "cancelled", "expired":
				m.Attempt = "attempt"
				a.Store = renewalReplacementStore{
					Store: backing,
					current: &storage.Replacement{
						ID:            "attempt",
						State:         mode,
						CandidateHash: "candidate",
						Issuer:        cms.Fingerprint(target.enrollment.caCert),
					},
				}
				if mode == "committed" {
					want = "confirmed"
				}
				if mode == "pending" || mode == "expired" {
					want = "identity-pending"
				}
			case "other replacement", "matching replacement":
				issuer := "other"
				want = "queued"
				if mode == "matching replacement" {
					issuer, want = cms.Fingerprint(target.enrollment.caCert), "identity-pending"
				}
				a.Store = renewalReplacementStore{
					Store: backing,
					current: &storage.Replacement{
						ID:     "existing",
						State:  storage.ReplacementPending,
						Issuer: issuer,
					},
				}
			case "replacement read failure":
				m.Attempt = "attempt"
				a.Store = renewalReplacementStore{Store: backing, readErr: io.ErrUnexpectedEOF}
			case "replacement begin failure":
				a.Store = renewalReplacementStore{Store: backing, beginErr: io.ErrUnexpectedEOF}
			}
			err := a.progressMigration(t.Context(), target, e, &m, trustRequired)
			if mode == "replacement read failure" || mode == "replacement begin failure" {
				setupRequire(t, err, io.ErrUnexpectedEOF)
				return
			}
			setupRequire(t, err, nil)
			if m.Phase != want {
				t.Fatal("wrong transition", m)
			}
			persisted, err := backing.Get(
				t.Context(),
				mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"},
			)
			setupRequire(t, err, nil)
			if persisted.CertHash != "original-pin" {
				t.Fatal("worker changed pin without device handshake")
			}
		})
	}
}

func TestHTTPSTrustOutcomesAndDisabledDevices(t *testing.T) {
	for _, mode := range []string{"missing", "disabled", "lookup failure", "queue failure", "pending", "acknowledged", "failed", "cleared"} {
		t.Run(mode, func(t *testing.T) {
			a, e, _, _ := renewalFixture(t)
			job := lifecycle.Rollover{IssuerID: "https-ca", To: "2"}
			m := lifecycle.Migration{Device: e.ID.ID}
			pairs, err := a.certificatePairs(t.Context(), "https-ca", false)
			setupRequire(t, err, nil)
			want := "trust-pending"
			switch mode {
			case "missing":
				m.Device, want = "missing", "disabled"
			case "disabled":
				e.Enabled, want = false, "disabled"
				setupRequire(
					t,
					a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}),
					nil,
				)
			case "lookup failure":
				a.Store = &storagetest.Failing{
					Store: a.Store,
					Fail:  map[string]error{"Get": io.ErrUnexpectedEOF},
				}
			case "queue failure":
				a.Store = &storagetest.Failing{
					Store: a.Store,
					Fail:  map[string]error{"Commands": io.ErrUnexpectedEOF},
				}
			default:
				status := storage.StatePending
				if mode == "acknowledged" {
					status, want = storage.StateAcknowledged, "confirmed"
				}
				if mode == "failed" {
					status, want = storage.StateError, "blocked"
				}
				if mode == "cleared" {
					status, want = storage.StateCleared, "blocked"
				}
				queueRenewalTrust(
					t,
					a,
					e,
					stableUUID("https-trust/https-ca/2/"+e.ID.ID+"/0"),
					status,
				)
			}
			err = a.progressHTTPSTrust(t.Context(), job, pairs, &m)
			if mode == "lookup failure" || mode == "queue failure" {
				setupRequire(t, err, io.ErrUnexpectedEOF)
			} else {
				setupRequire(t, err, nil)
				if m.Phase != want {
					t.Fatal(m)
				}
			}
		})
	}
}

func TestIdentityEvidenceLegacyRegistryAndDamagedRecords(t *testing.T) {
	a, _ := memorySetupApp(t)
	ctx := t.Context()
	k := "pki/certificate/legacy"
	setupJSON(
		t,
		a.protocol,
		k,
		revocation.Certificate{
			Issuer:     "legacy-ca",
			NotAfter:   a.cfg.Clock.Now().Add(time.Hour),
			Provenance: revocation.Provenance{Source: "scep"},
		},
	)
	setupRequire(
		t,
		a.protocol.Update(ctx, []string{"pki/fingerprint/hash"}, func(tx state.Tx) error {
			return tx.Put(ctx, state.Record{Key: "pki/fingerprint/hash", Value: []byte(k)})
		}),
		nil,
	)
	evidence, err := a.issuedIdentity(ctx, "hash")
	setupRequire(t, err, nil)
	if evidence.Issuer != "legacy-ca" || evidence.Method != "scep" {
		t.Fatal("legacy evidence not recovered", evidence)
	}
	setupJSON(t, a.protocol, "issued-identity:hash", identityEvidence{Method: "acme"})
	evidence, err = a.issuedIdentity(ctx, "hash")
	setupRequire(t, err, nil)
	if evidence.Method != "acme" {
		t.Fatal("known method overwritten")
	}
	for _, key := range []string{k, "issued-identity:hash"} {
		setupRequire(
			t,
			a.protocol.Update(
				ctx,
				[]string{key},
				func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: []byte("corrupt")}) },
			),
			nil,
		)
		if _, err := a.issuedIdentity(ctx, "hash"); err == nil {
			t.Fatal("corrupt evidence accepted")
		}
	}
	a.protocol = issuanceStateFault{Store: a.protocol, readErr: io.ErrUnexpectedEOF}
	_, err = a.issuedIdentity(ctx, "hash")
	setupRequire(t, err, io.ErrUnexpectedEOF)
}

func TestDeviceRenewalPersistsProgressAndExplicitRetries(t *testing.T) {
	a, e, _, job := renewalFixture(t)
	ctx := t.Context()
	e.LastSeenAt = time.Time{}
	setupRequire(t, a.renewOneIdentity(ctx, *e), nil)
	status, err := a.deviceRenewalStatus(ctx, e.ID.ID)
	setupRequire(t, err, nil)
	if status.Reason != "waiting for device check-in" || status.Generation != 1 {
		t.Fatal(status)
	}
	setupRequire(t, a.renewOneIdentity(ctx, *e), lifecycle.ErrConflict)
	setupRequire(t, a.retryMigration(ctx, "issuer", "", e.ID.ID), nil)
	status, err = a.deviceRenewalStatus(ctx, e.ID.ID)
	setupRequire(t, err, nil)
	if status.Retry != 1 || status.Phase != "queued" || !status.NextAttempt.IsZero() {
		t.Fatal(status)
	}
	result := setupExecute(t, a, lifecycle.Issuer, "device-status", SetupRequest{Device: e.ID.ID})
	if len(result.Migrations) != 1 {
		t.Fatal(result)
	}
	setupExecute(t, a, lifecycle.Issuer, "retry", SetupRequest{Device: e.ID.ID})
	setupRequire(t, a.reconcileDeviceRenewals(ctx), nil)
	for _, args := range [][3]string{{"wrong", "", e.ID.ID}, {"issuer", "", ""}, {"issuer", "", "missing"}, {"issuer", "2", "missing"}} {
		if err := a.retryMigration(ctx, args[0], args[1], args[2]); err == nil {
			t.Fatal("invalid retry accepted", args)
		}
	}
	setupRequire(t, a.retryMigration(ctx, "issuer", job.To, e.ID.ID), nil)
	rows, _, err := a.Certificates.Migrations(ctx, "issuer", job.To, "", 100)
	setupRequire(t, err, nil)
	rows[0].Phase = "confirmed"
	setupRequire(t, a.Certificates.SaveMigration(ctx, "issuer", job.To, rows[0]), nil)
	setupRequire(t, a.retryMigration(ctx, "issuer", job.To, e.ID.ID), lifecycle.ErrConflict)
	result = setupExecute(t, a, lifecycle.Issuer, "status", SetupRequest{})
	if result.Rollover == nil || len(result.Migrations) != 1 {
		t.Fatal(result)
	}
	key := "pki/lifecycle/device-renewal/" + digest([]byte(e.ID.ID))
	for _, phase := range []string{"confirmed", "disabled", "blocked"} {
		setupJSON(
			t,
			a.protocol,
			key,
			deviceRenewal{
				OldCertificate: e.CertHash,
				Migration:      lifecycle.Migration{Device: e.ID.ID, Phase: phase},
			},
		)
		if phase == "confirmed" || phase == "disabled" {
			setupRequire(t, a.retryMigration(ctx, "issuer", "", e.ID.ID), lifecycle.ErrConflict)
		}
		setupRequire(t, a.reconcileDeviceRenewals(ctx), nil)
	}
	setupJSON(
		t,
		a.protocol,
		key,
		deviceRenewal{
			OldCertificate: "old",
			Migration:      lifecycle.Migration{Device: e.ID.ID, Phase: "confirmed"},
		},
	)
	setupRequire(t, a.renewOneIdentity(ctx, *e), nil)
	setupRequire(
		t,
		a.protocol.Update(
			ctx,
			[]string{key},
			func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: []byte("corrupt")}) },
		),
		nil,
	)
	for _, operation := range []func() error{func() error { return a.renewOneIdentity(ctx, *e) }, func() error { return a.retryMigration(ctx, "issuer", "", e.ID.ID) }, func() error { return a.reconcileDeviceRenewals(ctx) }} {
		if err := operation(); err == nil {
			t.Fatal("corrupt renewal state accepted")
		}
	}
}

func TestRenewalWorkersRescanLateAndDisabledEnrollments(t *testing.T) {
	a, e, _, job := renewalFixture(t)
	ctx := t.Context()
	setupRequire(t, a.identityRenewalPass(ctx), nil)
	setupRequire(t, a.Store.Disable(ctx, e.ID, a.cfg.Clock.Now()), nil)
	for i := range 101 {
		id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: fmt.Sprintf("late-%03d", i)}
		setupRequire(
			t,
			a.Store.Import(
				ctx,
				storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true}},
			),
			nil,
		)
	}
	setupRequire(t, a.advanceRollover(ctx, job), nil)
	setupRequire(t, a.advanceRollover(ctx, job), nil)
	devices, err := a.enabledDevices(ctx)
	setupRequire(t, err, nil)
	if len(devices) != 101 {
		t.Fatal("incomplete enrollment scan")
	}
	rows, next, err := a.Certificates.Migrations(ctx, "issuer", "2", "", 100)
	setupRequire(t, err, nil)
	if len(rows) != 100 || next == "" {
		t.Fatal("late devices dropped")
	}
	for _, row := range rows {
		if row.Device != e.ID.ID && row.Phase != "blocked" {
			t.Fatal("unknown issuer allowed to migrate", row)
		}
	}
	_, err = a.retireManagedIssuer(ctx, "wrong", "1")
	setupRequire(t, err, lifecycle.ErrConflict)
	_, err = a.startIssuerRollover(ctx, "wrong", "2")
	setupRequire(t, err, lifecycle.ErrConflict)
	_, err = a.startHTTPSTrustRollover(ctx, "wrong", "2")
	setupRequire(t, err, lifecycle.ErrConflict)
	setupRequire(t, a.advanceHTTPSTrust(ctx, lifecycle.Rollover{Phase: "complete"}), nil)
	for _, worker := range []func(context.Context) error{a.renewDeviceIdentities, a.renewCertificates} {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		setupRequire(t, worker(cancelled), nil)
	}
}
