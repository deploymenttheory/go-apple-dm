package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
	"github.com/deploymenttheory/go-apple-dm/server/axmwebhook"
)

// TestAXMWebhookAdministration exercises authorized provisioning and callback-to-refresh.
func TestAXMWebhookAdministration(t *testing.T) {
	remote := axmtest.NewServer()
	t.Cleanup(remote.Close)
	key := accountTestKey(t)
	parsed, err := axm.ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	remote.RegisterKey("BUSINESSAPI.webhooks", "kid", &parsed.PublicKey)
	a, err := Build(t.Context(), Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "server.db"), BootstrapToken: "bootstrap-secret", StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, AxM: AxMConfig{HTTPClient: remote.Client()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	account, err := a.Inventory.SaveAccount(t.Context(), inventory.Account{ID: "apple", Name: "Apple", ClientID: "BUSINESSAPI.webhooks", KeyID: "kid", Enabled: true, BaseURL: remote.URL, TokenURL: remote.TokenURL}, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	root := bootstrapRBAC(t, a)
	if w := rbacRequest(a, "GET", "/axm/accounts/apple/webhooks", root, ""); w.Code != 403 {
		t.Fatal("missing grant accepted", w.Code)
	}
	policy := `{"Source":"permit(principal, action in [MDM::Action::\"readBusinessManager\",MDM::Action::\"manageAxMAccounts\"], resource);"}`
	if w := rbacRequest(a, "PUT", "/policies/webhooks", root, policy); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	receiver := httptest.NewTLSServer(a.Handler)
	t.Cleanup(receiver.Close)
	body, _ := json.Marshal(map[string]any{"name": "lab", "enabled": true, "event_types": []string{axm.EventOrgDeviceAttributeChanged}, "public_url": receiver.URL})
	w := rbacRequest(a, "POST", "/axm/accounts/apple/webhooks", root, string(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var b axmwebhook.Binding
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.State != "active" {
		t.Fatal(b, err)
	}
	base := "/axm/accounts/apple/webhooks/" + b.ID
	for _, path := range []string{"/axm/accounts/apple/webhooks", base, "/axm/accounts/apple/webhook-bindings", "/axm/accounts/apple/webhook-bindings/" + b.ID} {
		if w := rbacRequest(a, "GET", path, root, ""); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := rbacRequest(a, "GET", "/axm/accounts/apple/webhook-bindings/"+b.ID+"/receipts/raw", root, ""); w.Code != 403 {
		t.Fatal("raw payload grant bypassed", w.Code)
	}
	if w := rbacRequest(a, "POST", base+"/ping", root, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	client, err := a.inventoryClient(t.Context(), account, key)
	if err != nil {
		t.Fatal(err)
	}
	query := axm.WebhookDeliveryQuery{Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour), IncludeEvent: true}
	page, err := client.ListWebhookDeliveries(t.Context(), b.RemoteID, query)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	if err := remote.DeliverWebhook(t.Context(), page.Items[0].ID, receiver.Client()); err != nil {
		t.Fatal(err)
	}
	dateQuery := "?start=" + query.Start.UTC().Format(time.RFC3339Nano) + "&end=" + query.End.UTC().Format(time.RFC3339Nano)
	if w := rbacRequest(a, "GET", base+"/deliveries"+dateQuery, root, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "GET", base+"/deliveries"+dateQuery+"&raw=true", root, ""); w.Code != 403 {
		t.Fatal("raw delivery grant bypassed", w.Code)
	}
	policy = `{"Source":"permit(principal, action in [MDM::Action::\"readBusinessManager\",MDM::Action::\"manageAxMAccounts\",MDM::Action::\"readRawInventory\"], resource);"}`
	if w := rbacRequest(a, "PUT", "/policies/webhooks", root, policy); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{base + "/deliveries" + dateQuery + "&raw=true", "/axm/accounts/apple/webhook-bindings/" + b.ID + "/receipts", "/axm/accounts/apple/webhook-bindings/" + b.ID + "/receipts/raw"} {
		if w := rbacRequest(a, "GET", path, root, ""); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	replayBody, _ := json.Marshal(map[string]any{"template_id": page.Items[0].ID, "start": query.Start, "end": query.End})
	if w := rbacRequest(a, "POST", base+"/redeliver", root, string(replayBody)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	reconcileBody, _ := json.Marshal(map[string]string{"remote_id": b.RemoteID})
	if w := rbacRequest(a, "POST", "/axm/accounts/apple/webhook-bindings/"+b.ID+"/reconcile", root, string(reconcileBody)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var errReload error
	b, errReload = a.AXMWebhooks.Binding(t.Context(), account.ID, b.ID)
	if errReload != nil {
		t.Fatal(errReload)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/axm/accounts/apple/webhooks", "invalid", 400},
		{"POST", "/axm/accounts/apple/webhooks", `{"name":""}`, 400},
		{"POST", "/axm/accounts/apple/webhooks", `{"name":"invalid","event_types":["ORG_DEVICE_ATTRIBUTE_CHANGED"],"public_url":"http://invalid"}`, 400},
		{"GET", base + "/deliveries", "", 400},
		{"POST", "/axm/accounts/missing/webhooks", string(body), 404},
		{"POST", "/axm/accounts/apple/webhook-bindings/missing/reconcile", `{"remote_id":"missing"}`, 404},
		{"POST", base + "/redeliver", `{"template_id":"absent"}`, 400},
		{"POST", base + "/redeliver", "invalid", 400},
		{"POST", base + "/redeliver", `{"template_id":"absent","start":"2026-01-01T00:00:00Z","end":"2027-01-01T00:00:00Z"}`, 404},
		{"POST", base + "/rotate", `{"overlap_seconds":-1}`, 400},
		{"POST", base + "/rotate", `{"overlap_seconds":86401}`, 400},
		{"POST", base + "/rotate", "invalid", 400},
		{"PATCH", base, "invalid", 400},
		{"PATCH", base, `{"secret":"forbidden"}`, 400},
		{"GET", "/axm/accounts/apple/webhooks/missing", "", 404},
		{"POST", "/axm/accounts/apple/webhook-bindings/" + b.ID + "/reconcile", "invalid", 400},
	} {
		if w := rbacRequest(a, tc.method, tc.path, root, tc.body); w.Code != tc.status {
			t.Fatal(tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	signatureKey, err := a.AXMWebhooks.SigningSecret(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, body string
		sign     bool
		status   int
	}{
		{"missing", "{}", false, 404}, {b.ID, "{}", false, 401}, {b.ID, "invalid", true, 400},
	} {
		req := httptest.NewRequestWithContext(t.Context(), "POST", axmwebhook.CallbackPath+tc.id, strings.NewReader(tc.body))
		if tc.sign {
			m := hmac.New(sha256.New, signatureKey)
			_, _ = m.Write([]byte(tc.body))
			req.Header.Set("x-apple-signature", "hmacsha256="+hex.EncodeToString(m.Sum(nil)))
		}
		response := httptest.NewRecorder()
		a.Handler.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatal("invalid callback", response.Code, response.Body.String())
		}
	}
	remote.ServerError(1)
	if w := rbacRequest(a, "POST", "/axm/accounts/apple/webhooks", root, string(body)); w.Code != 502 {
		t.Fatal("uncertain creation was not reported", w.Code, w.Body.String())
	}
	bindings, err := a.AXMWebhooks.Bindings(t.Context(), account.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var pending axmwebhook.Binding
	for _, candidate := range bindings {
		if candidate.State == "uncertain" {
			pending = candidate
		}
	}
	if pending.ID == "" {
		t.Fatal("lost provisioning intent")
	}
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/axm/accounts/apple/webhooks/" + pending.ID + "/ping", "", 409},
		{"/axm/accounts/apple/webhook-bindings/" + pending.ID + "/reconcile", `{"remote_id":"missing"}`, 404},
		{"/axm/accounts/apple/webhook-bindings/" + pending.ID + "/reconcile", string(reconcileBody), 409},
	} {
		if w := rbacRequest(a, "POST", tc.path, root, tc.body); w.Code != tc.status {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	pendingSecret, err := a.AXMWebhooks.SigningSecret(t.Context(), pending)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := client.CreateWebhook(t.Context(), axm.WebhookCreateAttributes{Name: "recovered", Enabled: true, URL: pending.CallbackURL, Secret: string(pendingSecret), EventTypes: []string{axm.EventOrgDeviceAttributeChanged}})
	if err != nil {
		t.Fatal(err)
	}
	recoveryBody, _ := json.Marshal(map[string]string{"remote_id": recovered.ID})
	if w := rbacRequest(a, "POST", "/axm/accounts/apple/webhook-bindings/"+pending.ID+"/reconcile", root, string(recoveryBody)); w.Code != 200 {
		t.Fatal("reconciliation failed", w.Code, w.Body.String())
	}
	if err := client.DeleteWebhook(t.Context(), recovered.ID); err != nil {
		t.Fatal(err)
	}

	remote.AddOrgDevice("opaque", map[string]any{"serialNumber": "SER", "future": false})
	secret, err := a.AXMWebhooks.SigningSecret(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	callback := `{"data":{"id":"device-change","type":"orgDeviceAttributeChanged","version":1,"relationships":{"instance":{"data":{"type":"orgDevices","id":"opaque"}}}}}`
	req := httptest.NewRequestWithContext(t.Context(), "POST", axmwebhook.CallbackPath+b.ID, strings.NewReader(callback))
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(callback))
	req.Header.Set("x-apple-signature", "hmacsha256="+hex.EncodeToString(mac.Sum(nil)))
	result := httptest.NewRecorder()
	a.Handler.ServeHTTP(result, req)
	if result.Code != 204 {
		t.Fatal("callback rejected", result.Code, result.Body.String())
	}
	syncer := inventory.Syncer{Repository: a.Inventory, Client: a.inventoryClient}
	job, err := syncer.RunOne(t.Context())
	if err != nil || job.State != "success" {
		t.Fatal(job, err)
	}
	observed, err := a.Inventory.SourceResource(t.Context(), source.Reference{Kind: "axm.detail", AccountID: account.ID, ResourceID: "opaque"})
	if err != nil || string(observed.Snapshot.Fields["attributes.future"]) != "false" {
		t.Fatal("no fresh evidence", err)
	}
	if w := rbacRequest(a, "PATCH", base, root, `{"enabled":false}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "PATCH", base, root, `{"enabled":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "POST", base+"/rotate", root, `{"overlap_seconds":60}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "DELETE", base, root, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := client.GetWebhook(t.Context(), b.RemoteID, axm.GetOptions{}); !axm.IsNotFound(err) {
		t.Fatal("remote cleanup failed", err)
	}
	a.cfg.Enroll.PublicURL = receiver.URL
	disabledBody, _ := json.Marshal(map[string]any{"name": "disabled", "enabled": false, "event_types": []string{axm.EventOrgDeviceAttributeChanged}})
	disabledResponse := rbacRequest(a, "POST", "/axm/accounts/apple/webhooks", root, string(disabledBody))
	var disabled axmwebhook.Binding
	if disabledResponse.Code != 200 || json.Unmarshal(disabledResponse.Body.Bytes(), &disabled) != nil || disabled.State != "disabled" {
		t.Fatal("disabled provisioning failed", disabledResponse.Code, disabledResponse.Body.String())
	}
	disabledReconcile, _ := json.Marshal(map[string]string{"remote_id": disabled.RemoteID})
	for _, tc := range []struct{ path, body string }{
		{"/axm/accounts/apple/webhook-bindings/" + disabled.ID + "/reconcile", string(disabledReconcile)},
		{"/axm/accounts/apple/webhooks/" + disabled.ID + "/rotate", `{"overlap_seconds":0}`},
	} {
		w := rbacRequest(a, "POST", tc.path, root, tc.body)
		var updated axmwebhook.Binding
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &updated) != nil || updated.State != "disabled" {
			t.Fatal("disabled subscription became active", tc.path, w.Code, w.Body.String())
		}
	}
	if w := rbacRequest(a, "DELETE", "/axm/accounts/apple/webhooks/"+disabled.ID, root, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	before := len(remote.Requests())
	if _, err := a.Inventory.SaveAccount(t.Context(), account, key, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if w := rbacRequest(a, "POST", base+"/ping", root, ""); w.Code != 409 {
		t.Fatal("stale binding reached remote account", w.Code)
	}
	if w := rbacRequest(a, "POST", "/axm/accounts/apple/webhook-bindings/"+b.ID+"/reconcile", root, string(reconcileBody)); w.Code != 409 {
		t.Fatal("stale binding reconciled against replaced account", w.Code, w.Body.String())
	}
	if len(remote.Requests()) != before {
		t.Fatal("stale binding contacted Apple")
	}
	baseTransport := remote.Client().Transport
	remote.Client().Transport = setupRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/v1/webhooks" {
			return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"errors":[{"status":"403","code":"FORBIDDEN"}]}`)), Request: req}, nil
		}
		return baseTransport.RoundTrip(req)
	})
	if w := rbacRequest(a, "GET", "/axm/accounts/apple/webhooks", root, ""); w.Code != 502 || !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatal("remote permission failure lost", w.Code, w.Body.String())
	}
	remote.Client().Transport = setupRoundTrip(func(req *http.Request) (*http.Response, error) {
		response, err := baseTransport.RoundTrip(req)
		if req.URL.Path == "/v1/webhooks" && err == nil {
			current, loadErr := a.Inventory.Account(t.Context(), account.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if _, saveErr := a.Inventory.SaveAccount(t.Context(), current, key, time.Now().UTC()); saveErr != nil {
				t.Fatal(saveErr)
			}
		}
		return response, err
	})
	if w := rbacRequest(a, "GET", "/axm/accounts/apple/webhooks", root, ""); w.Code != 409 {
		t.Fatal("stale remote response accepted", w.Code, w.Body.String())
	}
	remote.Client().Transport = baseTransport
}

// TestAXMWebhookInterruptedAdministration preserves provisioning intent when credentials
// or signing keys change across remote calls and when Apple rejects a secret update.
func TestAXMWebhookInterruptedAdministration(t *testing.T) {
	for _, tc := range []struct {
		name, operation, boundary string
		reject, replace, rotate   bool
		status                    int
	}{
		{"reconcile/account_changed_after_read", "reconcile", "GET", false, true, false, 409},
		{"reconcile/account_changed_after_update", "reconcile", "PATCH", false, true, false, 409},
		{"reconcile/signing_key_changed_after_read", "reconcile", "GET", false, false, true, 409},
		{"reconcile/secret_update_rejected", "reconcile", "PATCH", true, false, false, 502},
		{"create/account_changed_after_create", "create", "POST", false, true, false, 409},
		{"create/rejected_after_account_change", "create", "POST", true, true, false, 502},
		{"get/account_changed_after_read", "get", "GET", false, true, false, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := axmtest.NewServer()
			t.Cleanup(remote.Close)
			key := accountTestKey(t)
			parsed, err := axm.ParseKey(key)
			if err != nil {
				t.Fatal(err)
			}
			remote.RegisterKey("BUSINESSAPI.webhooks", "kid", &parsed.PublicKey)
			a, err := Build(t.Context(), Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "server.db"), BootstrapToken: "bootstrap-secret", StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, AxM: AxMConfig{HTTPClient: remote.Client()}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			account, err := a.Inventory.SaveAccount(t.Context(), inventory.Account{ID: "apple", Name: "Apple", ClientID: "BUSINESSAPI.webhooks", KeyID: "kid", Enabled: true, BaseURL: remote.URL, TokenURL: remote.TokenURL}, key, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			root := bootstrapRBAC(t, a)
			policy := `{"Source":"permit(principal, action in [MDM::Action::\"readBusinessManager\",MDM::Action::\"manageAxMAccounts\"], resource);"}`
			if w := rbacRequest(a, "PUT", "/policies/webhooks", root, policy); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			create := `{"name":"test","enabled":true,"event_types":["ORG_DEVICE_ATTRIBUTE_CHANGED"],"public_url":"https://mdm.example"}`
			created := rbacRequest(a, "POST", "/axm/accounts/apple/webhooks", root, create)
			var binding axmwebhook.Binding
			if created.Code != 200 || json.Unmarshal(created.Body.Bytes(), &binding) != nil {
				t.Fatal(created.Code, created.Body.String())
			}
			binding, err = a.AXMWebhooks.SetState(t.Context(), binding, "uncertain", binding.RemoteID)
			if err != nil {
				t.Fatal(err)
			}
			base := remote.Client().Transport
			boundaries := 0
			remote.Client().Transport = setupRoundTrip(func(req *http.Request) (*http.Response, error) {
				if req.Method != tc.boundary || !strings.HasPrefix(req.URL.Path, "/v1/webhooks") {
					return base.RoundTrip(req)
				}
				boundaries++
				var response *http.Response
				var err error
				if tc.reject {
					response = &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"errors":[{"status":"403","code":"PRIVATE_DIAGNOSTIC"}]}`)), Request: req}
				} else {
					response, err = base.RoundTrip(req)
				}
				if err != nil {
					return response, err
				}
				if tc.replace {
					if _, err := a.Inventory.SaveAccount(req.Context(), account, key, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				if tc.rotate {
					if _, err := a.AXMWebhooks.Rotate(req.Context(), binding, 0); err != nil {
						t.Fatal(err)
					}
				}
				return response, nil
			})
			method, path, body := "POST", "/axm/accounts/apple/webhook-bindings/"+binding.ID+"/reconcile", `{"remote_id":"`+binding.RemoteID+`"}`
			switch tc.operation {
			case "create":
				path, body = "/axm/accounts/apple/webhooks", create
			case "get":
				method, path, body = "GET", "/axm/accounts/apple/webhooks/"+binding.ID, ""
			}
			w := rbacRequest(a, method, path, root, body)
			if w.Code != tc.status || boundaries != 1 || strings.Contains(w.Body.String(), "PRIVATE_DIAGNOSTIC") {
				t.Fatal("unsafe remote result", w.Code, boundaries, w.Body.String())
			}
			bindings, err := a.AXMWebhooks.Bindings(t.Context(), account.ID, "")
			wantBindings := 1
			if tc.operation == "create" {
				wantBindings = 2
			}
			if err != nil || len(bindings) != wantBindings {
				t.Fatal("provisioning intent lost", len(bindings), err)
			}
			for _, current := range bindings {
				if current.State == "active" {
					t.Fatal("interrupted operation activated subscription")
				}
			}
		})
	}
}
