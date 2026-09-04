package ddm_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	json "encoding/json/v2"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	schemaddm "github.com/deploymenttheory/go-apple-dm/v3/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/ddm/ddmtest"
	ddminmem "github.com/deploymenttheory/go-apple-dm/v3/storage/ddm/inmem"
)

// report renders a status report in Apple's wire form. full is nil when the
// report carries no FullReport member.
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

// row is one management.declarations entry as a device reports it.
func row(identifier, token string, active bool, valid string, reasons ...map[string]any) map[string]any {
	r := map[string]any{"identifier": identifier, "server-token": token, "active": active, "valid": valid, "reasons": []any{}}
	if len(reasons) > 0 {
		r["reasons"] = reasons
	}
	return r
}

// declarationsItem nests management.declarations the way Apple sends it:
// StatusItems.management.declarations.{activations,configurations,assets,management}.
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

func values(t *testing.T, h *harness, id mdm.EnrollmentID, prefix string) map[string]string {
	t.Helper()
	res, err := h.engine.StatusValues(context.Background(), id, ddm.StatusValueQuery{PathPrefix: prefix}, paging.Page{Limit: 1000})
	if err != nil {
		t.Fatalf("StatusValues: %v", err)
	}
	out := make(map[string]string, len(res.Items))
	for _, v := range res.Items {
		out[v.Path] = string(v.Value)
	}
	return out
}

func declarationRows(t *testing.T, h *harness, id mdm.EnrollmentID) map[string]ddm.DeclarationStatus {
	t.Helper()
	rows, err := h.engine.DeclarationStatus(context.Background(), id)
	if err != nil {
		t.Fatalf("DeclarationStatus: %v", err)
	}
	out := make(map[string]ddm.DeclarationStatus, len(rows))
	for _, r := range rows {
		out[string(r.Kind)+"/"+r.Identifier] = r
	}
	return out
}

func TestStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := ddmtest.Device(1)
	t.Run("TooLarge", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(c *ddm.Config) { c.MaxStatusBytes = 32 })
		body := []byte(`{"StatusItems":{"x":"` + strings.Repeat("a", 32) + `"}}`)
		if _, err := h.engine.Status(ctx, dev, body); !errors.Is(err, ddm.ErrStatusTooLarge) {
			t.Fatalf("oversized: %v", err)
		}
		if res, err := h.engine.StatusReports(ctx, dev, paging.Page{}); err != nil || len(res.Items) != 0 {
			t.Fatalf("oversized report stored: %+v %v", res, err)
		}
		if _, err := h.engine.Status(ctx, dev, []byte(`{"StatusItems":{}}`)); err != nil {
			t.Fatalf("small report: %v", err)
		}
	})
	t.Run("DuplicateKeyRejected", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		for _, body := range []string{
			`{"StatusItems":{},"StatusItems":{}}`,
			`{"StatusItems":{"device":{"a":1,"a":2}}}`,
		} {
			if _, err := h.engine.Status(ctx, dev, []byte(body)); !errors.Is(err, ddm.ErrStatusMalformed) {
				t.Errorf("%s: %v", body, err)
			}
		}
		if res, err := h.engine.StatusReports(ctx, dev, paging.Page{}); err != nil || len(res.Items) != 0 {
			t.Fatalf("malformed report stored: %+v %v", res, err)
		}
		if len(h.Events()) != 0 {
			t.Fatal("event published for a rejected report")
		}
	})
	t.Run("InvalidUTF8Rejected", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		if _, err := h.engine.Status(ctx, dev, []byte("{\"StatusItems\":{\"x\":\"\xff\"}}")); !errors.Is(err, ddm.ErrStatusMalformed) {
			t.Fatalf("invalid UTF-8: %v", err)
		}
	})
	t.Run("NotJSON", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		for _, body := range []string{"nope", "", "[]", `{"StatusItems":[]}`, `{"StatusItems":{}} trailing`} {
			if _, err := h.engine.Status(ctx, dev, []byte(body)); !errors.Is(err, ddm.ErrStatusMalformed) {
				t.Errorf("%q: %v", body, err)
			}
		}
	})
	t.Run("NestedItemsResolved", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		items := map[string]any{"device": map[string]any{"model": map[string]any{"family": "Mac", "identifier": "Mac15,6"}}}
		for k, v := range declarationsItem([]map[string]any{row("com.example.act", "tok1", true, "valid")}, nil) {
			items[k] = v
		}
		out, err := h.engine.Status(ctx, dev, report(t, boolp(true), items, nil))
		if err != nil {
			t.Fatal(err)
		}
		if out.Seq == 0 || len(out.Removed) != 0 || len(out.RemovedValues) != 0 {
			t.Fatalf("outcome %+v", out)
		}
		got := values(t, h, dev, "")
		if got["device.model.family"] != `"Mac"` || got["device.model.identifier"] != `"Mac15,6"` {
			t.Fatalf("values %v", got)
		}
		if _, ok := got["device"]; ok {
			t.Fatalf("intermediate path stored: %v", got)
		}
		want := `{"activations":[{"active":true,"identifier":"com.example.act","reasons":[],"server-token":"tok1","valid":"valid"}],"assets":[],"configurations":[],"management":[]}`
		if got["management.declarations"] != want {
			t.Fatalf("management.declarations = %s", got["management.declarations"])
		}
		rows := declarationRows(t, h, dev)
		r, ok := rows["activation/com.example.act"]
		if !ok || len(rows) != 1 || r.ServerToken != "tok1" || !r.Active || r.Valid != "valid" || r.Reasons != nil || r.FirstSeen != t0 || r.LastSeen != t0 {
			t.Fatalf("rows %+v", rows)
		}
	})
	t.Run("UnknownPathsRetained", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		items := map[string]any{
			"foo":   map[string]any{"bar": []any{1, nil}, "empty": map[string]any{}, "null": nil},
			"plain": "x",
		}
		if _, err := h.engine.Status(ctx, dev, report(t, nil, items, nil)); err != nil {
			t.Fatal(err)
		}
		got := values(t, h, dev, "")
		want := map[string]string{"foo.bar": "[1,null]", "foo.empty": "{}", "foo.null": "null", "plain": `"x"`}
		for path, v := range want {
			if got[path] != v {
				t.Errorf("%s = %q, want %q", path, got[path], v)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("values %v", got)
		}
		// Prefix queries see the retained paths.
		if got := values(t, h, dev, "foo."); len(got) != 3 {
			t.Fatalf("prefix query %v", got)
		}
	})
	t.Run("DuplicateIdentifierPrefersSnapshotToken", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		d := h.put(configTest("com.example.cfg", "hi"))
		h.assign(dev, "com.example.cfg")
		if _, err := h.engine.Manifest(ctx, dev); err != nil {
			t.Fatal(err)
		}
		for name, rows := range map[string][]map[string]any{
			"SnapshotFirst": {row("com.example.cfg", d.ServerToken, true, "valid"), row("com.example.cfg", "stale", false, "invalid")},
			"SnapshotLast":  {row("com.example.cfg", "stale", false, "invalid"), row("com.example.cfg", d.ServerToken, true, "valid")},
		} {
			if _, err := h.engine.Status(ctx, dev, report(t, boolp(true), declarationsItem(nil, rows), nil)); err != nil {
				t.Fatal(err)
			}
			got := declarationRows(t, h, dev)
			r := got["configuration/com.example.cfg"]
			if len(got) != 1 || r.ServerToken != d.ServerToken || !r.Active || r.Valid != "valid" {
				t.Fatalf("%s: rows %+v", name, got)
			}
		}
		// Without a snapshot the later entry wins.
		other := ddmtest.Device(2)
		rows := []map[string]any{row("com.example.cfg", "first", true, "valid"), row("com.example.cfg", "second", false, "invalid")}
		if _, err := h.engine.Status(ctx, other, report(t, boolp(true), declarationsItem(nil, rows), nil)); err != nil {
			t.Fatal(err)
		}
		if got := declarationRows(t, h, other); got["configuration/com.example.cfg"].ServerToken != "second" {
			t.Fatalf("rows without snapshot %+v", got)
		}
	})
	t.Run("FullReportRemovesAbsent", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		both := declarationsItem([]map[string]any{row("com.example.act", "t1", true, "valid")}, []map[string]any{row("com.example.cfg", "t2", true, "valid")})
		both["device"] = map[string]any{"model": map[string]any{"family": "Mac"}}
		if _, err := h.engine.Status(ctx, dev, report(t, boolp(true), both, nil)); err != nil {
			t.Fatal(err)
		}
		h.clock.Advance(time.Hour)
		only := declarationsItem(nil, []map[string]any{row("com.example.cfg", "t2", false, "valid")})
		out, err := h.engine.Status(ctx, dev, report(t, boolp(true), only, nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Removed) != 1 || out.Removed[0] != (ddm.DeclarationRef{Kind: schemaddm.KindActivation, Identifier: "com.example.act", ServerToken: "t1"}) {
			t.Fatalf("Removed = %+v", out.Removed)
		}
		if strings.Join(out.RemovedValues, ",") != "device.model.family" {
			t.Fatalf("RemovedValues = %v", out.RemovedValues)
		}
		rows := declarationRows(t, h, dev)
		if len(rows) != 1 || rows["configuration/com.example.cfg"].Active || rows["configuration/com.example.cfg"].FirstSeen != t0 || rows["configuration/com.example.cfg"].LastSeen != t0.Add(time.Hour) {
			t.Fatalf("rows %+v", rows)
		}
		evs := h.Events()
		if len(evs) != 2 || evs[1].Type != event.DDMStatusReceived {
			t.Fatalf("events %+v", evs)
		}
		data, ok := evs[1].Data.(*ddm.StatusOutcome)
		if !ok || len(data.Removed) != 1 || data.Removed[0].Identifier != "com.example.act" {
			t.Fatalf("event data %#v", evs[1].Data)
		}
	})
	t.Run("PartialKeeps", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		both := declarationsItem([]map[string]any{row("com.example.act", "t1", true, "valid")}, []map[string]any{row("com.example.cfg", "t2", true, "valid")})
		both["device"] = map[string]any{"model": map[string]any{"family": "Mac"}}
		if _, err := h.engine.Status(ctx, dev, report(t, boolp(true), both, nil)); err != nil {
			t.Fatal(err)
		}
		h.clock.Advance(time.Hour)
		for _, full := range []*bool{nil, boolp(false)} {
			only := declarationsItem(nil, []map[string]any{row("com.example.cfg", "t3", true, "valid")})
			out, err := h.engine.Status(ctx, dev, report(t, full, only, nil))
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Removed) != 0 || len(out.RemovedValues) != 0 {
				t.Fatalf("partial report removed %+v", out)
			}
			rows := declarationRows(t, h, dev)
			if len(rows) != 2 || rows["activation/com.example.act"].ServerToken != "t1" || rows["configuration/com.example.cfg"].ServerToken != "t3" {
				t.Fatalf("rows %+v", rows)
			}
			if rows["configuration/com.example.cfg"].FirstSeen != t0 || rows["configuration/com.example.cfg"].LastSeen != t0.Add(time.Hour) {
				t.Fatalf("FirstSeen not kept: %+v", rows["configuration/com.example.cfg"])
			}
			if got := values(t, h, dev, "device."); got["device.model.family"] != `"Mac"` {
				t.Fatalf("value dropped by a partial report: %v", got)
			}
		}
	})
	t.Run("NoDeclarationsItemLeavesRows", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		both := declarationsItem([]map[string]any{row("com.example.act", "t1", true, "valid")}, []map[string]any{row("com.example.cfg", "t2", true, "valid")})
		if _, err := h.engine.Status(ctx, dev, report(t, boolp(true), both, nil)); err != nil {
			t.Fatal(err)
		}
		out, err := h.engine.Status(ctx, dev, report(t, boolp(true), map[string]any{"device": map[string]any{"model": map[string]any{"family": "Mac"}}}, nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Removed) != 0 {
			t.Fatalf("rows removed without a declarations item: %+v", out.Removed)
		}
		if rows := declarationRows(t, h, dev); len(rows) != 2 {
			t.Fatalf("rows %+v", rows)
		}
		// A report with no StatusItems at all is accepted too.
		if _, err := h.engine.Status(ctx, dev, []byte(`{"FullReport":false}`)); err != nil {
			t.Fatal(err)
		}
		if rows := declarationRows(t, h, dev); len(rows) != 2 {
			t.Fatalf("rows after an empty report %+v", rows)
		}
	})
	t.Run("ErrorsStored", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		errs := []map[string]any{
			{"StatusItem": "management.declarations", "Reasons": []map[string]any{{"Code": "Error.Unknown", "Description": "oops", "Details": map[string]any{"Key": 1}}}},
			{"StatusItem": "device.model.family"},
		}
		if _, err := h.engine.Status(ctx, dev, report(t, nil, map[string]any{}, errs)); err != nil {
			t.Fatal(err)
		}
		res, err := h.engine.StatusErrors(ctx, dev, paging.Page{})
		if err != nil || len(res.Items) != 2 {
			t.Fatalf("StatusErrors = %+v, %v", res, err)
		}
		// Newest first: the second Errors entry has the higher sequence.
		newest, oldest := res.Items[0], res.Items[1]
		if newest.StatusItem != "device.model.family" || oldest.StatusItem != "management.declarations" || newest.Seq <= oldest.Seq {
			t.Fatalf("errors %+v", res.Items)
		}
		if string(oldest.Reasons) != `[{"Code":"Error.Unknown","Description":"oops","Details":{"Key":1}}]` {
			t.Fatalf("reasons %s", oldest.Reasons)
		}
		if oldest.ReceivedAt != t0 {
			t.Fatalf("ReceivedAt %v", oldest.ReceivedAt)
		}
		// Reasons on declaration rows are stored raw too, lower-case keys as sent.
		rows := []map[string]any{row("com.example.cfg", "t1", false, "invalid", map[string]any{"code": "Error.Bad", "description": "d"})}
		if _, err := h.engine.Status(ctx, dev, report(t, nil, declarationsItem(nil, rows), nil)); err != nil {
			t.Fatal(err)
		}
		if got := declarationRows(t, h, dev)["configuration/com.example.cfg"]; string(got.Reasons) != `[{"code":"Error.Bad","description":"d"}]` {
			t.Fatalf("row reasons %s", got.Reasons)
		}
	})
	t.Run("PublishesEvent", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		out, err := h.engine.Status(ctx, dev, report(t, nil, map[string]any{"device": map[string]any{"model": map[string]any{"family": "Mac"}}}, nil))
		if err != nil {
			t.Fatal(err)
		}
		evs := h.Events()
		if len(evs) != 1 {
			t.Fatalf("events %+v", evs)
		}
		e := evs[0]
		if e.Type != event.DDMStatusReceived || e.Enrollment != dev || e.Actor != "ddm" || e.At != t0 {
			t.Fatalf("event %+v", e)
		}
		if data, ok := e.Data.(*ddm.StatusOutcome); !ok || data.Seq != out.Seq {
			t.Fatalf("event data %#v", e.Data)
		}
		// No bus: still fine.
		quiet := newHarness(t, func(c *ddm.Config) { c.Bus = nil })
		if _, err := quiet.engine.Status(ctx, dev, []byte(`{"StatusItems":{}}`)); err != nil {
			t.Fatal(err)
		}
		// A closed bus is logged, not surfaced.
		if err := h.bus.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.Status(ctx, dev, []byte(`{"StatusItems":{}}`)); err != nil {
			t.Fatalf("status with a closed bus: %v", err)
		}
		if !strings.Contains(h.logs.String(), "publish") {
			t.Fatalf("publish failure not logged: %s", h.logs.String())
		}
	})
	t.Run("StoreFailure", func(t *testing.T) {
		t.Parallel()
		failing := &ddmtest.Failing{Store: ddminmem.New(), Fail: map[string]error{"PutStatus": errBoom}}
		h := newHarness(t, func(c *ddm.Config) { c.Store = failing })
		if _, err := h.engine.Status(ctx, dev, []byte(`{"StatusItems":{}}`)); !errors.Is(err, errBoom) {
			t.Fatalf("Status: %v", err)
		}
		if len(h.Events()) != 0 {
			t.Fatal("event published after a failed write")
		}
	})
	t.Run("InvalidID", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		if _, err := h.engine.Status(ctx, mdm.EnrollmentID{}, []byte(`{"StatusItems":{}}`)); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
	})
	t.Run("MalformedDeclarations", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		for name, item := range map[string]any{
			"String":       "nope",
			"NoIdentifier": map[string]any{"configurations": []map[string]any{{"server-token": "t", "active": true, "valid": "valid"}}},
			"WrongShape":   map[string]any{"configurations": "nope"},
		} {
			items := map[string]any{"management": map[string]any{"declarations": item}}
			if _, err := h.engine.Status(ctx, dev, report(t, boolp(true), items, nil)); !errors.Is(err, ddm.ErrStatusMalformed) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if res, err := h.engine.StatusReports(ctx, dev, paging.Page{}); err != nil || len(res.Items) != 0 {
			t.Fatalf("malformed reports stored: %+v %v", res, err)
		}
	})
}

func TestStatusQueries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev, other := ddmtest.Device(1), ddmtest.Device(2)
	h := newHarness(t)
	items := declarationsItem(nil, []map[string]any{row("com.example.cfg", "t1", true, "valid")})
	items["device"] = map[string]any{"model": map[string]any{"family": "Mac", "identifier": "Mac15,6"}, "identifier": map[string]any{"udid": "U1"}}
	errs := []map[string]any{{"StatusItem": "device.model.family", "Reasons": []map[string]any{{"Code": "Error.Unknown"}}}}
	body := report(t, boolp(true), items, errs)
	if _, err := h.engine.Status(ctx, dev, body); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.Status(ctx, other, report(t, boolp(true), declarationsItem(nil, []map[string]any{row("com.example.cfg", "t2", false, "invalid")}), nil)); err != nil {
		t.Fatal(err)
	}
	t.Run("DeclarationStatus", func(t *testing.T) {
		t.Parallel()
		rows, err := h.engine.DeclarationStatus(ctx, dev)
		if err != nil || len(rows) != 1 || rows[0].Identifier != "com.example.cfg" || rows[0].ServerToken != "t1" {
			t.Fatalf("rows %+v, %v", rows, err)
		}
	})
	t.Run("ByIdentifier", func(t *testing.T) {
		t.Parallel()
		res, err := h.engine.DeclarationStatusByIdentifier(ctx, "com.example.cfg", paging.Page{})
		if err != nil || len(res.Items) != 2 {
			t.Fatalf("by identifier %+v, %v", res, err)
		}
		if res.Items[0].ID != dev || res.Items[0].ServerToken != "t1" || res.Items[1].ID != other || res.Items[1].ServerToken != "t2" {
			t.Fatalf("by identifier %+v", res.Items)
		}
		if res, err := h.engine.DeclarationStatusByIdentifier(ctx, "com.example.none", paging.Page{}); err != nil || len(res.Items) != 0 {
			t.Fatalf("unknown identifier %+v, %v", res, err)
		}
	})
	t.Run("Values", func(t *testing.T) {
		t.Parallel()
		got := values(t, h, dev, "device.model.")
		if len(got) != 2 || got["device.model.family"] != `"Mac"` {
			t.Fatalf("prefix values %v", got)
		}
		if all := values(t, h, dev, ""); len(all) != 4 {
			t.Fatalf("all values %v", all)
		}
		res, err := h.engine.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{Limit: 2})
		if err != nil || len(res.Items) != 2 || res.NextCursor == "" {
			t.Fatalf("paged values %+v, %v", res, err)
		}
	})
	t.Run("Errors", func(t *testing.T) {
		t.Parallel()
		res, err := h.engine.StatusErrors(ctx, dev, paging.Page{})
		if err != nil || len(res.Items) != 1 || res.Items[0].StatusItem != "device.model.family" {
			t.Fatalf("errors %+v, %v", res, err)
		}
		if res, err := h.engine.StatusErrors(ctx, other, paging.Page{}); err != nil || len(res.Items) != 0 {
			t.Fatalf("other errors %+v, %v", res, err)
		}
	})
	t.Run("Reports", func(t *testing.T) {
		t.Parallel()
		res, err := h.engine.StatusReports(ctx, dev, paging.Page{})
		if err != nil || len(res.Items) != 1 || string(res.Items[0].Raw) != string(body) || !res.Items[0].FullReport || res.Items[0].ReceivedAt != t0 {
			t.Fatalf("reports %+v, %v", res, err)
		}
	})
}

func TestClientCapabilities(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev := ddmtest.Device(1)
	capabilities := func(v any) map[string]any {
		return map[string]any{"management": map[string]any{"client-capabilities": v}}
	}
	t.Run("Decoded", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		caps := map[string]any{
			"supported-versions": []string{"1.0.0"},
			"supported-features": map[string]any{},
			"supported-payloads": map[string]any{
				"declarations": map[string]any{
					"activations":    []string{"com.apple.activation.simple"},
					"configurations": []string{"com.apple.configuration.management.test"},
					"assets":         []string{"com.apple.asset.data"},
					"management":     []string{"com.apple.management.properties"},
				},
				"status-items": []string{"device.model.family", "management.declarations"},
			},
		}
		if _, err := h.engine.Status(ctx, dev, report(t, boolp(true), capabilities(caps), nil)); err != nil {
			t.Fatal(err)
		}
		got, err := h.engine.ClientCapabilities(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got.SupportedVersions, ",") != "1.0.0" || got.SupportedFeatures == nil {
			t.Fatalf("capabilities %+v", got)
		}
		if strings.Join(got.SupportedPayloads.StatusItems, ",") != "device.model.family,management.declarations" {
			t.Fatalf("status items %v", got.SupportedPayloads.StatusItems)
		}
		d := got.SupportedPayloads.Declarations
		if strings.Join(d.Activations, ",") != "com.apple.activation.simple" || strings.Join(d.Configurations, ",") != "com.apple.configuration.management.test" ||
			strings.Join(d.Assets, ",") != "com.apple.asset.data" || strings.Join(d.Management, ",") != "com.apple.management.properties" {
			t.Fatalf("declarations %+v", d)
		}
		// The item is stored whole at its own path.
		if got := values(t, h, dev, "management.client-capabilities"); len(got) != 1 || !strings.Contains(got["management.client-capabilities"], `"status-items":["device.model.family","management.declarations"]`) {
			t.Fatalf("stored value %v", got)
		}
	})
	t.Run("NeverReported", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		if _, err := h.engine.ClientCapabilities(ctx, dev); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("never reported: %v", err)
		}
		// Other management items do not count as capabilities.
		if _, err := h.engine.Status(ctx, dev, report(t, nil, declarationsItem(nil, nil), nil)); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.ClientCapabilities(ctx, dev); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatalf("after unrelated report: %v", err)
		}
	})
	t.Run("MissingIsEmpty", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		for name, v := range map[string]any{"EmptyObject": map[string]any{}, "Null": nil, "PayloadsOnly": map[string]any{"supported-payloads": map[string]any{}}} {
			if _, err := h.engine.Status(ctx, dev, report(t, boolp(true), capabilities(v), nil)); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got, err := h.engine.ClientCapabilities(ctx, dev)
			if err != nil || got == nil || len(got.SupportedVersions) != 0 || len(got.SupportedPayloads.StatusItems) != 0 {
				t.Fatalf("%s: %+v, %v", name, got, err)
			}
		}
	})
	for _, name := range []string{"Malformed", "MalformedIsEmpty"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			cases := map[string]any{
				"String":          "nope",
				"Array":           []any{1, 2},
				"Number":          42,
				"PayloadsString":  map[string]any{"supported-versions": []string{"1.0.0"}, "supported-payloads": "nope"},
				"VersionsObject":  map[string]any{"supported-versions": map[string]any{"a": 1}, "supported-payloads": map[string]any{"status-items": []string{"device.model.family"}}},
				"StatusItemsBool": map[string]any{"supported-payloads": map[string]any{"status-items": true}},
			}
			for label, v := range cases {
				if _, err := h.engine.Status(ctx, dev, report(t, boolp(true), capabilities(v), nil)); err != nil {
					t.Fatalf("%s: store: %v", label, err)
				}
				got, err := h.engine.ClientCapabilities(ctx, dev)
				if err != nil || got == nil {
					t.Fatalf("%s: %+v, %v", label, got, err)
				}
				switch label {
				case "PayloadsString":
					if strings.Join(got.SupportedVersions, ",") != "1.0.0" || len(got.SupportedPayloads.StatusItems) != 0 {
						t.Fatalf("%s: well-formed keys lost: %+v", label, got)
					}
				case "VersionsObject":
					if len(got.SupportedVersions) != 0 || strings.Join(got.SupportedPayloads.StatusItems, ",") != "device.model.family" {
						t.Fatalf("%s: %+v", label, got)
					}
				default:
					if len(got.SupportedVersions) != 0 || len(got.SupportedPayloads.StatusItems) != 0 {
						t.Fatalf("%s: %+v", label, got)
					}
				}
			}
			if !strings.Contains(h.logs.String(), "client-capabilities") {
				t.Fatalf("malformed capabilities not logged: %s", h.logs.String())
			}
		})
	}
}
