package dmctl_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestInventoryCLIWorkflow follows Apple discovery through SQLite, permissions and every CLI family.
func TestInventoryCLIWorkflow(t *testing.T) {
	apple := axmtest.NewServer()
	t.Cleanup(apple.Close)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	apple.RegisterKey("BUSINESSAPI.workflow", "key", &key.PublicKey)
	apple.AddOrgDevice("opaque", map[string]any{"serialNumber": "SERIAL", "deviceModel": "Mac", "imei": []string{"123"}, "futurePrivate": "private-value"})
	apple.AddAppleCareCoverage("opaque", "plan", map[string]any{"agreementNumber": "AGREEMENT", "endDateTime": "2030-01-01T00:00:00Z"})
	cfg := app.Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "inventory.db"), BootstrapToken: "operator", StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AxM: app.AxMConfig{HTTPClient: apple.Client()}}
	a, err := app.Build(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	env := noConfig(t)
	env["DMCTL_SERVER"] = srv.URL
	env["DMCTL_TOKEN"] = "operator"
	env["DMCTL_OUTPUT"] = "json"
	token, _, err := run(t, env, "-output", "human", "auth", "bootstrap", "operator")
	if err != nil {
		t.Fatal(err)
	}
	env["DMCTL_TOKEN"] = strings.TrimSpace(token)
	req := httptest.NewRequestWithContext(t.Context(), "PUT", "/admin/v1/policies/inventory", strings.NewReader(`{"Source":"permit(principal == MDM::Principal::\"operator\",action,resource);"}`))
	req.Header.Set("Authorization", "Bearer "+env["DMCTL_TOKEN"])
	response := httptest.NewRecorder()
	a.Handler.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	call := func(args ...string) string {
		t.Helper()
		out, stderr, e := run(t, env, args...)
		if e != nil {
			t.Fatalf("%v: %v %s", args, e, stderr)
		}
		return out
	}
	input := struct {
		Account inventory.Account `json:"account"`
		Key     string            `json:"private_key_pem"`
	}{inventory.Account{Name: "Apple", ClientID: "BUSINESSAPI.workflow", KeyID: "key", BaseURL: apple.URL, TokenURL: apple.TokenURL, Enabled: true}, string(keyPEM)}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := runWithStdin(t, env, string(raw), "axm", "accounts", "create", "--file", "-")
	if err != nil {
		t.Fatal(err)
	}
	var account inventory.Account
	if err := json.Unmarshal([]byte(out), &account); err != nil || account.ID == "" {
		t.Fatal(out, err)
	}
	if strings.Contains(out, "PRIVATE KEY") {
		t.Fatal("credential in response")
	}
	call("axm", "accounts", "list")
	call("axm", "accounts", "get", account.ID)
	call("axm", "accounts", "verify", account.ID)
	input.Account = account
	input.Account.Name = "Renamed"
	input.Key = ""
	raw, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err = runWithStdin(t, env, string(raw), "axm", "accounts", "update", account.ID, "--file", "-")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &account); err != nil {
		t.Fatal(err)
	}
	call("inventory", "sync")
	var jobs []inventory.Job
	if err := json.Unmarshal([]byte(call("inventory", "jobs", "list")), &jobs); err != nil || len(jobs) == 0 {
		t.Fatal(err)
	}
	for _, j := range jobs {
		call("inventory", "jobs", "get", j.ID)
		call("inventory", "jobs", "pause", j.ID)
		call("inventory", "jobs", "resume", j.ID)
	}
	call("inventory", "jobs", "cancel-all")
	call("inventory", "schedules", "set", account.ID, "--every", "6", "--unit", "hours", "--zone", "Europe/London")
	call("inventory", "schedules", "set", account.ID, "--cron", "0 2 * * *", "--disabled")
	call("inventory", "schedules", "list")
	syncer := &inventory.Syncer{Repository: a.Inventory, Client: func(ctx context.Context, source inventory.Account, key []byte) (*axm.Client, error) {
		return axm.New(ctx, axm.Config{ClientID: source.ClientID, KeyID: source.KeyID, PrivateKeyPEM: key, BaseURL: source.BaseURL, TokenURL: source.TokenURL, HTTPClient: apple.Client()})
	}}
	workerCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- syncer.Run(workerCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	call("axm", "accounts", "sync", account.ID, "--wait")
	var page inventory.DevicePage
	if err := json.Unmarshal([]byte(call("devices", "list", "--account", account.ID, "--focus", "apple", "--where", `[{"field":"imei","operator":"contains","value":"123"}]`)), &page); err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	device := page.Items[0]
	if device.SerialNumber != "SERIAL" || len(device.Coverage) != 1 {
		t.Fatal(device)
	}
	for _, args := range [][]string{{"devices", "get", device.ID}, {"devices", "fields"}, {"devices", "fields", "--raw"}, {"devices", "collect", device.ID}, {"inventory", "reports", "--focus", "apple"}, {"inventory", "export", "--format", "csv", "--columns", "serial_number,imei"}, {"devices", "list", "--all"}, {"devices", "list", "--all", "--raw", "--output", "ndjson"}} {
		call(args...)
	}
	if out := call("devices", "get", device.ID, "--raw"); !strings.Contains(out, "private-value") {
		t.Fatal("raw evidence lost", out)
	}
	if out := call("inventory", "export", "--raw", "--format", "ndjson"); !strings.Contains(out, "private-value") {
		t.Fatal(out)
	}
	if out := call("inventory", "diagnostics"); strings.Contains(out, "SERIAL") || strings.Contains(out, "private-value") {
		t.Fatal("diagnostics leaked identity", out)
	}
	if _, _, err := runWithStdin(t, env, `{"name":"Warranty","format":"csv","columns":["serial_number","coverage_state"]}`, "inventory", "presets", "save", "warranty", "--file", "-"); err != nil {
		t.Fatal(err)
	}
	call("inventory", "presets")
	call("axm", "accounts", "delete", account.ID, "--revision", strconv.FormatInt(account.Revision, 10))
	persisted, err := a.Inventory.Device(t.Context(), device.ID)
	if err != nil || persisted.ID != device.ID {
		t.Fatal(persisted, err)
	}
	for _, o := range persisted.Sources {
		if !o.Disconnected {
			t.Fatal("deleted source still connected")
		}
	}
	// Reopening the same encrypted database must retain the record and its raw evidence.
	reopened, err := app.Build(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	persisted, err = reopened.Inventory.Device(t.Context(), device.ID)
	if err != nil || persisted.SerialNumber != "SERIAL" {
		t.Fatal(persisted, err)
	}
	if persisted.CoverageAt(time.Now()).State != "unknown" {
		t.Fatal("disconnected coverage projected as current")
	}
}
