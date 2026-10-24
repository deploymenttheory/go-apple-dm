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
func (q ResourceQuery) Validate() error {
	if q.Limit < 0 || q.Limit > 1000 || len(q.Conditions) > 64 {
		return ErrInvalid
	}
	if q.Focus != "" && q.Focus != "combined" && q.Focus != "apple" && q.Focus != "managed" {
		return ErrInvalid
	}
	for _, c := range q.Conditions {
		if !validSelector(c.Field) {
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

// Resources evaluates a bounded query over independent source records.
func (r *Repository) Resources(ctx context.Context, q ResourceQuery) (ResourcePage, error) {
	out := ResourcePage{Items: []ResourceRecord{}, AsOf: q.AsOf}
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
	after := "resource/" + q.Cursor
	for {
		entries, err := r.Backend.Scan(ctx, "resource/", after, 128)
		if err != nil {
			return out, err
		}
		for _, entry := range entries {
			after = entry.Key
			var d ResourceRecord
			if err := json.Unmarshal(entry.Value, &d); err != nil {
				return out, err
			}
			if !Matches(d, q) {
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

// EachResource streams all matching records without changing the caller's page options.
func (r *Repository) EachResource(ctx context.Context, q ResourceQuery, fn func(ResourceRecord) error) error {
	q.Limit = 250
	if q.AsOf.IsZero() {
		q.AsOf = time.Now().UTC()
	}
	for {
		p, e := r.Resources(ctx, q)
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

// Matches applies the same original-source predicates to lists and exports.
func Matches(d ResourceRecord, q ResourceQuery) bool {
	apple := strings.HasPrefix(d.Source.Kind, "axm.")
	if (q.AccountID != "" && d.Source.AccountID != q.AccountID) || (q.Source != "" && d.Source.Kind != q.Source) || (q.Focus == "apple" && !apple) || (q.Focus == "managed" && apple) {
		return false
	}
	if q.Search != "" {
		needle := strings.ToLower(q.Search)
		if !strings.Contains(strings.ToLower(d.ID+" "+d.Source.Kind+" "+d.Source.ResourceID), needle) {
			return false
		}
	}
	for _, c := range q.Conditions {
		value, ok := d.Value(c.Field)
		if c.Operator == "exists" {
			if ok != (string(c.Value) != "false") {
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

// Value reads an unambiguous metadata or original-data selector. meta.id is
// the repository identity; data.id is the original source field named id. Data
// paths use Flatten's dot/backslash escaping and never imply semantic aliases.
func (d ResourceRecord) Value(selector string) (json.RawMessage, bool) {
	var value any
	switch selector {
	case "meta.id":
		value = d.ID
	case "meta.source":
		value = d.Source.Kind
	case "meta.account_id":
		value = d.Source.AccountID
	case "meta.resource_id":
		value = d.Source.ResourceID
	case "meta.observed_at":
		value = d.Snapshot.ObservedAt
	case "meta.incomplete":
		value = d.Incomplete
	default:
		path, ok := strings.CutPrefix(selector, "data.")
		if !ok || path == "" {
			return nil, false
		}
		raw, ok := d.Snapshot.Fields[path]
		return raw, ok
	}
	raw, _ := json.Marshal(value)
	return raw, true
}

// validSelector keeps metadata and original source members in distinct namespaces.
func validSelector(selector string) bool {
	return len(selector) <= 1024 && (PublicField(selector) || strings.HasPrefix(selector, "data.") && len(selector) > len("data."))
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

// decodeJSON keeps integers above 2^53 exact in filters.
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

// canonicalJSON encodes an exact, type-tagged value for exact equality.
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
