package app_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	depinmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// profileCommitGate delays the admin transaction after the client has accepted its reply.
type profileCommitGate struct {
	dep.Store
	response         atomic.Bool
	updates          atomic.Int64
	entered, release chan struct{}
}

// Update leaves the account unlocked while the test replaces its credentials or identity.
func (s *profileCommitGate) Update(ctx context.Context, fn func(dep.Tx) error) error {
	if s.response.Load() && s.updates.Add(1) == 2 {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.Update(ctx, fn)
}

type profileResponseTransport struct {
	base  http.RoundTripper
	store *profileCommitGate
}

// RoundTrip marks the response so the store gate can distinguish client validation from commit.
func (r profileResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := r.base.RoundTrip(req)
	if err == nil && req.URL.Path == dep.PathProfile {
		r.store.response.Store(true)
	}
	return res, err
}

// TestDEPProfileCommitRejectsReplacedAccount covers the gap after remote response validation.
func TestDEPProfileCommitRejectsReplacedAccount(t *testing.T) {
	for _, change := range []string{"credentials", "identity"} {
		t.Run(change, func(t *testing.T) {
			clk := clock.NewFake(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
			fake := deptest.NewServer(deptest.Options{Clock: clk})
			t.Cleanup(fake.Close)
			st := &profileCommitGate{Store: depinmem.New(), entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(st.release) }) }
			t.Cleanup(release)
			a := build(t, app.Config{Storage: "inmem", BootstrapToken: "t", Clock: clk, DEP: app.DEPConfig{Store: st, BaseURL: fake.URL(), HTTPClient: &http.Client{Transport: profileResponseTransport{base: fake.Client().Transport, store: st}}, ProfileURL: "https://mdm.example/enroll"}})
			if _, err := a.DEP.StoreTokens(t.Context(), "account", fake.Tokens()); err != nil {
				t.Fatal(err)
			}
			srv := serve(t, a)
			// Release any waiting handler before the HTTP server cleanup runs.
			t.Cleanup(release)
			done := make(chan int, 1)
			go func() {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL+"/admin/v1/dep/accounts/account/profile", strings.NewReader(`{"profile_name":"test","org_magic":"test"}`))
				if err != nil {
					done <- 0
					return
				}
				req.Header.Set("Authorization", "Bearer t")
				res, err := srv.Client().Do(req)
				if err != nil {
					done <- 0
					return
				}
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
				done <- res.StatusCode
			}()
			select {
			case <-st.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("profile did not reach commit gate")
			}
			acct, err := st.Store.GetAccount(t.Context(), "account")
			if err != nil {
				t.Fatal(err)
			}
			if change == "identity" {
				acct.ServerUUID = "replacement"
			} else {
				acct.AccessToken = "renewed-token"
			}
			if err := st.Store.PutAccount(t.Context(), acct); err != nil {
				t.Fatal(err)
			}
			release()
			if status := <-done; status != http.StatusConflict {
				t.Fatalf("stale profile commit: HTTP %d", status)
			}
			profiles, err := st.Store.ListProfiles(t.Context(), "account", paging.Page{})
			if err != nil || len(profiles.Items) != 0 {
				t.Fatalf("obsolete profile persisted: %+v %v", profiles, err)
			}
			acct, _ = st.Store.GetAccount(t.Context(), "account")
			if acct.ProfileUUID != "" {
				t.Fatal("obsolete profile selected")
			}
		})
	}
}
