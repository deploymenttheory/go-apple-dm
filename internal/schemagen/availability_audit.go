package schemagen

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// AvailabilityAudit is the result of auditing one schema tree, merged with its
// retained releases, against the support entries its generated validation
// consults.
type AvailabilityAudit struct {
	// Uses is the number of key uses audited.
	Uses int
	// Problems names each key use whose consulted support entry is missing or
	// differs from the use's own source availability, and each recursive key
	// whose values would be validated with different availability from the use
	// they nest under. Each problem begins with the Apple schema file's path.
	Problems []string
}

// AuditAvailability loads the schema at root, merges each retained release
// newest first as generation does, and audits every key use. A non-empty
// Problems means generated validation would check some configuration against
// the wrong availability; an error means the schema could not be loaded or
// built.
func AuditAvailability(root string, histories ...string) (*AvailabilityAudit, error) {
	tree, err := loadMerged(root, histories...)
	if err != nil {
		return nil, err
	}
	pkgs, err := Build(tree)
	if err != nil {
		return nil, err
	}
	problems, uses, err := auditAvailability(pkgs, effective)
	if err != nil {
		return nil, err
	}
	recursive, err := auditRecursion(tree)
	if err != nil {
		return nil, err
	}
	return &AvailabilityAudit{Uses: uses, Problems: append(problems, recursive...)}, nil
}

// auditAvailability walks every key use of the built type model and reports
// each use whose generated validation consults a support entry that is missing
// or differs from the use's own source availability. Support paths are composed
// as the validation emitter composes them, so a struct shared by several uses
// is checked once per use against the entries that use's validation reads.
func auditAvailability(
	pkgs []*Package,
	tables func(*SchemaType) (map[string]*support.Entry, error),
) (problems []string, uses int, err error) {
	for _, pkg := range pkgs {
		for _, st := range pkg.Schemas {
			table, err := tables(st)
			if err != nil {
				return nil, 0, err
			}
			source, err := keyAvailability(st.Schema)
			if err != nil {
				return nil, 0, err
			}
			check := func(use, consulted string) {
				uses++
				want, got := source[use], table[consulted]
				switch {
				case want == nil:
					problems = append(problems, fmt.Sprintf("%s: no source key for use %s", st.Schema.Path, use))
				case got == nil:
					problems = append(problems, fmt.Sprintf("%s: use %s consults missing entry %s", st.Schema.Path, use, consulted))
				case !reflect.DeepEqual(want, got.OS):
					problems = append(problems, fmt.Sprintf("%s: use %s consults %s with different availability", st.Schema.Path, use, consulted))
				}
			}
			var walk func(td *TypeDef, prefix, supportPath string)
			walk = func(td *TypeDef, prefix, supportPath string) {
				if td == nil || td.Leaf {
					return
				}
				for _, f := range td.Fields {
					use := joinKeyPath(prefix, f.Key)
					check(use, supportPath+fieldSuffix(f))
					walk(f.Struct, use, supportPath+fieldSuffix(f))
					if f.Elem != nil {
						item := use + "." + f.Elem.Key
						walk(f.Elem.Struct, item, supportPath+elemSuffix(f))
						if f.Elem.Kind == KindScalar && f.Elem.Base == "string" {
							for _, value := range f.Elem.Src.Subkeys {
								check(item+"."+value.Key, supportPath+elemSuffix(f)+"."+value.Key)
							}
						}
						consulted := supportPath + elemSuffix(f)
						for el := f.Elem; el.Kind == KindArray && el.Elem != nil; el = el.Elem {
							item += "." + el.Elem.Key
							consulted += "." + el.Elem.Key
							walk(el.Elem.Struct, item, consulted)
						}
					}
					for i, key := range variantKeys(f) {
						walk(f.Variants[i], use+"."+key, supportPath+fieldSuffix(f)+"."+key)
					}
				}
			}
			walk(st.Request, "", rootSupportPath(st.Request))
			if st.Response != nil {
				walk(st.Response, responsePrefix, rootSupportPath(st.Response))
			}
		}
	}
	sort.Strings(problems)
	return problems, uses, nil
}

// auditRecursion reports recursive references whose values would be
// validated with different availability from the use they nest under.
// Generated validation checks a recursive value with its owner's struct, and
// therefore against the owner's entries, so the recursive key must make
// those values available exactly as the owner does.
func auditRecursion(tree *Tree) ([]string, error) {
	var problems []string
	for _, s := range tree.Schemas {
		source, err := keyAvailability(s)
		if err != nil {
			return nil, err
		}
		type ancestor struct {
			key  *Key
			path string
		}
		var walk func(keys []Key, prefix string, stack []ancestor) error
		walk = func(keys []Key, prefix string, stack []ancestor) error {
			for i := range keys {
				k := &keys[i]
				path := joinKeyPath(prefix, k.Key)
				if k.RecursiveTo != "" {
					var owner *ancestor
					for j := len(stack) - 1; j >= 0 && owner == nil; j-- {
						if stack[j].key.Key == k.RecursiveTo {
							owner = &stack[j]
						}
					}
					if owner == nil {
						return fmt.Errorf("%s: %s: recursive reference %q has no ancestor", s.Path, path, k.RecursiveTo)
					}
					// An array owner's values are its item dictionaries, whose
					// own block applies at both levels.
					want, got := source[owner.path], source[path]
					if owner.key.Type == "<array>" && len(owner.key.Subkeys) == 1 {
						item := &owner.key.Subkeys[0]
						want = source[owner.path+"."+item.Key]
						if got, err = convertOS(got, item.SupportedOS); err != nil {
							return err
						}
					}
					if !reflect.DeepEqual(want, got) {
						problems = append(problems, fmt.Sprintf("%s: recursive use %s differs from its owner %s", s.Path, path, owner.path))
					}
				}
				if err := walk(k.Subkeys, path, append(append([]ancestor(nil), stack...), ancestor{k, path})); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(s.PayloadKeys, "", nil); err != nil {
			return nil, err
		}
		if err := walk(s.ResponseKeys, responsePrefix, nil); err != nil {
			return nil, err
		}
	}
	sort.Strings(problems)
	return problems, nil
}
