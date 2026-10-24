package inventory

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"
	"time"
)

// TestOriginalAndMetadataSelectors keeps original field names independent from
// repository metadata across lookup, filters, reports, discovery and exports.
func TestOriginalAndMetadataSelectors(t *testing.T) {
	r := repository(t)
	now := time.Now().UTC()
	d := observe(t, r, "", "axm.device", "account", "opaque", `{"id":"opaque","type":"orgDevices","attributes":{},"meta":{"id":"vendor-meta"},"meta.id":"literal"}`, now)
	for selector, want := range map[string]string{"meta.id": d.ID, "meta.resource_id": "opaque", "data.id": "opaque", "data.meta.id": "vendor-meta", `data.meta\.id`: "literal"} {
		value, ok := d.Value(selector)
		if !ok || String(value) != want {
			t.Fatal("selector collision", selector, string(value), want, ok)
		}
	}
	for _, selector := range []string{"id", "source", "meta.unknown", "data."} {
		if _, ok := d.Value(selector); ok {
			t.Fatal("unqualified/unknown selector retained an alias", selector)
		}
		q := ResourceQuery{Conditions: []Condition{{selector, "exists", nil}}}
		if err := q.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatal("ambiguous selector accepted", selector, err)
		}
		if err := r.Export(t.Context(), io.Discard, ResourceQuery{}, "csv", []string{selector}, true); !errors.Is(err, ErrInvalid) {
			t.Fatal("ambiguous export selector accepted", selector, err)
		}
	}
	localID, err := json.Marshal(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := ResourceQuery{Conditions: []Condition{{"meta.id", "eq", localID}, {"data.id", "eq", json.RawMessage(`"opaque"`)}}}
	page, err := r.Resources(t.Context(), q)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("source and repository identity filters collided", page, err)
	}
	report, err := r.Report(t.Context(), q)
	if err != nil || report.Resources != 1 {
		t.Fatal(report, err)
	}
	var output bytes.Buffer
	columns := []string{"meta.id", "data.id", "data.meta.id", `data.meta\.id`}
	if err := r.Export(t.Context(), &output, q, "csv", columns, true); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&output).ReadAll()
	if err != nil || len(rows) != 2 || !slices.Equal(rows[0], columns) || !slices.Equal(rows[1], []string{d.ID, "opaque", "vendor-meta", "literal"}) {
		t.Fatal(rows, err)
	}
	if err := r.Export(t.Context(), io.Discard, q, "csv", columns, false); !errors.Is(err, ErrInvalid) {
		t.Fatal("original data bypassed raw authority", err)
	}
	fields, err := r.Fields(t.Context(), q, true)
	if err != nil || !slices.Contains(fields, "meta.id") || !slices.Contains(fields, "data.id") {
		t.Fatal(fields, err)
	}
	public, err := r.Fields(t.Context(), q, false)
	if err != nil || slices.Contains(public, "data.id") {
		t.Fatal("source selector leaked into metadata discovery", public, err)
	}
	if _, err := r.SavePreset(t.Context(), ExportPreset{Name: "ambiguous", Format: "csv", Columns: []string{"id"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unqualified preset accepted", err)
	}
}
