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

// validateFile emits the schema-driven validation methods for one family.
func (e *emitter) validateFile() []byte {
	b := buf()
	b.WriteString(e.header())
	body := buf()
	var patterns []string
	for _, td := range e.pkg.Types {
		e.validateType(body, td, &patterns)
	}
	b.WriteString("import (\n")
	var std []string
	if len(patterns) > 0 {
		std = append(std, "regexp")
	}
	if bytes.Contains(body.Bytes(), []byte("strings.TrimSuffix(")) {
		std = append(std, "strings")
	}
	for _, name := range std {
		fmt.Fprintf(b, "\t%q\n", name)
	}
	if len(std) > 0 {
		b.WriteString("\n")
	}
	b.WriteString(
		"\t\"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support\"\n\t\"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation\"\n)\n\n",
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

// validateType emits field and relationship validation for one generated type.
func (e *emitter) validateType(b *bytes.Buffer, td *TypeDef, patterns *[]string) {
	if !td.Nested {
		fmt.Fprintf(
			b,
			"// Validate checks x against the schema. With a non-zero target it also checks\n// that every present key is supported on that OS version and enrollment context.\nfunc (x *%s) Validate(t support.Target) error {\n\tc := validation.New(t)\n\tx.validate(c, \"\", %q)\n\treturn c.Err()\n}\n\n",
			td.Name,
			rootSupportPath(td),
		)
	}
	// path is the wire-key path of the value, used in validation errors.
	// supportPath is the support table path of the key use being validated, so
	// a struct shared by several uses checks each against that use's entries.
	fmt.Fprintf(b, "func (x *%s) validate(c *validation.Collector, path, supportPath string) {\n\tif x == nil { c.Required(path, false); return }\n", td.Name)
	if !td.Nested {
		fmt.Fprintf(b, "\tc.Support(path, true, supportTable[%q])\n", td.Name)
	}
	if len(td.Fields) == 0 {
		b.WriteString("\t_, _, _ = c, path, supportPath\n}\n\n")
		return
	}
	e.reviewedValidation(b, td)
	for _, f := range td.Fields {
		if f.Src.LegacyRequired {
			alternative := legacyOptionalAlternatives[td.Schema.Path+"."+f.Key]
			fmt.Fprintf(
				b,
				"\tc.Required(%q, x.%s != \"\" || (x.%s != nil && *x.%s != \"\"))\n",
				f.Key+" or "+alternative,
				f.Name,
				GoName(alternative),
				GoName(alternative),
			)
		}
	}
	for _, f := range td.Fields {
		present, value := presenceExpr(f)
		key := f.Key
		if td.Leaf {
			key = ""
		}
		fmt.Fprintf(
			b,
			"\t{\n\t\tfieldPath := validation.Join(path, %q)\n\t\tpresent := %s\n\t\t_, _ = fieldPath, present\n",
			key,
			present,
		)
		if !td.Leaf {
			fmt.Fprintf(b, "\t\tc.Support(fieldPath, present, supportTable[supportPath+%q])\n", fieldSuffix(f))
		}
		if !f.Optional && !td.Leaf {
			b.WriteString("\t\tc.Required(fieldPath, present)\n")
		}
		if f.Kind == KindScalar && hasValueFloor(td, f) {
			fmt.Fprintf(b, "\t\tif present { c.Support(fieldPath, true, ValueSupport(supportPath+%q, %s)) }\n", fieldSuffix(f), value)
		}
		e.constraintChecks(b, f, value, "fieldPath", patterns)
		switch f.Kind {
		case KindStruct:
			fmt.Fprintf(b, "\t\tif present {\n\t\t\tx.%s.validate(c, fieldPath, %s)\n\t\t}\n", f.Name, e.childSupportPath(td, f.Struct, f.Base, fieldSuffix(f)))
		case KindArray:
			if f.Elem != nil {
				e.arrayChecks(b, f, patterns, td)
			}
			if len(f.Variants) > 0 {
				e.variantChecks(b, f)
			}
		case KindMap:
			if f.Elem != nil {
				e.mapChecks(b, f, patterns, td)
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
func (e *emitter) arrayChecks(b *bytes.Buffer, f *Field, patterns *[]string, td *TypeDef) {
	k := f.Src
	if k.Repetition != nil {
		fmt.Fprintf(
			b,
			"\t\tc.Repetition(fieldPath, present, len(x.%s), %d, %d)\n",
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
			"\t\tfor i := range x.%s {\n\t\t\tx.%s[i].validate(c, validation.Index(fieldPath, i), %s)\n\t\t}\n",
			f.Name,
			f.Name,
			e.childSupportPath(td, el.Struct, el.Base, elemSuffix(f)),
		)
	case KindScalar:
		if hasValueFloor(td, f) {
			fmt.Fprintf(b, "\t\tfor i, v := range x.%s { c.Support(validation.Index(fieldPath, i), true, ValueSupport(supportPath+%q, v)) }\n", f.Name, fieldSuffix(f))
		}
		ek := el.Src
		if el.Base == "string" && len(ek.Subkeys) > 0 {
			fmt.Fprintf(b, "\t\tfor i, v := range x.%s { c.Support(validation.Index(fieldPath, i), true, supportTable[supportPath+%q+v]) }\n", f.Name, elemSuffix(f)+".")
		}
		if len(ek.RangeList) > 0 || ek.Range != nil || (ek.Format != "" && el.Base == "string") {
			fmt.Fprintf(
				b,
				"\t\tfor i, v := range x.%s {\n\t\t\titemPath := validation.Index(fieldPath, i)\n",
				f.Name,
			)
			if len(ek.RangeList) > 0 &&
				(el.Base == "string" || el.Base == "int64" || el.Base == "float64") {
				fmt.Fprintf(b, "\t\t\tc.Enum(itemPath, true, v, []any{%s})\n", joinLiterals(ek.RangeList))
			}
			if ek.Range != nil && (el.Base == "int64" || el.Base == "float64") {
				fmt.Fprintf(
					b,
					"\t\t\tc.Range(itemPath, true, float64(v), %s, %s)\n",
					floatPtr(ek.Range.Min),
					floatPtr(ek.Range.Max),
				)
			}
			if ek.Format != "" && el.Base == "string" {
				if _, err := regexp.Compile(ek.Format); err == nil {
					fmt.Fprintf(
						b,
						"\t\t\tc.Pattern(itemPath, true, v, format%d)\n",
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
		"\t\tfor i, v := range x.%s {\n\t\t\titemPath := validation.Index(fieldPath, i)\n\t\t\tswitch t := v.(type) {\n",
		f.Name,
	)
	keys := variantKeys(f)
	for i, v := range f.Variants {
		variantPath := "supportPath+" + strconv.Quote(fieldSuffix(f)+"."+keys[i])
		fmt.Fprintf(
			b,
			"\t\t\tcase *%s:\n\t\t\t\tif t != nil {\n\t\t\t\t\tt.validate(c, itemPath, %s)\n\t\t\t\t}\n\t\t\tcase %s:\n\t\t\t\tt.validate(c, itemPath, %s)\n",
			v.Name,
			variantPath,
			v.Name,
			variantPath,
		)
	}
	b.WriteString("\t\t\t}\n\t\t}\n")
}

// mapChecks emits per-value checks for wildcard-key dictionaries.
func (e *emitter) mapChecks(b *bytes.Buffer, f *Field, patterns *[]string, td *TypeDef) {
	el := f.Elem
	switch el.Kind {
	case KindStruct:
		fmt.Fprintf(
			b,
			"\t\tfor k := range x.%s {\n\t\t\tv := x.%s[k]\n\t\t\tv.validate(c, validation.Join(fieldPath, k), %s)\n\t\t}\n",
			f.Name,
			f.Name,
			e.childSupportPath(td, el.Struct, el.Base, elemSuffix(f)),
		)
	case KindScalar:
		ek := el.Src
		if len(ek.RangeList) > 0 &&
			(el.Base == "string" || el.Base == "int64" || el.Base == "float64") {
			fmt.Fprintf(
				b,
				"\t\tfor k, v := range x.%s {\n\t\t\tc.Enum(validation.Join(fieldPath, k), true, v, []any{%s})\n\t\t}\n",
				f.Name,
				joinLiterals(ek.RangeList),
			)
		}
		if ek.Range != nil && (el.Base == "int64" || el.Base == "float64") {
			fmt.Fprintf(
				b,
				"\t\tfor k, v := range x.%s {\n\t\t\tc.Range(validation.Join(fieldPath, k), true, float64(v), %s, %s)\n\t\t}\n",
				f.Name,
				floatPtr(ek.Range.Min),
				floatPtr(ek.Range.Max),
			)
		}
		if ek.Format != "" && el.Base == "string" {
			if _, err := regexp.Compile(ek.Format); err == nil {
				fmt.Fprintf(
					b,
					"\t\tfor k, v := range x.%s {\n\t\t\tc.Pattern(validation.Join(fieldPath, k), true, v, format%d)\n\t\t}\n",
					f.Name,
					indexOf(patterns, ek.Format),
				)
			}
		}
	case KindArray, KindMap:
	}
}

// indexOf returns the pattern index, appending the pattern when it is not already
// recorded.
func indexOf(patterns *[]string, p string) int {
	for i, x := range *patterns {
		if x == p {
			return i
		}
	}
	*patterns = append(*patterns, p)
	return len(*patterns) - 1
}

// joinLiterals renders values as Go literals and joins them with commas.
func joinLiterals(vs []any) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, goLiteral(v))
	}
	return strings.Join(parts, ", ")
}

// floatPtr emits a Go expression for an optional floating-point value, using nil for an
// absent value.
func floatPtr(f *float64) string {
	if f == nil {
		return "nil"
	}
	return "new(" + goLiteral(*f) + ")"
}

// fieldSuffix is what validation appends to a struct's support path to check
// field f.
func fieldSuffix(f *Field) string { return "." + f.Key }

// elemSuffix is the support path suffix of f's array or map element.
func elemSuffix(f *Field) string { return fieldSuffix(f) + "." + f.Elem.Key }

// rootSupportPath is the support path of a top-level request or response.
func rootSupportPath(td *TypeDef) string {
	if td.IsResponse {
		return topName(td) + ".response"
	}
	return td.Name
}

// childSupportPath returns the Go expression for the supportPath argument of a
// nested struct value. A value reached through a recursive reference has its
// owner's type and is checked against the owner's use, which encloses the
// current struct: the owner's path is the current path without the keys between
// them.
func (e *emitter) childSupportPath(td, child *TypeDef, base, suffix string) string {
	if child != nil {
		return "supportPath+" + strconv.Quote(suffix)
	}
	owner := e.typeNamed(base)
	switch {
	case owner == nil:
		return "supportPath"
	case !owner.Nested:
		return strconv.Quote(rootSupportPath(owner))
	}
	if rel, ok := strings.CutPrefix(td.KeyPath, owner.KeyPath); ok && (rel == "" || rel[0] == '.') {
		if rel == "" {
			return "supportPath"
		}
		return "strings.TrimSuffix(supportPath, " + strconv.Quote(rel) + ")"
	}
	use := owner.KeyPath
	if owner.IsResponse {
		use = responsePrefix + use
	}
	return strconv.Quote(tablePath(topName(owner), use))
}

// typeNamed returns the package type with the given Go name, or nil.
func (e *emitter) typeNamed(name string) *TypeDef {
	for _, td := range e.pkg.Types {
		if td.Name == name {
			return td
		}
	}
	return nil
}
