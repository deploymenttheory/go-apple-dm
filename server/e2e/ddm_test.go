//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm/predicate"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
)

const (
	declProps  = `{"Type":"com.apple.management.properties","Identifier":"com.example.props","Payload":{"shard":%d}}`
	declConfig = `{"Type":"com.apple.configuration.management.test","Identifier":"com.example.config","Payload":{"Echo":%q}}`
	declAct    = `{"Type":"com.apple.activation.simple","Identifier":"com.example.act","Payload":{"StandardConfigurations":["com.example.config"]%s}}`
)

func reasonCodes(d *simulator.DDMDeclaration) []string {
	var out []string
	if d == nil {
		return out
	}
	for _, r := range d.Reasons {
		out = append(out, r.Code)
	}
	return out
}

func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// TestE2E_DDMRoundTrip is E2E-008: a declaration change reaches the device
// through push, tokens, declaration-items, and fetch, and the device's
// status report is stored.
func TestE2E_DDMRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newDDMHarness(t, false)
	dev := h.ddmDevice("UDID-DDM-1", map[string]any{"shard": 10})
	id := deviceID("UDID-DDM-1")

	h.put(fmt.Sprintf(declProps, 1))
	h.put(fmt.Sprintf(declConfig, "hello"))
	h.put(fmt.Sprintf(declAct, ""))
	h.assign(id, "fleet", "com.example.props", "com.example.config", "com.example.act")

	// One coalesced command and one push for the whole burst.
	res := h.drain()
	if res.Queued != 1 || res.Pushed != 1 {
		t.Fatalf("drain = %+v", res)
	}
	if len(h.apns.Requests()) != 1 {
		t.Fatalf("apns requests = %d, want 1", len(h.apns.Requests()))
	}
	ok, data := h.pendingDDMCommand(id)
	if !ok || len(data) == 0 {
		t.Fatal("expected a DeclarativeManagement command with tokens")
	}

	// Connect delivers the command; the simulator syncs and reports.
	cmds, err := dev.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].RequestType != "DeclarativeManagement" {
		t.Fatalf("commands = %+v", cmds)
	}
	st := dev.DDM()
	if len(st.Declarations) != 3 || st.Properties["shard"] != float64(1) {
		t.Fatalf("state = %+v", st)
	}
	act := st.Declarations["activation/com.example.act"]
	cfg := st.Declarations["configuration/com.example.config"]
	if act == nil || cfg == nil || !act.Active || !cfg.Active || act.Valid != "valid" || cfg.Valid != "valid" {
		t.Fatalf("activation %+v configuration %+v", act, cfg)
	}
	status := h.status(id)
	if row, ok := status["com.example.act"]; !ok || !row.Active || row.Valid != "valid" || row.ServerToken != act.ServerToken {
		t.Fatalf("status rows = %+v", status)
	}
	if h.countEvents(event.DDMChanged) != 1 || h.countEvents(event.DDMStatusReceived) != 1 {
		t.Fatalf("events = %v", h.eventTypes())
	}

	// An equivalent re-upload changes nothing: no command, no push.
	h.put(`{"Identifier":"com.example.config","Payload":{"Echo":"hello"},"Type":"com.apple.configuration.management.test"}`)
	if res := h.drain(); res.Queued != 0 || res.Pushed != 0 {
		t.Fatalf("no-op drain = %+v", res)
	}
	if got, _ := dev.Connect(ctx); len(got) != 0 {
		t.Fatalf("idle connect delivered %d commands", len(got))
	}

	// An edit re-syncs only the changed declaration.
	h.put(fmt.Sprintf(declConfig, "changed"))
	if res := h.drain(); res.Queued != 1 {
		t.Fatalf("edit drain = %+v", res)
	}
	if _, err := dev.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	sync, err := dev.SyncDDM(ctx) // already settled by Connect
	if err != nil || sync.Changed {
		t.Fatalf("sync after connect = %+v, %v", sync, err)
	}
	if dev.DDM().Declarations["configuration/com.example.config"].Payload["Echo"] != "changed" {
		t.Fatal("edited payload not applied")
	}

	// Delete: direct fetch is 404, the sync removes it, the activation
	// reports the missing configuration.
	if err := h.engine.DeleteDeclaration(ctx, "com.example.config"); err != nil {
		t.Fatal(err)
	}
	if res := h.drain(); res.Queued != 1 {
		t.Fatalf("delete drain = %+v", res)
	}
	var herr *simulator.HTTPError
	if _, err := dev.DeclarativeManagement(ctx, "declaration/configuration/com.example.config", nil); !errors.As(err, &herr) || herr.Status != http.StatusNotFound {
		t.Fatalf("fetch after delete: %v, want 404", err)
	}
	if _, err := dev.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	st = dev.DDM()
	if _, still := st.Declarations["configuration/com.example.config"]; still {
		t.Fatal("deleted declaration still on the device")
	}
	codes := reasonCodes(st.Declarations["activation/com.example.act"])
	if !hasCode(codes, "Error.MissingConfigurations") {
		t.Fatalf("activation reasons = %v", codes)
	}
	// The incremental report carried only the changed activation: the
	// removed configuration stays until a full report says it is gone.
	status = h.status(id)
	if _, kept := status["com.example.config"]; !kept {
		t.Fatal("incremental report must not remove absent declarations")
	}
	if row := status["com.example.act"]; row.Valid == "valid" {
		t.Fatalf("activation should report invalid after the delete: %+v", row)
	}
	if err := dev.PostDDMStatus(ctx, true); err != nil {
		t.Fatal(err)
	}
	status = h.status(id)
	if _, still := status["com.example.config"]; still {
		t.Fatal("full report absent declaration still in status")
	}
	if h.countEvents(event.DDMStatusReceived) < 3 {
		t.Fatalf("events = %v", h.eventTypes())
	}
	if got, _ := dev.Connect(ctx); len(got) != 0 {
		t.Fatal("idle connect delivered a command")
	}
}

// TestE2E_DDMPredicate is E2E-009: an activation predicate excludes a
// device; the excluded device reports Info.Predicate and the failed
// activation, the server stores the reasons, and bad predicates are
// rejected at upload with no push.
func TestE2E_DDMPredicate(t *testing.T) {
	ctx := context.Background()
	h := newDDMHarness(t, false)
	low := h.ddmDevice("UDID-LOW", map[string]any{})
	high := h.ddmDevice("UDID-HIGH", map[string]any{})
	lowID, highID := deviceID("UDID-LOW"), deviceID("UDID-HIGH")

	h.put(fmt.Sprintf(declConfig, "shared"))
	h.put(fmt.Sprintf(declAct, `,"Predicate":"(@property(shard) <= 50)"`))
	h.assign(lowID, "all", "com.example.config", "com.example.act")
	h.assign(highID, "all")
	// Per-device properties through direct assignment of distinct declarations.
	h.put(`{"Type":"com.apple.management.properties","Identifier":"com.example.props.low","Payload":{"shard":10}}`)
	h.put(`{"Type":"com.apple.management.properties","Identifier":"com.example.props.high","Payload":{"shard":90}}`)
	if _, err := h.engine.AssignDeclaration(ctx, lowID, "com.example.props.low"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.AssignDeclaration(ctx, highID, "com.example.props.high"); err != nil {
		t.Fatal(err)
	}
	if res := h.drain(); res.Queued != 2 {
		t.Fatalf("drain = %+v", res)
	}
	for _, d := range []*simulator.Device{low, high} {
		if _, err := d.Connect(ctx); err != nil {
			t.Fatal(err)
		}
	}
	lowAct := low.DDM().Declarations["activation/com.example.act"]
	highAct := high.DDM().Declarations["activation/com.example.act"]
	if lowAct == nil || !lowAct.Active || !low.DDM().Declarations["configuration/com.example.config"].Active {
		t.Fatalf("low device: %+v", lowAct)
	}
	if highAct == nil || highAct.Active || !hasCode(reasonCodes(highAct), "Info.Predicate") {
		t.Fatalf("high device activation: %+v", highAct)
	}
	if hc := high.DDM().Declarations["configuration/com.example.config"]; hc == nil || hc.Active || !hasCode(reasonCodes(hc), "Error.ActivationFailed") {
		t.Fatalf("high device configuration: %+v", hc)
	}
	// The server stored the reasons.
	if row := h.status(highID)["com.example.act"]; row.Active || len(row.Reasons) == 0 {
		t.Fatalf("stored high status = %+v", row)
	}
	if row := h.status(lowID)["com.example.act"]; !row.Active {
		t.Fatalf("stored low status = %+v", row)
	}

	// Flipping the property re-notifies only that device.
	before := len(h.apns.Requests())
	h.put(`{"Type":"com.apple.management.properties","Identifier":"com.example.props.high","Payload":{"shard":20}}`)
	if res := h.drain(); res.Queued != 1 {
		t.Fatalf("flip drain = %+v", res)
	}
	if got := len(h.apns.Requests()) - before; got != 1 {
		t.Fatalf("pushes after flip = %d, want 1", got)
	}
	if got, _ := low.Connect(ctx); len(got) != 0 {
		t.Fatal("low device was notified")
	}
	if _, err := high.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if a := high.DDM().Declarations["activation/com.example.act"]; a == nil || !a.Active {
		t.Fatalf("high device after flip: %+v", a)
	}

	// Bad predicates never reach devices.
	events := len(h.eventTypes())
	pushes := len(h.apns.Requests())
	for _, pred := range []string{`"@property(shard) <="`, `"shard MATCHES 'x'"`} {
		_, _, err := h.engine.PutDeclaration(ctx, []byte(fmt.Sprintf(declAct, ",\"Predicate\":"+pred)))
		if !errors.Is(err, ddm.ErrInvalidDeclaration) || (!errors.Is(err, predicate.ErrSyntax) && !errors.Is(err, predicate.ErrUnsupported)) {
			t.Fatalf("predicate %s: err = %v", pred, err)
		}
	}
	if res := h.drain(); res.Queued != 0 || len(h.eventTypes()) != events || len(h.apns.Requests()) != pushes {
		t.Fatalf("rejected upload had side effects: %+v", res)
	}
}

// TestE2E_DDMCheckOutClears is E2E-017: after CheckOut and re-enrollment no
// DDM state survives and no stale push is sent.
func TestE2E_DDMCheckOutClears(t *testing.T) {
	ctx := context.Background()
	h := newDDMHarness(t, true)
	dev := h.ddmDevice("UDID-OUT", map[string]any{})
	id := deviceID("UDID-OUT")
	h.put(fmt.Sprintf(declConfig, "x"))
	h.assign(id, "s", "com.example.config")
	h.drain()
	if _, err := dev.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.status(id)) == 0 {
		t.Fatal("no status before checkout")
	}
	if err := dev.CheckOut(ctx); err != nil {
		t.Fatal(err)
	}
	if sets, err := h.engine.EnrollmentSets(ctx, id); err != nil || len(sets) != 0 {
		t.Fatalf("sets after checkout = %v, %v", sets, err)
	}
	if rows := h.status(id); len(rows) != 0 {
		t.Fatalf("status after checkout = %+v", rows)
	}
	if _, err := h.ddmStore.Snapshot(ctx, id); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("snapshot after checkout: %v", err)
	}
	// A change to the old set does not push the checked-out device.
	pushes := len(h.apns.Requests())
	h.put(fmt.Sprintf(declConfig, "y"))
	if res := h.drain(); res.Pushed != 0 || len(h.apns.Requests()) != pushes {
		t.Fatalf("stale push after checkout: %+v", res)
	}
	// Re-enroll: the manifest is empty apart from the synthesised subscriptions.
	fresh := h.ddmDevice("UDID-OUT", map[string]any{})
	sync, err := fresh.SyncDDM(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range sync.Fetched {
		if key != "configuration/"+ddm.SubscriptionIdentifier {
			t.Fatalf("re-enrolled device fetched %s", key)
		}
	}
	if len(fresh.DDM().Declarations) > 1 {
		t.Fatalf("re-enrolled device inherited declarations: %v", sync.Fetched)
	}
}
