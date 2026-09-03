package app_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/axm"
	"github.com/deploymenttheory/go-apple-dm/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/internal/app"
)

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

func TestAxM(t *testing.T) {
	t.Run("ConfiguredFromEnv", func(t *testing.T) {
		_, keyPEM := axmKey(t)
		keyFile := filepath.Join(t.TempDir(), "axm.pem")
		if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
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
		cfg.Role, cfg.Storage, cfg.AdminToken, cfg.Logger = app.RoleDDM, "inmem", "t", quiet
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
		app.AxMWaitInterval = 10 * time.Millisecond
		a := build(t, app.Config{Role: app.RoleDDM, Storage: "inmem", AdminToken: "t", AxM: app.AxMConfig{ClientID: "BUSINESSAPI.abc", KeyID: "kid-1", KeyPEM: keyPEM, BaseURL: fake.URL, TokenURL: fake.TokenURL, HTTPClient: fake.Client()}})
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
		// An oversized body is refused before it is parsed.
		big := bytes.Repeat([]byte("x"), app.MaxAdminBody+1)
		for _, route := range []string{"assign", "unassign"} {
			res := do(t, srv, "POST", "/admin/v1/axm/"+route, "t", big)
			if res.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("%s oversized = %d", route, res.StatusCode)
			}
		}
		var act axm.OrgDeviceActivity
		decode(t, do(t, srv, "POST", "/admin/v1/axm/assign", "t", []byte(`{"server":"`+server+`","serials":["SER1","SER2"],"wait":true}`)), http.StatusAccepted, &act)
		if act.Attributes.Status != axm.ActivityCompleted {
			t.Fatalf("activity = %+v", act.Attributes)
		}
		for _, s := range []string{"SER1", "SER2"} {
			if fake.AssignedServer(s) != server {
				t.Fatalf("%s assigned to %q", s, fake.AssignedServer(s))
			}
		}
		decode(t, do(t, srv, "GET", "/admin/v1/axm/activities/"+act.ID, "t", nil), http.StatusOK, &act)
		// A fault at Apple is relayed as a gateway error rather than
		// reported as an empty inventory.
		fake.ExpireTokens()
		fake.RejectNextTokenRequests(1)
		if res := do(t, srv, "GET", "/admin/v1/axm/devices", "t", nil); res.StatusCode < 400 {
			t.Fatalf("devices during an outage = %d", res.StatusCode)
		}
		decode(t, do(t, srv, "POST", "/admin/v1/axm/unassign", "t", []byte(`{"serials":["SER1"]}`)), http.StatusAccepted, &act)
		if act.Attributes.Status != axm.ActivityInProgress && act.Attributes.Status != axm.ActivityCompleted {
			t.Fatalf("unassign = %+v", act.Attributes)
		}
		for name, body := range map[string][]byte{"empty": []byte(`{"serials":[]}`), "garbage": []byte(`{`)} {
			if res := do(t, srv, "POST", "/admin/v1/axm/assign", "t", body); res.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s = %d", name, res.StatusCode)
			}
		}
		if res := do(t, srv, "POST", "/admin/v1/axm/assign", "t", []byte(`{"serials":["SER9"]}`)); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("assign without server = %d", res.StatusCode)
		}
		if res := do(t, srv, "GET", "/admin/v1/axm/activities/nope", "t", nil); res.StatusCode != http.StatusNotFound {
			t.Fatalf("unknown activity = %d", res.StatusCode)
		}
		app.AxMWaitTimeout = 50 * time.Millisecond
		fake.SetConsistencyLag(time.Hour)
		if res := do(t, srv, "POST", "/admin/v1/axm/assign", "t", []byte(`{"server":"`+server+`","serials":["SER3"],"wait":true}`)); res.StatusCode != http.StatusAccepted && res.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("waited assign = %d", res.StatusCode)
		}
		app.AxMWaitTimeout = 5 * time.Minute
		fake.RateLimit(5, "1")
		if res := do(t, srv, "GET", "/admin/v1/axm/servers?limit=1", "t", nil); res.StatusCode != http.StatusOK && res.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("rate limited = %d", res.StatusCode)
		}
	})
}
