//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/ddm/adapter/inproc"
	ddminmem "github.com/deploymenttheory/go-apple-dm/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/ddm/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/ddmengine"
	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/service"
	"github.com/deploymenttheory/go-apple-dm/simulator"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/storage/postgres"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlite"
)

// ddmHarness is a harness with the DDM engine wired in-process: the
// engine's store shares the e2e database, inproc.Handler answers
// DeclarativeManagement check-ins, ddmengine.ServiceHook clears state on
// CheckOut and Authenticate, and the notifier enqueues through the core
// and pushes through the fake APNs.
type ddmHarness struct {
	*harness
	engine   *ddm.Engine
	ddmStore ddm.Store
	changes  *ddmengine.Notifier
}

// newDDMStore opens the engine's store on the same database as the
// enrollment store (one SQLite file, one PostgreSQL schema) or in memory.
func newDDMStore(t *testing.T, s storage.Store) ddm.Store {
	t.Helper()
	var (
		db      *sql.DB
		dialect sqlcommon.Dialect
	)
	switch st := s.(type) {
	case *sqlite.Store:
		db, dialect = st.DB(), sqlite.Dialect
	case *postgres.Store:
		db, dialect = st.DB(), postgres.Dialect
	case *inmem.Store:
		return ddminmem.New()
	default:
		t.Fatalf("no DDM store for %T", s)
	}
	ds, err := sqlstore.Open(context.Background(), db, dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

// newDDMHarness builds a harness with the engine; subs enables the
// synthesised status-subscriptions declaration.
func newDDMHarness(t *testing.T, subs bool) *ddmHarness {
	t.Helper()
	store := newStore(t)
	bus := newBus()
	fake := clock.NewFake(t0)
	ds := newDDMStore(t, store)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &ddmHarness{ddmStore: ds}
	engine, err := ddm.New(ddm.Config{
		Store: ds, Bus: bus, Clock: fake, Logger: quiet,
		Subscriptions: ddm.Subscriptions{Enabled: subs},
	})
	if err != nil {
		t.Fatal(err)
	}
	d.engine = engine
	cfg := service.Config{
		DeclarativeManagement: inproc.Handler(engine),
		Hooks:                 []service.Hook{ddmengine.NewServiceHook(engine, store, quiet)},
	}
	d.harness = newHarnessWith(t, cfg, store, bus)
	// The harness made its own fake clock; the engine must share it.
	d.harness.clock = fake
	d.changes, err = ddmengine.NewNotifier(ddmengine.NotifierConfig{
		Store: ds, Tokens: engine, Enqueuer: d.core, Pusher: d.notifier, Bus: bus, Clock: fake, Logger: quiet,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// drain advances past the coalescing window and drains the notifier once.
func (d *ddmHarness) drain() ddmengine.DrainResult {
	d.t.Helper()
	d.clock.Advance(ddmengine.DefaultNotifyWindow)
	res, err := d.changes.DrainOnce(context.Background())
	if err != nil {
		d.t.Fatal(err)
	}
	return res
}

func (d *ddmHarness) put(raw string) *ddm.Declaration {
	d.t.Helper()
	decl, _, err := d.engine.PutDeclaration(context.Background(), []byte(raw))
	if err != nil {
		d.t.Fatalf("put declaration: %v", err)
	}
	return decl
}

func (d *ddmHarness) assign(id mdm.EnrollmentID, set string, identifiers ...string) {
	d.t.Helper()
	ctx := context.Background()
	if _, err := d.engine.PutSet(ctx, set); err != nil {
		d.t.Fatal(err)
	}
	for _, ident := range identifiers {
		if _, err := d.engine.AddToSet(ctx, set, ident); err != nil {
			d.t.Fatal(err)
		}
	}
	if _, err := d.engine.AssignSet(ctx, id, set); err != nil {
		d.t.Fatal(err)
	}
}

func (d *ddmHarness) status(id mdm.EnrollmentID) map[string]ddm.DeclarationStatus {
	d.t.Helper()
	rows, err := d.engine.DeclarationStatus(context.Background(), id)
	if err != nil {
		d.t.Fatal(err)
	}
	out := map[string]ddm.DeclarationStatus{}
	for _, r := range rows {
		out[r.Identifier] = r
	}
	return out
}

func (d *ddmHarness) countEvents(typ event.Type) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, e := range d.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// pendingDDMCommand reports whether the next queued command for id is a
// DeclarativeManagement kick, decoding its Data.
func (d *ddmHarness) pendingDDMCommand(id mdm.EnrollmentID) (bool, []byte) {
	d.t.Helper()
	cmd, err := d.store.Next(context.Background(), id, false, d.clock.Now())
	if err != nil || cmd == nil {
		return false, nil
	}
	if cmd.RequestType != "DeclarativeManagement" {
		return false, nil
	}
	if dm, ok := cmd.Payload.(*commands.DeclarativeManagement); ok {
		return true, dm.Data
	}
	// Stored commands keep the plist bytes; decode the Data from them.
	var env struct {
		Command struct {
			Data []byte `plist:"Data"`
		} `plist:"Command"`
	}
	if err := plist.Unmarshal(cmd.Raw, &env); err != nil {
		d.t.Fatalf("decode stored command: %v", err)
	}
	return true, env.Command.Data
}

// ddmDevice builds an enrolled simulator device with the DDM client and
// the given @property values.
func (d *ddmHarness) ddmDevice(udid string, props map[string]any) *simulator.Device {
	return d.harness.ddmDevice(udid, props)
}

// ddmDevice builds an enrolled simulator device with the DDM client and
// the given @property values.
func (h *harness) ddmDevice(udid string, props map[string]any) *simulator.Device {
	h.t.Helper()
	id, err := h.ca.Issue(udid, time.Now().Add(-time.Minute))
	if err != nil {
		h.t.Fatal(err)
	}
	dev := simulator.New(udid,
		simulator.WithURLs(h.server.URL+"/mdm", h.server.URL+"/mdm"),
		simulator.WithClient(h.server.Client()),
		simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}),
		simulator.WithDDM(props),
	)
	if err := dev.Enroll(context.Background()); err != nil {
		h.t.Fatalf("enroll %s: %v", udid, err)
	}
	return dev
}
