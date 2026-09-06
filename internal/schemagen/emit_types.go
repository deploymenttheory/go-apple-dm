package schemagen

import (
	"bytes"
	"fmt"
	"strings"
)

func (e *emitter) typesFile() []byte {
	b := buf()
	b.WriteString(e.header())
	needTime, needJSON := false, false
	for _, td := range e.pkg.Types {
		if td.Leaf {
			needJSON = true
		}
		for _, f := range td.Fields {
			if strings.Contains(f.GoType, "time.Time") {
				needTime = true
			}
		}
	}
	if needTime || needJSON {
		b.WriteString("import (\n")
		if needJSON {
			b.WriteString("\t\"encoding/json\"\n")
		}
		if needTime {
			b.WriteString("\t\"time\"\n")
		}
		b.WriteString(")\n\n")
	}
	for _, td := range e.pkg.Types {
		e.typeDecl(b, td)
	}
	return b.Bytes()
}

func (e *emitter) typeDecl(b *bytes.Buffer, td *TypeDef) {
	doc := strings.TrimSpace(td.Doc)
	if doc == "" {
		doc = "is generated from " + td.Schema.Path + "."
	}
	b.WriteString(wrap("// ", td.Name+": "+doc, 90))
	if !td.Nested {
		fmt.Fprintf(
			b,
			"//\n// %s corresponds to %s (%s).\n",
			td.Name,
			td.Schema.Path,
			td.Schema.Title,
		)
	}
	if td.Leaf {
		b.WriteString(
			"//\n// The wire value is the Value field itself; this type exists so the status\n// item has a name, a schema path, and support metadata.\n",
		)
	}
	fmt.Fprintf(b, "type %s struct {\n", td.Name)
	for _, f := range td.Fields {
		if d := wrap("\t// ", f.Doc, 88); d != "" {
			b.WriteString(d)
		}
		if td.Leaf {
			fmt.Fprintf(b, "\t%s %s `plist:\"-\" json:\"-\"`\n", f.Name, f.GoType)
			continue
		}
		omit := ",omitempty"
		if !f.Optional && f.Kind == KindScalar && !f.Pointer && f.GoType != "[]byte" &&
			f.GoType != "any" {
			omit = ""
		}
		fmt.Fprintf(
			b,
			"\t%s %s `plist:\"%s%s\" json:\"%s%s\"`\n",
			f.Name,
			f.GoType,
			f.Key,
			omit,
			f.Key,
			omit,
		)
	}
	b.WriteString("}\n\n")
	if td.Leaf {
		f := td.Fields[0]
		fmt.Fprintf(
			b,
			"// MarshalJSON encodes the status value.\nfunc (x %s) MarshalJSON() ([]byte, error) { return json.Marshal(x.%s) }\n\n",
			td.Name,
			f.Name,
		)
		fmt.Fprintf(
			b,
			"// UnmarshalJSON decodes the status value.\nfunc (x *%s) UnmarshalJSON(data []byte) error { return json.Unmarshal(data, &x.%s) }\n\n",
			td.Name,
			f.Name,
		)
	}
	if !td.Nested {
		fmt.Fprintf(
			b,
			"// SchemaPath returns the Apple schema file this type was generated from.\nfunc (*%s) SchemaPath() string { return %q }\n\n",
			td.Name,
			td.Schema.Path,
		)
	}
}
