package app_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/webhook"
)

// collector records webhook deliveries.
type collector struct {
	mu     sync.Mutex
	bodies []string
	got    chan struct{}
}

// newCollector creates a webhook collector with a buffered delivery signal channel.
func newCollector() *collector { return &collector{got: make(chan struct{}, 64)} }

// webhookRoot writes the test receiver's certificate as a trusted webhook root PEM file.
func webhookRoot(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webhook-root.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// server starts a TLS webhook receiver that captures bodies and signals delivery.
func (c *collector) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, string(body))
		c.mu.Unlock()
		select {
		case c.got <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wait waits up to five seconds for a webhook delivery.
func (c *collector) wait(t *testing.T) {
	t.Helper()
	select {
	case <-c.got:
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook delivery arrived")
	}
}

// all joins all captured webhook bodies under the collector mutex.
func (c *collector) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.bodies, "\n")
}

// enrollAndCheckOut makes the server publish an event without needing a
// device: importing an enrollment publishes EnrollmentImported.
func publishSomething(t *testing.T, a *app.App) {
	t.Helper()
	err := a.Core.ImportEnrollment(context.Background(), storage.EnrollmentExport{
		Enrollment: storage.Enrollment{
			ID:      mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-SINK"},
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("ImportEnrollment: %v", err)
	}
}

// nativeWebhookConfig builds persistent native-webhook configuration trusting the test receiver
// and allowing loopback delivery.
func nativeWebhookConfig(t *testing.T, srv *httptest.Server) app.Config {
	t.Helper()
	return app.Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "webhooks.sqlite"), BootstrapToken: "t", StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, Webhooks: webhook.Config{Enabled: true, RootCAFile: webhookRoot(t, srv), PrivateNetworks: []string{"127.0.0.0/8"}}}
}

// createNativeSubscription creates a native subscription for enrollment-imported events and
// decodes its returned secret-bearing change.
func createNativeSubscription(t *testing.T, a *app.App, endpoint string) webhook.Change {
	t.Helper()
	b, err := json.Marshal(webhook.Spec{Name: "workflow", URL: endpoint, Events: []string{"server.enrollment.imported"}})
	if err != nil {
		t.Fatal(err)
	}
	w := eventRequest(a, "POST", "/webhooks", "t", string(b))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var change webhook.Change
	if err := json.Unmarshal(w.Body.Bytes(), &change); err != nil {
		t.Fatal(err)
	}
	return change
}

// TestWebhookSinkReceivesEnrollmentEvents checks webhook sink receives enrollment events.
func TestWebhookSinkReceivesEnrollmentEvents(t *testing.T) {
	c := newCollector()
	srv := c.server(t)
	a := build(t, nativeWebhookConfig(t, srv))
	createNativeSubscription(t, a, srv.URL)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() { cancel(); <-done }()
	publishSomething(t, a)
	c.wait(t)
	body := c.all()
	if !strings.Contains(body, `"type":"server.enrollment.imported"`) || strings.Contains(body, `"topic"`) {
		t.Fatal(body)
	}
}

// TestManagedWebhookRequiresEncryptedSQL checks that managed webhook requires encrypted SQL.
func TestManagedWebhookRequiresEncryptedSQL(t *testing.T) {
	for _, cfg := range []app.Config{
		{Storage: "inmem", BootstrapToken: "t", Webhooks: webhook.Config{Enabled: true}},
		{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "unencrypted.sqlite"), BootstrapToken: "t", Webhooks: webhook.Config{Enabled: true}},
	} {
		if a, err := app.Build(t.Context(), cfg); err == nil {
			_ = a.Close()
			t.Fatal("accepted unencrypted or ephemeral webhook store")
		}
	}
}

// Without a sink configured nothing subscribes and no bus is created, which
// is what every deployment before this change had.
func TestNoSinksMeansNoBus(t *testing.T) {
	a := build(t, app.Config{Storage: "inmem", Listen: ":0"})
	publishSomething(t, a)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// A webhook URL that cannot be used is a build error rather than a server
// that silently publishes nowhere.
func TestBadWebhookURLFailsBuild(t *testing.T) {
	_, err := app.Build(context.Background(), app.Config{
		Storage: "inmem", Listen: ":0", Logger: quiet,
		Sinks: app.SinkConfig{WebhookURL: "\x7f://bad"},
	})
	if err == nil {
		t.Fatal("Build accepted an unusable webhook URL")
	}
}
