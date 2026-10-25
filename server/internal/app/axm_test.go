package app_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/server/axmassignment"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// axmKey generates a P-256 private key and its SEC 1 PEM encoding.
func axmKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

// axmActivityDeadlineTransport observes accepted activities and optionally holds
// their first read until the admin wait deadline expires.
type axmActivityDeadlineTransport struct {
	base        http.RoundTripper
	blockRead   bool
	reads       atomic.Int32
	mutations   atomic.Int32
	activityIDs chan string
	expire      func()
}

// RoundTrip preserves token and mutation requests while observing the activity
// read through the outbound client's actual transport path.
func (tr *axmActivityDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost && r.URL.Path == "/v1/orgDeviceActivities" {
		tr.mutations.Add(1)
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/orgDeviceActivities/") && tr.reads.Add(1) == 1 {
		tr.activityIDs <- strings.TrimPrefix(r.URL.Path, "/v1/orgDeviceActivities/")
		if tr.blockRead {
			tr.expire()
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
	}
	return tr.base.RoundTrip(r)
}

// axmWaitDeadlineContext signals an incoming request deadline at a chosen
// observation boundary so scheduling cannot expire it before the test's seam.
type axmWaitDeadlineContext struct {
	context.Context
	expired chan struct{}
	once    sync.Once
}

// Done is closed when the controlled request deadline expires.
func (ctx *axmWaitDeadlineContext) Done() <-chan struct{} { return ctx.expired }

// Err reports the simulated request deadline without changing caller values.
func (ctx *axmWaitDeadlineContext) Err() error {
	select {
	case <-ctx.expired:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// expire delivers the request deadline exactly once.
func (ctx *axmWaitDeadlineContext) expire() { ctx.once.Do(func() { close(ctx.expired) }) }

// TestAxMWaitDeadline checks cancellation inside a remote read and between durable observations.
// An accepted activity remains queryable and is never submitted again.
func TestAxMWaitDeadline(t *testing.T) {
	for _, blockRead := range []bool{true, false} {
		t.Run(map[bool]string{true: "activity_read", false: "between_observations"}[blockRead], func(t *testing.T) {
			key, keyPEM := axmKey(t)
			fake := axmtest.NewServer()
			t.Cleanup(fake.Close)
			fake.RegisterKey("BUSINESSAPI.abc", "kid-1", &key.PublicKey)
			service := fake.AddMDMServer("go-apple-dm", nil)
			fake.AddOrgDevice("DEADLINE", nil)
			deadline := &axmWaitDeadlineContext{Context: t.Context(), expired: make(chan struct{})}
			client := fake.Client()
			transport := &axmActivityDeadlineTransport{base: client.Transport, blockRead: blockRead, activityIDs: make(chan string, 1), expire: deadline.expire}
			client.Transport = transport
			a := build(t, app.Config{Storage: "inmem", BootstrapToken: "t", AxM: app.AxMConfig{ClientID: "BUSINESSAPI.abc", KeyID: "kid-1", KeyPEM: keyPEM, BaseURL: fake.URL, TokenURL: fake.TokenURL, HTTPClient: client}})
			account, e := a.Inventory.Account(t.Context(), "default")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = a.Assignments.Configure(t.Context(), "default", account.Revision, service, axm.ActivityLogColumns{Serial: "serialNumber", Outcome: "status", Success: "SUCCESS"}); e != nil {
				t.Fatal(e)
			}
			if _, e = a.Assignments.Request(t.Context(), "default", "deadline", []string{"DEADLINE"}, time.Now()); e != nil {
				t.Fatal(e)
			}
			e = a.Assignments.Step(deadline, "deadline")
			if blockRead {
				if !errors.Is(e, context.DeadlineExceeded) {
					t.Fatal(e)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				deadline.expire()
				if e = a.Assignments.Step(deadline, "deadline"); !errors.Is(e, context.DeadlineExceeded) {
					t.Fatal(e)
				}
			}
			if transport.reads.Load() != 1 || !errors.Is(deadline.Err(), context.DeadlineExceeded) {
				t.Fatal("deadline missed observation boundary")
			}
			id := <-transport.activityIDs
			activity, e := a.AxM.GetOrgDeviceActivity(t.Context(), id, axm.GetOptions{})
			if e != nil || activity.ID != id || activity.Attributes.Status != axm.ActivityInProgress {
				t.Fatal(activity, e)
			}
			w, e := a.Assignments.Get(t.Context(), "default", "deadline")
			if e != nil || w.ActivityID != id || w.AcceptedAt == nil || w.TerminalStatus != "" {
				t.Fatal(w, e)
			}
			if transport.mutations.Load() != 1 {
				t.Fatal("cancellation repeated mutation")
			}
		})
	}
}

// TestAxM checks AXM environment configuration and admin assignment requests.
func TestAxM(t *testing.T) {
	t.Run("ConfiguredFromEnv", func(t *testing.T) {
		_, keyPEM := axmKey(t)
		keyFile := filepath.Join(t.TempDir(), "axm.pem")
		if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
			t.Fatal(err)
		}
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
		cfg, err := app.ParseEnv(env(map[string]string{app.EnvAxMClientID: "BUSINESSAPI.abc", app.EnvAxMKeyID: "kid-1", app.EnvAxMKeyFile: keyFile, app.EnvAxMScope: "business.api"}))
		if err != nil || cfg.AxM.ClientID != "BUSINESSAPI.abc" || cfg.AxM.KeyFile != keyFile {
			t.Fatalf("env = %+v %v", cfg.AxM, err)
		}
		if _, err := app.ParseEnv(env(map[string]string{app.EnvAxMClientID: "BUSINESSAPI.abc"})); !errors.Is(err, app.ErrConfig) {
			t.Fatalf("missing key = %v", err)
		}
		fake := axmtest.NewServer()
		t.Cleanup(fake.Close)
		cfg.AxM.BaseURL, cfg.AxM.TokenURL, cfg.AxM.HTTPClient = fake.URL, fake.TokenURL, fake.Client()
		cfg.Storage, cfg.BootstrapToken, cfg.Logger = "inmem", "t", quiet
		cfg.StorageKeys = nil
		a := build(t, cfg)
		if a.AxM == nil {
			t.Fatal("client not built from the key file")
		}
		bad := cfg
		bad.AxM.KeyFile = filepath.Join(t.TempDir(), "missing.pem")
		if _, err := app.Build(context.Background(), bad); !errors.Is(err, app.ErrConfig) {
			t.Fatalf("missing key file = %v", err)
		}
		bad = cfg
		bad.AxM.KeyFile, bad.AxM.KeyPEM = "", []byte("not a key")
		if _, err := app.Build(context.Background(), bad); err == nil {
			t.Fatal("unparsable key accepted")
		}
	})
	t.Run("AdminAssign", func(t *testing.T) {
		key, keyPEM := axmKey(t)
		fake := axmtest.NewServer()
		t.Cleanup(fake.Close)
		fake.RegisterKey("BUSINESSAPI.abc", "kid-1", &key.PublicKey)
		server := fake.AddMDMServer("go-apple-dm", nil)
		for _, s := range []string{"SER1", "SER2", "SER3"} {
			fake.AddOrgDevice(s, nil)
		}
		fake.AutoAdvance(10 * time.Millisecond)
		a := build(t, app.Config{Storage: "inmem", BootstrapToken: "t", AxM: app.AxMConfig{ClientID: "BUSINESSAPI.abc", KeyID: "kid-1", KeyPEM: keyPEM, BaseURL: fake.URL, TokenURL: fake.TokenURL, HTTPClient: fake.Client()}})
		srv := serve(t, a)
		if got := get(t, srv.URL+"/admin/v1/axm/servers", ""); got != http.StatusUnauthorized {
			t.Fatalf("axm without token = %d", got)
		}
		var servers axm.Page[axm.MDMServer]
		decode(t, do(t, srv, "GET", "/admin/v1/axm/servers", "t", nil), http.StatusOK, &servers)
		if len(servers.Items) != 1 || servers.Items[0].ID != server {
			t.Fatalf("servers = %+v", servers.Items)
		}
		var devices axm.Page[axm.OrgDevice]
		decode(t, do(t, srv, "GET", "/admin/v1/axm/devices?limit=2", "t", nil), http.StatusOK, &devices)
		if len(devices.Items) != 2 || devices.Meta.Paging.NextCursor == "" {
			t.Fatalf("devices page = %+v", devices)
		}

		account, e := a.Inventory.Account(t.Context(), "default")
		if e != nil {
			t.Fatal(e)
		}
		columns := axm.ActivityLogColumns{Serial: "serialNumber", Outcome: "status", Success: "SUCCESS"}
		config, _ := json.Marshal(map[string]any{"account_revision": account.Revision, "service_id": server, "log_columns": columns})
		var binding axmassignment.Binding
		decode(t, do(t, srv, "PUT", "/admin/v1/axm/accounts/default/assignment-service", "t", config), http.StatusOK, &binding)
		path := "/admin/v1/axm/accounts/default/assignments"
		big := bytes.Repeat([]byte("x"), app.MaxAdminBody+1)
		for _, route := range []string{"assignments", "unassignments"} {
			res := do(t, srv, "POST", "/admin/v1/axm/accounts/default/"+route, "t", big)
			if res.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("%s oversized = %d", route, res.StatusCode)
			}
			_ = res.Body.Close()
		}
		var w axmassignment.Workflow
		decode(t, do(t, srv, "POST", path, "t", []byte(`{"request_id":"assign","serials":["SER1","SER2"]}`)), http.StatusAccepted, &w)
		if w.State != "queued" || w.AcceptedAt != nil {
			t.Fatal(w)
		}
		if e = a.Assignments.Step(t.Context(), w.ID); e != nil {
			t.Fatal(e)
		}
		w, e = a.Assignments.Get(t.Context(), "default", w.ID)
		if e != nil || w.ActivityID == "" {
			t.Fatal(w, e)
		}
		var act axm.OrgDeviceActivity
		decode(t, do(t, srv, "GET", "/admin/v1/axm/activities/"+w.ActivityID, "t", nil), http.StatusOK, &act)
		fake.Complete()
		for _, serial := range []string{"SER1", "SER2"} {
			if fake.AssignedServer(serial) != server {
				t.Fatalf("%s assignment missing", serial)
			}
		}

		advance := func(id string) {
			t.Helper()
			if e := a.Inventory.Backend.Update(t.Context(), func(tx inventory.Tx) error {
				raw, e := tx.Get(t.Context(), "assignment/"+id)
				if e != nil {
					return e
				}
				var next axmassignment.Workflow
				if e = json.Unmarshal(raw, &next); e != nil {
					return e
				}
				next.NextAttempt = time.Time{}
				raw, e = json.Marshal(next)
				if e != nil {
					return e
				}
				return tx.Put(t.Context(), "assignment/"+id, raw)
			}); e != nil {
				t.Fatal(e)
			}
			if e := a.Assignments.Step(t.Context(), id); e != nil {
				t.Fatal(e)
			}
		}
		advance("assign")
		decode(t, do(t, srv, "GET", "/admin/v1/axm/accounts/default/assignment-service", "t", nil), http.StatusOK, &binding)
		resLog := do(t, srv, "GET", "/admin/v1/axm/accounts/default/assignments/assign/log", "t", nil)
		rawLog, e := io.ReadAll(resLog.Body)
		_ = resLog.Body.Close()
		if e != nil || resLog.StatusCode != 200 || !strings.Contains(string(rawLog), "SER1,ASSIGN_DEVICES,SUCCESS") {
			t.Fatal(string(rawLog), e, resLog.StatusCode)
		}
		decode(t, do(t, srv, "GET", "/admin/v1/axm/accounts/default/assignments/assign", "t", nil), http.StatusOK, &w)
		if w.State != "complete" || !w.LogSucceeded {
			t.Fatal(w)
		}
		decode(t, do(t, srv, "POST", "/admin/v1/axm/accounts/default/unassignments", "t", []byte(`{"request_id":"unassign","serials":["SER1"]}`)), http.StatusAccepted, &w)
		if e := a.Assignments.Step(t.Context(), w.ID); e != nil {
			t.Fatal(e)
		}
		fake.Complete()
		advance(w.ID)
		if fake.AssignedServer("SER1") != "" {
			t.Fatal("explicit scoped unassignment failed")
		}
		fake.ExpireTokens()
		fake.RejectNextTokenRequests(1)
		res := do(t, srv, "GET", "/admin/v1/axm/devices", "t", nil)
		if res.StatusCode < 400 {
			t.Fatal("outage hidden")
		}
		_ = res.Body.Close()
		for name, body := range map[string][]byte{"empty": []byte(`{"request_id":"invalid","serials":[]}`), "garbage": []byte(`{`), "missing_id": []byte(`{"serials":["SER9"]}`)} {
			res := do(t, srv, "POST", path, "t", body)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s=%d", name, res.StatusCode)
			}
			_ = res.Body.Close()
		}
		for _, route := range []string{"assign", "unassign"} {
			res := do(t, srv, "POST", "/admin/v1/axm/"+route, "t", []byte(`{"serials":["SER1"]}`))
			if res.StatusCode != http.StatusNotFound {
				t.Fatal("global mutation bypass remains", res.StatusCode)
			}
			_ = res.Body.Close()
		}
		res = do(t, srv, "GET", "/admin/v1/axm/activities/nope", "t", nil)
		if res.StatusCode != http.StatusNotFound {
			t.Fatal(res.StatusCode)
		}
		_ = res.Body.Close()

		fake.RateLimit(5, "1")
		if res := do(t, srv, "GET", "/admin/v1/axm/servers?limit=1", "t", nil); res.StatusCode != http.StatusOK && res.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("rate limited = %d", res.StatusCode)
		}
	})
}
