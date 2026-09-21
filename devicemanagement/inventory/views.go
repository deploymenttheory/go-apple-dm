package inventory

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultColumns are stable, nonsensitive CSV fields; network identifiers are opt-in.
var DefaultColumns = []string{"id", "serial_number", "model", "product_family", "product_type", "os_version", "managed", "mdm_assigned", "order_number", "order_date", "purchase_source_type", "coverage_state", "coverage_expiry", "migration_status", "migration_deadline"}

// PublicRecord excludes unreviewed source data, embedded profiles and operational secrets.
// Complete evidence is available separately to callers with raw-inventory permission.
func PublicRecord(d DeviceRecord) DeviceRecord {
	out := d
	out.Coverage = make([]CoveragePlan, 0, len(d.Coverage))
	for _, plan := range d.Coverage {
		safe := CoveragePlan{ID: plan.ID, AccountID: plan.AccountID, Attributes: map[string]json.RawMessage{}}
		for _, key := range []string{"status", "paymentType", "description", "startDateTime", "endDateTime", "isRenewable", "isCanceled", "contractCancelDateTime", "agreementNumber"} {
			if v, ok := plan.Attributes[key]; ok {
				safe.Attributes[key] = v
			}
		}
		out.Coverage = append(out.Coverage, safe)
	}
	out.Fields = map[string]FieldValue{}
	out.Sources = map[string]Observation{}
	for k, v := range d.Fields {
		if PublicField(k) {
			out.Fields[k] = v
		}
	}
	for k, o := range d.Sources {
		o.Raw = nil
		o.Fields = nil
		out.Sources[k] = o
	}
	return out
}

// PublicField identifies deliberately reviewed projection names. Unknown fields require raw access.
func PublicField(name string) bool {
	if strings.Contains(name, ".") {
		return false
	}
	if name == "id" || strings.HasPrefix(name, "coverage_") {
		return true
	}
	for _, alias := range aliases {
		if alias == name {
			return true
		}
	}
	switch name {
	case "managed", "enrollment_id", "enrollment_channel", "enrolled_at", "last_seen", "disabled_at", "mdm_assigned", "mdm_server_id", "os_family", "identity_certificate_expiry":
		return true
	}
	return false
}

// Fields discovers every currently populated field, including future Apple attributes.
func (r *Repository) Fields(ctx context.Context, q DeviceQuery, raw bool) ([]string, error) {
	set := map[string]bool{}
	e := r.EachDevice(ctx, q, func(d DeviceRecord) error {
		for name := range d.Fields {
			if raw || PublicField(name) {
				set[name] = true
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	for _, name := range []string{"id", "coverage_state", "coverage_expiry"} {
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
	Devices     int                       `json:"devices"`
	Facets      map[string]map[string]int `json:"facets"`
	Unavailable []string                  `json:"unavailable"`
}

// Report derives fleet-relative major OS currency separately for each OS family.
func (r *Repository) Report(ctx context.Context, q DeviceQuery) (Report, error) {
	if q.AsOf.IsZero() {
		q.AsOf = time.Now().UTC()
	}
	out := Report{AsOf: q.AsOf, Facets: map[string]map[string]int{}, Unavailable: []string{"ram"}}
	maxOS := map[string]int{}
	family := func(d DeviceRecord) string {
		v := String(d.Fields["os_family"].Value)
		if v == "" {
			v = String(d.Fields["product_family"].Value)
		}
		return v
	}
	major := func(d DeviceRecord) int {
		n, _ := strconv.Atoi(strings.Split(String(d.Fields["os_version"].Value), ".")[0])
		return n
	}
	fleet := q
	fleet.Search = ""
	fleet.Conditions = nil
	fleet.Cursor = ""
	e := r.EachDevice(ctx, fleet, func(d DeviceRecord) error {
		f := family(d)
		if n := major(d); n > maxOS[f] {
			maxOS[f] = n
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	count := func(facet, value string) {
		if value == "" {
			value = "unknown"
		}
		if out.Facets[facet] == nil {
			out.Facets[facet] = map[string]int{}
		}
		out.Facets[facet][value]++
	}
	e = r.EachDevice(ctx, q, func(d DeviceRecord) error {
		out.Devices++
		for _, name := range []string{"product_family", "product_type", "model", "purchase_source_type", "managed", "mdm_assigned", "migration_status", "migration_capable", "filevault_enabled", "apple_silicon", "os_version"} {
			v := d.Fields[name].Value
			s := String(v)
			if s == "" && len(v) > 0 {
				s = string(v)
			}
			count(name, s)
		}
		coverage := d.CoverageAt(q.AsOf)
		count("coverage", coverage.State)
		if coverage.Stale {
			count("coverage_freshness", "stale")
		} else {
			count("coverage_freshness", "current")
		}
		sources := map[string]bool{}
		for _, o := range d.Sources {
			if !o.Absent && !o.Disconnected {
				sources[o.Source.Kind] = true
			}
		}
		for k := range sources {
			count("sources", k)
		}
		cloud, native := false, false
		for k := range sources {
			cloud = cloud || strings.HasPrefix(k, "axm.")
			native = native || k == "enrollment" || strings.HasPrefix(k, "mdm.") || strings.HasPrefix(k, "ddm.")
		}
		switch {
		case cloud && native:
			count("overlap", "apple_and_managed")
		case cloud:
			count("overlap", "apple_only")
		case native:
			count("overlap", "managed_only")
		}
		if n := major(d); n > 0 {
			delta := maxOS[family(d)] - n
			label := "older"
			if delta == 0 {
				label = "current"
			} else if delta == 1 {
				label = "n-1"
			} else if delta == 2 {
				label = "n-2"
			}
			count("os_currency."+family(d), label)
		} else {
			count("os_currency", "unknown")
		}
		if at, ok := Time(d.Fields["added_to_org"].Value); ok {
			count("added_year", strconv.Itoa(at.Year()))
		}
		if at, ok := Time(d.Fields["last_seen"].Value); ok {
			age := q.AsOf.Sub(at)
			bucket := "over_30_days"
			if age <= 24*time.Hour {
				bucket = "within_24_hours"
			} else if age <= 7*24*time.Hour {
				bucket = "within_7_days"
			} else if age <= 30*24*time.Hour {
				bucket = "within_30_days"
			}
			count("checkin", bucket)
		} else {
			count("checkin", "unknown")
		}
		for _, name := range []string{"coverage_expiry", "migration_deadline", "identity_certificate_expiry"} {
			if name == "migration_deadline" {
				state := String(d.Fields["migration_status"].Value)
				if state != "STARTED" && state != "IN_PROGRESS" {
					continue
				}
				if at, ok := Time(d.Fields[name].Value); !ok || !at.After(q.AsOf) {
					continue
				}
			}
			raw, ok := d.Value(name, q.AsOf)
			if !ok {
				count(name, "unknown")
				continue
			}
			at, ok := Time(raw)
			if !ok {
				count(name, "unknown")
				continue
			}
			days := at.Sub(q.AsOf).Hours() / 24
			bucket := "over_90_days"
			if days <= 0 {
				bucket = "expired"
			} else if days <= 30 {
				bucket = "next_30_days"
			} else if days <= 60 {
				bucket = "31_60_days"
			} else if days <= 90 {
				bucket = "61_90_days"
			}
			count(name, bucket)
		}
		return nil
	})
	return out, e
}

// Export streams JSON, NDJSON, or CSV using exactly the list query and requested column order.
// Raw controls access to unreviewed Apple payloads and source-qualified fields.
func (r *Repository) Export(ctx context.Context, w io.Writer, q DeviceQuery, format string, columns []string, raw bool) error {
	if q.AsOf.IsZero() {
		q.AsOf = time.Now().UTC()
	}
	if len(columns) == 0 {
		columns = DefaultColumns
	}
	for _, c := range columns {
		if !raw && !PublicField(c) {
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
	e := r.EachDevice(ctx, q, func(d DeviceRecord) error {
		if !raw {
			d = PublicRecord(d)
		}
		if csvw != nil {
			row := make([]string, len(columns))
			for i, c := range columns {
				if c == "id" {
					row[i] = d.ID
					continue
				}
				v, _ := d.Value(c, q.AsOf)
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
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Query   DeviceQuery `json:"query"`
	Columns []string    `json:"columns"`
	Format  string      `json:"format"`
}

// SavePreset validates and persists an ordered export projection and query.
func (r *Repository) SavePreset(ctx context.Context, p ExportPreset) (ExportPreset, error) {
	if p.Name == "" || p.Query.Validate() != nil || (p.ID != "" && !validID(p.ID)) || (p.Format != "csv" && p.Format != "json" && p.Format != "ndjson") {
		return p, ErrInvalid
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
	return r.EachDevice(ctx, DeviceQuery{}, func(d DeviceRecord) error {
		n++
		sources := map[string]any{}
		for _, o := range d.Sources {
			sources[o.Source.Kind] = map[string]any{"observed_at": o.ObservedAt, "attempted_at": o.AttemptedAt, "error": o.Error, "absent": o.Absent, "disconnected": o.Disconnected}
		}
		return json.NewEncoder(w).Encode(map[string]any{"device": fmt.Sprintf("<device %d>", n), "sources": sources, "conflict_count": len(d.Conflicts)})
	})
}
