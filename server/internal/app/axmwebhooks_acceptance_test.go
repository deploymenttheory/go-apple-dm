//go:build acceptance

package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/axmwebhook"
)

type liveResponse struct {
	http.ResponseWriter
	status int
}

// WriteHeader records successful authenticated callback acknowledgement.
func (w *liveResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// TestAcceptanceAXMWebhooks exercises the independent client and actual SQL-backed receiver.
func TestAcceptanceAXMWebhooks(t *testing.T) {
	if os.Getenv("AXM_WEBHOOK_E2E") != "true" {
		t.Skip("set AXM_WEBHOOK_E2E=true for isolated live Apple webhook acceptance")
	}
	binary := os.Getenv("CLOUDFLARED_BIN")
	if binary == "" {
		t.Fatal("CLOUDFLARED_BIN required")
	}
	key := []byte(os.Getenv("APPLE_PRIVATE_KEY_PEM"))
	if len(key) == 0 {
		var err error
		key, err = os.ReadFile(os.Getenv("APPLE_PRIVATE_KEY_PATH")) // #nosec G703 -- The opt-in lab operator selects this private key file; it is not a request path.
		if err != nil {
			t.Fatal("private key configuration unavailable")
		}
	}
	signals, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signals, 8*time.Minute)
	defer cancel()
	storageKey := make([]byte, 32)
	if _, err := rand.Read(storageKey); err != nil {
		t.Fatal("temporary storage key unavailable")
	}
	a, err := Build(ctx, Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "lab.db"), StorageKeys: []string{"lab"}, Secrets: secrets.Static{"lab": storageKey}})
	if err != nil {
		t.Fatal("local encrypted server configuration failed")
	}
	t.Cleanup(func() { _ = a.Close() })
	account, err := a.Inventory.SaveAccount(ctx, inventory.Account{Name: "Webhook lab", ClientID: os.Getenv("APPLE_ISSUER_ID"), KeyID: os.Getenv("APPLE_KEY_ID"), Enabled: true}, key, time.Now().UTC())
	if err != nil {
		t.Fatal("lab account configuration failed")
	}
	client, err := a.inventoryClient(ctx, account, key)
	if err != nil {
		t.Fatal("lab client configuration failed")
	}
	if _, err := client.ListWebhooks(ctx, axm.ListOptions{Limit: 1}); err != nil {
		t.Fatal("webhook permission/authentication preflight failed")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var receipts atomic.Int64
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture := &liveResponse{ResponseWriter: w, status: 200}
		a.Handler.ServeHTTP(capture, r)
		if strings.HasPrefix(r.URL.Path, axmwebhook.CallbackPath) && capture.status == 204 {
			receipts.Add(1)
		}
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(cleanup)
		<-done
	})
	origin := liveTunnel(t, ctx, binary, "http://"+listener.Addr().String())
	b, err := a.AXMWebhooks.Prepare(ctx, account.ID, origin)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := a.AXMWebhooks.SigningSecret(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute)
	created, err := client.CreateWebhook(ctx, axm.WebhookCreateAttributes{Name: "go-apple-dm-" + rand.Text()[:10], Enabled: true, EventTypes: []string{axm.EventOrgDeviceAttributeChanged}, URL: b.CallbackURL, Secret: string(secret)})
	if err != nil {
		t.Fatal("CreateWebhook failed")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.DeleteWebhook(cleanup, created.ID); err != nil && !axm.IsNotFound(err) {
			t.Error("test webhook cleanup failed")
		}
		if _, err := client.GetWebhook(cleanup, created.ID, axm.GetOptions{}); !axm.IsNotFound(err) {
			t.Error("cleanup GET404 unconfirmed")
		} else {
			t.Log("Cleanup: GET404 confirmed")
		}
	})
	b, err = a.AXMWebhooks.SetState(ctx, b, "active", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("CreateWebhook: 201")
	first, err := client.ListWebhooks(ctx, axm.ListOptions{Limit: 1})
	if err != nil {
		t.Fatal("ListWebhooks failed")
	}
	all, err := axm.All(ctx, client, first)
	if err != nil {
		t.Fatal("webhook pagination failed")
	}
	found := false
	for _, item := range all {
		found = found || item.ID == created.ID
	}
	if !found {
		t.Fatal("created webhook not listed")
	}
	t.Log("ListWebhooks: 200; created resource found")
	if _, err := client.GetWebhook(ctx, created.ID, axm.GetOptions{}); err != nil {
		t.Fatal("GetWebhook failed")
	}
	t.Log("GetWebhook: 200")
	for _, enabled := range []bool{false, true} {
		updated, err := client.UpdateWebhook(ctx, created.ID, axm.WebhookUpdateAttributes{Enabled: &enabled})
		if err != nil || updated.Attributes.Enabled != enabled {
			t.Fatal("UpdateWebhook explicit boolean failed")
		}
	}
	t.Log("UpdateWebhook: 200; disabled and re-enabled")
	ping, err := client.CreateWebhookPing(ctx, created.ID)
	if err != nil {
		t.Fatal("CreateWebhookPing failed")
	}
	phase, stopPhase := context.WithTimeout(ctx, 90*time.Second)
	defer stopPhase()
	liveWait(t, phase, func() bool { return receipts.Load() >= 1 })
	retained, err := a.AXMWebhooks.Receipts(ctx, account.ID, b.ID, "", true)
	if err != nil || len(retained) != 1 {
		t.Fatal("signed receipt was not retained")
	}
	var notification axm.WebhookNotification
	if json.Unmarshal(retained[0].Body, &notification) != nil || notification.Data.ID != ping.ID {
		t.Fatal("signed callback does not match created ping")
	}
	t.Log("CreateWebhookPing: 201; signed callback retained in encrypted SQL")
	var original axm.WebhookDelivery
	liveWait(t, phase, func() bool {
		page, err := client.ListWebhookDeliveries(phase, created.ID, axm.WebhookDeliveryQuery{Start: start, End: time.Now().UTC(), IncludeEvent: true, EventFields: []string{"payload", "isPingEvent"}})
		if err != nil {
			return false
		}
		for _, included := range page.Included {
			var attrs axm.WebhookEventAttributes
			var stored axm.WebhookNotification
			if json.Unmarshal(included.Attributes, &attrs) != nil || !attrs.IsPingEvent || json.Unmarshal([]byte(attrs.Payload), &stored) != nil {
				continue
			}
			matches := stored.Data.ID == notification.Data.ID
			if stored.Data.ID == "" {
				matches = !stored.Data.Attributes.CreatedDateTime.IsZero() && stored.Data.Attributes.CreatedDateTime.Equal(notification.Data.Attributes.CreatedDateTime)
			}
			if !matches || stored.Data.Type != notification.Data.Type {
				continue
			}
			for _, d := range page.Items {
				var link axm.Linkage
				if json.Unmarshal(d.Relationships["event"].Data, &link) == nil && link.ID == included.ID && d.Attributes.DeliveryState == "SUCCEEDED" {
					original = d
					return true
				}
			}
		}
		return false
	})
	t.Log("ListWebhookDeliveries: 200; ping correlated and SUCCEEDED")
	before := receipts.Load()
	replay, err := client.CreateWebhookDelivery(ctx, original.ID)
	if err != nil {
		t.Fatal("CreateWebhookDelivery failed")
	}
	replayCtx, stopReplay := context.WithTimeout(ctx, 90*time.Second)
	defer stopReplay()
	liveWait(t, replayCtx, func() bool { return receipts.Load() > before })
	liveWait(t, replayCtx, func() bool {
		page, err := client.ListWebhookDeliveries(replayCtx, created.ID, axm.WebhookDeliveryQuery{Start: start, End: time.Now().UTC(), IncludeEvent: true})
		if err != nil {
			return false
		}
		for _, d := range page.Items {
			if d.ID == replay.ID && d.Attributes.Redelivery && d.Attributes.DeliveryState == "SUCCEEDED" && liveSameEvent(d, original) {
				return true
			}
		}
		return false
	})
	t.Log("CreateWebhookDelivery: 201; signed redelivery acknowledged and SUCCEEDED")
	if err := client.DeleteWebhook(ctx, created.ID); err != nil {
		t.Fatal("DeleteWebhook failed")
	}
	t.Log("DeleteWebhook: 204")
}

// liveWait bounds asynchronous observations; its callback must avoid private logs.
func liveWait(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	for {
		if ready() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("live webhook observation deadline exceeded")
		case <-time.After(2 * time.Second):
		}
	}
}

// liveTunnel waits for registration and public DNS publication before system DNS.
func liveTunnel(t *testing.T, ctx context.Context, binary, origin string) string {
	t.Helper()
	processCtx, stop := context.WithCancel(ctx)
	// #nosec G204 G702 -- This opt-in test explicitly runs the operator-selected tunnel binary with fixed arguments and a loopback origin.
	command := exec.CommandContext(processCtx, binary, "tunnel", "--no-autoupdate", "--protocol", "http2", "--edge-ip-version", "4", "--url", origin)
	pipe, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal("temporary tunnel failed to start")
	}
	lines := make(chan string, 32)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-processCtx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() { stop(); _ = command.Wait() })
	startup, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	pattern := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	public := ""
	registered := false
	for public == "" || !registered {
		select {
		case <-startup.Done():
			t.Fatal("tunnel registration deadline exceeded")
		case line, ok := <-lines:
			if !ok {
				t.Fatal("temporary tunnel exited")
			}
			if url := pattern.FindString(line); url != "" {
				public = url
			}
			registered = registered || strings.Contains(line, "Registered tunnel connection")
		}
	}
	go func() {
		for range lines {
		}
	}()
	select {
	case <-startup.Done():
		t.Fatal("tunnel DNS deadline exceeded")
	case <-time.After(5 * time.Second):
	}
	parsed, err := url.Parse(public)
	if err != nil {
		t.Fatal("invalid public tunnel URL")
	}
	probe := &http.Client{Timeout: 10 * time.Second}
	liveWait(t, startup, func() bool {
		req, err := http.NewRequestWithContext(startup, "GET", "https://cloudflare-dns.com/dns-query?name="+url.QueryEscape(parsed.Hostname())+"&type=A", nil)
		if err != nil {
			return false
		}
		req.Header.Set("Accept", "application/dns-json")
		response, err := probe.Do(req)
		if err != nil {
			return false
		}
		var dns struct {
			Answer []struct {
				Type int `json:"type"`
			} `json:"Answer"`
		}
		err = json.NewDecoder(response.Body).Decode(&dns)
		_ = response.Body.Close()
		if err != nil {
			return false
		}
		published := false
		for _, answer := range dns.Answer {
			published = published || answer.Type == 1
		}
		return published
	})
	liveWait(t, startup, func() bool {
		req, err := http.NewRequestWithContext(startup, "GET", public+"/healthz", nil)
		if err != nil {
			return false
		}
		response, err := probe.Do(req)
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == 200
	})
	return public
}

// liveSameEvent compares typed linkage rather than JSON formatting.
func liveSameEvent(a, b axm.WebhookDelivery) bool {
	var left, right axm.Linkage
	return json.Unmarshal(a.Relationships["event"].Data, &left) == nil && json.Unmarshal(b.Relationships["event"].Data, &right) == nil && left.ID != "" && left == right
}
