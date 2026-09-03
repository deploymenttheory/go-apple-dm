package schemagen

import (
	"fmt"
	"sort"
	"strconv"
)

// Apple publishes, for some schemas, the closed set of values that may appear
// in the Reasons array of a status report. The set is per schema rather than
// global: declarative/status/statusreason.yaml states that "each status item
// defines its own set of code, description, and details values", and the same
// code does appear under two status items with different prose. A declaration
// of a code is therefore kept with the schema that declares it.

// reasonDecl is one reason code as a single schema declares it.
type reasonDecl struct {
	code   string
	schema string
	reason Reason
}

// reasonConstName is the exported constant for a reason code.
func reasonConstName(code string) string { return "Reason" + GoName(code) }

// reasons returns every reason declaration in the package, sorted by code
// then by schema path so the emitted map is deterministic.
func (e *emitter) reasons() []reasonDecl {
	var out []reasonDecl
	for _, st := range e.pkg.Schemas {
		for _, r := range st.Schema.Reasons {
			out = append(out, reasonDecl{code: r.Value, schema: st.Schema.Path, reason: r})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].code != out[j].code {
			return out[i].code < out[j].code
		}
		return out[i].schema < out[j].schema
	})
	return out
}

// reasonCodes returns the distinct codes in the order reasons emits them.
func reasonCodes(decls []reasonDecl) []string {
	var out []string
	for i, d := range decls {
		if i == 0 || d.code != decls[i-1].code {
			out = append(out, d.code)
		}
	}
	return out
}

// reasonDescriptions returns the distinct descriptions declared for a code.
func reasonDescriptions(decls []reasonDecl, code string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range decls {
		if d.code != code || seen[d.reason.Description] {
			continue
		}
		seen[d.reason.Description] = true
		out = append(out, d.reason.Description)
	}
	return out
}

// reasonsFile emits the package's reason vocabulary, or nil when its schemas
// declare none.
func (e *emitter) reasonsFile() []byte {
	decls := e.reasons()
	if len(decls) == 0 {
		return nil
	}
	const width = 70
	b := buf()
	b.WriteString(e.header())

	b.WriteString(wrap("// ",
		"ReasonDetail is one key Apple documents in the details dictionary of a "+
			"reason. The keys a reason carries depend on the reason.", width))
	b.WriteString("type ReasonDetail struct {\n")
	b.WriteString("\t// Key is the wire key inside the details dictionary.\n\tKey string\n")
	b.WriteString("\t// Type is Apple's declared type for the value, such as \"<string>\".\n\tType string\n")
	b.WriteString("\t// Description is Apple's prose for the key.\n\tDescription string\n}\n\n")

	b.WriteString(wrap("// ",
		"ReasonEntry is one reason code as a single schema declares it. Apple "+
			"scopes the vocabulary to the schema, so a code that two schemas declare "+
			"has one entry per schema and the prose differs between them.", width))
	b.WriteString("type ReasonEntry struct {\n")
	b.WriteString("\t// Code is the wire value, matching the Code field of a status report reason.\n\tCode string\n")
	b.WriteString("\t// Description is Apple's prose for the code in this schema.\n\tDescription string\n")
	b.WriteString("\t// Schema is the YAML path in apple/device-management that declares it.\n\tSchema string\n")
	b.WriteString("\t// Details are the keys Apple documents in the reason's details dictionary.\n\tDetails []ReasonDetail\n}\n\n")

	codes := reasonCodes(decls)
	b.WriteString("// Reason codes.\nconst (\n")
	for _, code := range codes {
		name := reasonConstName(code)
		doc := fmt.Sprintf("%s is Apple's %q", name, code)
		// A code two schemas declare carries two meanings, so the constant
		// documents the divergence rather than picking one of them.
		if descs := reasonDescriptions(decls, code); len(descs) == 1 {
			doc += ": " + descs[0]
		} else {
			doc += ". Its meaning depends on the status item reporting it; see Reasons."
		}
		b.WriteString(wrap("\t// ", doc, width))
		fmt.Fprintf(b, "\t%s = %s\n", name, strconv.Quote(code))
	}
	b.WriteString(")\n\n")

	b.WriteString(wrap("// ",
		"Reasons maps each reason code to every schema declaration of it. A code "+
			"absent from the map is one Apple's schema does not define, which makes "+
			"the map the bound on the vocabulary for a caller labelling by code.", width))
	b.WriteString("var Reasons = map[string][]ReasonEntry{\n")
	for _, code := range codes {
		fmt.Fprintf(b, "\t%s: {\n", reasonConstName(code))
		for _, d := range decls {
			if d.code != code {
				continue
			}
			fmt.Fprintf(b, "\t\t{Code: %s, Description: %s, Schema: %s",
				reasonConstName(code), strconv.Quote(d.reason.Description), strconv.Quote(d.schema))
			if len(d.reason.Details) > 0 {
				b.WriteString(", Details: []ReasonDetail{\n")
				for _, det := range d.reason.Details {
					fmt.Fprintf(b, "\t\t\t{Key: %s, Type: %s, Description: %s},\n",
						strconv.Quote(det.Key), strconv.Quote(det.Type), strconv.Quote(det.Description))
				}
				b.WriteString("\t\t}")
			}
			b.WriteString("},\n")
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n\n")

	b.WriteString("// ReasonCodes returns the distinct reason codes in sorted order.\n")
	b.WriteString("func ReasonCodes() []string {\n\tout := make([]string, 0, len(Reasons))\n")
	b.WriteString("\tfor code := range Reasons {\n\t\tout = append(out, code)\n\t}\n")
	b.WriteString("\tsortStrings(out)\n\treturn out\n}\n")
	return b.Bytes()
}
