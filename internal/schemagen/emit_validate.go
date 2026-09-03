package schemagen

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// presenceExpr returns the Go expression that reports whether a field is
// present, and the expression yielding its value.
func presenceExpr(f *Field) (present, value string) {
	x := "x." + f.Name
	switch {
	case f.Pointer:
		return x + " != nil", "*" + x
	case f.Kind == KindArray || f.Kind == KindMap || f.GoType == "[]byte":
		return "len(" + x + ") > 0", x
	case f.GoType == "any":
		return x + " != nil", x
	case f.GoType == "time.Time":
		return "!" + x + ".IsZero()", x
	case f.GoType == "string":
		return x + ` != ""`, x
	}
	return "true", x
}

func (e *emitter) validateFile() []byte {
	b := buf()
	b.WriteString(e.header())
	body := buf()
	var patterns []string
	for _, td := range e.pkg.Types {
		e.validateType(body, td, &patterns)
	}
	b.WriteString("import (\n")
	if len(patterns) > 0 {
		b.WriteString("\t\"regexp\"\n\n")
	}
	b.WriteString(
		"\t\"github.com/deploymenttheory/go-apple-dm/schema/support\"\n\t\"github.com/deploymenttheory/go-apple-dm/schema/validation\"\n)\n\n",
	)
	if len(patterns) > 0 {
		b.WriteString("// Formats from the schema, compiled once.\nvar (\n")
		for i, p := range patterns {
			fmt.Fprintf(b, "\tformat%d = regexp.MustCompile(%s)\n", i, strconv.Quote(p))
		}
		b.WriteString(")\n\n")
	}
	b.Write(body.Bytes())
	return b.Bytes()
}

func (e *emitter) validateType(b *bytes.Buffer, td *TypeDef, patterns *[]string) {
	if !td.Nested {
		fmt.Fprintf(
			b,
			"// Validate checks x against the schema. With a non-zero target it also checks\n// that every present key is supported on that OS version and enrollment context.\nfunc (x *%s) Validate(t support.Target) error {\n\tc := validation.New(t)\n\tx.validate(c, \"\")\n\treturn c.Err()\n}\n\n",
			td.Name,
		)
	}
	fmt.Fprintf(b, "func (x *%s) validate(c *validation.Collector, p string) {\n", td.Name)
	if len(td.Fields) == 0 {
		b.WriteString("\t_, _ = c, p\n}\n\n")
		return
	}
	top := topName(td)
	for _, f := range td.Fields {
		present, value := presenceExpr(f)
		key := f.Key
		if td.Leaf {
			key = ""
		}
		fmt.Fprintf(
			b,
			"\t{\n\t\tpath := validation.Join(p, %q)\n\t\tpresent := %s\n\t\t_, _ = path, present\n",
			key,
			present,
		)
		if !td.Leaf {
			fmt.Fprintf(b, "\t\tc.Support(path, present, supportTable[%q])\n", supportPath(top, f))
		}
		if !f.Optional && !td.Leaf {
			b.WriteString("\t\tc.Required(path, present)\n")
		}
		e.constraintChecks(b, f, value, "path", patterns)
		switch f.Kind {
		case KindStruct:
			b.WriteString("\t\tif present {\n\t\t\tx." + f.Name + ".validate(c, path)\n\t\t}\n")
		case KindArray:
			if f.Elem != nil {
				e.arrayChecks(b, f, patterns)
			}
			if len(f.Variants) > 0 {
				e.variantChecks(b, f)
			}
		case KindMap:
			if f.Elem != nil {
				e.mapChecks(b, f, patterns)
			}
		case KindScalar:
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n\n")
}

// constraintChecks emits enum, range, and pattern checks for a scalar value
// expression.
func (e *emitter) constraintChecks(
	b *bytes.Buffer,
	f *Field,
	value, path string,
	patterns *[]string,
) {
	k := f.Src
	base := f.Base
	if f.Kind == KindArray && f.Elem != nil {
		return
	}
	var checks []string
	if len(k.RangeList) > 0 && (base == "string" || base == "int64" || base == "float64") {
		checks = append(
			checks,
			fmt.Sprintf("c.Enum(%s, true, %s, []any{%s})", path, value, joinLiterals(k.RangeList)),
		)
	}
	if k.Range != nil && (base == "int64" || base == "float64") {
		checks = append(
			checks,
			fmt.Sprintf(
				"c.Range(%s, true, float64(%s), %s, %s)",
				path,
				value,
				floatPtr(k.Range.Min),
				floatPtr(k.Range.Max),
			),
		)
	}
	if k.Format != "" && base == "string" {
		if _, err := regexp.Compile(k.Format); err != nil {
			fmt.Fprintf(b, "\t\t// format %q is not RE2-compatible: %v\n", k.Format, err)
		} else {
			idx := indexOf(patterns, k.Format)
			checks = append(
				checks,
				fmt.Sprintf("c.Pattern(%s, true, %s, format%d)", path, value, idx),
			)
		}
	}
	if k.Repetition != nil && f.Kind == KindArray {
		checks = append(
			checks,
			fmt.Sprintf(
				"c.Repetition(%s, true, len(%s), %d, %d)",
				path,
				value,
				k.Repetition.Min,
				k.Repetition.Max,
			),
		)
	}
	if len(checks) == 0 {
		return
	}
	// Guard every check with presence so pointer fields are never dereferenced when nil.
	b.WriteString("\t\tif present {\n")
	for _, c := range checks {
		b.WriteString("\t\t\t" + c + "\n")
	}
	b.WriteString("\t\t}\n")
}

// arrayChecks emits per-element checks.
func (e *emitter) arrayChecks(b *bytes.Buffer, f *Field, patterns *[]string) {
	k := f.Src
	if k.Repetition != nil {
		fmt.Fprintf(
			b,
			"\t\tc.Repetition(path, present, len(x.%s), %d, %d)\n",
			f.Name,
			k.Repetition.Min,
			k.Repetition.Max,
		)
	}
	el := f.Elem
	switch el.Kind {
	case KindStruct:
		fmt.Fprintf(
			b,
			"\t\tfor i := range x.%s {\n\t\t\tx.%s[i].validate(c, validation.Index(path, i))\n\t\t}\n",
			f.Name,
			f.Name,
		)
	case KindScalar:
		ek := el.Src
		if len(ek.RangeList) > 0 || ek.Range != nil || (ek.Format != "" && el.Base == "string") {
			fmt.Fprintf(
				b,
				"\t\tfor i, v := range x.%s {\n\t\t\tip := validation.Index(path, i)\n",
				f.Name,
			)
			if len(ek.RangeList) > 0 &&
				(el.Base == "string" || el.Base == "int64" || el.Base == "float64") {
				fmt.Fprintf(b, "\t\t\tc.Enum(ip, true, v, []any{%s})\n", joinLiterals(ek.RangeList))
			}
			if ek.Range != nil && (el.Base == "int64" || el.Base == "float64") {
				fmt.Fprintf(
					b,
					"\t\t\tc.Range(ip, true, float64(v), %s, %s)\n",
					floatPtr(ek.Range.Min),
					floatPtr(ek.Range.Max),
				)
			}
			if ek.Format != "" && el.Base == "string" {
				if _, err := regexp.Compile(ek.Format); err == nil {
					fmt.Fprintf(
						b,
						"\t\t\tc.Pattern(ip, true, v, format%d)\n",
						indexOf(patterns, ek.Format),
					)
				}
			}
			b.WriteString("\t\t}\n")
		}
	case KindArray, KindMap:
	}
}

// variantChecks emits a type switch validating each element of a
// heterogeneous array that is one of the generated variant structs.
func (e *emitter) variantChecks(b *bytes.Buffer, f *Field) {
	fmt.Fprintf(
		b,
		"\t\tfor i, v := range x.%s {\n\t\t\tip := validation.Index(path, i)\n\t\t\tswitch t := v.(type) {\n",
		f.Name,
	)
	for _, v := range f.Variants {
		fmt.Fprintf(
			b,
			"\t\t\tcase *%s:\n\t\t\t\tif t != nil {\n\t\t\t\t\tt.validate(c, ip)\n\t\t\t\t}\n\t\t\tcase %s:\n\t\t\t\tt.validate(c, ip)\n",
			v.Name,
			v.Name,
		)
	}
	b.WriteString("\t\t\t}\n\t\t}\n")
}

// mapChecks emits per-value checks for wildcard-key dictionaries.
func (e *emitter) mapChecks(b *bytes.Buffer, f *Field, patterns *[]string) {
	el := f.Elem
	switch el.Kind {
	case KindStruct:
		fmt.Fprintf(
			b,
			"\t\tfor k := range x.%s {\n\t\t\tv := x.%s[k]\n\t\t\tv.validate(c, validation.Join(path, k))\n\t\t}\n",
			f.Name,
			f.Name,
		)
	case KindScalar:
		ek := el.Src
		if len(ek.RangeList) > 0 &&
			(el.Base == "string" || el.Base == "int64" || el.Base == "float64") {
			fmt.Fprintf(
				b,
				"\t\tfor k, v := range x.%s {\n\t\t\tc.Enum(validation.Join(path, k), true, v, []any{%s})\n\t\t}\n",
				f.Name,
				joinLiterals(ek.RangeList),
			)
		}
		if ek.Range != nil && (el.Base == "int64" || el.Base == "float64") {
			fmt.Fprintf(
				b,
				"\t\tfor k, v := range x.%s {\n\t\t\tc.Range(validation.Join(path, k), true, float64(v), %s, %s)\n\t\t}\n",
				f.Name,
				floatPtr(ek.Range.Min),
				floatPtr(ek.Range.Max),
			)
		}
		if ek.Format != "" && el.Base == "string" {
			if _, err := regexp.Compile(ek.Format); err == nil {
				fmt.Fprintf(
					b,
					"\t\tfor k, v := range x.%s {\n\t\t\tc.Pattern(validation.Join(path, k), true, v, format%d)\n\t\t}\n",
					f.Name,
					indexOf(patterns, ek.Format),
				)
			}
		}
	case KindArray, KindMap:
	}
}

func indexOf(patterns *[]string, p string) int {
	for i, x := range *patterns {
		if x == p {
			return i
		}
	}
	*patterns = append(*patterns, p)
	return len(*patterns) - 1
}

func joinLiterals(vs []any) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, goLiteral(v))
	}
	return strings.Join(parts, ", ")
}

func floatPtr(f *float64) string {
	if f == nil {
		return "nil"
	}
	return "new(" + goLiteral(*f) + ")"
}
