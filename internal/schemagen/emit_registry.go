package schemagen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func (e *emitter) registryFile() []byte {
	b := buf()
	b.WriteString(e.header())
	b.WriteString("import (\n\t\"github.com/deploymenttheory/go-apple-mdm/schema/support\"\n)\n\n")

	iface, method := e.familyInterface(), e.identifierMethod()
	fmt.Fprintf(b, "// %s is implemented by every top-level type in this package.\n", iface)
	fmt.Fprintf(
		b,
		"type %s interface {\n\t// %s returns the wire identifier from Apple's schema.\n\t%s() string\n",
		iface,
		method,
		method,
	)
	b.WriteString(
		"\t// SchemaPath returns the schema file the type was generated from.\n\tSchemaPath() string\n",
	)
	b.WriteString(
		"\t// Validate checks the value against the schema for the target.\n\tValidate(t support.Target) error\n}\n\n",
	)
	if e.pkg.Family == FamilyCommands {
		b.WriteString(
			"// Response is implemented by every command response type.\ntype Response interface {\n",
		)
		b.WriteString(
			"\t// ResponseRequestTypeName returns the RequestType this responds to.\n\tResponseRequestTypeName() string\n",
		)
		b.WriteString("\tSchemaPath() string\n\tValidate(t support.Target) error\n}\n\n")
	}
	if e.pkg.Family == FamilyDDM {
		b.WriteString(
			"// Kind classifies a declaration.\ntype Kind string\n\n// Declaration kinds.\nconst (\n",
		)
		b.WriteString(
			"\tKindActivation Kind = \"activation\"\n\tKindAsset Kind = \"asset\"\n\tKindConfiguration Kind = \"configuration\"\n",
		)
		b.WriteString(
			"\tKindManagement Kind = \"management\"\n\tKindCredential Kind = \"credential\"\n\tKindBase Kind = \"base\"\n)\n\n",
		)
	}

	b.WriteString("// Entry describes one schema in the Registry.\ntype Entry struct {\n")
	b.WriteString(
		"\t// ID is the wire identifier: RequestType, MessageType, PayloadType,\n\t// DeclarationType, StatusItemType, error code, or type name.\n\tID string\n",
	)
	b.WriteString(
		"\t// Schema is the YAML path in apple/device-management.\n\tSchema string\n\tTitle string\n",
	)
	if e.pkg.Family == FamilyDDM {
		b.WriteString("\tKind Kind\n")
	}
	fmt.Fprintf(
		b,
		"\t// New returns a zero value of the type as %s.\n\tNew func() %s\n",
		iface,
		iface,
	)
	if e.pkg.Family == FamilyCommands {
		b.WriteString(
			"\t// NewResponse returns a zero response value.\n\tNewResponse func() Response\n",
		)
	} else {
		b.WriteString(
			"\t// NewResponse returns a zero response value, or nil when the schema\n\t// defines no response keys.\n\tNewResponse func() any\n",
		)
	}
	b.WriteString("}\n\n")

	consts := e.constants()
	if len(consts) > 0 {
		b.WriteString("// Wire identifiers.\nconst (\n")
		for _, c := range consts {
			nosec := ""
			if looksLikeSecretName(c.name) {
				// gosec G101 matches identifier names such as *Credential*; these
				// are Apple wire identifiers, not secrets.
				nosec = " // #nosec G101 -- Apple wire identifier, not a credential"
			}
			fmt.Fprintf(
				b,
				"\t// %s: %s\n\t%s = %s%s\n",
				c.name,
				c.doc,
				c.name,
				strconv.Quote(c.value),
				nosec,
			)
		}
		b.WriteString(")\n\n")
	}

	b.WriteString(
		"// Registry maps Go type names to constructors, one entry per schema file.\n// Several schemas may share a wire identifier (for example six profile\n// payloads use com.apple.MCX), so look up by identifier with ByID.\nvar Registry = map[string]Entry{\n",
	)
	entries := e.registryEntries()
	for _, en := range entries {
		fmt.Fprintf(
			b,
			"\t%s: {ID: %s, Schema: %q, Title: %q, ",
			strconv.Quote(en.st.Name),
			strconv.Quote(en.id),
			en.st.Schema.Path,
			en.st.Schema.Title,
		)
		if e.pkg.Family == FamilyDDM {
			fmt.Fprintf(b, "Kind: %s, ", kindConst(en.st.Schema.Kind))
		}
		fmt.Fprintf(b, "New: func() %s { return new(%s) }", iface, en.st.Name)
		switch {
		case e.pkg.Family == FamilyCommands:
			fmt.Fprintf(b, ", NewResponse: func() Response { return new(%s) }", en.st.Response.Name)
		case en.st.Response != nil:
			fmt.Fprintf(b, ", NewResponse: func() any { return new(%s) }", en.st.Response.Name)
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n\n")

	b.WriteString(
		"// IDs returns the distinct wire identifiers in sorted order.\nfunc IDs() []string {\n\tseen := map[string]bool{}\n\tout := make([]string, 0, len(Registry))\n\tfor _, en := range Registry {\n\t\tif !seen[en.ID] {\n\t\t\tseen[en.ID] = true\n\t\t\tout = append(out, en.ID)\n\t\t}\n\t}\n\tsortStrings(out)\n\treturn out\n}\n\n",
	)
	b.WriteString(
		"// ByID returns every entry with the given wire identifier, sorted by type name.\nfunc ByID(id string) []Entry {\n\tvar names []string\n\tfor name, en := range Registry {\n\t\tif en.ID == id {\n\t\t\tnames = append(names, name)\n\t\t}\n\t}\n\tsortStrings(names)\n\tout := make([]Entry, 0, len(names))\n\tfor _, n := range names {\n\t\tout = append(out, Registry[n])\n\t}\n\treturn out\n}\n\n",
	)
	b.WriteString(
		"func sortStrings(s []string) {\n\tfor i := 1; i < len(s); i++ {\n\t\tfor j := i; j > 0 && s[j-1] > s[j]; j-- {\n\t\t\ts[j-1], s[j] = s[j], s[j-1]\n\t\t}\n\t}\n}\n\n",
	)

	for _, st := range e.pkg.Schemas {
		id, _ := e.identifier(st)
		fmt.Fprintf(
			b,
			"// %s returns %q.\nfunc (*%s) %s() string { return %q }\n\n",
			method,
			id,
			st.Name,
			method,
			id,
		)
		if e.pkg.Family == FamilyCommands {
			fmt.Fprintf(
				b,
				"// ResponseRequestTypeName returns %q.\nfunc (*%s) ResponseRequestTypeName() string { return %q }\n\n",
				id,
				st.Response.Name,
				id,
			)
		}
		if e.pkg.Family == FamilyDDM {
			fmt.Fprintf(
				b,
				"// DeclarationKind returns the declaration kind.\nfunc (*%s) DeclarationKind() Kind { return %s }\n\n",
				st.Name,
				kindConst(st.Schema.Kind),
			)
		}
	}
	return b.Bytes()
}

// looksLikeSecretName mirrors the identifier patterns gosec's G101 rule
// flags, so generated constants can carry an explicit annotation.
func looksLikeSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range []string{"passwd", "pass", "pwd", "secret", "token", "cred", "apikey", "bearer", "private"} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

type registryEntry struct {
	id string
	st *SchemaType
}

// registryEntries lists schemas with a wire identifier, sorted by id.
func (e *emitter) registryEntries() []registryEntry {
	var out []registryEntry
	for _, st := range e.pkg.Schemas {
		id, _ := e.identifier(st)
		if id == "" {
			continue
		}
		out = append(out, registryEntry{id: id, st: st})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].st.Name < out[j].st.Name })
	return out
}

func kindConst(k Kind) string {
	switch k {
	case KindActivation:
		return "KindActivation"
	case KindAsset:
		return "KindAsset"
	case KindConfiguration:
		return "KindConfiguration"
	case KindManagement:
		return "KindManagement"
	case KindCredential:
		return "KindCredential"
	case KindBase:
		return "KindBase"
	case KindDefault:
	}
	return "Kind(\"\")"
}
