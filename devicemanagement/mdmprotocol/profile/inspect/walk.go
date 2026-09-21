package inspect

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// Walk original keys, including explicitly present zero values and fields the
// typed decoder ignores. Maps/interfaces intentionally model Apple's ANY data.
func (r *Report) walk(raw any, typ reflect.Type, schemaPath, path string, target support.Target) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Slice {
		items, ok := raw.([]any)
		if !ok {
			return
		}
		for i, item := range items {
			r.walk(item, typ.Elem(), schemaPath, fmt.Sprintf("%s[%d]", path, i), target)
		}
		return
	}
	keys, ok := raw.(map[string]any)
	if ok && typ.Kind() == reflect.Map {
		// Dictionary keys may be arbitrary domains, while their values still
		// have modeled fields. Only interface-valued maps are fully open.
		if typ.Elem().Kind() == reflect.Interface {
			return
		}
		names := make([]string, 0, len(keys))
		for key := range keys {
			names = append(names, key)
		}
		sort.Strings(names)
		for _, key := range names {
			r.walk(keys[key], typ.Elem(), schemaPath+".ANY", join(path, key), target)
		}
		return
	}
	if !ok || typ.Kind() != reflect.Struct {
		return
	}
	if _, anyKeys := fieldType(typ, "ANY"); anyKeys {
		return
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	sort.Strings(names)
	for _, key := range names {
		childType, known := fieldType(typ, key)
		childPath := join(path, key)
		if !known {
			r.add(childPath, "unvalidated", "unknown", "key is not modeled by the selected schema")
			continue
		}
		childSchema := supportPath(schemaPath, key)
		r.checkSupport(childSchema, childPath, target)
		r.walk(keys[key], childType, childSchema, childPath, target)
	}
}

// Array item schema names (e.g. AirPrintItem) occur between the array and its
// keys in Apple's support table, but are absent from the actual plist.
func supportPath(parent, key string) string {
	exact := join(parent, key)
	if profiles.Support(exact) != nil {
		return exact
	}
	for _, candidate := range support.Paths("profiles") {
		middle, ok := strings.CutPrefix(candidate, parent+".")
		if ok && strings.Count(middle, ".") == 1 && strings.HasSuffix(middle, "."+key) {
			return candidate
		}
	}
	return exact
}

// checkSupport adds diagnostics when a payload or field is unavailable for the selected
// target.
func (r *Report) checkSupport(schemaPath, path string, target support.Target) {
	entry := profiles.Support(schemaPath)
	if entry == nil {
		return
	}
	result := entry.Check(target)
	if !result.Supported {
		r.add(path, "error", "support", result.Reason)
	} else if result.Deprecated {
		r.add(path, "warning", "deprecated", result.Reason)
	}
}
