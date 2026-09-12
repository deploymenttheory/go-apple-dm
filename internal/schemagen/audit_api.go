package schemagen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// APIChange identifies a removal or incompatible generated declaration change.
type APIChange struct {
	Name   string `json:"name"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// APIReport complements the exported-name guard with signatures and wire tags.
type APIReport struct {
	Added   []string    `json:"added"`
	Removed []string    `json:"removed"`
	Changed []APIChange `json:"changed"`
}

// CompareAPI reads generated Go declarations without compiling either tree.
// This keeps API evidence available when a candidate fails to build.
func CompareAPI(baseline, candidate string) (*APIReport, error) {
	before, err := generatedAPI(baseline)
	if err != nil {
		return nil, err
	}
	after, err := generatedAPI(candidate)
	if err != nil {
		return nil, err
	}
	report := &APIReport{Added: []string{}, Removed: []string{}, Changed: []APIChange{}}
	for name, signature := range before {
		current, ok := after[name]
		if !ok {
			report.Removed = append(report.Removed, name)
		} else if current != signature {
			report.Changed = append(
				report.Changed,
				APIChange{Name: name, Before: signature, After: current},
			)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			report.Added = append(report.Added, name)
		}
	}
	sort.Strings(report.Added)
	sort.Strings(report.Removed)
	sort.Slice(
		report.Changed,
		func(i, j int) bool { return report.Changed[i].Name < report.Changed[j].Name },
	)
	return report, nil
}

func generatedAPI(directory string) (map[string]string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("api audit: %w", err)
	}
	defer root.Close()
	result := map[string]string{}
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(name, ".gen.go") {
			return nil
		}
		data, readErr := root.ReadFile(name)
		if readErr != nil {
			return fmt.Errorf("read audit input: %w", readErr)
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), name, data, 0)
		if parseErr != nil {
			return fmt.Errorf("parse API input: %w", parseErr)
		}
		prefix := path.Dir(name) + "/"
		for _, decl := range file.Decls {
			recordAPIDecl(result, prefix, decl)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("api audit: %w", err)
	}
	return result, nil
}

func apiNode(n any) string {
	var b bytes.Buffer
	// All nodes have already passed Go's parser.
	_ = format.Node(&b, token.NewFileSet(), n)
	return b.String()
}

func apiFunction(f *ast.FuncType) string {
	fieldTypes := func(fields *ast.FieldList) string {
		if fields == nil {
			return ""
		}
		var parts []string
		for _, field := range fields.List {
			count := max(1, len(field.Names))
			for range count {
				parts = append(parts, apiNode(field.Type))
			}
		}
		return strings.Join(parts, ",")
	}
	return "[" + fieldTypes(
		f.TypeParams,
	) + "](" + fieldTypes(
		f.Params,
	) + ")(" + fieldTypes(
		f.Results,
	) + ")"
}

func recordAPIDecl(out map[string]string, prefix string, decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if !d.Name.IsExported() {
			return
		}
		name := d.Name.Name
		if d.Recv != nil && len(d.Recv.List) > 0 {
			name = apiNode(d.Recv.List[0].Type) + "." + name
		}
		out[prefix+name] = apiFunction(d.Type)
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				if !s.Name.IsExported() {
					continue
				}
				name := prefix + s.Name.Name
				if st, ok := s.Type.(*ast.StructType); ok {
					out[name] = "struct"
					for _, field := range st.Fields.List {
						signature := apiNode(field.Type)
						if field.Tag != nil {
							signature += " " + field.Tag.Value
						}
						if len(field.Names) == 0 {
							out[name+"."+apiNode(field.Type)] = signature
						}
						for _, fieldName := range field.Names {
							if fieldName.IsExported() {
								out[name+"."+fieldName.Name] = signature
							}
						}
					}
				} else {
					out[name] = apiNode(s.Type)
				}
				if s.Assign.IsValid() {
					out[name] = "alias " + out[name]
				}
			case *ast.ValueSpec:
				for i, name := range s.Names {
					if !name.IsExported() {
						continue
					}
					signature := d.Tok.String()
					if s.Type != nil {
						signature += " " + apiNode(s.Type)
					}
					if i < len(s.Values) {
						if d.Tok == token.CONST {
							signature += " = " + apiNode(s.Values[i])
						} else if literal, ok := s.Values[i].(*ast.CompositeLit); ok {
							signature += " " + apiNode(literal.Type)
						}
					}
					out[prefix+name.Name] = signature
				}
			}
		}
	}
}
