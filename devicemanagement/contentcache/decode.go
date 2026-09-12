package contentcache

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"reflect"
	"strings"
)

// Decode reads one report, rejecting duplicate properties, invalid UTF-8,
// wrong types, and null known properties. Unknown properties are retained.
func Decode(data []byte) (*Report, error) {
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidReport, err)
	}
	if err := rejectKnownNulls(data, reflect.TypeFor[Report]()); err != nil {
		return nil, err
	}
	if err := report.Validate(); err != nil {
		return nil, err
	}
	return &report, nil
}

// Pointer fields preserve absence but JSON null is not allowed by this schema.
// Check only known properties: unknown extensions may legitimately contain null.
func rejectKnownNulls(data []byte, typ reflect.Type) error {
	var members map[string]jsontext.Value
	if err := json.Unmarshal(data, &members); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidReport, err)
	}
	if members == nil {
		return fmt.Errorf("%w: expected object", ErrInvalidReport)
	}
	for i := range typ.NumField() {
		field := typ.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		raw, present := members[name]
		if !present || name == "" {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%w: %s cannot be null", ErrInvalidReport, name)
		}
		if field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Struct {
			var items []jsontext.Value
			if err := json.Unmarshal(raw, &items); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidReport, err)
			}
			for _, item := range items {
				if err := rejectKnownNulls(item, field.Type.Elem()); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
		}
	}
	return nil
}
