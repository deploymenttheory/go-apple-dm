package schemagen

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Package is the intermediate representation of one generated Go package.
type Package struct {
	Family  Family
	Name    string        // Go package name
	Schemas []*SchemaType // one per YAML file, in path order
	Types   []*TypeDef    // every type to emit, in emission order
	used    map[string]bool
}

// SchemaType links a schema file to its generated top-level types.
type SchemaType struct {
	Schema   *Schema
	Name     string   // top-level Go type name
	Request  *TypeDef // payloadkeys type (always present)
	Response *TypeDef // responsekeys type; always present for commands, otherwise nil when no responsekeys
}

// TypeDef is one Go struct to emit.
type TypeDef struct {
	Name       string
	Doc        string
	Fields     []*Field
	Schema     *Schema
	KeyPath    string // dotted wire-key path of the dictionary this struct represents ("" for top level)
	IsResponse bool
	Nested     bool
	// Leaf is set for status items: the wire value is not a dictionary of
	// the struct's fields but the single Value field.
	Leaf bool
	// SubkeyType records the Apple subkeytype this struct was emitted for.
	SubkeyType string
	shape      string
}

// FieldKind classifies the Go representation of a field.
type FieldKind int

// Field kinds.
const (
	KindScalar FieldKind = iota // string, int64, float64, bool, time.Time, []byte, any
	KindStruct                  // named struct
	KindArray                   // slice
	KindMap                     // map[string]any
)

// Field is one struct field.
type Field struct {
	Name     string // Go field name
	Key      string // wire key
	GoType   string // full Go type expression, including pointer or slice
	Base     string // Go type without pointer or slice prefix
	Optional bool
	Pointer  bool
	Kind     FieldKind
	Elem     *Field     // for arrays: the element described as a field (Key is the item key)
	Variants []*TypeDef // for heterogeneous arrays: one struct per dictionary variant
	Struct   *TypeDef
	Src      *Key
	Path     string // dotted wire-key path used for support metadata
	Doc      string
}

// ErrNaming reports a naming collision or an unsupported schema shape.
var ErrNaming = errors.New("schemagen: naming")

// packageName maps a family to its Go package name.
func packageName(f Family) string { return string(f) }

// Build converts a loaded tree into packages. It fails on any type or field
// name collision so that overrides can be added deliberately.
func Build(tree *Tree) ([]*Package, error) {
	var pkgs []*Package
	var problems []string
	for _, fam := range AllFamilies {
		schemas := tree.ByFamily(fam)
		if len(schemas) == 0 {
			continue
		}
		p := &Package{Family: fam, Name: packageName(fam), used: map[string]bool{}}
		for _, s := range schemas {
			b := &builder{pkg: p, schema: s, subkeyTypes: map[string]*TypeDef{}}
			st, err := b.build()
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			p.Schemas = append(p.Schemas, st)
		}
		pkgs = append(pkgs, p)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%w: %s", ErrNaming, strings.Join(problems, "; "))
	}
	return pkgs, nil
}

// builder builds the types of one schema.
type builder struct {
	pkg         *Package
	schema      *Schema
	top         string
	subkeyTypes map[string]*TypeDef // subkeytype name -> emitted type (once per schema)
	fixups      []fixup
}

// frame is an ancestor on the key walk, used to resolve recursive references.
// owner is the subkeytype (or key name) of the key whose subkeys sequence an
// alias may point back to; field is the Field being built for that key, whose
// Base type is read once the whole schema is built (see fixup).
type frame struct {
	owner string
	field *Field
}

// fixup is a recursive reference to resolve after the schema is built.
type fixup struct {
	field    *Field
	owner    *Field
	key      *Key
	wrapElem bool // field is the array whose element is owner
}

// pushFrames appends the frame a key contributes, under its key name, so a
// cycle marker (see ownerName) resolves to the nearest ancestor with that
// key.
func pushFrames(stack []frame, k *Key, f *Field) []frame {
	return append(append([]frame(nil), stack...), frame{owner: k.Key, field: f})
}

func (b *builder) build() (*SchemaType, error) {
	b.top = TypeNameForSchema(b.schema)
	if b.pkg.used[b.top] {
		return nil, fmt.Errorf(
			"%w: %s: type %s already used in package %s",
			ErrNaming,
			b.schema.Path,
			b.top,
			b.pkg.Name,
		)
	}
	st := &SchemaType{Schema: b.schema, Name: b.top}
	var err error
	if b.schema.Family == FamilyStatus && b.schema.Payload.StatusItemType != "" {
		st.Request, err = b.buildStatusItem()
	} else {
		st.Request, err = b.buildStruct(b.top, b.schema.PayloadKeys, "", false, nil)
	}
	if err != nil {
		return nil, err
	}
	st.Request.Doc = b.schema.Description
	if len(b.schema.ResponseKeys) > 0 || b.schema.Family == FamilyCommands {
		st.Response, err = b.buildStruct(
			ResponseTypeName(b.top),
			b.schema.ResponseKeys,
			"",
			true,
			nil,
		)
		if err != nil {
			return nil, err
		}
		st.Response.Doc = "Response to " + b.top + "."
	}
	for _, fx := range b.fixups {
		// The recursive key's subkeys are the owner's subkeys, so it has the
		// owner's type: an array owner gives its full slice type, a
		// dictionary owner gives its struct.
		base := fx.owner.Base
		fx.field.Base = base
		if fx.wrapElem {
			fx.field.Kind = KindArray
			fx.field.Base = fx.owner.GoType
			fx.field.GoType = "[]" + fx.owner.GoType
			continue
		}
		switch fx.key.Type {
		case "<array>":
			fx.field.Kind = KindArray
			if fx.owner.Kind == KindArray {
				fx.field.GoType = strings.TrimPrefix(fx.owner.GoType, "*")
			} else {
				fx.field.GoType = "[]" + base
			}
		default:
			fx.field.Kind = KindStruct
			fx.field.GoType = base
			if fx.field.Optional && pointerable(base) {
				fx.field.GoType = "*" + base
				fx.field.Pointer = true
			}
		}
	}
	return st, nil
}

// buildStatusItem emits a struct with a single Value field carrying the
// status item's wire value, because status items are addressed by dotted
// path inside the StatusItems dictionary rather than as dictionaries.
func (b *builder) buildStatusItem() (*TypeDef, error) {
	if len(b.schema.PayloadKeys) != 1 {
		return nil, fmt.Errorf(
			"%w: %s: status item must have exactly one payload key, has %d",
			ErrNaming,
			b.schema.Path,
			len(b.schema.PayloadKeys),
		)
	}
	td := b.newType(b.top, "", false)
	td.Leaf = true
	k := &b.schema.PayloadKeys[0]
	f, err := b.field(td, k, "", nil)
	if err != nil {
		return nil, err
	}
	f.Name = "Value"
	f.Optional = false
	f.Pointer = false
	f.GoType = strings.TrimPrefix(f.GoType, "*")
	td.Fields = []*Field{f}
	return td, nil
}

// newType registers a type name and appends the TypeDef to the package.
func (b *builder) newType(name, keyPath string, isResp bool) *TypeDef {
	td := &TypeDef{
		Name:       name,
		Schema:     b.schema,
		KeyPath:    keyPath,
		IsResponse: isResp,
		Nested:     keyPath != "",
	}
	b.pkg.used[name] = true
	b.pkg.Types = append(b.pkg.Types, td)
	return td
}

// uniqueName returns the first unused candidate, then falls back to a
// numeric suffix. Candidates are tried in order so names stay stable.
func (b *builder) uniqueName(candidates ...string) string {
	for _, c := range candidates {
		if c != "" && !b.pkg.used[c] {
			return c
		}
	}
	base := candidates[len(candidates)-1]
	for i := 2; ; i++ {
		c := base + strconv.Itoa(i)
		if !b.pkg.used[c] {
			return c
		}
	}
}

func (b *builder) buildStruct(
	name string,
	keys []Key,
	keyPath string,
	isResp bool,
	stack []frame,
) (*TypeDef, error) {
	td := b.newType(name, keyPath, isResp)
	if err := b.fillStruct(td, keys, keyPath, stack); err != nil {
		return nil, err
	}
	return td, nil
}

func (b *builder) fillStruct(td *TypeDef, keys []Key, keyPath string, stack []frame) error {
	names := map[string]string{}
	for i := range keys {
		k := &keys[i]
		f, err := b.field(td, k, keyPath, stack)
		if err != nil {
			return err
		}
		if prev, dup := names[f.Name]; dup {
			return fmt.Errorf(
				"%w: %s: field %s (from %q) collides with %q in %s",
				ErrNaming,
				b.schema.Path,
				f.Name,
				k.Key,
				prev,
				td.Name,
			)
		}
		if reservedFieldNames[f.Name] {
			return fmt.Errorf(
				"%w: %s: field %s (from %q) collides with a generated method; add a fieldNameOverrides entry",
				ErrNaming,
				b.schema.Path,
				f.Name,
				k.Key,
			)
		}
		names[f.Name] = k.Key
		td.Fields = append(td.Fields, f)
	}
	return nil
}

// field builds one field, emitting nested types as needed.
func (b *builder) field(parent *TypeDef, k *Key, parentPath string, stack []frame) (*Field, error) {
	path := k.Key
	if parentPath != "" {
		path = parentPath + "." + k.Key
	}
	if parent.IsResponse && !strings.HasPrefix(path, "response:") {
		path = "response:" + path
	}
	f := &Field{Key: k.Key, Src: k, Path: path, Optional: !k.Required(), Doc: k.Content}
	f.Name = GoName(k.Key)
	if o, ok := fieldNameOverrides[b.schema.Path+"#"+path]; ok {
		f.Name = o
	}
	if err := b.resolveType(f, parent, k, path, stack); err != nil {
		return nil, err
	}
	return f, nil
}

// scalarType maps Apple scalar types to Go.
func scalarType(t string) (string, bool) {
	switch t {
	case "<string>":
		return "string", true
	case "<integer>":
		return "int64", true
	case "<real>":
		return "float64", true
	case "<boolean>":
		return "bool", true
	case "<date>":
		return "time.Time", true
	case "<data>":
		return "[]byte", true
	case "<any>":
		return "any", true
	}
	return "", false
}

// pointerable reports whether an optional field of this Go type becomes a
// pointer. Slices, maps, byte slices, and any already have a nil state.
func pointerable(goType string) bool {
	switch goType {
	case "[]byte", "any":
		return false
	}
	return !strings.HasPrefix(goType, "[]") && !strings.HasPrefix(goType, "map[")
}

func findFrame(stack []frame, owner string) *frame {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].owner == owner {
			return &stack[i]
		}
	}
	return nil
}

func (b *builder) resolveType(f *Field, parent *TypeDef, k *Key, path string, stack []frame) error {
	if k.RecursiveTo != "" {
		return b.resolveRecursive(f, k, path, stack)
	}
	if s, ok := scalarType(k.Type); ok {
		f.Kind = KindScalar
		f.Base = s
		f.GoType = s
		if f.Optional && pointerable(s) {
			f.GoType = "*" + s
			f.Pointer = true
		}
		return nil
	}
	switch k.Type {
	case "<dictionary>":
		if len(k.Subkeys) == 0 {
			f.Kind = KindMap
			f.Base = "map[string]any"
			f.GoType = f.Base
			return nil
		}
		if len(k.Subkeys) == 1 && strings.HasPrefix(k.Subkeys[0].Key, "ANY") {
			return b.resolveWildcardMap(f, parent, k, path, stack)
		}
		td, err := b.nested(parent, k, k, path, stack, f, f)
		if err != nil {
			return err
		}
		f.Kind = KindStruct
		f.Struct = td
		f.Base = td.Name
		f.GoType = td.Name
		if f.Optional {
			f.GoType = "*" + td.Name
			f.Pointer = true
		}
		return nil
	case "<array>":
		return b.resolveArray(f, parent, k, path, stack)
	}
	return fmt.Errorf("%w: %s: unsupported type %q at %s", ErrNaming, b.schema.Path, k.Type, path)
}

// resolveRecursive defers a recursive reference until the owner's type is
// known. The field temporarily carries the owner's name as its type so that
// shape comparisons and emission order are unaffected.
func (b *builder) resolveRecursive(f *Field, k *Key, path string, stack []frame) error {
	fr := findFrame(stack, k.RecursiveTo)
	if fr == nil || fr.field == nil {
		return fmt.Errorf(
			"%w: %s: recursive reference %q at %s has no ancestor",
			ErrNaming,
			b.schema.Path,
			k.RecursiveTo,
			path,
		)
	}
	if k.Type != "<array>" && k.Type != "<dictionary>" {
		return fmt.Errorf(
			"%w: %s: recursive reference on non-container %s at %s",
			ErrNaming,
			b.schema.Path,
			k.Type,
			path,
		)
	}
	f.GoType = "recursive:" + k.RecursiveTo
	f.Base = f.GoType
	b.fixups = append(b.fixups, fixup{field: f, owner: fr.field, key: k})
	return nil
}

// resolveWildcardMap handles dictionaries whose single subkey is named
// "ANY ..." in Apple's schema, meaning arbitrary keys with a common value
// shape: they become map[string]T.
func (b *builder) resolveWildcardMap(
	f *Field,
	parent *TypeDef,
	k *Key,
	path string,
	stack []frame,
) error {
	item := k.Subkeys[0]
	if item.SubkeyType == "" && k.SubkeyType != "" {
		item.SubkeyType = k.SubkeyType
	}
	elem := &Field{
		Key:  item.Key,
		Src:  &k.Subkeys[0],
		Path: path + "." + item.Key,
		Doc:  item.Content,
		Name: GoName(item.Key),
	}
	if item.Type == "<dictionary>" && len(item.Subkeys) > 0 &&
		(len(item.Subkeys) != 1 || !strings.HasPrefix(item.Subkeys[0].Key, "ANY")) {
		td, err := b.nested(parent, k, &item, path+"."+item.Key, stack, f, elem)
		if err != nil {
			return err
		}
		elem.Kind = KindStruct
		elem.Struct = td
		elem.Base = td.Name
		elem.GoType = td.Name
	} else {
		next := pushFrames(stack, k, f)
		if err := b.resolveType(elem, parent, &item, path+"."+item.Key, next); err != nil {
			return err
		}
		elem.GoType = strings.TrimPrefix(elem.GoType, "*")
		elem.Pointer = false
	}
	f.Kind = KindMap
	f.Elem = elem
	f.Base = elem.GoType
	f.GoType = "map[string]" + elem.GoType
	return nil
}

func (b *builder) resolveArray(
	f *Field,
	parent *TypeDef,
	k *Key,
	path string,
	stack []frame,
) error {
	f.Kind = KindArray
	switch len(k.Subkeys) {
	case 0:
		f.Base = "any"
		f.GoType = "[]any"
		return nil
	case 1:
	default:
		// Heterogeneous array (for example the Settings command): elements
		// are any; each dictionary variant gets its own struct so callers
		// can build typed values.
		f.Base = "any"
		f.GoType = "[]any"
		for i := range k.Subkeys {
			v := &k.Subkeys[i]
			if v.Type != "<dictionary>" || len(v.Subkeys) == 0 {
				continue
			}
			vf := &Field{Key: v.Key, Src: v, Path: path + "." + v.Key, Name: GoName(v.Key)}
			vt, err := b.nestedVariant(parent, k, v, path+"."+v.Key, stack, f, vf)
			if err != nil {
				return err
			}
			vf.Base, vf.GoType, vf.Kind, vf.Struct = vt.Name, vt.Name, KindStruct, vt
			f.Variants = append(f.Variants, vt)
		}
		return nil
	}
	item := k.Subkeys[0] // copy: the array's subkeytype may be inherited
	if item.SubkeyType == "" && k.SubkeyType != "" {
		item.SubkeyType = k.SubkeyType
	}
	elem := &Field{
		Key:  item.Key,
		Src:  &k.Subkeys[0],
		Path: path + "." + item.Key,
		Doc:  item.Content,
		Name: GoName(item.Key),
	}
	if item.Type == "<dictionary>" && len(item.Subkeys) > 0 {
		td, err := b.nested(parent, k, &item, path+"."+item.Key, stack, f, elem)
		if err != nil {
			return err
		}
		elem.Kind = KindStruct
		elem.Struct = td
		elem.Base = td.Name
		elem.GoType = td.Name
	} else {
		next := pushFrames(stack, k, f)
		if err := b.resolveType(elem, parent, &item, path+"."+item.Key, next); err != nil {
			return err
		}
		elem.GoType = strings.TrimPrefix(elem.GoType, "*")
		elem.Pointer = false
	}
	f.Elem = elem
	f.Base = elem.GoType
	f.GoType = "[]" + elem.GoType
	if strings.HasPrefix(elem.GoType, "recursive:") {
		// The element itself is recursive; its fixup runs first (fixups are
		// applied in order), then this one derives the array type from it.
		b.fixups = append(
			b.fixups,
			fixup{field: f, owner: elem, key: &Key{Type: "<array>"}, wrapElem: true},
		)
	}
	return nil
}

// nestedVariant emits the struct for one dictionary variant of a
// heterogeneous array, always named after the array key and the variant key.
func (b *builder) nestedVariant(
	parent *TypeDef,
	owner, dict *Key,
	path string,
	stack []frame,
	ownerField, dictField *Field,
) (*TypeDef, error) {
	name := b.uniqueName(parent.Name + GoName(owner.Key) + GoName(dict.Key))
	return b.emitStruct(name, parent, owner, dict, path, stack, ownerField, dictField)
}

// nested emits (or reuses) the struct for a dictionary described by dict,
// which is either the key itself or the element of the array key owner.
func (b *builder) nested(
	parent *TypeDef,
	owner, dict *Key,
	path string,
	stack []frame,
	ownerField, dictField *Field,
) (*TypeDef, error) {
	sig := shapeSig(dict)
	if dict.SubkeyType != "" {
		if td, ok := b.subkeyTypes[dict.SubkeyType]; ok && td.shape == sig {
			return td, nil
		}
	}
	name := b.nestedName(parent, owner, dict)
	td, err := b.emitStruct(name, parent, owner, dict, path, stack, ownerField, dictField)
	if err != nil {
		return nil, err
	}
	td.shape = sig
	if dict.SubkeyType != "" {
		if _, exists := b.subkeyTypes[dict.SubkeyType]; !exists {
			b.subkeyTypes[dict.SubkeyType] = td
		}
	}
	return td, nil
}

// emitStruct registers and fills a nested struct with the right frames.
func (b *builder) emitStruct(
	name string,
	parent *TypeDef,
	owner, dict *Key,
	path string,
	stack []frame,
	ownerField, dictField *Field,
) (*TypeDef, error) {
	keyPath := strings.TrimPrefix(path, "response:")
	td := b.newType(name, keyPath, parent.IsResponse)
	td.Doc = dict.Content
	td.SubkeyType = dict.SubkeyType
	next := pushFrames(stack, owner, ownerField)
	if owner != dict {
		next = pushFrames(next, dict, dictField)
	}
	if err := b.fillStruct(td, dict.Subkeys, keyPath, next); err != nil {
		return nil, err
	}
	return td, nil
}

// nestedName derives the struct name for a nested dictionary. Shared
// subkeytypes are named after the top-level type plus the subkeytype; other
// dictionaries after their enclosing struct plus their key. Array element
// dictionaries drop the item key when the array key alone is unique.
func (b *builder) nestedName(parent *TypeDef, owner, dict *Key) string {
	if dict.SubkeyType != "" {
		return b.uniqueName(
			b.top+GoName(dict.SubkeyType),
			parent.Name+GoName(owner.Key)+GoName(dict.Key),
		)
	}
	if owner == dict {
		return b.uniqueName(parent.Name + GoName(dict.Key))
	}
	return b.uniqueName(
		parent.Name+GoName(owner.Key),
		parent.Name+GoName(owner.Key)+GoName(dict.Key),
	)
}

// shapeSig is a structural signature of a key's subkeys used to decide
// whether a subkeytype can be reused.
func shapeSig(k *Key) string {
	var sb strings.Builder
	var walk func(keys []Key)
	walk = func(keys []Key) {
		sb.WriteByte('{')
		for i := range keys {
			sk := &keys[i]
			sb.WriteString(sk.Key)
			sb.WriteByte(':')
			sb.WriteString(sk.Type)
			if sk.Required() {
				sb.WriteByte('!')
			}
			if sk.RecursiveTo != "" {
				sb.WriteString("->" + sk.RecursiveTo)
			}
			walk(sk.Subkeys)
			sb.WriteByte(',')
		}
		sb.WriteByte('}')
	}
	walk(k.Subkeys)
	return sb.String()
}
