package schemagen

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// Reviewed value availability supplements structured supportedOS metadata.
// Source: mdm/profiles/com.apple.extensiblesso.yaml at the adopted schema pin;
// OpenID and the Touch ID policy additions belong to macOS 27. Keep these floors
// separate from field relationships and never change Apple's vendored files.
var ssoValueFloors = map[string][]string{
	"ExtensibleSingleSignOn.AuthenticationMethod":                     {"OpenID"},
	"ExtensibleSingleSignOn.PlatformSSO.AuthenticationMethod":         {"OpenID"},
	"ExtensibleSingleSignOn.PlatformSSO.NewUserAuthenticationMethods": {"OpenID"},
	"ExtensibleSingleSignOn.PlatformSSO.FileVaultPolicy":              {"RequireTouchID", "RequireTouchIDOrWatch", "AllowOpenIDForTouchIDFallback"},
	"ExtensibleSingleSignOn.PlatformSSO.LoginPolicy":                  {"RequireTouchID", "RequireTouchIDOrWatch", "AllowOpenIDForTouchIDFallback"},
	"ExtensibleSingleSignOn.PlatformSSO.UnlockPolicy":                 {"RequireTouchID", "RequireTouchIDOrWatch", "AllowOpenIDForTouchIDFallback"},
}

func hasValueFloor(td *TypeDef, f *Field) bool {
	return td.Schema != nil && td.Schema.Path == "mdm/profiles/com.apple.extensiblesso.yaml" &&
		len(ssoValueFloors[supportPath(topName(td), f)]) > 0
}

// Value entries inherit all ordinary key requirements; a reviewed floor may
// raise the introduction version but cannot relax a newer structured boundary.
func reviewedValueSupport(st *SchemaType) map[string]map[string]*support.Entry {
	result := map[string]map[string]*support.Entry{}
	if st.Schema.Path != "mdm/profiles/com.apple.extensiblesso.yaml" {
		return result
	}
	table, err := effective(st)
	if err != nil {
		return result // supportFile already emits a compile error for this schema.
	}
	for path, values := range ssoValueFloors {
		base := table[path]
		if base == nil {
			continue // A historical schema may predate the containing field.
		}
		result[path] = map[string]*support.Entry{}
		for _, value := range values {
			entry := &support.Entry{Path: path + "=" + value, OS: map[support.OS]*support.OSSupport{}}
			for os, metadata := range base.OS {
				copy := *metadata
				floor := osversion.New(osversion.MacOS27, 0, 0)
				if os == support.MacOS && copy.Introduced.Compare(floor) < 0 {
					copy.Introduced = floor
				}
				entry.OS[os] = &copy
			}
			result[path][value] = entry
		}
	}
	return result
}

func (e *emitter) reviewedValues() map[string]map[string]*support.Entry {
	all := map[string]map[string]*support.Entry{}
	for _, st := range e.pkg.Schemas {
		for path, entries := range reviewedValueSupport(st) {
			all[path] = entries
		}
	}
	return all
}

func (e *emitter) valueSupport(b *bytes.Buffer) {
	all := e.reviewedValues()
	if len(all) == 0 {
		return
	}
	b.WriteString("// ValueSupport returns reviewed availability for a specific value of a key,\n// including inherited key requirements, or nil when no additional rule exists.\nfunc ValueSupport(path, value string) *support.Entry { return valueSupportTable[path][value] }\n\n")
	b.WriteString("// Reviewed supplements to Apple's structured supportedOS metadata.\nvar valueSupportTable = map[string]map[string]*support.Entry{\n")
	paths := make([]string, 0, len(all))
	for path := range all {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fmt.Fprintf(b, "\t%q: {\n", path)
		values := make([]string, 0, len(all[path]))
		for value := range all[path] {
			values = append(values, value)
		}
		sort.Strings(values)
		for _, value := range values {
			fmt.Fprintf(b, "\t\t%q: %s,\n", value, entryLiteral(all[path][value]))
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")
}
