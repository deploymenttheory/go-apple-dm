package ddm_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	json "encoding/json/v2"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm/predicate"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	schemaddm "github.com/deploymenttheory/go-apple-dm/v3/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/ddm/ddmtest"
	ddminmem "github.com/deploymenttheory/go-apple-dm/v3/storage/ddm/inmem"
)

// t0 is the fixed clock every engine test starts from.
var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// errBoom is the error injected through ddmtest.Failing and the hooks.
var errBoom = errors.New("boom")

// Fixture builders in Apple's wire form {"Type","Identifier","Payload"}.

func activation(id string, configs ...string) []byte {
	return activationWithPredicate(id, "", configs...)
}

func activationWithPredicate(id, pred string, configs ...string) []byte {
	payload := map[string]any{"StandardConfigurations": configs}
	if pred != "" {
		payload["Predicate"] = pred
	}
	return declJSON(schemaddm.DeclarationTypeActivationSimple, id, payload)
}

func configTest(id, echo string) []byte {
	return declJSON(schemaddm.DeclarationTypeManagementTest, id, map[string]any{"Echo": echo})
}

func properties(id string, payload map[string]any) []byte {
	return declJSON(schemaddm.DeclarationTypeManagementProperties, id, payload)
}

func assetData(id, url string) []byte {
	return declJSON(schemaddm.DeclarationTypeAssetData, id, map[string]any{"Reference": map[string]any{"DataURL": url}})
}

func declJSON(typ, id string, payload map[string]any) []byte {
	b, err := json.Marshal(map[string]any{"Type": typ, "Identifier": id, "Payload": payload})
	if err != nil {
		panic(err)
	}
	return b
}

// harness wires an Engine to an in-memory store, a fake clock, a bus that
// records every event.
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

func pendingIDs(rows []ddm.Change) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID.ID+"/"+r.Reason)
	}
	return out
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return m
}

// resolverFunc adapts a function to ddm.Resolver.
type resolverFunc func(ctx context.Context, id mdm.EnrollmentID) ([]string, error)

func (f resolverFunc) Resolve(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	return f(ctx, id)
}

// expanderFunc adapts a function to ddm.Expander.
type expanderFunc func(ctx context.Context, id mdm.EnrollmentID, d *ddm.Declaration) ([]byte, error)

func (f expanderFunc) Expand(ctx context.Context, id mdm.EnrollmentID, d *ddm.Declaration) ([]byte, error) {
	return f(ctx, id, d)
}

func TestNew(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("RequiresStore", func(t *testing.T) {
		t.Parallel()
		if _, err := ddm.New(ddm.Config{}); !errors.Is(err, ddm.ErrNoStore) {
			t.Fatalf("New without store: %v", err)
		}
	})
	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		store := ddminmem.New()
		e, err := ddm.New(ddm.Config{Store: store, Subscriptions: ddm.Subscriptions{Enabled: true}})
		if err != nil {
			t.Fatal(err)
		}
		if e.Store() != store {
			t.Fatal("Store() is not the configured store")
		}
		dev := ddmtest.Device(1)
		// MaxStatusBytes defaults to 1 MiB: one byte over is rejected, the
		// limit itself is accepted.
		big := []byte(`{"StatusItems":{"x":"` + strings.Repeat("a", ddm.DefaultMaxStatusBytes-len(`{"StatusItems":{"x":""}}`)) + `"}}`)
		if len(big) != ddm.DefaultMaxStatusBytes {
			t.Fatalf("fixture is %d bytes", len(big))
		}
		if _, err := e.Status(ctx, dev, big); err != nil {
			t.Fatalf("report at the limit: %v", err)
		}
		if _, err := e.Status(ctx, dev, append(big, ' ')); !errors.Is(err, ddm.ErrStatusTooLarge) {
			t.Fatalf("report over the limit: %v", err)
		}
		// KeepReports defaults to 10.
		for range ddm.DefaultKeepReports + 2 {
			if _, err := e.Status(ctx, dev, []byte(`{"StatusItems":{}}`)); err != nil {
				t.Fatal(err)
			}
		}
		res, err := e.StatusReports(ctx, dev, paging.Page{Limit: 100})
		if err != nil || len(res.Items) != ddm.DefaultKeepReports {
			t.Fatalf("retained %d reports (%v), want %d", len(res.Items), err, ddm.DefaultKeepReports)
		}
		// A real clock stamps the tokens response with the current year.
		body, err := e.Tokens(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if ts := decode(t, body)["SyncTokens"].(map[string]any)["Timestamp"].(string); !strings.HasPrefix(ts, fmt.Sprint(time.Now().UTC().Year())) {
			t.Fatalf("Timestamp %q does not use the real clock", ts)
		}
		// The subscriptions baseline defaults to the 11 Apple items.
		sub, err := e.Declaration(ctx, dev, schemaddm.KindConfiguration, ddm.SubscriptionIdentifier)
		if err != nil {
			t.Fatal(err)
		}
		if got := subscriptionNames(t, sub); len(got) != 11 {
			t.Fatalf("baseline has %d items: %v", len(got), got)
		}
	})
}

func TestPutDeclaration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("Creates", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		d, changed, err := h.engine.PutDeclaration(ctx, configTest("com.example.cfg", "hi"))
		if err != nil || !changed {
			t.Fatalf("PutDeclaration = %v, %v", changed, err)
		}
		if d.Kind != schemaddm.KindConfiguration || d.CreatedAt != t0 || d.UpdatedAt != t0 || len(d.ServerToken) != 64 {
			t.Fatalf("declaration %+v", d)
		}
		got, err := h.engine.GetDeclaration(ctx, "com.example.cfg")
		if err != nil || got.ServerToken != d.ServerToken || !bytes.Equal(got.Canonical, d.Canonical) {
			t.Fatalf("GetDeclaration = %+v, %v", got, err)
		}
		res, err := h.engine.ListDeclarations(ctx, ddm.DeclarationQuery{Kind: schemaddm.KindConfiguration}, paging.Page{})
		if err != nil || len(res.Items) != 1 {
			t.Fatalf("ListDeclarations = %+v, %v", res, err)
		}
	})
	t.Run("EquivalentJSONIsNoop", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		first := h.put([]byte(`{"Type":"com.apple.configuration.management.test","Identifier":"com.example.cfg","Payload":{"Echo":"hi"}}`))
		h.assign(ddmtest.Device(1), "com.example.cfg")
		h.drain()
		h.clock.Advance(time.Hour)
		d, changed, err := h.engine.PutDeclaration(ctx, []byte("{ \"Payload\" : { \"Echo\" : \"hi\" },\n\"Identifier\":\"com.example.cfg\", \"Type\":\"com.apple.configuration.management.test\" }"))
		if err != nil || changed {
			t.Fatalf("second put = %v, %v", changed, err)
		}
		if d.ServerToken != first.ServerToken {
			t.Fatal("equivalent JSON produced a different token")
		}
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("change rows recorded for a no-op: %v", pendingIDs(rows))
		}
		got, err := h.engine.GetDeclaration(ctx, "com.example.cfg")
		if err != nil || got.UpdatedAt != t0 {
			t.Fatalf("UpdatedAt moved on a no-op: %+v %v", got, err)
		}
	})
	t.Run("ChangeRecordsAffected", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "one"))
		h.put(configTest("com.example.other", "x"))
		if _, err := h.engine.PutSet(ctx, "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AddToSet(ctx, "lab", "com.example.cfg"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignSet(ctx, ddmtest.Device(1), "lab"); err != nil {
			t.Fatal(err)
		}
		h.assign(ddmtest.Device(2), "com.example.cfg")
		h.assign(ddmtest.Device(3), "com.example.other")
		h.drain()
		_, changed, err := h.engine.PutDeclaration(ctx, configTest("com.example.cfg", "two"))
		if err != nil || !changed {
			t.Fatalf("re-upload = %v, %v", changed, err)
		}
		got := pendingIDs(h.pending())
		want := []string{"DEVICE-01/declaration", "DEVICE-02/declaration"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("pending = %v, want %v", got, want)
		}
		d, err := h.engine.GetDeclaration(ctx, "com.example.cfg")
		if err != nil || d.CreatedAt != t0 {
			t.Fatalf("CreatedAt not kept across a change: %+v %v", d, err)
		}
	})
	t.Run("KindChangeConflict", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.x", "hi"))
		_, _, err := h.engine.PutDeclaration(ctx, activation("com.example.x", "com.example.cfg"))
		if !errors.Is(err, ddm.ErrConflict) {
			t.Fatalf("kind change: %v", err)
		}
		d, err := h.engine.GetDeclaration(ctx, "com.example.x")
		if err != nil || d.Kind != schemaddm.KindConfiguration {
			t.Fatalf("declaration replaced despite the conflict: %+v %v", d, err)
		}
	})
	t.Run("UploadedServerTokenIgnored", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		plain := h.put(configTest("com.example.cfg", "hi"))
		d, changed, err := h.engine.PutDeclaration(ctx, []byte(`{"Type":"com.apple.configuration.management.test","Identifier":"com.example.cfg","ServerToken":"authored-by-admin","Payload":{"Echo":"hi"}}`))
		if err != nil || changed {
			t.Fatalf("put with ServerToken = %v, %v", changed, err)
		}
		if d.ServerToken != plain.ServerToken || d.ServerToken == "authored-by-admin" {
			t.Fatalf("token %q", d.ServerToken)
		}
	})
	t.Run("InvalidPredicateRejected", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		cases := map[string]error{
			"@property(shard) <=": predicate.ErrSyntax,
			"x MATCHES 'y'":       predicate.ErrUnsupported,
		}
		for pred, want := range cases {
			_, _, err := h.engine.PutDeclaration(ctx, activationWithPredicate("com.example.act", pred, "com.example.cfg"))
			if !errors.Is(err, ddm.ErrInvalidDeclaration) || !errors.Is(err, want) {
				t.Fatalf("%q: %v", pred, err)
			}
		}
		if _, err := h.engine.GetDeclaration(ctx, "com.example.act"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("rejected declaration was written: %v", err)
		}
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("change rows written: %v", pendingIDs(rows))
		}
	})
	t.Run("ValidPredicateAccepted", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		d := h.put(activationWithPredicate("com.example.act", "@property(shard) <= 5 AND @status(device.model.family) == 'Mac'", "com.example.cfg"))
		if d.Kind != schemaddm.KindActivation {
			t.Fatalf("kind %q", d.Kind)
		}
		// An activation without a predicate is accepted too.
		h.put(activation("com.example.act2", "com.example.cfg"))
	})
	t.Run("StoreFailure", func(t *testing.T) {
		t.Parallel()
		failing := &ddmtest.Failing{Store: ddminmem.New(), Fail: map[string]error{"PutDeclaration": errBoom}}
		h := newHarness(t, func(c *ddm.Config) { c.Store = failing })
		if _, _, err := h.engine.PutDeclaration(ctx, configTest("com.example.cfg", "hi")); !errors.Is(err, errBoom) {
			t.Fatalf("PutDeclaration: %v", err)
		}
		// A failed write must leave no change rows behind: the rows are the
		// persistent signal the notifier drains, so a row here would wake a
		// device for a declaration that was never stored.
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("change rows recorded after a failed write: %v", pendingIDs(rows))
		}
	})
}

func TestDeleteDeclaration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("NotifiesBeforeDelete", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		if _, err := h.engine.PutSet(ctx, "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AddToSet(ctx, "lab", "com.example.cfg"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignSet(ctx, ddmtest.Device(1), "lab"); err != nil {
			t.Fatal(err)
		}
		h.assign(ddmtest.Device(2), "com.example.cfg")
		h.drain()
		if err := h.engine.DeleteDeclaration(ctx, "com.example.cfg"); err != nil {
			t.Fatal(err)
		}
		// The change rows were computed while the membership still existed.
		got := pendingIDs(h.pending())
		if strings.Join(got, ",") != "DEVICE-01/declaration,DEVICE-02/declaration" {
			t.Fatalf("pending = %v", got)
		}
		if _, err := h.engine.GetDeclaration(ctx, "com.example.cfg"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("still present: %v", err)
		}
		if members, err := h.engine.SetDeclarations(ctx, "lab"); err != nil || len(members) != 0 {
			t.Fatalf("set members = %v, %v", members, err)
		}
	})
	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		if err := h.engine.DeleteDeclaration(ctx, "com.example.missing"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("delete unknown: %v", err)
		}
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("change rows written: %v", pendingIDs(rows))
		}
	})
	t.Run("StoreFailure", func(t *testing.T) {
		t.Parallel()
		inner := ddminmem.New()
		failing := &ddmtest.Failing{Store: inner, Fail: map[string]error{}}
		h := newHarness(t, func(c *ddm.Config) { c.Store = failing })
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(ddmtest.Device(1), "com.example.cfg")
		h.drain()
		failing.Fail["DeleteDeclaration"] = errBoom
		if err := h.engine.DeleteDeclaration(ctx, "com.example.cfg"); !errors.Is(err, errBoom) {
			t.Fatalf("DeleteDeclaration: %v", err)
		}
		// The transaction rolled back: the change row recorded before the
		// delete is gone with it.
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("change rows survived the rollback: %v", pendingIDs(rows))
		}
		if _, err := inner.GetDeclaration(ctx, "com.example.cfg"); err != nil {
			t.Fatalf("declaration lost despite the failure: %v", err)
		}
	})
}

func TestSets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("PutDeleteNotify", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		created, err := h.engine.PutSet(ctx, "lab")
		if err != nil || !created {
			t.Fatalf("PutSet = %v, %v", created, err)
		}
		if created, err = h.engine.PutSet(ctx, "lab"); err != nil || created {
			t.Fatalf("second PutSet = %v, %v", created, err)
		}
		s, err := h.engine.GetSet(ctx, "lab")
		if err != nil || s.Name != "lab" || s.CreatedAt != t0 {
			t.Fatalf("GetSet = %+v, %v", s, err)
		}
		list, err := h.engine.ListSets(ctx, paging.Page{})
		if err != nil || len(list.Items) != 1 {
			t.Fatalf("ListSets = %+v, %v", list, err)
		}
		if _, err := h.engine.AssignSet(ctx, ddmtest.Device(1), "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignSet(ctx, ddmtest.User(1, "u"), "lab"); err != nil {
			t.Fatal(err)
		}
		h.drain()
		if err := h.engine.DeleteSet(ctx, "lab"); err != nil {
			t.Fatal(err)
		}
		got := pendingIDs(h.pending())
		if strings.Join(got, ",") != "DEVICE-01/set,DEVICE-01:u/set" {
			t.Fatalf("pending = %v", got)
		}
		if _, err := h.engine.GetSet(ctx, "lab"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("set survives delete: %v", err)
		}
		if sets, err := h.engine.EnrollmentSets(ctx, ddmtest.Device(1)); err != nil || len(sets) != 0 {
			t.Fatalf("EnrollmentSets = %v, %v", sets, err)
		}
		if err := h.engine.DeleteSet(ctx, "lab"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("delete unknown set: %v", err)
		}
	})
	t.Run("AddRemoveNotify", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		if _, err := h.engine.PutSet(ctx, "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignSet(ctx, ddmtest.Device(1), "lab"); err != nil {
			t.Fatal(err)
		}
		h.drain()
		changed, err := h.engine.AddToSet(ctx, "lab", "com.example.cfg")
		if err != nil || !changed {
			t.Fatalf("AddToSet = %v, %v", changed, err)
		}
		if got := pendingIDs(h.pending()); strings.Join(got, ",") != "DEVICE-01/set" {
			t.Fatalf("pending after add = %v", got)
		}
		members, err := h.engine.SetDeclarations(ctx, "lab")
		if err != nil || strings.Join(members, ",") != "com.example.cfg" {
			t.Fatalf("SetDeclarations = %v, %v", members, err)
		}
		sets, err := h.engine.DeclarationSets(ctx, "com.example.cfg")
		if err != nil || strings.Join(sets, ",") != "lab" {
			t.Fatalf("DeclarationSets = %v, %v", sets, err)
		}
		res, err := h.engine.SetEnrollments(ctx, "lab", paging.Page{})
		if err != nil || len(res.Items) != 1 || res.Items[0] != ddmtest.Device(1) {
			t.Fatalf("SetEnrollments = %+v, %v", res, err)
		}
		h.drain()
		if changed, err = h.engine.RemoveFromSet(ctx, "lab", "com.example.cfg"); err != nil || !changed {
			t.Fatalf("RemoveFromSet = %v, %v", changed, err)
		}
		if got := pendingIDs(h.pending()); strings.Join(got, ",") != "DEVICE-01/set" {
			t.Fatalf("pending after remove = %v", got)
		}
		if _, err := h.engine.AddToSet(ctx, "lab", "com.example.missing"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("add unknown declaration: %v", err)
		}
		if _, err := h.engine.AddToSet(ctx, "nope", "com.example.cfg"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("add to unknown set: %v", err)
		}
	})
	t.Run("Unchanged", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		if _, err := h.engine.PutSet(ctx, "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignSet(ctx, ddmtest.Device(1), "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AddToSet(ctx, "lab", "com.example.cfg"); err != nil {
			t.Fatal(err)
		}
		h.drain()
		if changed, err := h.engine.AddToSet(ctx, "lab", "com.example.cfg"); err != nil || changed {
			t.Fatalf("repeat add = %v, %v", changed, err)
		}
		if changed, err := h.engine.RemoveFromSet(ctx, "lab", "com.example.other"); err != nil || changed {
			t.Fatalf("remove non-member = %v, %v", changed, err)
		}
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("change rows for no-ops: %v", pendingIDs(rows))
		}
	})
}

func TestAssign(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("SetAndDeclaration", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		if _, err := h.engine.PutSet(ctx, "lab"); err != nil {
			t.Fatal(err)
		}
		dev := ddmtest.Device(1)
		changed, err := h.engine.AssignSet(ctx, dev, "lab")
		if err != nil || !changed {
			t.Fatalf("AssignSet = %v, %v", changed, err)
		}
		if changed, err = h.engine.AssignDeclaration(ctx, dev, "com.example.cfg"); err != nil || !changed {
			t.Fatalf("AssignDeclaration = %v, %v", changed, err)
		}
		sets, err := h.engine.EnrollmentSets(ctx, dev)
		if err != nil || strings.Join(sets, ",") != "lab" {
			t.Fatalf("EnrollmentSets = %v, %v", sets, err)
		}
		decls, err := h.engine.EnrollmentDeclarations(ctx, dev)
		if err != nil || strings.Join(decls, ",") != "com.example.cfg" {
			t.Fatalf("EnrollmentDeclarations = %v, %v", decls, err)
		}
		if changed, err = h.engine.UnassignSet(ctx, dev, "lab"); err != nil || !changed {
			t.Fatalf("UnassignSet = %v, %v", changed, err)
		}
		if changed, err = h.engine.UnassignDeclaration(ctx, dev, "com.example.cfg"); err != nil || !changed {
			t.Fatalf("UnassignDeclaration = %v, %v", changed, err)
		}
		if _, err := h.engine.AssignSet(ctx, dev, "nope"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("assign unknown set: %v", err)
		}
		if _, err := h.engine.AssignDeclaration(ctx, dev, "com.example.nope"); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("assign unknown declaration: %v", err)
		}
	})
	t.Run("InvalidID", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		bad := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "orphan"}
		for name, fn := range map[string]func() (bool, error){
			"AssignSet":           func() (bool, error) { return h.engine.AssignSet(ctx, bad, "lab") },
			"UnassignSet":         func() (bool, error) { return h.engine.UnassignSet(ctx, bad, "lab") },
			"AssignDeclaration":   func() (bool, error) { return h.engine.AssignDeclaration(ctx, bad, "x") },
			"UnassignDeclaration": func() (bool, error) { return h.engine.UnassignDeclaration(ctx, bad, "x") },
		} {
			if _, err := fn(); !errors.Is(err, ddm.ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
	t.Run("Unchanged", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		dev := ddmtest.Device(1)
		h.assign(dev, "com.example.cfg")
		h.drain()
		if changed, err := h.engine.AssignDeclaration(ctx, dev, "com.example.cfg"); err != nil || changed {
			t.Fatalf("repeat assign = %v, %v", changed, err)
		}
		if changed, err := h.engine.UnassignSet(ctx, dev, "lab"); err != nil || changed {
			t.Fatalf("unassign absent set = %v, %v", changed, err)
		}
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("change rows for no-ops: %v", pendingIDs(rows))
		}
	})
	t.Run("Notifies", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		if _, err := h.engine.PutSet(ctx, "lab"); err != nil {
			t.Fatal(err)
		}
		dev, user := ddmtest.Device(1), ddmtest.User(1, "u")
		if _, err := h.engine.AssignSet(ctx, dev, "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignDeclaration(ctx, user, "com.example.cfg"); err != nil {
			t.Fatal(err)
		}
		got := pendingIDs(h.pending())
		if strings.Join(got, ",") != "DEVICE-01/assignment,DEVICE-01:u/assignment" {
			t.Fatalf("pending = %v", got)
		}
		h.drain()
		if _, err := h.engine.UnassignSet(ctx, dev, "lab"); err != nil {
			t.Fatal(err)
		}
		if got := pendingIDs(h.pending()); strings.Join(got, ",") != "DEVICE-01/assignment" {
			t.Fatalf("pending after unassign = %v", got)
		}
	})
}

func TestTouch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("Records", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		if err := h.engine.Touch(ctx, []mdm.EnrollmentID{ddmtest.Device(1), ddmtest.Device(2)}, ""); err != nil {
			t.Fatal(err)
		}
		if err := h.engine.Touch(ctx, []mdm.EnrollmentID{ddmtest.Device(3)}, "resolver"); err != nil {
			t.Fatal(err)
		}
		got := pendingIDs(h.pending())
		if strings.Join(got, ",") != "DEVICE-01/touch,DEVICE-02/touch,DEVICE-03/resolver" {
			t.Fatalf("pending = %v", got)
		}
	})
	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		failing := &ddmtest.Failing{Store: ddminmem.New(), Fail: map[string]error{"RecordChanges": errBoom}}
		h := newHarness(t, func(c *ddm.Config) { c.Store = failing })
		if err := h.engine.Touch(ctx, nil, ""); err != nil {
			t.Fatalf("Touch(nil): %v", err)
		}
		// Nothing to touch means nothing recorded: the store is set to fail
		// RecordChanges, so a row here would also have surfaced as an error.
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("change rows for an empty touch: %v", pendingIDs(rows))
		}
	})
	t.Run("InvalidID", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		err := h.engine.Touch(ctx, []mdm.EnrollmentID{ddmtest.Device(1), {Channel: mdm.ChannelDevice}}, "")
		if !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("Touch with an invalid id: %v", err)
		}
		if rows := h.pending(); len(rows) != 0 {
			t.Fatalf("partial touch recorded: %v", pendingIDs(rows))
		}
	})
}

func TestManifest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev, other := ddmtest.Device(1), ddmtest.Device(2)
	identifiers := func(s *ddm.Snapshot) []string {
		out := make([]string, 0, len(s.Items))
		for _, it := range s.Items {
			out = append(out, string(it.Kind)+"/"+it.Identifier)
		}
		return out
	}
	t.Run("StaticOnly", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.put(configTest("com.example.cfg", "hi"))
		h.put(activation("com.example.act", "com.example.cfg"))
		h.put(properties("com.example.props", map[string]any{"team": "a"}))
		if _, err := h.engine.PutSet(ctx, "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AddToSet(ctx, "lab", "com.example.act"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AddToSet(ctx, "lab", "com.example.cfg"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.AssignSet(ctx, dev, "lab"); err != nil {
			t.Fatal(err)
		}
		h.assign(dev, "com.example.cfg", "com.example.props")
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if got := identifiers(snap); strings.Join(got, ",") != "activation/com.example.act,configuration/com.example.cfg,management/com.example.props" {
			t.Fatalf("items = %v", got)
		}
		if snap.ID != dev || snap.TokenChangedAt != t0 || snap.RefreshedAt != t0 || len(snap.DeclarationsToken) != 64 {
			t.Fatalf("snapshot %+v", snap)
		}
		for _, it := range snap.Items {
			if it.Expanded != nil || it.BaseToken != it.ServerToken {
				t.Fatalf("static item expanded: %+v", it)
			}
		}
		stored, err := h.store.Snapshot(ctx, dev)
		if err != nil || stored.DeclarationsToken != snap.DeclarationsToken {
			t.Fatalf("snapshot not persisted: %+v %v", stored, err)
		}
		empty, err := h.engine.Manifest(ctx, other)
		if err != nil || len(empty.Items) != 0 || empty.DeclarationsToken != ddm.DeclarationsToken(nil) {
			t.Fatalf("unknown enrollment manifest = %+v, %v", empty, err)
		}
		if _, err := h.engine.Manifest(ctx, mdm.EnrollmentID{}); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
	})
	t.Run("ResolverAdds", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Resolvers = []ddm.Resolver{
				resolverFunc(func(_ context.Context, id mdm.EnrollmentID) ([]string, error) {
					if id == dev {
						return []string{"com.example.dyn", "com.example.cfg"}, nil
					}
					return nil, nil
				}),
				resolverFunc(func(context.Context, mdm.EnrollmentID) ([]string, error) {
					return []string{"com.example.dyn"}, nil // duplicate of the first resolver
				}),
			}
		})
		h.put(configTest("com.example.cfg", "hi"))
		h.put(configTest("com.example.dyn", "dyn"))
		h.assign(dev, "com.example.cfg")
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if got := identifiers(snap); strings.Join(got, ",") != "configuration/com.example.cfg,configuration/com.example.dyn" {
			t.Fatalf("items = %v", got)
		}
		snap, err = h.engine.Manifest(ctx, other)
		if err != nil {
			t.Fatal(err)
		}
		if got := identifiers(snap); strings.Join(got, ",") != "configuration/com.example.dyn" {
			t.Fatalf("other items = %v", got)
		}
	})
	t.Run("ResolverErrorFailsClosed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Resolvers = []ddm.Resolver{resolverFunc(func(context.Context, mdm.EnrollmentID) ([]string, error) { return nil, errBoom })}
		})
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		if _, err := h.engine.Manifest(ctx, dev); !errors.Is(err, ddm.ErrResolver) || !errors.Is(err, errBoom) {
			t.Fatalf("Manifest: %v", err)
		}
		if _, err := h.engine.Tokens(ctx, dev); !errors.Is(err, ddm.ErrResolver) {
			t.Fatalf("Tokens: %v", err)
		}
		if _, err := h.store.Snapshot(ctx, dev); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("partial snapshot written: %v", err)
		}
	})
	t.Run("ResolverFailsClosed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Resolvers = []ddm.Resolver{resolverFunc(func(context.Context, mdm.EnrollmentID) ([]string, error) { return nil, errBoom })}
		})
		if _, err := h.engine.DeclarationItems(ctx, dev); !errors.Is(err, ddm.ErrResolver) {
			t.Fatalf("DeclarationItems: %v", err)
		}
	})
	t.Run("ResolverUnknownIdSkipped", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Resolvers = []ddm.Resolver{resolverFunc(func(context.Context, mdm.EnrollmentID) ([]string, error) {
				return []string{"com.example.ghost", "com.example.dyn"}, nil
			})}
		})
		h.put(configTest("com.example.dyn", "dyn"))
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if got := identifiers(snap); strings.Join(got, ",") != "configuration/com.example.dyn" {
			t.Fatalf("items = %v", got)
		}
		if !strings.Contains(h.logs.String(), "com.example.ghost") {
			t.Fatalf("unknown identifier not logged: %s", h.logs.String())
		}
	})
	t.Run("ExpanderChangesToken", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Expander = expanderFunc(func(_ context.Context, id mdm.EnrollmentID, d *ddm.Declaration) ([]byte, error) {
				if id != dev || d.Identifier != "com.example.cfg" {
					return nil, nil
				}
				return []byte(`{"Type":"` + d.Type + `","Identifier":"` + d.Identifier + `","Payload":{"Echo":"` + id.ID + `"}}`), nil
			})
		})
		base := h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		h.assign(other, "com.example.cfg")
		snapDev, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		snapOther, err := h.engine.Manifest(ctx, other)
		if err != nil {
			t.Fatal(err)
		}
		if snapDev.DeclarationsToken == snapOther.DeclarationsToken {
			t.Fatal("expansion did not change the manifest token")
		}
		item := snapDev.Items[0]
		if item.ServerToken == base.ServerToken || item.BaseToken != base.ServerToken || item.Expanded == nil {
			t.Fatalf("expanded item %+v", item)
		}
		if snapOther.Items[0].ServerToken != base.ServerToken || snapOther.Items[0].Expanded != nil {
			t.Fatalf("other item %+v", snapOther.Items[0])
		}
		body, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg")
		if err != nil {
			t.Fatal(err)
		}
		got := decode(t, body)
		if got["ServerToken"] != item.ServerToken || got["Payload"].(map[string]any)["Echo"] != "DEVICE-01" {
			t.Fatalf("served %s", body)
		}
		items, err := h.engine.DeclarationItems(ctx, dev)
		if err != nil || !strings.Contains(string(items), item.ServerToken) {
			t.Fatalf("items %s (%v) do not advertise the expanded token", items, err)
		}
	})
	t.Run("ExpanderRewritesAndRetokens", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Expander = expanderFunc(func(_ context.Context, id mdm.EnrollmentID, d *ddm.Declaration) ([]byte, error) {
				return []byte(strings.Replace(string(d.Canonical), "$SERIAL", id.ID, 1)), nil
			})
		})
		h.put(configTest("com.example.cfg", "serial $SERIAL"))
		h.assign(dev, "com.example.cfg")
		body, err := h.engine.Declaration(ctx, dev, schemaddm.KindConfiguration, "com.example.cfg")
		if err != nil {
			t.Fatal(err)
		}
		got := decode(t, body)
		if got["Payload"].(map[string]any)["Echo"] != "serial DEVICE-01" {
			t.Fatalf("served %s", body)
		}
		if got["ServerToken"] != ddm.TokenFor([]byte(`{"Identifier":"com.example.cfg","Payload":{"Echo":"serial DEVICE-01"},"Type":"com.apple.configuration.management.test"}`)) {
			t.Fatalf("token not derived from the expanded bytes: %s", body)
		}
	})
	t.Run("ExpanderUnchangedBytesKeepToken", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Expander = expanderFunc(func(_ context.Context, _ mdm.EnrollmentID, d *ddm.Declaration) ([]byte, error) {
				switch d.Identifier {
				case "com.example.same":
					return d.Canonical, nil
				case "com.example.reordered":
					// Different text, same value.
					return []byte(`{ "Type" : "com.apple.configuration.management.test", "Payload" : {"Echo":"b"}, "Identifier" : "com.example.reordered" }`), nil
				default:
					return nil, nil
				}
			})
		})
		h.put(configTest("com.example.same", "a"))
		h.put(configTest("com.example.reordered", "b"))
		h.put(configTest("com.example.nil", "c"))
		h.assign(dev, "com.example.same", "com.example.reordered", "com.example.nil")
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range snap.Items {
			if it.Expanded != nil || it.ServerToken != it.BaseToken {
				t.Fatalf("item %s re-tokened without a change: %+v", it.Identifier, it)
			}
		}
	})
	t.Run("ExpanderErrorFails", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) {
			c.Expander = expanderFunc(func(context.Context, mdm.EnrollmentID, *ddm.Declaration) ([]byte, error) { return nil, errBoom })
		})
		h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		if _, err := h.engine.Manifest(ctx, dev); !errors.Is(err, ddm.ErrExpander) || !errors.Is(err, errBoom) {
			t.Fatalf("Manifest: %v", err)
		}
	})
	t.Run("ExpanderBadJSONFails", func(t *testing.T) {
		t.Parallel()
		for name, out := range map[string][]byte{
			"NotJSON":             []byte("nope"),
			"Array":               []byte("[]"),
			"IdentifierRewritten": []byte(`{"Type":"com.apple.configuration.management.test","Identifier":"com.example.other","Payload":{"Echo":"x"}}`),
		} {
			h := newHarness(t, func(c *ddm.Config) {
				c.Expander = expanderFunc(func(context.Context, mdm.EnrollmentID, *ddm.Declaration) ([]byte, error) { return out, nil })
			})
			h.put(configTest("com.example.cfg", "hi"))
			h.assign(dev, "com.example.cfg")
			if _, err := h.engine.Manifest(ctx, dev); !errors.Is(err, ddm.ErrExpander) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
	t.Run("GoldenToken", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		refs := goldenManifest(t, h)
		snap, err := h.engine.Manifest(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		want := goldenToken(t)
		if snap.DeclarationsToken != want {
			t.Fatalf("manifest token %s, golden %s", snap.DeclarationsToken, want)
		}
		if got := ddm.DeclarationsToken(refs); got != want {
			t.Fatalf("DeclarationsToken over the same refs %s, golden %s", got, want)
		}
	})
}

// goldenManifest uploads the fixed declarations behind testdata/golden_token.txt
// to DEVICE-01 and returns their refs.
func goldenManifest(t *testing.T, h *harness) []ddm.DeclarationRef {
	t.Helper()
	uploads := [][]byte{
		[]byte(`{"Type":"com.apple.activation.simple","Identifier":"com.example.golden.act","Payload":{"StandardConfigurations":["com.example.golden.cfg"]}}`),
		[]byte(`{"Type":"com.apple.configuration.management.test","Identifier":"com.example.golden.cfg","Payload":{"Echo":"golden"}}`),
		[]byte(`{"Type":"com.apple.management.properties","Identifier":"com.example.golden.props","Payload":{"team":"golden","shard":7}}`),
	}
	refs := make([]ddm.DeclarationRef, 0, len(uploads))
	for _, raw := range uploads {
		d := h.put(raw)
		refs = append(refs, ddm.DeclarationRef{Kind: d.Kind, Identifier: d.Identifier, ServerToken: d.ServerToken})
		h.assign(ddmtest.Device(1), d.Identifier)
	}
	return refs
}

func TestEngineStoreFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev, dev2, dev3 := ddmtest.Device(1), ddmtest.Device(2), ddmtest.Device(3)
	const a, b, set = "com.example.a", "com.example.b", "lab"
	type call struct {
		name string
		fn   func(e *ddm.Engine) error
	}
	cases := map[string][]call{
		"Update": {
			{"PutDeclaration", func(e *ddm.Engine) error { _, _, err := e.PutDeclaration(ctx, configTest(a, "new")); return err }},
			{"DeleteDeclaration", func(e *ddm.Engine) error { return e.DeleteDeclaration(ctx, a) }},
			{"DeleteSet", func(e *ddm.Engine) error { return e.DeleteSet(ctx, set) }},
			{"AddToSet", func(e *ddm.Engine) error { _, err := e.AddToSet(ctx, set, b); return err }},
			{"AssignSet", func(e *ddm.Engine) error { _, err := e.AssignSet(ctx, dev3, set); return err }},
			{"Manifest", func(e *ddm.Engine) error { _, err := e.Manifest(ctx, dev); return err }},
			{"Status", func(e *ddm.Engine) error { _, err := e.Status(ctx, dev, []byte(`{"StatusItems":{}}`)); return err }},
			{"ClearEnrollment", func(e *ddm.Engine) error { return e.ClearEnrollment(ctx, dev) }},
		},
		"PutDeclaration": {{"PutDeclaration", func(e *ddm.Engine) error { _, _, err := e.PutDeclaration(ctx, configTest(a, "new")); return err }}},
		"GetDeclaration": {
			{"GetDeclaration", func(e *ddm.Engine) error { _, err := e.GetDeclaration(ctx, a); return err }},
			{"Manifest", func(e *ddm.Engine) error { _, err := e.Manifest(ctx, dev); return err }},
		},
		"GetDeclarationVersion": {{"Declaration", func(e *ddm.Engine) error {
			_, err := e.Declaration(ctx, dev, schemaddm.KindConfiguration, a)
			return err
		}}},
		"DeleteDeclaration": {{"DeleteDeclaration", func(e *ddm.Engine) error { return e.DeleteDeclaration(ctx, a) }}},
		"ListDeclarations": {{"ListDeclarations", func(e *ddm.Engine) error {
			_, err := e.ListDeclarations(ctx, ddm.DeclarationQuery{}, paging.Page{})
			return err
		}}},
		"PruneVersions":          {{"PruneVersions", func(e *ddm.Engine) error { _, err := e.PruneVersions(ctx); return err }}},
		"PutSet":                 {{"PutSet", func(e *ddm.Engine) error { _, err := e.PutSet(ctx, "new"); return err }}},
		"DeleteSet":              {{"DeleteSet", func(e *ddm.Engine) error { return e.DeleteSet(ctx, set) }}},
		"GetSet":                 {{"GetSet", func(e *ddm.Engine) error { _, err := e.GetSet(ctx, set); return err }}},
		"ListSets":               {{"ListSets", func(e *ddm.Engine) error { _, err := e.ListSets(ctx, paging.Page{}); return err }}},
		"AddSetDeclaration":      {{"AddToSet", func(e *ddm.Engine) error { _, err := e.AddToSet(ctx, set, b); return err }}},
		"RemoveSetDeclaration":   {{"RemoveFromSet", func(e *ddm.Engine) error { _, err := e.RemoveFromSet(ctx, set, a); return err }}},
		"SetDeclarations":        {{"SetDeclarations", func(e *ddm.Engine) error { _, err := e.SetDeclarations(ctx, set); return err }}},
		"DeclarationSets":        {{"DeclarationSets", func(e *ddm.Engine) error { _, err := e.DeclarationSets(ctx, a); return err }}},
		"AssignSet":              {{"AssignSet", func(e *ddm.Engine) error { _, err := e.AssignSet(ctx, dev3, set); return err }}},
		"UnassignSet":            {{"UnassignSet", func(e *ddm.Engine) error { _, err := e.UnassignSet(ctx, dev, set); return err }}},
		"EnrollmentSets":         {{"EnrollmentSets", func(e *ddm.Engine) error { _, err := e.EnrollmentSets(ctx, dev); return err }}},
		"SetEnrollments":         {{"SetEnrollments", func(e *ddm.Engine) error { _, err := e.SetEnrollments(ctx, set, paging.Page{}); return err }}},
		"AssignDeclaration":      {{"AssignDeclaration", func(e *ddm.Engine) error { _, err := e.AssignDeclaration(ctx, dev3, a); return err }}},
		"UnassignDeclaration":    {{"UnassignDeclaration", func(e *ddm.Engine) error { _, err := e.UnassignDeclaration(ctx, dev2, a); return err }}},
		"EnrollmentDeclarations": {{"EnrollmentDeclarations", func(e *ddm.Engine) error { _, err := e.EnrollmentDeclarations(ctx, dev2); return err }}},
		"StaticDeclarations": {
			{"Manifest", func(e *ddm.Engine) error { _, err := e.Manifest(ctx, dev); return err }},
			{"Tokens", func(e *ddm.Engine) error { _, err := e.Tokens(ctx, dev); return err }},
			{"DeclarationItems", func(e *ddm.Engine) error { _, err := e.DeclarationItems(ctx, dev); return err }},
			{"Declaration", func(e *ddm.Engine) error {
				_, err := e.Declaration(ctx, dev3, schemaddm.KindConfiguration, a)
				return err
			}},
		},
		"AffectedEnrollments": {
			{"PutDeclaration", func(e *ddm.Engine) error { _, _, err := e.PutDeclaration(ctx, configTest(a, "new")); return err }},
			{"DeleteDeclaration", func(e *ddm.Engine) error { return e.DeleteDeclaration(ctx, a) }},
			{"DeleteSet", func(e *ddm.Engine) error { return e.DeleteSet(ctx, set) }},
			{"AddToSet", func(e *ddm.Engine) error { _, err := e.AddToSet(ctx, set, b); return err }},
		},
		"PutSnapshot": {{"Tokens", func(e *ddm.Engine) error { _, err := e.Tokens(ctx, dev); return err }}},
		"Snapshot": {
			{"Tokens", func(e *ddm.Engine) error { _, err := e.Tokens(ctx, dev); return err }},
			{"Declaration", func(e *ddm.Engine) error {
				_, err := e.Declaration(ctx, dev, schemaddm.KindConfiguration, a)
				return err
			}},
		},
		"PutStatus":         {{"Status", func(e *ddm.Engine) error { _, err := e.Status(ctx, dev, []byte(`{"StatusItems":{}}`)); return err }}},
		"DeclarationStatus": {{"DeclarationStatus", func(e *ddm.Engine) error { _, err := e.DeclarationStatus(ctx, dev); return err }}},
		"DeclarationStatusByIdentifier": {{"DeclarationStatusByIdentifier", func(e *ddm.Engine) error {
			_, err := e.DeclarationStatusByIdentifier(ctx, a, paging.Page{})
			return err
		}}},
		"StatusValues": {
			{"StatusValues", func(e *ddm.Engine) error {
				_, err := e.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{})
				return err
			}},
			{"ClientCapabilities", func(e *ddm.Engine) error { _, err := e.ClientCapabilities(ctx, dev); return err }},
		},
		"StatusErrors":  {{"StatusErrors", func(e *ddm.Engine) error { _, err := e.StatusErrors(ctx, dev, paging.Page{}); return err }}},
		"StatusReports": {{"StatusReports", func(e *ddm.Engine) error { _, err := e.StatusReports(ctx, dev, paging.Page{}); return err }}},
		"RecordChanges": {
			{"Touch", func(e *ddm.Engine) error { return e.Touch(ctx, []mdm.EnrollmentID{dev}, "") }},
			{"PutDeclaration", func(e *ddm.Engine) error { _, _, err := e.PutDeclaration(ctx, configTest(a, "new")); return err }},
			{"DeleteDeclaration", func(e *ddm.Engine) error { return e.DeleteDeclaration(ctx, a) }},
			{"DeleteSet", func(e *ddm.Engine) error { return e.DeleteSet(ctx, set) }},
			{"AddToSet", func(e *ddm.Engine) error { _, err := e.AddToSet(ctx, set, b); return err }},
			{"AssignSet", func(e *ddm.Engine) error { _, err := e.AssignSet(ctx, dev3, set); return err }},
		},
		"ClearEnrollment": {{"ClearEnrollment", func(e *ddm.Engine) error { return e.ClearEnrollment(ctx, dev) }}},
	}
	for method, calls := range cases {
		for _, c := range calls {
			t.Run(method+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				inner := ddminmem.New()
				failing := &ddmtest.Failing{Store: inner, Fail: map[string]error{}}
				h := newHarness(t, func(c *ddm.Config) {
					c.Store = failing
					// A resolver naming b makes manifests read a declaration the
					// static membership does not carry.
					c.Resolvers = []ddm.Resolver{resolverFunc(func(context.Context, mdm.EnrollmentID) ([]string, error) { return []string{b}, nil })}
				})
				h.put(configTest(a, "a"))
				h.put(configTest(b, "b"))
				if _, err := h.engine.PutSet(ctx, set); err != nil {
					t.Fatal(err)
				}
				if _, err := h.engine.AddToSet(ctx, set, a); err != nil {
					t.Fatal(err)
				}
				if _, err := h.engine.AssignSet(ctx, dev, set); err != nil {
					t.Fatal(err)
				}
				h.assign(dev2, a)
				if _, err := h.engine.Manifest(ctx, dev); err != nil {
					t.Fatal(err)
				}
				failing.Fail[method] = errBoom
				if err := c.fn(h.engine); !errors.Is(err, errBoom) {
					t.Fatalf("%s with %s failing: %v", c.name, method, err)
				}
			})
		}
	}
}
