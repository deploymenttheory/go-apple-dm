package app_test

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	depinmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestDEPProfileChoicesProductionEnrollment exercises signed ADE requests, SCEP
// issuance and check-in through app.Build's file-configured admission policy.
// It supplies no custom admission callback or ADE lookup/resolution function.
func TestDEPProfileChoicesProductionEnrollment(t *testing.T) {
	ctx := t.Context()
	policy := filepath.Join(t.TempDir(), "admission.json")
	if err := os.WriteFile(policy, []byte(`{"depAccounts":["fleet"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	faults := &deptest.Failing{Store: depinmem.New()}
	f := newEnrollFixture(t, "", func(cfg *app.Config) {
		cfg.DEP.Store = faults
		cfg.Enroll.Admission = nil
		cfg.Enroll.AdmissionFile = policy
		cfg.Enroll.OIDC = app.OIDCConfig{}
	})
	store := f.app.DEPStoreForTests()
	now := time.Now().UTC()
	if err := store.PutAccount(ctx, &dep.Account{Name: "fleet", ProfileUUID: "default", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, uuid := range []string{"default", "explicit"} {
		if err := store.PutProfile(ctx, "fleet", &dep.Profile{ProfileUUID: uuid, ProfileName: uuid, URL: f.publicURL + app.PathADE}); err != nil {
			t.Fatal(err)
		}
	}
	const serial = "ADE-PRODUCTION-POLICY"
	cases := []struct {
		name     string
		observed string
		edit     func(*dep.Account)
		allowed  bool
	}{
		{"explicit choice", "explicit", func(a *dep.Account) { a.ProfileTargets = map[string]string{serial: "explicit"} }, true},
		{"explicit rejects observed default", "default", nil, false},
		{"override removal falls back to default", "default", func(a *dep.Account) { delete(a.ProfileTargets, serial) }, true},
		{"default rejects former override", "explicit", nil, false},
		{"no desired profile rejects observed assignment", "explicit", func(a *dep.Account) { a.ProfileUUID = "" }, false},
		{"explicit choice without account default", "explicit", func(a *dep.Account) { a.ProfileTargets = map[string]string{serial: "explicit"} }, true},
		{"different observed assignment rejected", "unselected", nil, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.edit != nil {
				if err := store.Update(ctx, func(tx dep.Tx) error {
					if err := tx.LockAccount(ctx, "fleet"); err != nil {
						return err
					}
					account, err := tx.GetAccount(ctx, "fleet")
					if err != nil {
						return err
					}
					tc.edit(account)
					return tx.PutAccount(ctx, account)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.PutDevices(ctx, "fleet", []dep.Device{{SerialNumber: serial, ProfileUUID: tc.observed, ProfileStatus: dep.ProfileStatusAssigned}}, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			udid := fmt.Sprintf("ADE-POLICY-%d", i)
			d := f.device(t, udid, "Mac16,1")
			d.SerialNumber = serial
			err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{})
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: udid}
			enrollment, lookupErr := f.app.Store.Get(ctx, id)
			if tc.allowed {
				if err != nil {
					t.Fatalf("production policy denied enrollment: %v", err)
				}
				if lookupErr != nil || !enrollment.Enabled {
					t.Fatalf("admitted request did not complete check-in: %+v %v", enrollment, lookupErr)
				}
				if d.Identity.Cert.Issuer.CommonName != f.appCA.Subject.CommonName {
					t.Fatal("admitted request did not obtain the server-issued SCEP identity")
				}
			} else {
				var denial *simulator.HTTPError
				if !errors.As(err, &denial) || denial.Status != http.StatusForbidden {
					t.Fatalf("production policy denial: %v", err)
				}
				if !errors.Is(lookupErr, storage.ErrNotFound) {
					t.Fatalf("denied request created enrollment: %+v %v", enrollment, lookupErr)
				}
			}
		})
	}
	t.Run("storage failure is not policy denial", func(t *testing.T) {
		faults.SetFail(map[string]error{"GetAccount": errors.New("DEP state unavailable")})
		defer faults.SetFail(nil)
		d := f.device(t, "ADE-POLICY-STORAGE-FAILURE", "Mac16,1")
		d.SerialNumber = serial
		var response *simulator.HTTPError
		err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{})
		if !errors.As(err, &response) || response.Status != http.StatusInternalServerError {
			t.Fatalf("storage failure lost server-error classification: %v", err)
		}
	})
}
