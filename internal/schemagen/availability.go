package schemagen

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// availability maps every key use in one schema, by its key path, to the
// support Apple documents for that use: the key's own supportedOS block over
// the effective support of its enclosing key, ending at the payload block.
type availability map[string]map[support.OS]*support.OSSupport

// keyAvailability resolves the effective support of every key use in s from
// Apple's source keys. Paths follow the builder's Field.Path format; a struct
// shared by several uses has one Field.Path but an entry for every use.
func keyAvailability(s *Schema) (availability, error) {
	base, err := convertOS(nil, &s.Payload.SupportedOS)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	out := availability{}
	var walk func(keys []Key, prefix string, parent map[support.OS]*support.OSSupport) error
	walk = func(keys []Key, prefix string, parent map[support.OS]*support.OSSupport) error {
		for i := range keys {
			k := &keys[i]
			path := joinKeyPath(prefix, k.Key)
			eff, err := convertOS(parent, k.SupportedOS)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", s.Path, path, err)
			}
			out[path] = eff
			if err := walk(k.Subkeys, path, eff); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(s.PayloadKeys, "", base); err != nil {
		return nil, err
	}
	if err := walk(s.ResponseKeys, responsePrefix, base); err != nil {
		return nil, err
	}
	return out, nil
}

// responsePrefix marks response key paths, as the builder does.
const responsePrefix = "response:"

// joinKeyPath appends key to a builder key path.
func joinKeyPath(prefix, key string) string {
	if prefix == "" || prefix == responsePrefix {
		return prefix + key
	}
	return prefix + "." + key
}

// variantKeys returns the source keys of f's dictionary variants, in the
// order the builder emits f.Variants.
func variantKeys(f *Field) []string {
	if len(f.Variants) == 0 {
		return nil
	}
	var keys []string
	for i := range f.Src.Subkeys {
		v := &f.Src.Subkeys[i]
		if v.Type == "<dictionary>" && len(v.Subkeys) > 0 {
			keys = append(keys, v.Key)
		}
	}
	return keys
}
