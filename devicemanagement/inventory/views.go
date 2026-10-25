package inventory

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// DefaultColumns expose source metadata without disclosing original payloads.
var DefaultColumns = []string{"meta.id", "meta.source", "meta.account_id", "meta.resource_id", "meta.observed_at", "meta.incomplete"}

// PublicRecord removes all unreviewed source values and original payloads.
func PublicRecord(d ResourceRecord) ResourceRecord {
	d.Snapshot.Raw = nil
	d.Snapshot.Fields = nil
	d.Snapshot.FieldTimes = nil
	d.Snapshot.Native = nil
	return d
}

// PublicField identifies metadata columns available without raw-data authority.
func PublicField(name string) bool {
	switch name {
	case "meta.id", "meta.source", "meta.account_id", "meta.resource_id", "meta.observed_at", "meta.incomplete":
		return true
	default:
		return false
	}
}

// Fields discovers every currently populated field, including future Apple attributes.
func (r *Repository) Fields(ctx context.Context, q ResourceQuery, raw bool) ([]string, error) {
	set := map[string]bool{}
	e := r.EachResource(ctx, q, func(d ResourceRecord) error {
		for name := range d.Snapshot.Fields {
			if raw {
				set["data."+name] = true
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	for _, name := range DefaultColumns {
		set[name] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// Report provides countable facets with the same filter contract as lists and exports.
type Report struct {
	AsOf        time.Time                 `json:"as_of"`
	Resources   int                       `json:"resources"`
	Facets      map[string]map[string]int `json:"facets"`
	Unavailable []string                  `json:"unavailable"`
}

// Report counts source resources without inventing a physical fleet projection.
func (r *Repository) Report(ctx context.Context, q ResourceQuery) (Report, error) {
	if q.AsOf.IsZero() {
		q.AsOf = time.Now().UTC()
	}
	out := Report{AsOf: q.AsOf, Facets: map[string]map[string]int{"sources": {}, "completeness": {}}, Unavailable: []string{}}
	err := r.EachResource(ctx, q, func(d ResourceRecord) error {
		out.Resources++
		out.Facets["sources"][d.Source.Kind]++
		state := "complete"
		if d.Incomplete {
			state = "incomplete"
		}
		out.Facets["completeness"][state]++
		return nil
	})
	return out, err
}

// Export streams JSON, NDJSON, or CSV using exactly the list query and requested column order.
// Raw controls access to unreviewed Apple payloads and source-qualified fields.
func (r *Repository) Export(ctx context.Context, w io.Writer, q ResourceQuery, format string, columns []string, raw bool) error {
	if q.AsOf.IsZero() {
		q.AsOf = time.Now().UTC()
	}
	if len(columns) == 0 {
		columns = DefaultColumns
	}
	for _, c := range columns {
		if !validSelector(c) || !raw && !PublicField(c) {
			return ErrInvalid
		}
	}
	if format != "csv" && format != "json" && format != "ndjson" {
		return ErrInvalid
	}
	var csvw *csv.Writer
	if format == "csv" {
		csvw = csv.NewWriter(w)
		if e := csvw.Write(columns); e != nil {
			return e
		}
	}
	first := true
	if format == "json" {
		if _, e := io.WriteString(w, "["); e != nil {
			return e
		}
	}
	e := r.EachResource(ctx, q, func(d ResourceRecord) error {
		if !raw {
			d = PublicRecord(d)
		}
		if csvw != nil {
			row := make([]string, len(columns))
			for i, c := range columns {
				if c == "meta.id" {
					row[i] = d.ID
					continue
				}
				v, _ := d.Value(c)
				var s string
				if json.Unmarshal(v, &s) == nil {
					row[i] = s
				} else {
					row[i] = string(v)
				}
				if len(row[i]) > 0 && strings.ContainsRune("=+-@\t\r", rune(row[i][0])) {
					row[i] = "'" + row[i]
				}
			}
			return csvw.Write(row)
		}
		if format == "json" && !first {
			if _, e := io.WriteString(w, ","); e != nil {
				return e
			}
		}
		first = false
		if raw {
			// Carry exact original bytes alongside the readable JSON representation.
			return json.NewEncoder(w).Encode(struct {
				ResourceRecord
				Original []byte `json:"original_json,omitempty"`
			}{d, append([]byte(nil), d.Snapshot.Raw...)})
		}
		return json.NewEncoder(w).Encode(d)
	})
	if e != nil {
		return e
	}
	if csvw != nil {
		csvw.Flush()
		return csvw.Error()
	}
	if format == "json" {
		_, e = io.WriteString(w, "]\n")
	}
	return e
}

// ExportPreset stores an ordered projection and reusable filter.
type ExportPreset struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Query   ResourceQuery `json:"query"`
	Columns []string      `json:"columns"`
	Format  string        `json:"format"`
}

// SavePreset validates and persists an ordered export projection and query.
func (r *Repository) SavePreset(ctx context.Context, p ExportPreset) (ExportPreset, error) {
	if p.Name == "" || p.Query.Validate() != nil || (p.ID != "" && !validID(p.ID)) || (p.Format != "csv" && p.Format != "json" && p.Format != "ndjson") {
		return p, ErrInvalid
	}
	for _, column := range p.Columns {
		if !validSelector(column) {
			return p, ErrInvalid
		}
	}
	if p.ID == "" {
		p.ID = ID()
	}
	e := r.Backend.Update(ctx, func(tx Tx) error { return put(ctx, tx, "preset/"+p.ID, p) })
	return p, e
}

// Presets lists saved export definitions without executing their queries.
func (r *Repository) Presets(ctx context.Context) ([]ExportPreset, error) {
	out := []ExportPreset{}
	e := walk(ctx, r.Backend, "preset/", func(entry Entry) error {
		var p ExportPreset
		if e := json.Unmarshal(entry.Value, &p); e != nil {
			return e
		}
		out = append(out, p)
		return nil
	})
	return out, e
}

// Diagnostics exports a purpose-built redacted inventory summary. It never includes
// raw logs, source responses, hosts, account credentials, or device identifiers.
func (r *Repository) Diagnostics(ctx context.Context, w io.Writer) error {
	n := 0
	return r.EachResource(ctx, ResourceQuery{}, func(d ResourceRecord) error {
		n++
		return json.NewEncoder(w).Encode(map[string]any{"resource": fmt.Sprintf("<resource %d>", n), "kind": d.Source.Kind, "observed_at": d.Snapshot.ObservedAt, "incomplete": d.Incomplete})
	})
}
