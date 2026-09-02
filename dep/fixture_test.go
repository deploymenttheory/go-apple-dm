package dep_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/dep"
	"github.com/deploymenttheory/go-apple-mdm/dep/deptest"
	"github.com/deploymenttheory/go-apple-mdm/dep/inmem"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
)

var t0 = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

const acct = "acct"

// fixture wires a fake DEP service, an in-memory store, a bus, and a
// client that share one clock.
type fixture struct {
	t      *testing.T
	srv    *deptest.Server
	store  *inmem.Store
	clock  *clock.Fake
	clk    clock.Clock
	bus    *event.Bus
	client *dep.Client

	mu     sync.Mutex
	events []event.Event
}

type fixtureOptions struct {
	real      bool
	noAccount bool
	server    deptest.Options
	client    func(*dep.ClientConfig)
}

// withRealClock uses clock.Real, for synctest-driven loops.
func withRealClock(o *fixtureOptions) { o.real = true }

// withoutAccount leaves the store empty.
func withoutAccount(o *fixtureOptions) { o.noAccount = true }

func withServer(fn func(*deptest.Options)) func(*fixtureOptions) {
	return func(o *fixtureOptions) { fn(&o.server) }
}

func withClient(fn func(*dep.ClientConfig)) func(*fixtureOptions) {
	return func(o *fixtureOptions) { o.client = fn }
}

func newFixture(t *testing.T, mutate ...func(*fixtureOptions)) *fixture {
	t.Helper()
	var o fixtureOptions
	for _, m := range mutate {
		m(&o)
	}
	f := &fixture{t: t, store: inmem.New(), bus: event.New()}
	if o.real {
		f.clk = clock.Real{}
	} else {
		f.clock = clock.NewFake(t0)
		f.clk = f.clock
	}
	o.server.Clock = f.clk
	f.srv = deptest.NewServer(o.server)
	// A private transport: connections opened inside a synctest bubble
	// must be closed there, not by another test through the default one.
	transport := &http.Transport{}
	t.Cleanup(f.srv.Close)
	t.Cleanup(transport.CloseIdleConnections)
	f.bus.Subscribe(event.All, func(_ context.Context, e event.Event) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.events = append(f.events, e)
		return nil
	})
	cfg := dep.ClientConfig{Store: f.store, BaseURL: f.srv.URL(), Clock: f.clk, Bus: f.bus, HTTPClient: &http.Client{Transport: transport, Timeout: 10 * time.Second}}
	if o.client != nil {
		o.client(&cfg)
	}
	var err error
	if f.client, err = dep.NewClient(cfg); err != nil {
		t.Fatal(err)
	}
	if !o.noAccount {
		f.putAccount()
	}
	return f
}

// putAccount stores the account with the fake's tokens.
func (f *fixture) putAccount(mutate ...func(*dep.Account)) {
	f.t.Helper()
	a := &dep.Account{Name: acct, CreatedAt: f.clk.Now(), UpdatedAt: f.clk.Now()}
	a.SetTokens(f.srv.Tokens())
	for _, m := range mutate {
		m(a)
	}
	if err := f.store.PutAccount(context.Background(), a); err != nil {
		f.t.Fatal(err)
	}
}

// eventsOf returns the recorded events of one type.
func (f *fixture) eventsOf(typ event.Type) []event.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []event.Event
	for _, e := range f.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// resetEvents clears the recorded events.
func (f *fixture) resetEvents() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = nil
}

// account reads the stored account.
func (f *fixture) account() *dep.Account {
	f.t.Helper()
	a, err := f.store.GetAccount(context.Background(), acct)
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

// device returns a device fixture with the serial.
func device(serial string) dep.Device {
	return dep.Device{SerialNumber: serial, Model: "iPad", DeviceFamily: "iPad", OS: "iPadOS", Description: "test " + serial}
}
