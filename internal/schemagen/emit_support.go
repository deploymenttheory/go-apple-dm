package schemagen

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// supportOS is a local alias for readability in tests.
type supportOS = support.OSSupport

// effective computes the support table for a schema: one entry for the
// schema itself (keyed by the top-level type name) and one per key use, with
// per-key blocks inheriting from the payload block and from parent keys, as
// docs/schema.md specifies. A struct shared by several uses is walked once per
// use, and each use's entry comes from that use's own source keys.
func effective(st *SchemaType) (map[string]*support.Entry, error) {
	source, err := keyAvailability(st.Schema)
	if err != nil {
		return nil, err
	}
	base, err := convertOS(nil, &st.Schema.Payload.SupportedOS)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", st.Schema.Path, err)
	}
	out := map[string]*support.Entry{st.Name: {Path: st.Name, OS: base}}
	add := func(use string) error {
		os, ok := source[use]
		if !ok {
			return fmt.Errorf("%s: %s: no source key for generated field", st.Schema.Path, use)
		}
		p := tablePath(st.Name, use)
		out[p] = &support.Entry{Path: p, OS: os}
		return nil
	}
	var walk func(td *TypeDef, prefix string) error
	walk = func(td *TypeDef, prefix string) error {
		if td == nil {
			return nil
		}
		for _, f := range td.Fields {
			use := joinKeyPath(prefix, f.Key)
			if err := add(use); err != nil {
				return err
			}
			if err := walk(f.Struct, use); err != nil {
				return err
			}
			if f.Elem != nil {
				item := use + "." + f.Elem.Key
				if err := walk(f.Elem.Struct, item); err != nil {
					return err
				}
				// String arrays such as DeviceInformation.Queries describe their
				// named values as subkeys. They have no Go struct, but their
				// per-value availability must still be retained and enforced.
				if f.Elem.Kind == KindScalar && f.Elem.Base == "string" {
					for _, value := range f.Elem.Src.Subkeys {
						if err := add(item + "." + value.Key); err != nil {
							return err
						}
					}
				}
				// An array of arrays, such as HomeScreenLayout's Pages, nests
				// its structs one element deeper per level.
				for el := f.Elem; el.Kind == KindArray && el.Elem != nil; el = el.Elem {
					item += "." + el.Elem.Key
					if err := walk(el.Elem.Struct, item); err != nil {
						return err
					}
				}
			}
			for i, key := range variantKeys(f) {
				if err := walk(f.Variants[i], use+"."+key); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(st.Request, ""); err != nil {
		return nil, err
	}
	if err := walk(st.Response, responsePrefix); err != nil {
		return nil, err
	}
	return out, nil
}

// convertOS merges a key-level block over the parent's effective map.
func convertOS(
	parent map[support.OS]*support.OSSupport,
	block *SupportedOS,
) (map[support.OS]*support.OSSupport, error) {
	out := map[support.OS]*support.OSSupport{}
	for os, s := range parent {
		c := *s
		out[os] = &c
	}
	if block == nil {
		return out, nil
	}
	for _, name := range OSNames {
		b := block.ByName(name)
		if b == nil {
			continue
		}
		os := support.OS(name)
		cur := out[os]
		if cur == nil {
			cur = &support.OSSupport{}
			out[os] = cur
		}
		if err := overlay(cur, b); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return out, nil
}

// overlay applies explicitly supplied schema support fields over inherited metadata,
// parsing version boundaries and preserving omitted values.
func overlay(dst *support.OSSupport, src *OSSupport) error {
	var err error
	if src.Introduced != "" {
		switch src.Introduced {
		case "n/a":
			dst.NotAvailable = true
			dst.Introduced = osversion.Version{}
		case "all":
			// Apple uses "all" for a key available on every version.
			dst.NotAvailable = false
			dst.Introduced = osversion.Version{}
		default:
			dst.NotAvailable = false
			if dst.Introduced, err = osversion.Parse(src.Introduced); err != nil {
				return fmt.Errorf("introduced: %w", err)
			}
		}
	}
	if src.Deprecated != "" {
		if dst.Deprecated, err = osversion.Parse(src.Deprecated); err != nil {
			return fmt.Errorf("deprecated: %w", err)
		}
	}
	if src.Removed != "" {
		if dst.Removed, err = osversion.Parse(src.Removed); err != nil {
			return fmt.Errorf("removed: %w", err)
		}
		// Apple uses removed: '0' for withdrawn properties. Version's zero
		// value otherwise means no removal boundary, which would enable them.
		if dst.Removed.IsZero() {
			dst.NotAvailable = true
		}
	}
	if src.AccessRights != "" {
		dst.AccessRights = src.AccessRights
	}
	if src.Beta != nil {
		dst.Beta = *src.Beta
	}
	copyBool := func(d **bool, s *bool) {
		if s != nil {
			v := *s
			*d = &v
		}
	}
	copyBool(&dst.Multiple, src.Multiple)
	copyBool(&dst.DeviceChannel, src.DeviceChannel)
	copyBool(&dst.UserChannel, src.UserChannel)
	copyBool(&dst.Supervised, src.Supervised)
	copyBool(&dst.RequiresDEP, src.RequiresDEP)
	copyBool(&dst.UserApprovedMDM, src.UserApprovedMDM)
	copyBool(&dst.AllowManualInstall, src.AllowManualInstall)
	copyBool(&dst.AlwaysSkippable, src.AlwaysSkippable)
	if src.AllowedEnrollments != nil {
		dst.AllowedEnrollments = slices.Clone(src.AllowedEnrollments)
	}
	if src.AllowedScopes != nil {
		dst.AllowedScopes = slices.Clone(src.AllowedScopes)
	}
	if src.SharedIPad != nil {
		if src.SharedIPad.Mode != "" {
			dst.SharedIPadMode = support.Mode(src.SharedIPad.Mode)
		}
		copyBool(&dst.SharedIPadDeviceChannel, src.SharedIPad.DeviceChannel)
		copyBool(&dst.SharedIPadUserChannel, src.SharedIPad.UserChannel)
		if src.SharedIPad.AllowedScopes != nil {
			dst.SharedIPadScopes = slices.Clone(src.SharedIPad.AllowedScopes)
		}
	}
	if src.UserEnrollment != nil {
		if src.UserEnrollment.Mode != "" {
			dst.UserEnrollmentMode = support.Mode(src.UserEnrollment.Mode)
		}
		if src.UserEnrollment.Behavior != "" {
			dst.UserEnrollmentBehavior = src.UserEnrollment.Behavior
		}
	}
	return nil
}

// supportFile emits platform, enrollment, and availability metadata for the generated
// types.
func (e *emitter) supportFile() []byte {
	b := buf()
	b.WriteString(e.header())
	b.WriteString(
		"import (\n\t\"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion\"\n\t\"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support\"\n)\n\n",
	)
	b.WriteString(
		"// Support returns the support entry for a key path such as \"DeviceLock.Message\"\n// or \"DeviceLock.response.MessageResult\", or nil when unknown.\nfunc Support(path string) *support.Entry { return supportTable[path] }\n\n",
	)
	// Each schema's entries are added by their own function: a single map
	// literal with thousands of nested entries exceeds compiler limits under
	// race and coverage instrumentation.
	b.WriteString(
		"// supportTable is the effective supportedOS data for every schema and key use.\nvar supportTable = map[string]*support.Entry{}\n\n",
	)
	b.WriteString("func init() {\n")
	for _, st := range e.pkg.Schemas {
		fmt.Fprintf(b, "\t%s(supportTable)\n", addSupportFunc(st))
	}
	fmt.Fprintf(b, "\tsupport.Register(%q, supportTable)\n}\n\n", e.pkg.Name)
	for _, st := range e.pkg.Schemas {
		fmt.Fprintf(
			b,
			"// %s adds the support entries for %s and each of its key uses.\nfunc %s(table map[string]*support.Entry) {\n",
			addSupportFunc(st),
			st.Name,
			addSupportFunc(st),
		)
		table, err := effective(st)
		if err != nil {
			// Surface as a compile error in the generated file so it cannot be missed.
			fmt.Fprintf(b, "\t// ERROR: %v\n\t%q: nil,\n}\n\n", err, st.Name)
			continue
		}
		for _, path := range slices.Sorted(maps.Keys(table)) {
			fmt.Fprintf(b, "\ttable[%s] = &support.Entry%s\n", strconv.Quote(path), entryLiteral(table[path]))
		}
		b.WriteString("}\n\n")
	}
	e.valueSupport(b)
	source := b.String()
	if !strings.Contains(source, "osversion.") {
		source = strings.ReplaceAll(source, "\t\"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion\"\n", "")
	}
	return []byte(source)
}

// addSupportFunc names the generated function that adds a schema's entries to
// the support table.
func addSupportFunc(st *SchemaType) string { return "add" + st.Name + "Support" }

// entryLiteral renders a support metadata entry as a Go literal.
func entryLiteral(en *support.Entry) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "{Path: %q, OS: map[support.OS]*support.OSSupport{", en.Path)
	for _, os := range support.AllOS {
		s := en.OS[os]
		if s == nil {
			continue
		}
		fmt.Fprintf(&sb, "support.%s: %s, ", osConst(os), osLiteral(s, os))
	}
	sb.WriteString("}}")
	return sb.String()
}

// osConst returns a known OS constant name or a conversion expression for an
// unrecognized OS value.
func osConst(os support.OS) string {
	switch os {
	case support.IOS:
		return "IOS"
	case support.MacOS:
		return "MacOS"
	case support.TvOS:
		return "TvOS"
	case support.VisionOS:
		return "VisionOS"
	case support.WatchOS:
		return "WatchOS"
	}
	return "OS(" + strconv.Quote(string(os)) + ")"
}

// osLiteral renders an operating-system support entry as a Go literal.
func osLiteral(s *support.OSSupport, os support.OS) string {
	var parts []string
	add := func(name, v string) { parts = append(parts, name+": "+v) }
	if s.NotAvailable {
		add("NotAvailable", "true")
	}
	if !s.Introduced.IsZero() {
		add("Introduced", versionLit(s.Introduced, os))
	}
	if !s.Deprecated.IsZero() {
		add("Deprecated", versionLit(s.Deprecated, os))
	}
	if !s.Removed.IsZero() {
		add("Removed", versionLit(s.Removed, os))
	}
	if s.AccessRights != "" {
		add("AccessRights", strconv.Quote(s.AccessRights))
	}
	if s.Beta {
		add("Beta", "true")
	}
	boolp := func(name string, v *bool) {
		if v != nil {
			add(name, "support.Bool("+strconv.FormatBool(*v)+")")
		}
	}
	boolp("Multiple", s.Multiple)
	boolp("DeviceChannel", s.DeviceChannel)
	boolp("UserChannel", s.UserChannel)
	boolp("Supervised", s.Supervised)
	boolp("RequiresDEP", s.RequiresDEP)
	boolp("UserApprovedMDM", s.UserApprovedMDM)
	boolp("AllowManualInstall", s.AllowManualInstall)
	boolp("AlwaysSkippable", s.AlwaysSkippable)
	if s.AllowedEnrollments != nil {
		add("AllowedEnrollments", stringSlice(s.AllowedEnrollments))
	}
	if s.AllowedScopes != nil {
		add("AllowedScopes", stringSlice(s.AllowedScopes))
	}
	if s.SharedIPadMode != "" {
		add("SharedIPadMode", strconv.Quote(string(s.SharedIPadMode)))
	}
	boolp("SharedIPadDeviceChannel", s.SharedIPadDeviceChannel)
	boolp("SharedIPadUserChannel", s.SharedIPadUserChannel)
	if s.SharedIPadScopes != nil {
		add("SharedIPadScopes", stringSlice(s.SharedIPadScopes))
	}
	if s.UserEnrollmentMode != "" {
		add("UserEnrollmentMode", strconv.Quote(string(s.UserEnrollmentMode)))
	}
	if s.UserEnrollmentBehavior != "" {
		add("UserEnrollmentBehavior", strconv.Quote(s.UserEnrollmentBehavior))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// versionLit renders an OS version as a typed Go expression.
func versionLit(v osversion.Version, os support.OS) string {
	major := strconv.Itoa(v.Major)
	if os == support.MacOS {
		switch v.Major {
		case osversion.MacOS10, osversion.MacOS11, osversion.MacOS12, osversion.MacOS13,
			osversion.MacOS14, osversion.MacOS15, osversion.MacOS26, osversion.MacOS27:
			major = "osversion.MacOS" + major
		}
	}
	return fmt.Sprintf("osversion.New(%s, %d, %d)", major, v.Minor, v.Patch)
}

// stringSlice renders string values as a Go slice literal.
func stringSlice(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(q, ", ") + "}"
}
