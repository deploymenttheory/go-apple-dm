package deptest_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
)

// Fault injection must preserve the store contract when no failure is set,
// including transaction isolation and the state used by real workers.
func TestFailingStorePreservesStoreContract(t *testing.T) {
	deptest.RunStoreSuite(t, func(_ *testing.T, keyring *crypt.Keyring) dep.Store {
		return &deptest.Failing{Store: inmem.New(inmem.WithKeyring(keyring))}
	})
}

// TestInjectedStoreFailuresRollbackTransactions checks injected store failures rollback
// transactions.
func TestInjectedStoreFailuresRollbackTransactions(t *testing.T) {
	ctx, now := t.Context(), time.Now().UTC()
	failure := errors.New("injected storage failure")
	for name, call := range map[string]func(dep.Records) error{
		"PutAccount":    func(s dep.Records) error { return s.PutAccount(ctx, &dep.Account{Name: "account"}) },
		"GetAccount":    func(s dep.Records) error { _, err := s.GetAccount(ctx, "account"); return err },
		"DeleteAccount": func(s dep.Records) error { return s.DeleteAccount(ctx, "account") },
		"ListAccounts":  func(s dep.Records) error { _, err := s.ListAccounts(ctx, paging.Page{}); return err },
		"SetAccountState": func(s dep.Records) error {
			return s.SetAccountState(ctx, "account", dep.AccountState{TokenInvalid: true})
		},
		"PutKeypair": func(s dep.Records) error {
			return s.PutKeypair(ctx, "account", dep.StageStaged, &dep.Keypair{CertPEM: []byte("cert"), KeyPEM: []byte("key")})
		},
		"Keypair":        func(s dep.Records) error { _, err := s.Keypair(ctx, "account", dep.StageStaged); return err },
		"UpstageKeypair": func(s dep.Records) error { return s.UpstageKeypair(ctx, "account") },
		"Session":        func(s dep.Records) error { _, err := s.Session(ctx, "account"); return err },
		"SetSession":     func(s dep.Records) error { return s.SetSession(ctx, "account", "changed") },
		"Cursor":         func(s dep.Records) error { _, err := s.Cursor(ctx, "account"); return err },
		"SetCursor":      func(s dep.Records) error { return s.SetCursor(ctx, "account", dep.Cursor{Value: "changed"}) },
		"PutDevices": func(s dep.Records) error {
			return s.PutDevices(ctx, "account", []dep.Device{{SerialNumber: "device"}}, now)
		},
		"GetDevice": func(s dep.Records) error { _, err := s.GetDevice(ctx, "account", "device"); return err },
		"ListDevices": func(s dep.Records) error {
			_, err := s.ListDevices(ctx, "account", dep.DeviceQuery{}, paging.Page{})
			return err
		},
		"PutProfile":    func(s dep.Records) error { return s.PutProfile(ctx, "account", deptest.SampleProfile("profile")) },
		"GetProfile":    func(s dep.Records) error { _, err := s.GetProfile(ctx, "account", "profile"); return err },
		"DeleteProfile": func(s dep.Records) error { return s.DeleteProfile(ctx, "account", "profile") },
		"ListProfiles":  func(s dep.Records) error { _, err := s.ListProfiles(ctx, "account", paging.Page{}); return err },
		"PutAssignment": func(s dep.Records) error {
			return s.PutAssignment(ctx, &dep.Assignment{Account: "account", SerialNumber: "device"})
		},
		"GetAssignment": func(s dep.Records) error { _, err := s.GetAssignment(ctx, "account", "device"); return err },
		"ListAssignments": func(s dep.Records) error {
			_, err := s.ListAssignments(ctx, "account", dep.AssignmentQuery{}, paging.Page{})
			return err
		},
		"MarkFetched":     func(s dep.Records) error { return s.MarkFetched(ctx, "account", "generation", []string{"device"}) },
		"AssignmentState": func(s dep.Records) error { _, err := s.AssignmentState(ctx, "account"); return err },
		"PutAssignmentState": func(s dep.Records) error {
			return s.PutAssignmentState(ctx, "account", dep.AssignmentState{Owner: "worker"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			base := inmem.New()
			if err := base.PutAccount(ctx, deptest.SampleAccount("account")); err != nil {
				t.Fatal(err)
			}
			if err := base.SetSession(ctx, "account", "original"); err != nil {
				t.Fatal(err)
			}
			store := &deptest.Failing{Store: base, Fail: map[string]error{name: failure}}
			if err := call(store); !errors.Is(err, failure) {
				t.Fatalf("direct call lost injected error: %v", err)
			}
			// A failure after a successful write must roll the earlier write back.
			err := store.Update(ctx, func(tx dep.Tx) error {
				if err := tx.SetSession(ctx, "marker", "uncommitted"); err != nil {
					return err
				}
				return call(tx)
			})
			if !errors.Is(err, failure) {
				t.Fatalf("transaction lost injected error: %v", err)
			}
			if got, err := base.Session(ctx, "marker"); err != nil || got != "" {
				t.Fatalf("failed transaction committed: %q %v", got, err)
			}
			if got, err := base.Session(ctx, "account"); err != nil || got != "original" {
				t.Fatalf("failed call changed stored state: %q %v", got, err)
			}
			store.SetFail(nil)
			if err := store.Update(ctx, func(tx dep.Tx) error { return tx.SetSession(ctx, "marker", "committed") }); err != nil {
				t.Fatal(err)
			}
			if got, err := base.Session(ctx, "marker"); err != nil || got != "committed" {
				t.Fatalf("cleared fault prevented commit: %q %v", got, err)
			}
		})
	}
}
