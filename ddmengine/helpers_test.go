package ddmengine_test

// Test helpers shared with package ddm's own tests. The engine harness is
// duplicated rather than exported: it wires unexported test doubles, and a
// contract suite in ddm/ddmtest is for backends, not for fixtures.

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	ddminmem "github.com/deploymenttheory/go-apple-dm/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func declJSON(typ, id string, payload map[string]any) []byte {
	b, err := json.Marshal(map[string]any{"Type": typ, "Identifier": id, "Payload": payload})
	if err != nil {
		panic(err)
	}
	return b
}

type harness struct {
	t      *testing.T
	engine *ddm.Engine
	store  *ddminmem.Store
	clock  *clock.Fake
	bus    *event.Bus
	logs   *bytes.Buffer

	mu     sync.Mutex
	events []event.Event
}

func newHarness(t *testing.T, opts ...func(*ddm.Config)) *harness {
	t.Helper()
	h := &harness{t: t, store: ddminmem.New(), clock: clock.NewFake(t0), bus: event.New(), logs: &bytes.Buffer{}}
	h.bus.Subscribe(event.All, func(_ context.Context, e event.Event) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.events = append(h.events, e)
		return nil
	})
	cfg := ddm.Config{
		Store: h.store, Bus: h.bus, Clock: h.clock,
		Logger: slog.New(slog.NewTextHandler(h.logs, nil)),
	}
	for _, o := range opts {
		o(&cfg)
	}
	e, err := ddm.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.engine = e
	return h
}

func configTest(id, echo string) []byte {
	return declJSON(schemaddm.DeclarationTypeManagementTest, id, map[string]any{"Echo": echo})
}

func report(t *testing.T, full *bool, items map[string]any, errs []map[string]any) []byte {
	t.Helper()
	m := map[string]any{"StatusItems": items}
	if full != nil {
		m["FullReport"] = *full
	}
	if errs != nil {
		m["Errors"] = errs
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func boolp(b bool) *bool { return &b }

func declarationsItem(activations, configurations []map[string]any) map[string]any {
	if activations == nil {
		activations = []map[string]any{}
	}
	if configurations == nil {
		configurations = []map[string]any{}
	}
	return map[string]any{"management": map[string]any{"declarations": map[string]any{
		"activations": activations, "configurations": configurations, "assets": []any{}, "management": []any{},
	}}}
}

func row(identifier, token string, active bool, valid string, reasons ...map[string]any) map[string]any {
	r := map[string]any{"identifier": identifier, "server-token": token, "active": active, "valid": valid, "reasons": []any{}}
	if len(reasons) > 0 {
		r["reasons"] = reasons
	}
	return r
}

func (h *harness) Events() []event.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]event.Event, len(h.events))
	copy(out, h.events)
	return out
}

// put uploads a declaration and fails the test on error.
func (h *harness) put(raw []byte) *ddm.Declaration {
	h.t.Helper()
	d, _, err := h.engine.PutDeclaration(context.Background(), raw)
	if err != nil {
		h.t.Fatalf("PutDeclaration: %v\n%s", err, raw)
	}
	return d
}

// assign binds identifiers directly to id.
func (h *harness) assign(id mdm.EnrollmentID, identifiers ...string) {
	h.t.Helper()
	for _, identifier := range identifiers {
		if _, err := h.engine.AssignDeclaration(context.Background(), id, identifier); err != nil {
			h.t.Fatalf("AssignDeclaration(%s, %s): %v", id.ID, identifier, err)
		}
	}
}

// pending lists the change rows due now.
func (h *harness) pending() []ddm.Change {
	h.t.Helper()
	rows, err := h.store.PendingChanges(context.Background(), h.clock.Now().Add(time.Hour), 1000)
	if err != nil {
		h.t.Fatalf("PendingChanges: %v", err)
	}
	return rows
}

// drain completes every pending change row so later assertions start clean.
func (h *harness) drain() {
	h.t.Helper()
	rows := h.pending()
	seqs := make([]int64, 0, len(rows))
	for _, r := range rows {
		seqs = append(seqs, r.Seq)
	}
	if len(seqs) == 0 {
		return
	}
	if err := h.store.CompleteChanges(context.Background(), seqs); err != nil {
		h.t.Fatalf("CompleteChanges: %v", err)
	}
}

var errBoom = errors.New("boom")
