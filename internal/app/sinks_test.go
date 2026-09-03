package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/app"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// collector records webhook deliveries.
type collector struct {
	mu     sync.Mutex
	bodies []string
	got    chan struct{}
}

func newCollector() *collector { return &collector{got: make(chan struct{}, 64)} }

func (c *collector) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func (c *collector) wait(t *testing.T) {
	t.Helper()
	select {
	case <-c.got:
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook delivery arrived")
	}
}

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

// The wiring is asserted end to end rather than by inspection: a state change
// through the real server has to reach a real webhook receiver.
func TestWebhookSinkReceivesEnrollmentEvents(t *testing.T) {
	c := newCollector()
	srv := c.server(t)
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0",
		Sinks: app.SinkConfig{Audit: true, WebhookURL: srv.URL},
	})
	if a.Core == nil {
		t.Fatal("core missing")
	}
	// Any state change will do; an import publishes without needing a device.
	publishSomething(t, a)
	c.wait(t)
	body := c.all()
	if !strings.Contains(body, `"topic":"mdm.`) {
		t.Fatalf("not the MicroMDM envelope:\n%s", body)
	}
}

// The bus Build creates is asynchronous, so Close must drain it or a
// delivery in flight is lost when the process exits.
func TestCloseDrainsTheEventBus(t *testing.T) {
	c := newCollector()
	srv := c.server(t)
	a, err := app.Build(context.Background(), app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0", Logger: quiet,
		Sinks: app.SinkConfig{WebhookURL: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	publishSomething(t, a)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.all() == "" {
		t.Fatal("Close returned before the asynchronous sink delivered")
	}
}

// Without a sink configured nothing subscribes and no bus is created, which
// is what every deployment before this change had.
func TestNoSinksMeansNoBus(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", Listen: ":0"})
	publishSomething(t, a)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// A webhook URL that cannot be used is a build error rather than a server
// that silently publishes nowhere.
func TestBadWebhookURLFailsBuild(t *testing.T) {
	_, err := app.Build(context.Background(), app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0", Logger: quiet,
		Sinks: app.SinkConfig{WebhookURL: "\x7f://bad"},
	})
	if err == nil {
		t.Fatal("Build accepted an unusable webhook URL")
	}
}
