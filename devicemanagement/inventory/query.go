package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Validate rejects unsupported filters before storage is read.
func (q DeviceQuery) Validate() error {
	if q.Limit < 0 || q.Limit > 1000 || len(q.Conditions) > 64 {
		return ErrInvalid
	}
	if q.Focus != "" && q.Focus != "combined" && q.Focus != "apple" && q.Focus != "managed" {
		return ErrInvalid
	}
	for _, c := range q.Conditions {
		if c.Field == "" || len(c.Field) > 1024 {
			return ErrInvalid
		}
		switch c.Operator {
		case "exists":
			if len(c.Value) > 0 && string(c.Value) != "true" && string(c.Value) != "false" {
				return ErrInvalid
			}
		case "eq", "contains", "lt", "lte", "gt", "gte":
			if !json.Valid(c.Value) {
				return ErrInvalid
			}
		default:
			return fmt.Errorf("%w: filter operator", ErrInvalid)
		}
	}
	return nil
}

// Devices evaluates a bounded query, using a materialized exact-value index when available.
func (r *Repository) Devices(ctx context.Context, q DeviceQuery) (DevicePage, error) {
	out := DevicePage{Items: []DeviceRecord{}, AsOf: q.AsOf}
	if out.AsOf.IsZero() {
		out.AsOf = time.Now().UTC()
	}
	if err := q.Validate(); err != nil {
		return out, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	prefix := "device/"
	indexed := false
	for _, c := range q.Conditions {
		if (c.Operator == "eq" || c.Operator == "contains") && c.Field != "id" && q.AccountID == "" && (q.Focus == "" || q.Focus == "combined") && c.Field != "coverage_state" && c.Field != "coverage_expiry" {
			prefix = indexPrefix(c.Field, c.Value)
			indexed = true
			break
		}
	}
	after := prefix + q.Cursor
	for {
		entries, err := r.Backend.Scan(ctx, prefix, after, 128)
		if err != nil {
			return out, err
		}
		for _, entry := range entries {
			after = entry.Key
			var d DeviceRecord
			if indexed {
				var id string
				if err := json.Unmarshal(entry.Value, &id); err != nil {
					return out, err
				}
				d, err = r.Device(ctx, id)
			} else {
				err = json.Unmarshal(entry.Value, &d)
			}
			if err != nil {
				return out, err
			}
			d = focusRecord(d, q)
			if !Matches(d, q, out.AsOf) {
				continue
			}
			if len(out.Items) == limit {
				out.NextCursor = out.Items[len(out.Items)-1].ID
				return out, nil
			}
			out.Items = append(out.Items, d)
		}
		if len(entries) < 128 {
			return out, nil
		}
	}
}

// EachDevice streams all matching records without changing the caller's page options.
func (r *Repository) EachDevice(ctx context.Context, q DeviceQuery, fn func(DeviceRecord) error) error {
	q.Limit = 250
	if q.AsOf.IsZero() {
		q.AsOf = time.Now().UTC()
	}
	for {
		p, e := r.Devices(ctx, q)
		if e != nil {
			return e
		}
		for _, d := range p.Items {
			if e := fn(d); e != nil {
				return e
			}
		}
		if p.NextCursor == "" {
			return nil
		}
		q.Cursor = p.NextCursor
	}
}

// Matches applies the same predicates used by reports and exports.
func Matches(d DeviceRecord, q DeviceQuery, now time.Time) bool {
	apple, managed, account, source := false, false, q.AccountID == "", q.Source == ""
	for _, o := range d.Sources {
		if o.Absent || o.Disconnected {
			continue
		}
		apple = apple || strings.HasPrefix(o.Source.Kind, "axm.")
		managed = managed || o.Source.Kind == "enrollment"
		account = account || o.Source.AccountID == q.AccountID
		source = source || o.Source.Kind == q.Source
	}
	if !account || !source || (q.Focus == "apple" && !apple) || (q.Focus == "managed" && !managed) {
		return false
	}
	if q.Search != "" {
		needle := strings.ToLower(q.Search)
		found := strings.Contains(strings.ToLower(d.SerialNumber), needle) || strings.Contains(strings.ToLower(d.ID), needle)
		for name, f := range d.Fields {
			if !PublicField(name) {
				continue
			}
			found = found || strings.Contains(strings.ToLower(string(f.Value)), needle)
		}
		if !found {
			return false
		}
	}
	for _, c := range q.Conditions {
		value, ok := d.Value(c.Field, now)
		if c.Operator == "exists" {
			want := string(c.Value) != "false"
			if ok != want {
				return false
			}
			continue
		}
		if !ok || !compare(value, c) {
			return false
		}
	}
	return true
}

// Value resolves a projected field or a time-dependent coverage summary.
func (d DeviceRecord) Value(path string, now time.Time) (json.RawMessage, bool) {
	if path == "id" {
		v, _ := json.Marshal(d.ID)
		return v, true
	}
	if path == "coverage_state" {
		v, _ := json.Marshal(d.CoverageAt(now).State)
		return v, true
	}
	if path == "coverage_expiry" {
		v := d.CoverageAt(now).NextExpiry
		if v == nil {
			return nil, false
		}
		b, _ := json.Marshal(v)
		return b, true
	}
	f, ok := d.Fields[path]
	return f.Value, ok
}

// compare performs typed JSON comparisons rather than stringifying numbers.
func compare(raw json.RawMessage, c Condition) bool {
	var a, b any
	if decodeJSON(raw, &a) != nil || decodeJSON(c.Value, &b) != nil {
		return false
	}
	if c.Operator == "eq" {
		return equalJSON(a, b)
	}
	if c.Operator == "contains" {
		list, ok := a.([]any)
		if !ok {
			return false
		}
		for _, v := range list {
			if equalJSON(v, b) {
				return true
			}
		}
		return false
	}
	cmp := 0
	switch av := a.(type) {
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok {
			return false
		}
		left, right := rational(av), rational(bv)
		if left == nil || right == nil {
			return false
		}
		cmp = left.Cmp(right)
	case string:
		bv, ok := b.(string)
		if !ok {
			return false
		}
		at, ae := time.Parse(time.RFC3339Nano, av)
		bt, be := time.Parse(time.RFC3339Nano, bv)
		if ae == nil && be == nil {
			if at.Before(bt) {
				cmp = -1
			} else if at.After(bt) {
				cmp = 1
			}
		} else {
			cmp = strings.Compare(av, bv)
		}
	default:
		return false
	}
	switch c.Operator {
	case "lt":
		return cmp < 0
	case "lte":
		return cmp <= 0
	case "gt":
		return cmp > 0
	case "gte":
		return cmp >= 0
	}
	return false
}

// focusRecord derives a source-scoped projection rather than showing native values on Apple reports.
func focusRecord(d DeviceRecord, q DeviceQuery) DeviceRecord {
	if q.AccountID == "" && (q.Focus == "" || q.Focus == "combined") {
		return d
	}
	sources := map[string]Observation{}
	for k, o := range d.Sources {
		if q.AccountID != "" && o.Source.AccountID != q.AccountID {
			continue
		}
		apple := strings.HasPrefix(o.Source.Kind, "axm.")
		if q.Focus == "apple" && !apple {
			continue
		}
		if q.Focus == "managed" && apple {
			continue
		}
		sources[k] = o
	}
	d.Sources = sources
	rebuild(&d)
	return d
}

// decodeJSON keeps integers above 2^53 exact in filters and indexes.
func decodeJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}

// rational parses bounded JSON numbers without floating-point rounding.
func rational(n json.Number) *big.Rat {
	text := n.String()
	if len(text) > 128 {
		return nil
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exp, e := strconv.Atoi(text[i+1:])
		if e != nil || exp < -4096 || exp > 4096 {
			return nil
		}
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil
	}
	return value
}

// canonicalNode tags JSON types so a string can never collide with a normalized number.
func canonicalNode(v any) any {
	switch value := v.(type) {
	case json.Number:
		if n := rational(value); n != nil {
			return []any{"number", n.RatString()}
		}
		return []any{"number", value.String()}
	case map[string]any:
		keys := make([]string, 0, len(value))
		for k := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []any{"object"}
		for _, k := range keys {
			out = append(out, []any{k, canonicalNode(value[k])})
		}
		return out
	case []any:
		out := []any{"array"}
		for _, item := range value {
			out = append(out, canonicalNode(item))
		}
		return out
	default:
		return []any{"scalar", v}
	}
}

// canonicalJSON encodes an exact, type-tagged value for equality indexes.
func canonicalJSON(raw []byte) ([]byte, error) {
	var value any
	if e := decodeJSON(raw, &value); e != nil {
		return nil, e
	}
	return json.Marshal(canonicalNode(value))
}

// equalJSON compares JSON trees with exact numeric equivalence.
func equalJSON(a, b any) bool {
	left, _ := json.Marshal(canonicalNode(a))
	right, _ := json.Marshal(canonicalNode(b))
	return bytes.Equal(left, right)
}
