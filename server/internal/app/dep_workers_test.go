package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	depinmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
)

type scheduledDEPStore struct {
	dep.Store
	lists    atomic.Int64
	accounts atomic.Int64
}

func (s *scheduledDEPStore) ListAccounts(ctx context.Context, page paging.Page) (paging.Result[dep.Account], error) {
	s.lists.Add(1)
	return s.Store.ListAccounts(ctx, page)
}

func (s *scheduledDEPStore) GetAccount(ctx context.Context, name string) (*dep.Account, error) {
	s.accounts.Add(1)
	return s.Store.GetAccount(ctx, name)
}

func TestDEPIndependentSchedules(t *testing.T) {
	for _, tc := range []struct {
		name          string
		sync, assign  time.Duration
		first, second int64
	}{
		{"disabled", 0, 0, 0, 0},
		{"sync only", time.Minute, 0, 1, 2},
		{"assignment only", 0, 2 * time.Minute, 0, 1},
		{"both", time.Minute, 2 * time.Minute, 1, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				st := &scheduledDEPStore{Store: depinmem.New()}
				a := &App{cfg: Config{Clock: clock.Real{}, Logger: slog.Default(), DEP: DEPConfig{SyncInterval: tc.sync, AssignInterval: tc.assign}}}
				d := &depService{app: a, store: st}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- d.Run(ctx) }()
				synctest.Wait()
				for _, want := range []int64{tc.first, tc.second} {
					time.Sleep(time.Minute)
					synctest.Wait()
					if got := st.lists.Load(); got != want {
						t.Fatalf("scheduled passes = %d, want %d", got, want)
					}
				}
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestDEPWorkerVisitsEveryAccountPage(t *testing.T) {
	ctx := t.Context()
	st := &scheduledDEPStore{Store: depinmem.New()}
	// Fill the first page with accounts awaiting credentials. The runnable
	// account lies on the second page and has assignment disabled by profile.
	if err := st.Update(ctx, func(tx dep.Tx) error {
		for i := range 1001 {
			account := &dep.Account{Name: fmt.Sprintf("account-%04d", i)}
			if i == 1000 {
				account.SetTokens(dep.Tokens{ConsumerKey: "key", ConsumerSecret: "secret", AccessToken: "token", AccessSecret: "access"})
			}
			if err := tx.PutAccount(ctx, account); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a, err := Build(ctx, Config{Storage: "inmem", DEP: DEPConfig{Store: st}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.dep.runAccounts(ctx, true); err != nil {
		t.Fatal(err)
	}
	if st.lists.Load() != 2 || st.accounts.Load() != 1 {
		t.Fatalf("pages=%d runnable accounts=%d", st.lists.Load(), st.accounts.Load())
	}
}
