package schemagen

import (
	"fmt"
	"sort"
)

// Apple added the alternative asset reference in OS 27. URL-based declarations
// remain valid on older systems; the asset's generated support gate rejects it
// before 27. Keep the established string fields for source compatibility.
var legacyOptionalAlternatives = map[string]string{
	"declarative/declarations/configurations/legacy.yaml.ProfileURL":             "ProfileAssetReference",
	"declarative/declarations/configurations/legacy.interactive.yaml.ProfileURL": "ProfileAssetReference",
}

// MergeHistory retains previously published keys while taking current support
// metadata and additions from the newer source. Apple deleting documentation
// does not delete older devices from a fleet. Retained keys keep Apple's last
// recorded availability, including explicit removal and unavailable markers.
// Neither input is modified. Incompatible wire types fail for explicit review.
func MergeHistory(current, previous *Tree) (*Tree, error) {
	out := &Tree{Root: current.Root}
	newSchemas := make(map[string]*Schema, len(current.Schemas))
	for _, schema := range current.Schemas {
		newSchemas[schema.Path] = schema
	}
	moves := historyMoves(current, previous)
	for _, old := range previous.Schemas {
		latest := newSchemas[old.Path]
		if latest == nil {
			latest = newSchemas[moves[old.Path]]
		}
		if latest == nil {
			latest = old
		}
		merged := *latest
		var err error
		merged.PayloadKeys, err = mergeKeys(latest.PayloadKeys, old.PayloadKeys, old.Path)
		if err != nil {
			return nil, err
		}
		merged.ResponseKeys, err = mergeKeys(
			latest.ResponseKeys,
			old.ResponseKeys,
			old.Path+"#response",
		)
		if err != nil {
			return nil, err
		}
		out.Schemas = append(out.Schemas, &merged)
		delete(newSchemas, latest.Path)
	}
	for _, schema := range newSchemas {
		out.Schemas = append(out.Schemas, schema)
	}
	sort.Slice(
		out.Schemas,
		func(i, j int) bool { return out.Schemas[i].Path < out.Schemas[j].Path },
	)
	return out, nil
}

// Match moved schemas only when both unmatched wire identities are unique.
// Apple intentionally has multiple documents with the same wire identifier.
func historyMoves(current, previous *Tree) map[string]string {
	oldPaths, newPaths := map[string]bool{}, map[string]bool{}
	for _, s := range previous.Schemas {
		oldPaths[s.Path] = true
	}
	for _, s := range current.Schemas {
		newPaths[s.Path] = true
	}
	oldIDs, newIDs := map[string][]string{}, map[string][]string{}
	identity := func(s *Schema) string { return string(s.Family) + ":" + s.Payload.Identifier() }
	for _, s := range previous.Schemas {
		if !newPaths[s.Path] && s.Payload.Identifier() != "" {
			oldIDs[identity(s)] = append(oldIDs[identity(s)], s.Path)
		}
	}
	for _, s := range current.Schemas {
		if !oldPaths[s.Path] && s.Payload.Identifier() != "" {
			newIDs[identity(s)] = append(newIDs[identity(s)], s.Path)
		}
	}
	moves := map[string]string{}
	for id, paths := range oldIDs {
		if len(paths) == 1 && len(newIDs[id]) == 1 {
			moves[paths[0]] = newIDs[id][0]
		}
	}
	return moves
}

func mergeKeys(current, previous []Key, path string) ([]Key, error) {
	keys := make(map[string]Key, len(current))
	for _, key := range current {
		keys[key.Key] = key
	}
	var out []Key
	// Preserve the established emission order so shared subkey types retain
	// their public names when Apple inserts a new use before an existing use.
	for _, old := range previous {
		key, exists := keys[old.Key]
		if !exists {
			key = old
		} else if key.Type != old.Type {
			return nil, fmt.Errorf(
				"%w: %s.%s wire type changed from %s to %s",
				ErrNaming,
				path,
				old.Key,
				old.Type,
				key.Type,
			)
		}
		if old.Required() && !key.Required() {
			if legacyOptionalAlternatives[path+"."+key.Key] == "" {
				return nil, fmt.Errorf(
					"%w: %s.%s presence change needs a version-aware validation rule",
					ErrNaming,
					path,
					old.Key,
				)
			}
			if _, scalar := scalarType(key.Type); !scalar {
				return nil, fmt.Errorf(
					"%w: %s.%s container presence changed",
					ErrNaming,
					path,
					old.Key,
				)
			}
			key.LegacyRequired = true
		}
		var err error
		prior := old.Subkeys
		// Single array-item names label schema structure rather than wire keys.
		// Follow Apple's current label without adding a fictitious second item.
		if key.Type == "<array>" && len(key.Subkeys) == 1 && len(prior) == 1 {
			prior = append([]Key(nil), prior...)
			prior[0].Key = key.Subkeys[0].Key
		}
		key.Subkeys, err = mergeKeys(key.Subkeys, prior, path+"."+key.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
		delete(keys, old.Key)
	}
	for _, key := range current {
		if _, exists := keys[key.Key]; exists {
			out = append(out, key)
		}
	}
	return out, nil
}
