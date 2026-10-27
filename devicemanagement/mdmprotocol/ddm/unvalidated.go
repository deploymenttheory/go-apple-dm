package ddm

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"maps"
	"reflect"
	"slices"
	"strings"

	schemaddm "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// reportUnknownKeys records each payload member that the declaration's
// generated type does not define as unvalidated. The device receives such a
// member unchanged, but it has no availability data. Members are named by
// schema key path, so map keys, which are configuration values, are never
// reported.
func reportUnknownKeys(c *validation.Collector, typed schemaddm.Declaration, payload jsontext.Value) {
	if c.Target().OS == "" {
		return
	}
	var doc any
	if json.Unmarshal(payload, &doc) != nil {
		return
	}
	typ := reflect.TypeOf(typed).Elem()
	walkUnknownKeys(c, typ.Name(), typ, doc)
}

// walkUnknownKeys reports the object members of value that typ, the generated
// type decoding value at the schema key path path, does not define. A map
// value's members are at path ANY and an array's items at its item key.
// Members are visited in name order, so warnings are reported in a stable
// order.
func walkUnknownKeys(c *validation.Collector, path string, typ reflect.Type, value any) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch v := value.(type) {
	case map[string]any:
		switch typ.Kind() {
		case reflect.Map:
			for _, name := range slices.Sorted(maps.Keys(v)) {
				walkUnknownKeys(c, path+".ANY", typ.Elem(), v[name])
			}
		case reflect.Struct:
			for _, name := range slices.Sorted(maps.Keys(v)) {
				field, ok := jsonField(typ, name)
				if !ok {
					c.Unvalidated(path+"."+name, path+"."+name)
					continue
				}
				walkUnknownKeys(c, path+"."+name, field.Type, v[name])
			}
		}
	case []any:
		if typ.Kind() == reflect.Slice {
			item := itemKeyPath(path)
			for _, element := range v {
				walkUnknownKeys(c, item, typ.Elem(), element)
			}
		}
	}
}

// jsonField returns the field of the generated struct type typ whose JSON
// member name is name.
func jsonField(typ reflect.Type, name string) (reflect.StructField, bool) {
	for field := range typ.Fields() {
		if tag, _, _ := strings.Cut(field.Tag.Get("json"), ","); tag == name {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

// itemKeyPath returns the schema key path of the items of the array at path,
// its child in the support table.
func itemKeyPath(path string) string {
	for _, p := range support.Paths("ddm") {
		if rest, ok := strings.CutPrefix(p, path+"."); ok {
			item, _, _ := strings.Cut(rest, ".")
			return path + "." + item
		}
	}
	return path
}
