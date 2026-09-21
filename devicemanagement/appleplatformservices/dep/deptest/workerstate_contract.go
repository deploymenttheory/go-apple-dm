package deptest

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
)

// These scenarios exercise actual workers against every contract backend.
func runWorkerState(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("ResumeSnapshotAndReconcile", func(t *testing.T) {
		ctx := t.Context()
		st := factory(t, nil)
		clk := clock.NewFake(t0)
		srv := NewServer(Options{Clock: clk})
		t.Cleanup(srv.Close)
		account := &dep.Account{Name: "sync", CreatedAt: t0, UpdatedAt: t0}
		account.SetTokens(srv.Tokens())
		if err := st.PutAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
		if err := st.PutDevices(ctx, account.Name, []dep.Device{{SerialNumber: "REMOVED"}}, t0); err != nil {
			t.Fatal(err)
		}
		srv.AddDevices(dep.Device{SerialNumber: "A"}, dep.Device{SerialNumber: "B"})
		client, err := dep.NewClient(dep.ClientConfig{Store: st, BaseURL: srv.URL(), HTTPClient: srv.Client(), Clock: clk})
		if err != nil {
			t.Fatal(err)
		}
		fault := errors.New("interrupted second page")
		broken := &Failing{Store: st, Fail: map[string]error{"MarkFetched": fault}, After: map[string]int{"MarkFetched": 2}}
		cfg := dep.SyncerConfig{Client: client, Store: broken, Account: account.Name, Clock: clk, Limit: 1}
		worker, err := dep.NewSyncer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := worker.RunOnce(ctx); !errors.Is(err, fault) {
			t.Fatalf("partial fetch: %v", err)
		}
		removed, err := st.GetDevice(ctx, account.Name, "REMOVED")
		if err != nil || removed.Deleted {
			t.Fatalf("partial fetch deleted inventory: %+v %v", removed, err)
		}
		cursor, err := st.Cursor(ctx, account.Name)
		if err != nil || cursor.Phase != dep.PhaseFetch || cursor.Generation == "" {
			t.Fatalf("resume cursor: %+v %v", cursor, err)
		}
		cfg.Store = st
		worker, err = dep.NewSyncer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		result, err := worker.RunOnce(ctx)
		if err != nil || result.Deleted != 1 {
			t.Fatalf("resumed fetch: %+v %v", result, err)
		}
		removed, err = st.GetDevice(ctx, account.Name, "REMOVED")
		if err != nil || !removed.Deleted {
			t.Fatalf("missing tombstone: %+v %v", removed, err)
		}
		rows, err := st.ListDevices(ctx, account.Name, dep.DeviceQuery{}, paging.Page{Limit: 10})
		if err != nil || len(rows.Items) != 2 {
			t.Fatalf("survivors: %+v %v", rows, err)
		}
		// An empty successful full snapshot must reconcile the last two rows.
		srv.DeleteDevice("A")
		srv.DeleteDevice("B")
		clk.Advance(8 * 24 * time.Hour)
		result, err = worker.RunOnce(ctx)
		if err != nil || result.Deleted != 2 {
			t.Fatalf("empty snapshot: %+v %v", result, err)
		}
		cursor, err = st.Cursor(ctx, account.Name)
		if err != nil || cursor.Generation != "" || cursor.Phase != dep.PhaseSync {
			t.Fatalf("completed cursor: %+v %v", cursor, err)
		}
	})
	t.Run("AssignmentBackoffSurvivesInstances", func(t *testing.T) {
		ctx := t.Context()
		st := factory(t, nil)
		clk := clock.NewFake(t0)
		srv := NewServer(Options{Clock: clk})
		t.Cleanup(srv.Close)
		account := &dep.Account{Name: "assignment", CreatedAt: t0, UpdatedAt: t0}
		account.SetTokens(srv.Tokens())
		if err := st.PutAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
		client, err := dep.NewClient(dep.ClientConfig{Store: st, BaseURL: srv.URL(), HTTPClient: srv.Client(), Clock: clk})
		if err != nil {
			t.Fatal(err)
		}
		profile, err := client.DefineProfile(ctx, account.Name, &dep.Profile{ProfileName: "contract", URL: "https://mdm.example.test/enroll", OrgMagic: "test"})
		if err != nil {
			t.Fatal(err)
		}
		account.ProfileUUID = profile.ProfileUUID
		if err := st.PutAccount(ctx, account); err != nil {
			t.Fatal(err)
		}
		device := dep.Device{SerialNumber: "A"}
		srv.AddDevices(device)
		if err := st.PutDevices(ctx, account.Name, []dep.Device{device}, t0); err != nil {
			t.Fatal(err)
		}
		cfg := dep.AssignerConfig{Client: client, Store: st, Account: account.Name, Clock: clk}
		worker, err := dep.NewAssigner(cfg)
		if err != nil {
			t.Fatal(err)
		}
		srv.Script(dep.PathProfileDevs, Scripted{Status: http.StatusTooManyRequests, RetryAfter: "3600"})
		first, err := worker.RunOnce(ctx)
		if err == nil || !first.NotBefore.Equal(t0.Add(time.Hour)) {
			t.Fatalf("429: %+v %v", first, err)
		}
		worker, err = dep.NewAssigner(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := worker.RunOnce(ctx); !errors.Is(err, dep.ErrBackoff) {
			t.Fatalf("new instance ignored backoff: %v", err)
		}
		if n := srv.Count(http.MethodPost, dep.PathProfileDevs); n != 1 {
			t.Fatalf("requests before deadline: %d", n)
		}
		clk.Advance(time.Hour)
		result, err := worker.RunOnce(ctx)
		if err != nil || result.Assigned != 1 {
			t.Fatalf("after deadline: %+v %v", result, err)
		}
		state, err := st.AssignmentState(ctx, account.Name)
		if err != nil || state.Owner != "" || state.Failures != 0 || !state.NotBefore.IsZero() {
			t.Fatalf("finished state: %+v %v", state, err)
		}
	})
	t.Run("AccountLockAndRollback", func(t *testing.T) {
		ctx := t.Context()
		st := factory(t, nil)
		if err := st.PutAccount(ctx, &dep.Account{Name: "lock", CreatedAt: t0, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				errs <- st.Update(ctx, func(tx dep.Tx) error {
					if err := tx.LockAccount(ctx, "lock"); err != nil {
						return err
					}
					state, err := tx.AssignmentState(ctx, "lock")
					if err != nil {
						return err
					}
					state.Failures++
					return tx.PutAssignmentState(ctx, "lock", state)
				})
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		state, err := st.AssignmentState(ctx, "lock")
		if err != nil || state.Failures != 8 {
			t.Fatalf("lost update: %+v %v", state, err)
		}
		rollback := errors.New("rollback")
		if err := st.Update(ctx, func(tx dep.Tx) error {
			if err := tx.LockAccount(ctx, "lock"); err != nil {
				return err
			}
			if err := tx.PutAssignmentState(ctx, "lock", dep.AssignmentState{Failures: 99}); err != nil {
				return err
			}
			return rollback
		}); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		state, err = st.AssignmentState(ctx, "lock")
		if err != nil || state.Failures != 8 {
			t.Fatalf("rollback failed: %+v %v", state, err)
		}
		if err := st.DeleteAccount(ctx, "lock"); err != nil {
			t.Fatal(err)
		}
		state, err = st.AssignmentState(ctx, "lock")
		if err != nil || state != (dep.AssignmentState{}) {
			t.Fatalf("orphan state: %+v %v", state, err)
		}
	})
}
