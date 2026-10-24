package deptest

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
)

// runProfileTargets checks serial-only desired state, revision fencing, isolation,
// rollback and definition lifecycle against every store implementation.
func runProfileTargets(t *testing.T, factory Factory) {
	t.Helper()
	s := factory(t, nil)
	ctx := t.Context()
	a := SampleAccount("choices")
	a.ProfileUUID = ""
	must(t, "create account", s.PutAccount(ctx, a))
	must(t, "define first", s.PutProfile(ctx, a.Name, SampleProfile("first")))
	must(t, "define second", s.PutProfile(ctx, a.Name, SampleProfile("second")))
	stored, err := s.GetAccount(ctx, a.Name)
	must(t, "read account", err)
	if stored.DesiredProfile("SERIAL") != "" {
		t.Fatal("definition selected a default")
	}
	profiles, err := s.ListProfiles(ctx, a.Name, paging.Page{})
	must(t, "profiles", err)
	if len(profiles.Items) != 2 {
		t.Fatal("profile definitions did not coexist")
	}
	a.ProfileUUID, a.ProfileTargets, a.ProfileRevision = "first", map[string]string{"SERIAL": "second"}, 12345
	must(t, "select profiles", s.PutAccount(ctx, a))
	stored, err = s.GetAccount(ctx, a.Name)
	must(t, "read choices", err)
	if stored.ProfileRevision != 1 || stored.DesiredProfile("SERIAL") != "second" || stored.DesiredProfile("OTHER") != "first" {
		t.Fatalf("resolution: %+v", stored)
	}
	stored.ProfileTargets["SERIAL"] = "first"
	again, err := s.GetAccount(ctx, a.Name)
	must(t, "isolation", err)
	if again.ProfileTargets["SERIAL"] != "second" {
		t.Fatal("mutable returned map")
	}
	a.ProfileTargets["SERIAL"] = "first"
	again, err = s.GetAccount(ctx, a.Name)
	must(t, "input isolation", err)
	if again.ProfileTargets["SERIAL"] != "second" {
		t.Fatal("mutable input map")
	}
	must(t, "identical write", s.PutAccount(ctx, again))
	stored, err = s.GetAccount(ctx, a.Name)
	must(t, "read revision", err)
	if stored.ProfileRevision != 1 {
		t.Fatal("identical account write changed revision")
	}
	for _, target := range []string{"first", "second"} {
		again.ProfileTargets["SERIAL"] = target
		must(t, "ABA change", s.PutAccount(ctx, again))
	}
	stored, err = s.GetAccount(ctx, a.Name)
	must(t, "read ABA", err)
	if stored.ProfileRevision != 3 {
		t.Fatalf("ABA revision: %d", stored.ProfileRevision)
	}
	must(t, "observed device", s.PutDevices(ctx, a.Name, []dep.Device{{SerialNumber: "SERIAL", ProfileUUID: "second", ProfileStatus: dep.ProfileStatusAssigned}}, t0))
	delete(stored.ProfileTargets, "SERIAL")
	must(t, "remove override", s.PutAccount(ctx, stored))
	stored, err = s.GetAccount(ctx, a.Name)
	must(t, "fallback", err)
	if stored.DesiredProfile("SERIAL") != "first" {
		t.Fatal("missing default fallback")
	}
	device, err := s.GetDevice(ctx, a.Name, "SERIAL")
	must(t, "observation retained", err)
	if device.ProfileUUID != "second" {
		t.Fatal("override removal changed observed Apple state")
	}
	stored.ProfileUUID = ""
	must(t, "remove default", s.PutAccount(ctx, stored))
	stored, err = s.GetAccount(ctx, a.Name)
	must(t, "read no policy", err)
	if stored.DesiredProfile("SERIAL") != "" {
		t.Fatal("profile inferred without policy")
	}
	for _, serial := range []string{"", " SERIAL", "SERIAL\n"} {
		invalid := stored.Clone()
		invalid.ProfileTargets = map[string]string{serial: "first"}
		wantErr(t, "invalid serial", s.PutAccount(ctx, invalid), dep.ErrInvalid)
	}
	invalid := stored.Clone()
	invalid.ProfileTargets = map[string]string{"SERIAL": ""}
	wantErr(t, "empty UUID", s.PutAccount(ctx, invalid), dep.ErrInvalid)
	invalid.ProfileTargets = map[string]string{"SERIAL": "missing"}
	wantErr(t, "missing definition", s.PutAccount(ctx, invalid), dep.ErrNotFound)
	must(t, "other account", s.PutAccount(ctx, &dep.Account{Name: "other", CreatedAt: t0, UpdatedAt: t0}))
	must(t, "foreign definition", s.PutProfile(ctx, "other", SampleProfile("foreign")))
	invalid.ProfileTargets = map[string]string{"SERIAL": "foreign"}
	wantErr(t, "account scoped UUID", s.PutAccount(ctx, invalid), dep.ErrNotFound)
	rollback := errors.New("rollback")
	err = s.Update(ctx, func(tx dep.Tx) error {
		if err := tx.LockAccount(ctx, a.Name); err != nil {
			return err
		}
		current, err := tx.GetAccount(ctx, a.Name)
		if err != nil {
			return err
		}
		current.ProfileTargets = map[string]string{"SERIAL": "first"}
		if err := tx.PutAccount(ctx, current); err != nil {
			return err
		}
		return rollback
	})
	wantErr(t, "rollback", err, rollback)
	again, err = s.GetAccount(ctx, a.Name)
	must(t, "read rollback", err)
	if again.ProfileRevision != stored.ProfileRevision || len(again.ProfileTargets) != 0 {
		t.Fatal("target update escaped rollback")
	}
	again.ProfileTargets = map[string]string{"SERIAL": "second"}
	must(t, "select definition", s.PutAccount(ctx, again))
	again, err = s.GetAccount(ctx, a.Name)
	must(t, "read selected", err)
	revision := again.ProfileRevision
	must(t, "edit selected definition", s.PutProfile(ctx, a.Name, SampleProfile("second")))
	again, err = s.GetAccount(ctx, a.Name)
	must(t, "read definition fence", err)
	if again.ProfileRevision != revision+1 {
		t.Fatal("definition did not fence in-flight assignment")
	}
	wantErr(t, "selected profile deletion", s.DeleteProfile(ctx, a.Name, "second"), dep.ErrConflict)
	must(t, "unused definition deletion", s.DeleteProfile(ctx, a.Name, "first"))
	must(t, "delete account", s.DeleteAccount(ctx, a.Name))
	must(t, "recreate account", s.PutAccount(ctx, &dep.Account{Name: a.Name, CreatedAt: t0, UpdatedAt: t0}))
	again, err = s.GetAccount(ctx, a.Name)
	must(t, "read recreation", err)
	if len(again.ProfileTargets) != 0 {
		t.Fatal("account recreation retained serial intent")
	}
}

// runTargetThrottle exercises a real assigner on every backend: Apple's serial
// cooldown remains binding after an explicit choice falls back to a new default.
func runTargetThrottle(t *testing.T, factory Factory) {
	t.Helper()
	f := newFenceFixture(t, factory)
	first, second := f.profile(t), f.profile(t)
	f.changeAccount(t, func(a *dep.Account) { a.ProfileUUID = first; a.ProfileTargets = map[string]string{"A": second} })
	f.server.Throttle("A", 90)
	worker, err := dep.NewAssigner(dep.AssignerConfig{Client: f.client, Store: f.store, Account: f.name, Clock: f.clock})
	must(t, "assigner", err)
	result, err := worker.RunOnce(t.Context())
	must(t, "first run", err)
	if result.Throttled != 1 {
		t.Fatalf("throttle: %+v", result)
	}
	f.changeAccount(t, func(a *dep.Account) { delete(a.ProfileTargets, "A") })
	count := f.server.Count(http.MethodPost, dep.PathProfileDevs)
	result, err = worker.RunOnce(t.Context())
	must(t, "deferred run", err)
	if result.Deferred != 1 || f.server.Count(http.MethodPost, dep.PathProfileDevs) != count {
		t.Fatalf("target edit bypassed Apple throttle: %+v", result)
	}
	f.clock.Advance(91 * time.Second)
	f.server.Throttle("A", 0)
	result, err = worker.RunOnce(t.Context())
	must(t, "retry", err)
	if result.Assigned != 1 {
		t.Fatalf("current target retry: %+v", result)
	}
	d, ok := f.server.Device("A")
	if !ok || d.ProfileUUID != first {
		t.Fatal("retry ignored current default")
	}
}
