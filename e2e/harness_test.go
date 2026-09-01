//go:build e2e

// Package e2e runs the named scenarios in docs/testing/e2e-scenarios.md
// against a real HTTP server built from the library: service core,
// in-memory storage, HTTP handlers with Mdm-Signature verification, and
// the device simulator as the client.
package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/cms"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/httpapi"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/service"
	"github.com/deploymenttheory/go-apple-mdm/simulator"
	"github.com/deploymenttheory/go-apple-mdm/storage/inmem"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// harness is one running server plus the pieces tests inspect.
type harness struct {
	t      *testing.T
	ca     *testpki.CA
	store  *inmem.Store
	core   *service.Core
	clock  *clock.Fake
	server *httptest.Server

	mu     sync.Mutex
	events []event.Event
}

func newHarness(t *testing.T, cfg service.Config) *harness {
	t.Helper()
	ca, err := testpki.NewCA("go-apple-mdm test CA")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ca: ca, store: inmem.New(), clock: clock.NewFake(t0)}
	bus := event.New()
	bus.Subscribe(event.All, func(_ context.Context, e event.Event) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.events = append(h.events, e)
		return nil
	})
	cfg.Store, cfg.Bus, cfg.Clock = h.store, bus, h.clock
	h.core, err = service.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	api := httpapi.Handler(httpapi.Config{Checkin: h.core, Connect: h.core, Now: h.clock.Now})
	verify := cms.VerifyOptions{Roots: ca.Pool(), ClockSkew: 5 * time.Minute, Now: time.Now}
	mux := http.NewServeMux()
	mux.Handle("/mdm", httpapi.CertFromMdmSignature(verify, 0)(api))
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

// device creates a simulated device with a freshly issued identity.
func (h *harness) device(udid string) *simulator.Device {
	h.t.Helper()
	id, err := h.ca.Issue(udid, time.Now().Add(-time.Minute))
	if err != nil {
		h.t.Fatal(err)
	}
	return simulator.New(udid,
		simulator.WithURLs(h.server.URL+"/mdm", h.server.URL+"/mdm"),
		simulator.WithClient(h.server.Client()),
		simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}),
	)
}

func (h *harness) identity(cn string) *simulator.Identity {
	h.t.Helper()
	id, err := h.ca.Issue(cn, time.Now().Add(-time.Minute))
	if err != nil {
		h.t.Fatal(err)
	}
	return &simulator.Identity{Cert: id.Cert, Key: id.Key}
}

func (h *harness) eventTypes() []event.Type {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]event.Type, 0, len(h.events))
	for _, e := range h.events {
		out = append(out, e.Type)
	}
	return out
}

func deviceID(udid string) mdm.EnrollmentID {
	return mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: udid}
}
