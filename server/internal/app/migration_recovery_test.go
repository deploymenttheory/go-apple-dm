package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func TestRolloverClaimsAndTrustFailuresPreserveRetryState(t *testing.T) {
	for _, authority := range []string{"issuer", "https-ca"} {
		for _, mode := range []string{"claim failure", "claim conflict", "final save failure", "blocked", "backoff", "unsupported", "enqueue failure"} {
			t.Run(authority+"/"+mode, func(t *testing.T) {
				a, e, _, job := renewalFixture(t)
				ctx := t.Context()
				if authority == "https-ca" {
					v := setupExecute(
						t,
						a,
						lifecycle.Issuer,
						"renew",
						SetupRequest{Request: lifecycle.Request{ID: authority}, Force: true},
					)
					_, err := a.Certificates.CreateIssuer(
						ctx,
						authority,
						v.Identity.Pending,
						20*365*24*time.Hour,
					)
					setupRequire(t, err, nil)
					job, err = a.startHTTPSTrustRollover(ctx, authority, v.Identity.Pending)
					setupRequire(t, err, nil)
				}
				rows, _, err := a.Certificates.Migrations(ctx, authority, job.To, "", 100)
				setupRequire(t, err, nil)
				m := rows[0]
				switch mode {
				case "blocked":
					m.Phase = "blocked"
				case "backoff":
					m.NextAttempt = a.cfg.Clock.Now().Add(time.Hour)
				case "unsupported":
					e.Device.OSVersion, e.Device.ProductName = "1.0", "Unknown"
					setupRequire(
						t,
						a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *e}),
						nil,
					)
				case "enqueue failure":
					a.Core, err = service.New(
						service.Config{
							Store: &storagetest.Failing{
								Store: a.Store,
								Fail:  map[string]error{"Enqueue": io.ErrUnexpectedEOF},
							},
							Clock: a.cfg.Clock,
						},
					)
					setupRequire(t, err, nil)
				}
				setupRequire(t, a.Certificates.SaveMigration(ctx, authority, job.To, m), nil)
				backing := a.Certificates.Store
				writes := 0
				a.Certificates.Store = &setupStateFault{
					Store: backing,
					fail: func(op, key string) error {
						if op == "write" && strings.HasPrefix(key, "pki/lifecycle/migration/") {
							writes++
							if mode == "claim conflict" && writes == 1 {
								return lifecycle.ErrConflict
							}
							if (mode == "claim failure" && writes == 1) ||
								(mode == "final save failure" && writes == 2) {
								return io.ErrUnexpectedEOF
							}
						}
						return nil
					},
				}
				if authority == "issuer" {
					err = a.advanceRollover(ctx, job)
				} else {
					err = a.advanceHTTPSTrust(ctx, job)
				}
				if mode == "claim failure" || mode == "final save failure" {
					setupRequire(t, err, io.ErrUnexpectedEOF)
				} else {
					setupRequire(t, err, nil)
				}
				a.Certificates.Store = backing
				rows, _, err = a.Certificates.Migrations(ctx, authority, job.To, "", 100)
				setupRequire(t, err, nil)
				got := rows[0]
				if mode == "unsupported" && got.Phase != "blocked" {
					t.Fatal(got)
				}
				if mode == "enqueue failure" && !strings.Contains(got.Reason, "retry") {
					t.Fatal(got)
				}
				if (mode == "blocked" || mode == "backoff" || mode == "claim conflict") &&
					got.Generation != m.Generation+1 {
					t.Fatal("unavailable claim changed progress", got)
				}
			})
		}
	}
}

func TestHTTPSTrustMigrationPagesAndExplicitRetry(t *testing.T) {
	a, job := httpsRecoveryFixture(t, "")
	ctx := t.Context()
	for i := range 101 {
		setupRequire(
			t,
			a.Certificates.SaveMigration(
				ctx,
				job.IssuerID,
				job.To,
				lifecycle.Migration{Device: fmt.Sprintf("absent-%03d", i), Phase: "queued"},
			),
			nil,
		)
	}
	setupRequire(t, a.advanceHTTPSTrust(ctx, job), nil)
	rows, next, err := a.Certificates.Migrations(ctx, job.IssuerID, job.To, "", 100)
	setupRequire(t, err, nil)
	if len(rows) != 100 || next == "" {
		t.Fatal("missing first migration page")
	}
	rows, _, err = a.Certificates.Migrations(ctx, job.IssuerID, job.To, next, 100)
	setupRequire(t, err, nil)
	for _, m := range rows {
		if m.Phase == "disabled" {
			setupRequire(
				t,
				a.retryMigration(ctx, job.IssuerID, job.To, m.Device),
				lifecycle.ErrConflict,
			)
		} else {
			setupRequire(t, a.retryMigration(ctx, job.IssuerID, job.To, m.Device), nil)
		}
	}
}

func TestManagedConfigurationPropagatesCorruptIdentityRecords(t *testing.T) {
	for _, id := range []string{"push", "issuer"} {
		t.Run(id, func(t *testing.T) {
			a, _ := memorySetupApp(t)
			setupExecute(t, a, lifecycle.Push, "request", setupRequest("push", lifecycle.Push))
			setupJSON(t, a.protocol, "pki/lifecycle/identity/"+id, "corrupt")
			if err := a.configureManagedIdentities(t.Context()); err == nil {
				t.Fatal("corrupt identity accepted")
			}
		})
	}
	a, _, _, _ := renewalFixture(t)
	if _, err := a.deviceRenewalStatus(t.Context(), "unknown"); err == nil {
		t.Fatal("unknown device has renewal status")
	}
	if _, err := a.retireManagedIssuer(t.Context(), "missing", "1"); err == nil {
		t.Fatal("unknown issuer retired")
	}
	a.cfg.Setup.HTTPSCAID = ""
	if _, err := a.managedProfileTrust(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.cfg.PKI.CRLTTL = -time.Hour
	if _, err := a.managedRegistry(t.Context()); err == nil {
		t.Fatal("invalid registry policy accepted")
	}
	a.cfg.PKI.CRLTTL = time.Hour
	a.issuerServices = nil
	editSetupIdentity(
		t,
		a.protocol,
		"issuer",
		func(r map[string]any) {
			requireType[map[string]any](t, requireType[[]any](t, r["Revisions"])[1])["Key"] = ""
		},
	)
	if _, err := a.certificatePairs(t.Context(), "issuer", false); err == nil {
		t.Fatal("issuer without key accepted")
	}
	if _, err := a.managedIssuer(t.Context(), "2"); err == nil {
		t.Fatal("issuer service without key accepted")
	}
	a.cfg.Setup = &SetupConfig{Role: "vendor"}
	setupRequire(t, a.openCertificates(t.Context()), ErrConfig)
}

type renewalCancelLog struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *renewalCancelLog) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}

func TestRenewalWorkersReportScanFailureAndStopOnCancellation(t *testing.T) {
	for _, name := range []string{"certificates", "devices"} {
		t.Run(name, func(t *testing.T) {
			a, _, _, _ := renewalFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			writer := &renewalCancelLog{cancel: cancel}
			a.cfg.Logger = slog.New(slog.NewTextHandler(writer, nil))
			a.Certificates.Store = &setupStateFault{
				Store: a.protocol,
				fail:  func(string, string) error { return io.ErrUnexpectedEOF },
			}
			if name == "certificates" {
				setupRequire(t, a.renewCertificates(ctx), nil)
			} else {
				setupRequire(t, a.renewDeviceIdentities(ctx), nil)
			}
			if !strings.Contains(writer.String(), "renewal scan failed") {
				t.Fatal("scan error was not reported", writer.String())
			}
		})
	}
}
