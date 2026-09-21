package app_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/webhook"
)

// TestWebhookSensitiveAccessRequiresSeparateGrant checks that webhook sensitive access requires
// separate grant.
func TestWebhookSensitiveAccessRequiresSeparateGrant(t *testing.T) {
	receiver := newCollector().server(t)
	cfg := nativeWebhookConfig(t, receiver)
	st := inmem.New()
	cfg.AdminStore = st
	a := build(t, cfg)
	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	m, err := adminauth.New(st, reg)
	if err != nil {
		t.Fatal(err)
	}
	token := mintPrincipal(t, m, adminauth.Principal{Name: "webhook-delegate"})
	if _, err := m.PutPolicy(t.Context(), adminauth.Root, adminauth.Policy{Name: "webhook-actions", Source: `permit(principal, action in [MDM::Action::"readWebhooks", MDM::Action::"manageWebhooks", MDM::Action::"replayWebhooks", MDM::Action::"retryEvents"], resource);`}); err != nil {
		t.Fatal(err)
	}
	spec := `{"name":"sensitive","url":"https://receiver.example.test/hook","events":["protocol.*"],"payload":{"full_json":true}}`
	if w := eventRequest(a, "POST", "/webhooks", token, spec); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := eventRequest(a, "POST", "/webhooks", "t", spec)
	var c webhook.Change
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &c) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	base := "/webhooks/" + c.Subscription.ID
	for _, op := range []string{"pause", "resume", "disable", "enable", "credentials", "test"} {
		if w := eventRequest(a, "POST", base+"/"+op, token, "{}"); w.Code != 403 {
			t.Fatal(op, w.Code, w.Body.String())
		}
	}
	if w := eventRequest(a, "PUT", base, token, `{"revision":1,"spec":`+spec+`}`); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := eventRequest(a, "DELETE", base, token, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := eventRequest(a, "GET", base, token, ""); w.Code != 200 || strings.Contains(w.Body.String(), "whsec_") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := eventRequest(a, "POST", base+"/test", "t", ""); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = eventRequest(a, "GET", "/webhooks/deliveries?subscription_id="+c.Subscription.ID, "t", "")
	var ds []webhook.Delivery
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ds) != nil || len(ds) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := ds[0].ID
	if w := eventRequest(a, "POST", "/webhooks/deliveries/"+id+"/retry", token, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := eventRequest(a, "POST", "/events/"+id+"/retry", token, `{"destination":"native-webhook:`+c.Subscription.ID+`:1"}`); w.Code != 403 {
		t.Fatal("generic retry bypass", w.Code, w.Body.String())
	}
	if w := eventRequest(a, "POST", "/webhooks/replays", token, `{"subscription_id":"`+c.Subscription.ID+`","dry_run":true}`); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := m.PutPolicy(t.Context(), adminauth.Root, adminauth.Policy{Name: "sensitive-grant", Source: `permit(principal == MDM::Principal::"webhook-delegate", action == MDM::Action::"manageSensitiveWebhooks",resource);`}); err != nil {
		t.Fatal(err)
	}
	if w := eventRequest(a, "POST", base+"/pause", token, ""); w.Code != 200 {
		t.Fatal("explicit sensitive grant rejected", w.Code)
	}
	summary := strings.Replace(spec, `"full_json":true`, `"full_json":false`, 1)
	if w := eventRequest(a, "POST", "/webhooks", token, summary); w.Code != 201 {
		t.Fatal("summary delegation rejected", w.Code, w.Body.String())
	}
}

// TestWebhookObservesRejectedContentCacheWithoutCredentialLeak checks webhook observes rejected
// content cache without credential leak.
func TestWebhookObservesRejectedContentCacheWithoutCredentialLeak(t *testing.T) {
	receiver := newCollector().server(t)
	cfg := nativeWebhookConfig(t, receiver)
	cfg.ContentCache.PublicURL = "https://mdm.example.test"
	a := build(t, cfg)
	spec := `{"name":"cache","url":"https://receiver.example.test/hook","events":["protocol.content_cache.exchange"]}`
	if w := eventRequest(a, "POST", "/webhooks", "t", spec); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/content-cache/metrics/private-token", "/content-cache/metrics/private-token/bad"} {
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(`{}`)))
		if w.Code < 400 {
			t.Fatal("untrusted report accepted")
		}
	}
	w := eventRequest(a, "GET", "/webhooks/deliveries", "t", "")
	var ds []webhook.Delivery
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ds) != nil || len(ds) != 2 || strings.Contains(w.Body.String(), "private-token") {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestWebhookEnvironment checks native webhook environment configuration.
func TestWebhookEnvironment(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		bad        bool
	}{
		{"DM_WEBHOOKS_ENABLED", "true", false},
		{"DM_WEBHOOKS_ENABLED", "false", false},
		{"DM_WEBHOOKS_ENABLED", "bad", true},
		{"DM_WEBHOOK_PAYLOAD_RETENTION", "24h", false},
		{"DM_WEBHOOK_PAYLOAD_RETENTION", "-1h", true},
		{"DM_WEBHOOK_METADATA_RETENTION", "bad", true},
		{"DM_WEBHOOK_MAX_BODY_BYTES", "2048", false},
		{"DM_WEBHOOK_MAX_BODY_BYTES", "bad", true},
		{"DM_WEBHOOK_MAX_BODY_BYTES", "-1", true},
		{"DM_WEBHOOK_PRIVATE_NETWORKS", "127.0.0.0/8, 10.0.0.0/8", false},
		{"DM_WEBHOOK_URL", "https://old.example.test", true},
		{"DM_WEBHOOK_HMAC_KEY", "legacy-key", true},
	} {
		cfg, err := app.ParseEnv(func(key string) string {
			if key == tc.key {
				return tc.value
			}
			switch key {
			case app.EnvStorage:
				return "sqlite"
			case app.EnvDSN:
				return "webhook-env-test.sqlite"
			case app.EnvStorageKeys:
				return "test"
			case app.EnvBootstrapToken:
				return "test-admin"
			}
			return ""
		})
		if tc.bad {
			if !errors.Is(err, app.ErrConfig) {
				t.Fatal(tc, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(tc, err)
		}
		if tc.key == "DM_WEBHOOK_PRIVATE_NETWORKS" && len(cfg.Webhooks.PrivateNetworks) != 2 {
			t.Fatal(cfg.Webhooks)
		}
	}
}

// TestWebhookSimulatorEnrollmentAndCommand checks webhook simulator enrollment and command.
func TestWebhookSimulatorEnrollmentAndCommand(t *testing.T) {
	type received struct {
		body    []byte
		headers http.Header
	}
	calls := make(chan received, 64)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		calls <- received{body, r.Header.Clone()}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	native := nativeWebhookConfig(t, receiver)
	f := newEnrollFixture(t, "", func(cfg *app.Config) {
		cfg.Storage, cfg.DSN, cfg.StorageKeys, cfg.Secrets = native.Storage, native.DSN, native.StorageKeys, native.Secrets
		cfg.BootstrapToken, cfg.Webhooks = native.BootstrapToken, native.Webhooks
	})
	spec, err := json.Marshal(webhook.Spec{Name: "simulator", URL: receiver.URL, Events: []string{"protocol.*", "server.enrolled", "server.token.updated", "server.command.result"}, Payload: webhook.PayloadPolicy{FullJSON: true, RawRequest: true, RawResponse: true}})
	if err != nil {
		t.Fatal(err)
	}
	w := eventRequest(f.app, "POST", "/webhooks", "t", string(spec))
	var c webhook.Change
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &c) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	d := f.device(t, "UDID-WEBHOOK-SIMULATOR", "Mac16,1")
	if err := d.ADEEnroll(t.Context(), f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
		t.Fatal(err)
	}
	cmd, err := mdm.NewCommand(&commands.ProfileList{}, mdm.WithUUID("webhook-command"))
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{ID: d.UDID, Channel: mdm.ChannelDevice}
	if _, err := f.app.Core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Connect(t.Context()); err != nil || len(got) != 1 || got[0].UUID != cmd.UUID {
		t.Fatal(got, err)
	}
	// Starting the worker after the exchanges also checks that captures are persisted.
	w = eventRequest(f.app, "GET", "/webhooks/deliveries?subscription_id="+c.Subscription.ID, "t", "")
	var deliveries []webhook.Delivery
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &deliveries) != nil || len(deliveries) < 7 {
		t.Fatal(w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.app.Run(ctx) }()
	defer func() { cancel(); <-done }()
	events := map[string][]webhook.Event{}
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for range deliveries {
		select {
		case call := <-calls:
			if err := webhook.Verify(c.Credentials.SigningSecret, call.headers, call.body, time.Now()); err != nil {
				t.Fatal(err)
			}
			var e webhook.Event
			if err := json.Unmarshal(call.body, &e); err != nil {
				t.Fatal(err)
			}
			events[e.Type] = append(events[e.Type], e)
		case <-timeout.C:
			t.Fatal("delivery did not drain", len(events), len(deliveries))
		}
	}
	for _, typ := range []string{"protocol.enrollment.exchange", "protocol.scep.exchange", "server.enrolled", "server.token.updated", "server.command.result"} {
		if len(events[typ]) == 0 {
			t.Fatal("missing production event", typ)
		}
	}
	var ack *webhook.Event
	for i := range events["protocol.mdm.exchange"] {
		e := &events["protocol.mdm.exchange"][i]
		if e.Data["device_status"] == "Acknowledged" {
			ack = e
		}
	}
	if ack == nil || ack.Subject == nil || ack.Subject.ID != d.UDID || ack.Data["command_type"] != "ProfileList" || ack.Data["outcome"] != "succeeded" {
		t.Fatal("missing accepted command response", ack)
	}
	result := events["server.command.result"][0]
	if ack.CorrelationID == "" || ack.CorrelationID != result.CorrelationID {
		t.Fatal("command result lost request correlation", ack, result)
	}
	var original string
	if err := json.Unmarshal(ack.Payloads["request_raw"].Value, &original); err != nil {
		t.Fatal(err)
	}
	body, err := base64.StdEncoding.DecodeString(original)
	if err != nil {
		t.Fatal(err)
	}
	response, err := mdm.DecodeResponse(body, "")
	if err != nil || response.CommandUUID != cmd.UUID || response.Status != mdm.StatusAcknowledged {
		t.Fatal("original device bytes not preserved", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(ack.Payloads["request_json"].Value, &decoded); err != nil || decoded["Status"] != "Acknowledged" {
		t.Fatal("decoded response absent", err)
	}
}
