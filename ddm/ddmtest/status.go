package ddmtest

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/paging"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

// status builds one reported declaration row.
func status(kind schemaddm.Kind, identifier, token string, active bool, valid string) ddm.DeclarationStatus {
	return ddm.DeclarationStatus{Kind: kind, Identifier: identifier, ServerToken: token, Active: active, Valid: valid}
}

// value builds one reported status value.
func value(path, json string) ddm.StatusValue {
	return ddm.StatusValue{Path: path, Value: []byte(json)}
}

// report builds an update at the given time. HasDeclarations is set when
// decls is non-nil.
func report(full bool, at time.Time, decls []ddm.DeclarationStatus, values []ddm.StatusValue) ddm.StatusUpdate {
	return ddm.StatusUpdate{
		Raw: []byte(fmt.Sprintf(`{"at":%q}`, at.Format(time.RFC3339))), ReceivedAt: at, FullReport: full,
		HasDeclarations: decls != nil, Declarations: decls, Values: values,
	}
}

// putStatus applies u and returns the outcome.
func putStatus(t *testing.T, s ddm.Tx, id mdm.EnrollmentID, u ddm.StatusUpdate) ddm.StatusOutcome {
	t.Helper()
	out, err := s.PutStatus(context.Background(), id, u)
	if err != nil {
		t.Fatalf("PutStatus %s: %v", id.ID, err)
	}
	return out
}

// declStatus lists the enrollment's declaration rows.
func declStatus(t *testing.T, s ddm.Tx, id mdm.EnrollmentID) []ddm.DeclarationStatus {
	t.Helper()
	rows, err := s.DeclarationStatus(context.Background(), id)
	if err != nil {
		t.Fatalf("DeclarationStatus %s: %v", id.ID, err)
	}
	return rows
}

// valuePaths lists the enrollment's value paths under prefix.
func valuePaths(t *testing.T, s ddm.Tx, id mdm.EnrollmentID, prefix string) []string {
	t.Helper()
	r, err := s.StatusValues(context.Background(), id, ddm.StatusValueQuery{PathPrefix: prefix}, paging.Page{})
	if err != nil {
		t.Fatalf("StatusValues %s: %v", id.ID, err)
	}
	out := make([]string, len(r.Items))
	for i, v := range r.Items {
		out[i] = v.Path
	}
	return out
}

// RunStatusSuite covers StatusStore.
func RunStatusSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()
	conf := schemaddm.KindConfiguration

	t.Run("PartialReportUpsertsKeepsFirstSeen", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		t1 := t0.Add(time.Minute)
		first := putStatus(t, s, dev, report(true, t0,
			[]ddm.DeclarationStatus{status(conf, "a", "t1", true, "valid")},
			[]ddm.StatusValue{value("device.model.family", `"Mac"`), value("device.model.identifier", `"Mac15,6"`)}))
		if first.Seq <= 0 || len(first.Removed) != 0 || len(first.RemovedValues) != 0 || first.PrunedReports != 0 {
			t.Fatalf("first outcome: %+v", first)
		}
		second := report(false, t1,
			[]ddm.DeclarationStatus{{Kind: conf, Identifier: "a", ServerToken: "t2", Active: false, Valid: "invalid", Reasons: []byte(`[{"Code":"x"}]`)}},
			[]ddm.StatusValue{value("device.model.family", `"Mac"`)})
		out := putStatus(t, s, dev, second)
		if out.Seq <= first.Seq || len(out.Removed) != 0 || len(out.RemovedValues) != 0 {
			t.Fatalf("second outcome: %+v", out)
		}
		rows := declStatus(t, s, dev)
		if len(rows) != 1 {
			t.Fatalf("rows: %+v", rows)
		}
		row := rows[0]
		if row.ServerToken != "t2" || row.Active || row.Valid != "invalid" || string(row.Reasons) != `[{"Code":"x"}]` {
			t.Fatalf("upserted row: %+v", row)
		}
		wantTime(t, "FirstSeen", row.FirstSeen, t0)
		wantTime(t, "LastSeen", row.LastSeen, t1)
		// Returned reasons are a copy.
		row.Reasons[0] = '!'
		if declStatus(t, s, dev)[0].Reasons[0] == '!' {
			t.Fatal("DeclarationStatus returned aliased reasons")
		}
		vals, err := s.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{})
		if err != nil || len(vals.Items) != 2 {
			t.Fatalf("values: %+v %v", vals, err)
		}
		wantTime(t, "family FirstSeen", vals.Items[0].FirstSeen, t0)
		wantTime(t, "family LastSeen", vals.Items[0].LastSeen, t1)
		wantTime(t, "identifier LastSeen untouched", vals.Items[1].LastSeen, t0)
	})

	t.Run("FullReportDeletesAbsentDeclarations", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		putStatus(t, s, dev, report(true, t0, []ddm.DeclarationStatus{
			status(conf, "a", "ta", true, "valid"), status(conf, "b", "tb", true, "valid"), status(schemaddm.KindActivation, "c", "tc", true, "valid"),
		}, nil))
		out := putStatus(t, s, dev, report(true, t0.Add(time.Minute), []ddm.DeclarationStatus{status(conf, "a", "ta", true, "valid")}, nil))
		want := []ddm.DeclarationRef{{Kind: schemaddm.KindActivation, Identifier: "c", ServerToken: "tc"}, {Kind: conf, Identifier: "b", ServerToken: "tb"}}
		if len(out.Removed) != 2 || out.Removed[0] != want[0] || out.Removed[1] != want[1] {
			t.Fatalf("removed: %+v", out.Removed)
		}
		rows := declStatus(t, s, dev)
		if len(rows) != 1 || rows[0].Identifier != "a" {
			t.Fatalf("rows after full report: %+v", rows)
		}
		// A partial report never deletes.
		putStatus(t, s, dev, report(true, t0, []ddm.DeclarationStatus{status(conf, "a", "ta", true, "valid"), status(conf, "b", "tb", true, "valid")}, nil))
		out = putStatus(t, s, dev, report(false, t0.Add(2*time.Minute), []ddm.DeclarationStatus{status(conf, "a", "ta", true, "valid")}, nil))
		if len(out.Removed) != 0 || len(declStatus(t, s, dev)) != 2 {
			t.Fatalf("partial report removed rows: %+v", out)
		}
	})

	t.Run("FullReportDeletesAbsentValues", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		putStatus(t, s, dev, report(true, t0, nil, []ddm.StatusValue{value("p.a", "1"), value("p.b", "2"), value("p.c", "3")}))
		out := putStatus(t, s, dev, report(true, t0, nil, []ddm.StatusValue{value("p.b", "2")}))
		wantStrings(t, "removed values", out.RemovedValues, []string{"p.a", "p.c"})
		wantStrings(t, "values after full report", valuePaths(t, s, dev, ""), []string{"p.b"})
		putStatus(t, s, dev, report(false, t0, nil, []ddm.StatusValue{value("p.a", "1")}))
		out = putStatus(t, s, dev, report(false, t0, nil, []ddm.StatusValue{value("p.b", "2")}))
		wantStrings(t, "partial removed values", out.RemovedValues, nil)
		wantStrings(t, "values after partial report", valuePaths(t, s, dev, ""), []string{"p.a", "p.b"})
	})

	t.Run("NoDeclarationsItemLeavesRows", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		putStatus(t, s, dev, report(true, t0, []ddm.DeclarationStatus{status(conf, "a", "ta", true, "valid")}, []ddm.StatusValue{value("p.a", "1")}))
		// A full report without a management.declarations item: values are
		// still replaced, declaration rows are not.
		out := putStatus(t, s, dev, report(true, t0.Add(time.Minute), nil, []ddm.StatusValue{value("p.b", "2")}))
		if len(out.Removed) != 0 {
			t.Fatalf("removed declarations: %+v", out.Removed)
		}
		wantStrings(t, "removed values", out.RemovedValues, []string{"p.a"})
		rows := declStatus(t, s, dev)
		if len(rows) != 1 || rows[0].Identifier != "a" {
			t.Fatalf("rows: %+v", rows)
		}
		wantTime(t, "LastSeen untouched", rows[0].LastSeen, t0)
		// An explicitly empty declarations item removes everything.
		out = putStatus(t, s, dev, report(true, t0.Add(2*time.Minute), []ddm.DeclarationStatus{}, nil))
		if len(out.Removed) != 1 || len(declStatus(t, s, dev)) != 0 {
			t.Fatalf("empty declarations item: %+v", out)
		}
	})

	t.Run("KeyedByIdentifierNotToken", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		putStatus(t, s, dev, report(false, t0, []ddm.DeclarationStatus{status(conf, "a", "t1", true, "valid")}, nil))
		putStatus(t, s, dev, report(false, t0.Add(time.Minute), []ddm.DeclarationStatus{status(conf, "a", "t2", true, "valid")}, nil))
		putStatus(t, s, dev, report(false, t0.Add(time.Minute), []ddm.DeclarationStatus{status(schemaddm.KindActivation, "a", "t3", true, "valid")}, nil))
		rows := declStatus(t, s, dev)
		if len(rows) != 2 {
			t.Fatalf("rows: %+v", rows)
		}
		// Sorted by (kind, identifier): activation before configuration.
		if rows[0].Kind != schemaddm.KindActivation || rows[0].ServerToken != "t3" || rows[1].Kind != conf || rows[1].ServerToken != "t2" {
			t.Fatalf("rows: %+v", rows)
		}
		wantTime(t, "FirstSeen kept across tokens", rows[1].FirstSeen, t0)
	})

	t.Run("ValuesPreserveArraysAndNull", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		want := map[string]string{"p.array": `[1,"two",{"three":3}]`, "p.null": `null`, "p.object": `{"a":[],"b":{}}`, "p.array.0": `1`}
		var values []ddm.StatusValue
		for path, json := range want {
			values = append(values, value(path, json))
		}
		putStatus(t, s, dev, report(true, t0, nil, values))
		r, err := s.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{})
		if err != nil || len(r.Items) != len(want) {
			t.Fatalf("values: %+v %v", r, err)
		}
		for _, v := range r.Items {
			if string(v.Value) != want[v.Path] {
				t.Fatalf("%s: %s", v.Path, v.Value)
			}
		}
		r.Items[0].Value[0] = '!'
		again, _ := s.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{})
		if again.Items[0].Value[0] == '!' {
			t.Fatal("StatusValues returned aliased bytes")
		}
	})

	t.Run("ErrorsAppendNewestFirst", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		u := report(false, t0, nil, nil)
		u.Errors = []ddm.StatusError{{StatusItem: "one", Reasons: []byte(`[1]`)}, {StatusItem: "two", Reasons: []byte(`[2]`)}}
		putStatus(t, s, dev, u)
		u = report(false, t0.Add(time.Minute), nil, nil)
		u.Errors = []ddm.StatusError{{StatusItem: "three", Reasons: []byte(`[3]`)}}
		putStatus(t, s, dev, u)
		r, err := s.StatusErrors(ctx, dev, paging.Page{Limit: 2})
		if err != nil || len(r.Items) != 2 || r.NextCursor != strconv.FormatInt(r.Items[1].Seq, 10) {
			t.Fatalf("first page: %+v %v", r, err)
		}
		if r.Items[0].StatusItem != "three" || r.Items[1].StatusItem != "two" || r.Items[0].Seq <= r.Items[1].Seq {
			t.Fatalf("order: %+v", r.Items)
		}
		wantTime(t, "three ReceivedAt", r.Items[0].ReceivedAt, t0.Add(time.Minute))
		wantTime(t, "two ReceivedAt", r.Items[1].ReceivedAt, t0)
		rest, err := s.StatusErrors(ctx, dev, paging.Page{Limit: 2, Cursor: r.NextCursor})
		if err != nil || len(rest.Items) != 1 || rest.Items[0].StatusItem != "one" || rest.NextCursor != "" {
			t.Fatalf("second page: %+v %v", rest, err)
		}
		if string(rest.Items[0].Reasons) != `[1]` {
			t.Fatalf("reasons: %s", rest.Items[0].Reasons)
		}
		rest.Items[0].Reasons[0] = '!'
		again, _ := s.StatusErrors(ctx, dev, paging.Page{Cursor: r.NextCursor})
		if again.Items[0].Reasons[0] == '!' {
			t.Fatal("StatusErrors returned aliased bytes")
		}
	})

	t.Run("ReportsRetentionKeepsNewestN", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		var seqs []int64
		for i := range 4 {
			out := putStatus(t, s, dev, report(i == 0, t0.Add(time.Duration(i)*time.Minute), nil, nil))
			if out.PrunedReports != 0 {
				t.Fatalf("report %d pruned %d", i, out.PrunedReports)
			}
			seqs = append(seqs, out.Seq)
		}
		u := report(false, t0.Add(4*time.Minute), nil, nil)
		u.KeepReports = 3
		out := putStatus(t, s, dev, u)
		if out.PrunedReports != 2 {
			t.Fatalf("pruned %d, want 2", out.PrunedReports)
		}
		r, err := s.StatusReports(ctx, dev, paging.Page{Limit: 2})
		if err != nil || len(r.Items) != 2 || r.Items[0].Seq != out.Seq || r.Items[1].Seq != seqs[3] || r.NextCursor != strconv.FormatInt(seqs[3], 10) {
			t.Fatalf("first page: %+v %v", r, err)
		}
		if r.Items[0].FullReport || string(r.Items[0].Raw) != string(u.Raw) {
			t.Fatalf("newest report: %+v", r.Items[0])
		}
		wantTime(t, "newest ReceivedAt", r.Items[0].ReceivedAt, t0.Add(4*time.Minute))
		rest, err := s.StatusReports(ctx, dev, paging.Page{Cursor: r.NextCursor})
		if err != nil || len(rest.Items) != 1 || rest.Items[0].Seq != seqs[2] || rest.NextCursor != "" {
			t.Fatalf("second page: %+v %v", rest, err)
		}
		rest.Items[0].Raw[0] = '!'
		again, _ := s.StatusReports(ctx, dev, paging.Page{Cursor: r.NextCursor})
		if again.Items[0].Raw[0] == '!' {
			t.Fatal("StatusReports returned aliased bytes")
		}
		// Retention counts the report being stored: keeping one leaves the
		// newest only.
		u = report(false, t0.Add(5*time.Minute), nil, nil)
		u.KeepReports = 1
		out = putStatus(t, s, dev, u)
		all, _ := s.StatusReports(ctx, dev, paging.Page{})
		if out.PrunedReports != 3 || len(all.Items) != 1 || all.Items[0].Seq != out.Seq {
			t.Fatalf("keep one: %+v %+v", out, all)
		}
	})

	t.Run("ByIdentifierAcrossEnrollments", func(t *testing.T) {
		s := newStore(t)
		dev1, dev2, usr := Device(1), Device(2), User(1, "alice")
		for _, id := range []mdm.EnrollmentID{dev2, usr, dev1} {
			putStatus(t, s, id, report(true, t0, []ddm.DeclarationStatus{status(conf, "a", "t-"+id.ID, true, "valid"), status(conf, "b", "tb", true, "valid")}, nil))
		}
		r, err := s.DeclarationStatusByIdentifier(ctx, "a", paging.Page{Limit: 2})
		if err != nil || len(r.Items) != 2 || r.NextCursor != usr.ID {
			t.Fatalf("first page: %+v %v", r, err)
		}
		if r.Items[0].ID != dev1 || r.Items[1].ID != usr || r.Items[0].ServerToken != "t-"+dev1.ID || r.Items[1].Identifier != "a" {
			t.Fatalf("first page rows: %+v", r.Items)
		}
		rest, err := s.DeclarationStatusByIdentifier(ctx, "a", paging.Page{Limit: 2, Cursor: r.NextCursor})
		if err != nil || len(rest.Items) != 1 || rest.Items[0].ID != dev2 || rest.NextCursor != "" {
			t.Fatalf("second page: %+v %v", rest, err)
		}
		none, err := s.DeclarationStatusByIdentifier(ctx, "nope", paging.Page{})
		if err != nil || len(none.Items) != 0 {
			t.Fatalf("unknown identifier: %+v %v", none, err)
		}
	})

	t.Run("ValuesPrefixQueryPaginates", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		putStatus(t, s, dev, report(true, t0, nil, []ddm.StatusValue{
			value("device.c", "3"), value("device.a", "1"), value("management.x", "0"), value("device.b", "2"), value("devices.z", "9"),
		}))
		r, err := s.StatusValues(ctx, dev, ddm.StatusValueQuery{PathPrefix: "device."}, paging.Page{Limit: 2})
		if err != nil || len(r.Items) != 2 || r.Items[0].Path != "device.a" || r.Items[1].Path != "device.b" || r.NextCursor != "device.b" {
			t.Fatalf("first page: %+v %v", r, err)
		}
		rest, err := s.StatusValues(ctx, dev, ddm.StatusValueQuery{PathPrefix: "device."}, paging.Page{Limit: 2, Cursor: r.NextCursor})
		if err != nil || len(rest.Items) != 1 || rest.Items[0].Path != "device.c" || rest.NextCursor != "" {
			t.Fatalf("second page: %+v %v", rest, err)
		}
		wantStrings(t, "no prefix", valuePaths(t, s, dev, ""), []string{"device.a", "device.b", "device.c", "devices.z", "management.x"})
		// The prefix is plain text, not a pattern or a path segment.
		wantStrings(t, "partial segment", valuePaths(t, s, dev, "device"), []string{"device.a", "device.b", "device.c", "devices.z"})
		wantStrings(t, "pattern chars", valuePaths(t, s, dev, "device.%"), nil)
	})

	t.Run("BadCursor", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		putStatus(t, s, dev, report(true, t0, nil, nil))
		_, err := s.StatusErrors(ctx, dev, paging.Page{Cursor: "x"})
		wantErr(t, "StatusErrors bad cursor", err, ddm.ErrInvalid)
		_, err = s.StatusReports(ctx, dev, paging.Page{Cursor: "1.5"})
		wantErr(t, "StatusReports bad cursor", err, ddm.ErrInvalid)
	})
}
