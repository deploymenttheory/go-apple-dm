package app_test

import (
	"bytes"
	"context"
	"crypto/x509"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	depinmem "github.com/deploymenttheory/go-apple-dm/v3/storage/dep/inmem"
)

func TestDEP(t *testing.T) {
	ctx := context.Background()
	t.Run("AdminLifecycle", func(t *testing.T) {
		clk := clock.NewFake(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
		fake := deptest.NewServer(deptest.Options{Clock: clk})
		t.Cleanup(fake.Close)
		a := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem", AdminToken: "t", Clock: clk,
			DEP: app.DEPConfig{BaseURL: fake.URL(), ProfileURL: "https://mdm.example/enroll/ade"}})
		srv := serve(t, a)
		if a.DEP == nil {
			t.Fatal("DEP client missing")
		}
		if got := get(t, srv.URL+"/admin/v1/dep/accounts", ""); got != http.StatusUnauthorized {
			t.Fatalf("dep without token = %d", got)
		}
		// Keypair for the portal, then the .p7m the portal produces.
		res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/keypair", "t", nil)
		certPEM, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/x-pem-file" {
			t.Fatalf("keypair = %d %s", res.StatusCode, certPEM)
		}
		block, _ := pem.Decode(certPEM)
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if c, err := app.CertificateFromPEM(certPEM); err != nil || !c.Equal(cert) {
			t.Fatalf("CertificateFromPEM = %v %v", c, err)
		}
		p7m, err := fake.TokenP7M(cert)
		if err != nil {
			t.Fatal(err)
		}
		var detail dep.AccountDetail
		decode(t, do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/token", "t", p7m), http.StatusOK, &detail)
		if detail.ServerUUID == "" {
			t.Fatalf("detail = %+v", detail)
		}
		if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/token", "t", nil); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("empty token body = %d", res.StatusCode)
		}
		var accounts struct {
			Items []struct {
				Name      string
				HasTokens bool
			}
		}
		decode(t, do(t, srv, "GET", "/admin/v1/dep/accounts", "t", nil), http.StatusOK, &accounts)
		if len(accounts.Items) != 1 || accounts.Items[0].Name != "abm" || !accounts.Items[0].HasTokens {
			t.Fatalf("accounts = %+v", accounts)
		}
		// Devices arrive through sync; the profile is defined and assigned.
		fake.AddDevices(dep.Device{SerialNumber: "SER1", DeviceFamily: "Mac"}, dep.Device{SerialNumber: "SER2", DeviceFamily: "iPad"})
		var resp dep.ProfileResponse
		decode(t, do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/profile", "t", []byte(`{"profile_name":"Corp","org_magic":"m","is_supervised":true}`)), http.StatusOK, &resp)
		if resp.ProfileUUID == "" {
			t.Fatalf("profile = %+v", resp)
		}
		if defined := fake.Profiles()[resp.ProfileUUID]; defined.URL != "https://mdm.example/enroll/ade" {
			t.Fatalf("profile url defaulted to %q", defined.URL)
		}
		var run struct {
			Sync   dep.SyncResult
			Assign dep.AssignResult
		}
		decode(t, do(t, srv, "POST", "/admin/v1/dep/accounts/abm/sync", "t", nil), http.StatusOK, &run)
		if run.Sync.Added != 2 || run.Assign.Assigned != 2 {
			t.Fatalf("sync and assign = %+v", run)
		}
		for _, s := range []string{"SER1", "SER2"} {
			if d, ok := fake.Device(s); !ok || d.ProfileUUID != resp.ProfileUUID {
				t.Fatalf("%s = %+v", s, d)
			}
		}
		var devices struct {
			Items []struct{ SerialNumber string }
		}
		decode(t, do(t, srv, "GET", "/admin/v1/dep/accounts/abm/devices?limit=1", "t", nil), http.StatusOK, &devices)
		if len(devices.Items) != 1 {
			t.Fatalf("devices page = %+v", devices)
		}
		// Error mapping.
		if res := do(t, srv, "POST", "/admin/v1/dep/accounts/nobody/sync", "t", nil); res.StatusCode != http.StatusNotFound {
			t.Fatalf("unknown account sync = %d", res.StatusCode)
		}
		if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/profile", "t", []byte(`{`)); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad profile body = %d", res.StatusCode)
		}
		if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/profile", "t", []byte(`{"profile_name":"","is_mdm_removable":false,"is_supervised":false}`)); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid profile = %d", res.StatusCode)
		}
		if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/tokens", "t", []byte(`{`)); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad tokens body = %d", res.StatusCode)
		}
		if res := do(t, srv, "GET", "/admin/v1/dep/accounts/nobody/devices", "t", nil); res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNotFound {
			t.Fatalf("unknown account devices = %d", res.StatusCode)
		}
		fake.SetTermsNotSigned(true)
		body, _ := json.Marshal(fake.Tokens())
		if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/other/tokens", "t", body); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("terms not signed = %d", res.StatusCode)
		}
		fake.SetTermsNotSigned(false)
		decode(t, do(t, srv, "PUT", "/admin/v1/dep/accounts/other/tokens", "t", body), http.StatusOK, &detail)
		// Bodies that are not what the route expects.
		if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/token", "t", []byte("not a p7m")); res.StatusCode < http.StatusBadRequest {
			t.Fatalf("garbage p7m = %d", res.StatusCode)
		}
		big := bytes.Repeat([]byte("x"), app.MaxAdminBody+1)
		for _, route := range []string{"tokens", "profile"} {
			if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/"+route, "t", big); res.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("%s oversized = %d", route, res.StatusCode)
			}
		}
		if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/token", "t", big); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("token oversized = %d", res.StatusCode)
		}
	})
	t.Run("FailingStore", func(t *testing.T) {
		boom := errors.New("boom")
		clk := clock.NewFake(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
		fake := deptest.NewServer(deptest.Options{Clock: clk})
		t.Cleanup(fake.Close)
		failing := &deptest.Failing{Store: depinmem.New()}
		a := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem", AdminToken: "t", Clock: clk,
			DEP: app.DEPConfig{BaseURL: fake.URL(), SyncInterval: time.Minute, ProfileURL: "https://mdm.example/enroll/ade", Store: failing}})
		srv := serve(t, a)
		body, _ := json.Marshal(fake.Tokens())
		var detail dep.AccountDetail
		decode(t, do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/tokens", "t", body), http.StatusOK, &detail)
		profile := []byte(`{"profile_name":"Corp","org_magic":"m","is_supervised":true}`)
		for _, c := range []struct {
			method, path, fails string
			body                []byte
		}{
			{"PUT", "/admin/v1/dep/accounts/abm/keypair", "PutKeypair", nil},
			{"GET", "/admin/v1/dep/accounts/abm/devices", "ListDevices", nil},
			{"GET", "/admin/v1/dep/accounts", "ListAccounts", nil},
			{"PUT", "/admin/v1/dep/accounts/abm/profile", "PutProfile", profile},
			{"PUT", "/admin/v1/dep/accounts/abm/profile", "PutAccount", profile},
		} {
			failing.SetFail(map[string]error{c.fails: boom})
			if res := do(t, srv, c.method, c.path, "t", c.body); res.StatusCode != http.StatusInternalServerError {
				t.Fatalf("%s %s with %s failing = %d", c.method, c.path, c.fails, res.StatusCode)
			}
		}
		// The worker logs store and service failures and keeps running.
		for _, fails := range []string{"ListAccounts", "GetAccount"} {
			failing.SetFail(map[string]error{fails: boom})
			ctx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Run(ctx) }()
			deadline := time.Now().Add(5 * time.Second)
			waitPending := func() {
				for clk.Pending() == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
			}
			waitPending()
			clk.Advance(time.Minute)
			waitPending()
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("Run = %v", err)
			}
		}
	})
	t.Run("BadBaseURL", func(t *testing.T) {
		_, err := app.Build(ctx, app.Config{
			Role: app.RoleDDM, Storage: "inmem", AdminToken: "t",
			DDMSendKey: []byte("hop-send-key"), DDMRecvKey: []byte("hop-recv-key"),
			DEP: app.DEPConfig{BaseURL: "not a url"},
		})
		if err == nil || !strings.Contains(err.Error(), "DEP client") {
			t.Fatalf("Build = %v", err)
		}
	})
	t.Run("Status", func(t *testing.T) {
		for err, want := range map[error]int{
			axm.ErrArgument:    http.StatusBadRequest,
			axm.ErrWaitTimeout: http.StatusGatewayTimeout,
			errors.New("boom"): http.StatusBadGateway,
		} {
			if got := app.AxMStatusForTests(err); got != want {
				t.Errorf("axmStatus(%v) = %d, want %d", err, got, want)
			}
		}
		cases := map[error]int{
			dep.ErrNotFound:     http.StatusNotFound,
			dep.ErrConflict:     http.StatusConflict,
			dep.ErrTokenExpired: http.StatusBadRequest,
			&dep.Error{}:        http.StatusBadGateway,
			errors.New("boom"):  http.StatusInternalServerError,
		}
		for err, want := range cases {
			if got := app.DEPStatusForTests(err); got != want {
				t.Errorf("depStatus(%v) = %d, want %d", err, got, want)
			}
		}
		if _, err := app.CertificateFromPEM([]byte("nope")); err == nil {
			t.Fatal("no PEM accepted")
		}
		if _, err := app.CertificateFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}})); err == nil {
			t.Fatal("garbage certificate accepted")
		}
	})
	t.Run("Worker", func(t *testing.T) {
		clk := clock.NewFake(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
		fake := deptest.NewServer(deptest.Options{Clock: clk})
		t.Cleanup(fake.Close)
		a := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem", AdminToken: "t", Clock: clk,
			DEP: app.DEPConfig{BaseURL: fake.URL(), SyncInterval: time.Minute, ProfileURL: "https://mdm.example/enroll/ade"}})
		srv := serve(t, a)
		body, _ := json.Marshal(fake.Tokens())
		var detail dep.AccountDetail
		decode(t, do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/tokens", "t", body), http.StatusOK, &detail)
		fake.AddDevices(dep.Device{SerialNumber: "SERW"})
		// An account without tokens is skipped by the worker.
		if err := a.DEPStoreForTests().PutAccount(ctx, &dep.Account{Name: "pending"}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx) }()
		deadline := time.Now().Add(5 * time.Second)
		for clk.Pending() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		clk.Advance(time.Minute)
		for time.Now().Before(deadline) {
			if _, err := a.DEPStoreForTests().GetDevice(ctx, "abm", "SERW"); err == nil {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		if _, err := a.DEPStoreForTests().GetDevice(ctx, "abm", "SERW"); err != nil {
			t.Fatalf("worker did not sync: %v", err)
		}
		// A failing service is logged and the worker keeps going.
		fake.Close()
		waitPending := func() {
			for clk.Pending() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
		}
		waitPending()
		clk.Advance(time.Minute)
		waitPending()
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run = %v", err)
		}
	})
	t.Run("Env", func(t *testing.T) {
		env := func(m map[string]string) func(string) string {
			return func(k string) string {
				// A persistent store needs a keyring, and sqlite is the default, so
				// every ParseEnv case supplies one unless it sets its own.
				if k == app.EnvStorageKeys && m[k] == "" {
					return "test"
				}
				return m[k]
			}
		}
		cfg, err := app.ParseEnv(env(map[string]string{app.EnvDEPBaseURL: "https://dep.example", app.EnvDEPSyncInterval: "15m", app.EnvDEPUsePUT: "true"}))
		if err != nil || cfg.DEP.BaseURL != "https://dep.example" || cfg.DEP.SyncInterval != 15*time.Minute || !cfg.DEP.UsePUT {
			t.Fatalf("env = %+v %v", cfg.DEP, err)
		}
		if _, err := app.ParseEnv(env(map[string]string{app.EnvDEPSyncInterval: "soon"})); !errors.Is(err, app.ErrConfig) {
			t.Fatal(err)
		}
		if _, err := app.ParseEnv(env(map[string]string{app.EnvDEPUsePUT: "maybe"})); !errors.Is(err, app.ErrConfig) {
			t.Fatal(err)
		}
	})
	t.Run("SQLiteStore", func(t *testing.T) {
		fake := deptest.NewServer(deptest.Options{})
		t.Cleanup(fake.Close)
		a := build(t, app.Config{Role: app.RoleAll, Storage: "sqlite", DSN: t.TempDir() + "/dep.db", AdminToken: "t", DEP: app.DEPConfig{BaseURL: fake.URL()}})
		srv := serve(t, a)
		body, _ := json.Marshal(fake.Tokens())
		var detail dep.AccountDetail
		decode(t, do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/tokens", "t", body), http.StatusOK, &detail)
		if !strings.Contains(detail.OrgName, "Deployment") {
			t.Fatalf("detail = %+v", detail)
		}
		// Without a profile url the server's own ADE path is used.
		if res := do(t, srv, "PUT", "/admin/v1/dep/accounts/abm/profile", "t", []byte(`{"org_magic":"m","is_supervised":true}`)); res.StatusCode == http.StatusInternalServerError {
			t.Fatalf("default profile = %d", res.StatusCode)
		}
		// The worker is disabled at interval zero and stops cleanly.
		ctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx) }()
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run = %v", err)
		}
		if err := a.Close(); err != nil {
			t.Fatalf("Close = %v", err)
		}
	})
}
