package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
)

// TestNamedAccountDiagnosticsReincarnation exercises actual synchronization and
// authorized HTTP administration. Old successful evidence remains inspectable but
// cannot complete a recreated connection that happens to have the same revision.
func TestNamedAccountDiagnosticsReincarnation(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			transport := setupRoundTrip(func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				body := `{"data":[]}`
				if r.URL.Path == "/token" {
					body = `{"access_token":"fixture","token_type":"Bearer","expires_in":3600}`
				}
				_, _ = w.WriteString(body)
				response := w.Result()
				response.Request = r
				return response, nil
			})
			cfg := Config{Storage: backend, BootstrapToken: "bootstrap-secret", AxM: AxMConfig{HTTPClient: &http.Client{Transport: transport}}}
			if backend == "sqlite" {
				cfg.DSN = filepath.Join(t.TempDir(), "diagnostics.sqlite")
				cfg.StorageKeys = []string{"test"}
				cfg.Secrets = secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}
			}
			a, err := Build(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			token := bootstrapRBAC(t, a)
			policy := `{"Source":"permit(principal, action in [MDM::Action::\"readBusinessManager\",MDM::Action::\"readInventory\",MDM::Action::\"manageAxMAccounts\",MDM::Action::\"manageInventory\"], resource);"}`
			if w := rbacRequest(a, "PUT", "/policies/incarnation", token, policy); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			save := func(revision int64, enabled bool, key []byte) inventory.Account {
				t.Helper()
				// #nosec G101 -- URLs belong to the supplied in-process transport; the credential is generated for this incarnation.
				account := inventory.Account{ID: "recreated", Name: "Recreated", ClientID: "BUSINESSAPI.recreated", KeyID: "kid", Enabled: enabled, Revision: revision, BaseURL: "https://fixture.example", TokenURL: "https://fixture.example/token"}
				body, e := json.Marshal(map[string]any{"account": account, "private_key_pem": string(key)})
				if e != nil {
					t.Fatal(e)
				}
				w := rbacRequest(a, "PUT", "/axm/accounts/recreated", token, string(body))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				if e = json.Unmarshal(w.Body.Bytes(), &account); e != nil {
					t.Fatal(e)
				}
				return account
			}
			diagnostics := func(want string) {
				t.Helper()
				w := rbacRequest(a, "GET", "/axm/accounts/recreated/diagnostics", token, "")
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"synchronization":"`+want+`"`) || !strings.Contains(w.Body.String(), `"inventory":"empty"`) {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			syncer := inventory.Syncer{Repository: a.Inventory, Client: a.inventoryClient}
			synchronize := func() inventory.Job {
				t.Helper()
				w := rbacRequest(a, "POST", "/axm/accounts/recreated/sync", token, "")
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var queued inventory.Job
				if e := json.Unmarshal(w.Body.Bytes(), &queued); e != nil {
					t.Fatal(e)
				}
				job, e := syncer.RunOne(t.Context())
				if e != nil || job.ID != queued.ID || job.State != "success" {
					t.Fatal("actual sync failed", job, e)
				}
				return job
			}
			key := accountTestKey(t)
			save(0, false, key)
			original := save(1, true, key)
			oldJob := synchronize()
			diagnostics("complete")
			if w := rbacRequest(a, "DELETE", "/axm/accounts/recreated?revision=2", token, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			key = accountTestKey(t)
			save(0, false, key)
			replacement := save(1, true, key)
			if replacement.Revision != original.Revision || !replacement.CreatedAt.After(oldJob.CreatedAt) {
				t.Fatal("fixture did not reproduce reused revision and retained earlier job", original, replacement, oldJob)
			}
			diagnostics("incomplete")
			newJob := synchronize()
			if newJob.ID == oldJob.ID {
				t.Fatal("new incarnation reused historical job")
			}
			diagnostics("complete")
			w := rbacRequest(a, "GET", "/axm/accounts/recreated/jobs/"+oldJob.ID, token, "")
			var retained inventory.Job
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &retained) != nil || retained.ID != oldJob.ID || retained.State != "success" {
				t.Fatal("successful history changed", w.Code, w.Body.String())
			}
		})
	}
}
