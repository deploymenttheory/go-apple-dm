package schemagen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/schema/support"
)

// supportOS is a local alias for readability in tests.
type supportOS = support.OSSupport

// effective computes the support table for a schema: one entry for the
// schema itself (keyed by the top-level type name) and one per key path,
// with per-key blocks inheriting from the payload block and from parent
// keys, as docs/schema.md specifies.
func effective(st *SchemaType) (map[string]*support.Entry, error) {
	out := map[string]*support.Entry{}
	base, err := convertOS(nil, &st.Schema.Payload.SupportedOS)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", st.Schema.Path, err)
	}
	out[st.Name] = &support.Entry{Path: st.Name, OS: base}
	var walk func(td *TypeDef, parent map[support.OS]*support.OSSupport) error
	walk = func(td *TypeDef, parent map[support.OS]*support.OSSupport) error {
		if td == nil {
			return nil
		}
		for _, f := range td.Fields {
			eff, err := convertOS(parent, f.Src.SupportedOS)
			if err != nil {
				return fmt.Errorf("%s: %s: %w", st.Schema.Path, f.Path, err)
			}
			p := supportPath(st.Name, f)
			out[p] = &support.Entry{Path: p, OS: eff}
			if f.Struct != nil {
				if err := walk(f.Struct, eff); err != nil {
					return err
				}
			}
			if f.Elem != nil && f.Elem.Struct != nil {
				elemEff, err := convertOS(eff, f.Elem.Src.SupportedOS)
				if err != nil {
					return err
				}
				if err := walk(f.Elem.Struct, elemEff); err != nil {
					return err
				}
			}
			for _, v := range f.Variants {
				if err := walk(v, eff); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(st.Request, base); err != nil {
		return nil, err
	}
	if err := walk(st.Response, base); err != nil {
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

func overlay(dst *support.OSSupport, src *OSSupport) error {
	var err error
	if src.Introduced != "" {
		switch src.Introduced {
		case "n/a":
			dst.NotAvailable = true
			dst.Introduced = support.Version{}
		case "all":
			// Apple uses "all" for a key available on every version.
			dst.NotAvailable = false
			dst.Introduced = support.Version{}
		default:
			dst.NotAvailable = false
			if dst.Introduced, err = support.ParseVersion(src.Introduced); err != nil {
				return fmt.Errorf("introduced: %w", err)
			}
		}
	}
	if src.Deprecated != "" {
		if dst.Deprecated, err = support.ParseVersion(src.Deprecated); err != nil {
			return fmt.Errorf("deprecated: %w", err)
		}
	}
	if src.Removed != "" {
		if dst.Removed, err = support.ParseVersion(src.Removed); err != nil {
			return fmt.Errorf("removed: %w", err)
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
		dst.AllowedEnrollments = append([]string(nil), src.AllowedEnrollments...)
	}
	if src.AllowedScopes != nil {
		dst.AllowedScopes = append([]string(nil), src.AllowedScopes...)
	}
	if src.SharedIPad != nil {
		if src.SharedIPad.Mode != "" {
			dst.SharedIPadMode = support.Mode(src.SharedIPad.Mode)
		}
		copyBool(&dst.SharedIPadDeviceChannel, src.SharedIPad.DeviceChannel)
		copyBool(&dst.SharedIPadUserChannel, src.SharedIPad.UserChannel)
		if src.SharedIPad.AllowedScopes != nil {
			dst.SharedIPadScopes = append([]string(nil), src.SharedIPad.AllowedScopes...)
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

func (e *emitter) supportFile() []byte {
	b := buf()
	b.WriteString(e.header())
	b.WriteString("import (\n\t\"github.com/deploymenttheory/go-apple-mdm/schema/support\"\n)\n\n")
	b.WriteString(
		"// Support returns the support entry for a key path such as \"DeviceLock.Message\"\n// or \"DeviceLock.response.MessageResult\", or nil when unknown.\nfunc Support(path string) *support.Entry { return supportTable[path] }\n\n",
	)
	all := map[string]*support.Entry{}
	for _, st := range e.pkg.Schemas {
		table, err := effective(st)
		if err != nil {
			// Surface as a compile error in the generated file so it cannot be missed.
			fmt.Fprintf(b, "\t// ERROR: %v\n\t%q: nil,\n", err, st.Name)
			continue
		}
		for k, v := range table {
			all[k] = v
		}
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// The table is assembled from small functions: a single map literal
	// with thousands of nested entries exceeds compiler limits under race
	// and coverage instrumentation.
	const chunk = 100
	parts := (len(keys) + chunk - 1) / chunk
	b.WriteString(
		"// supportTable is the effective supportedOS data for every schema and key.\nvar supportTable = map[string]*support.Entry{}\n\n",
	)
	b.WriteString("func init() {\n\tfor _, part := range []func(map[string]*support.Entry){\n")
	for i := 0; i < parts; i++ {
		fmt.Fprintf(b, "\t\tsupportPart%d,\n", i)
	}
	fmt.Fprintf(
		b,
		"\t} {\n\t\tpart(supportTable)\n\t}\n\tsupport.Register(%q, supportTable)\n}\n\n",
		e.pkg.Name,
	)
	for i := 0; i < parts; i++ {
		fmt.Fprintf(b, "func supportPart%d(t map[string]*support.Entry) {\n", i)
		end := (i + 1) * chunk
		if end > len(keys) {
			end = len(keys)
		}
		for _, k := range keys[i*chunk : end] {
			fmt.Fprintf(b, "\tt[%s] = &support.Entry%s\n", strconv.Quote(k), entryLiteral(all[k]))
		}
		b.WriteString("}\n\n")
	}
	return b.Bytes()
}

func entryLiteral(en *support.Entry) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "{Path: %q, OS: map[support.OS]*support.OSSupport{", en.Path)
	for _, os := range support.AllOS {
		s := en.OS[os]
		if s == nil {
			continue
		}
		fmt.Fprintf(&sb, "support.%s: %s, ", osConst(os), osLiteral(s))
	}
	sb.WriteString("}}")
	return sb.String()
}

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

func osLiteral(s *support.OSSupport) string {
	var parts []string
	add := func(name, v string) { parts = append(parts, name+": "+v) }
	if s.NotAvailable {
		add("NotAvailable", "true")
	}
	if !s.Introduced.IsZero() {
		add("Introduced", versionLit(s.Introduced))
	}
	if !s.Deprecated.IsZero() {
		add("Deprecated", versionLit(s.Deprecated))
	}
	if !s.Removed.IsZero() {
		add("Removed", versionLit(s.Removed))
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

func versionLit(v support.Version) string {
	return fmt.Sprintf("support.V(%d, %d, %d)", v.Major, v.Minor, v.Patch)
}

func stringSlice(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(q, ", ") + "}"
}
