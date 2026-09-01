// Package schemagen turns Apple's device management YAML schema
// (https://github.com/apple/device-management, vendored under
// third_party/device-management) into Go packages under schema/.
//
// The meta-schema is docs/schema.yaml in that repository and is documented at
// https://github.com/apple/device-management/blob/release/docs/schema.md.
// The loader is strict: any key not modelled here fails, so Apple additions
// surface as explicit work rather than silent data loss.
package schemagen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Family is the generated package a schema file belongs to.
type Family string

// Families, in the order they are generated.
const (
	FamilyCommands Family = "commands"
	FamilyCheckin  Family = "checkin"
	FamilyErrors   Family = "errors"
	FamilyProfiles Family = "profiles"
	FamilyDDM      Family = "ddm"
	FamilyDDMProto Family = "ddmproto"
	FamilyStatus   Family = "status"
	FamilyOther    Family = "other"
	familyUnknown  Family = ""
)

// AllFamilies lists every family in generation order.
var AllFamilies = []Family{
	FamilyCommands, FamilyCheckin, FamilyErrors, FamilyProfiles,
	FamilyDDM, FamilyDDMProto, FamilyStatus, FamilyOther,
}

// Kind refines a family: declarations are split into their sub-kinds.
type Kind string

// Kinds within a family.
const (
	KindDefault       Kind = ""
	KindActivation    Kind = "activation"
	KindAsset         Kind = "asset"
	KindConfiguration Kind = "configuration"
	KindManagement    Kind = "management"
	KindCredential    Kind = "credential"
	KindBase          Kind = "base"
)

// Schema is one YAML file from the Apple repository.
//
//nolint:tagliatelle // tags mirror Apple's YAML keys exactly
type Schema struct {
	Title              string              `yaml:"title"`
	Description        string              `yaml:"description"`
	Payload            Payload             `yaml:"payload"`
	PayloadKeys        []Key               `yaml:"payloadkeys"`
	ResponseKeys       []Key               `yaml:"responsekeys"`
	Reasons            []Reason            `yaml:"reasons"`
	Notes              []Note              `yaml:"notes"`
	RelatedStatusItems []RelatedStatusItem `yaml:"related-status-items"`

	// Derived, not from YAML.
	Path   string `yaml:"-"` // path relative to the schema root, forward slashes
	Family Family `yaml:"-"`
	Kind   Kind   `yaml:"-"`
}

// Payload describes the schema object as a whole.
//
//nolint:tagliatelle // tags mirror Apple's YAML keys exactly
type Payload struct {
	PayloadType     string      `yaml:"payloadtype"`
	RequestType     string      `yaml:"requesttype"`
	DeclarationType string      `yaml:"declarationtype"`
	StatusItemType  string      `yaml:"statusitemtype"`
	CredentialType  string      `yaml:"credentialtype"`
	SupportedOS     SupportedOS `yaml:"supportedOS"`
	Apply           string      `yaml:"apply"`
	Beta            *bool       `yaml:"beta"`
	Content         string      `yaml:"content"`
}

// Identifier returns the value that identifies the schema on the wire
// (RequestType, PayloadType, DeclarationType, StatusItemType or
// CredentialType), or the empty string when none applies.
func (p Payload) Identifier() string {
	for _, s := range []string{p.RequestType, p.DeclarationType, p.StatusItemType, p.CredentialType, p.PayloadType} {
		if s != "" {
			return s
		}
	}
	return ""
}

// SupportedOS holds per-OS support. Nil means the OS is not mentioned.
//
//nolint:tagliatelle // tags mirror Apple's YAML keys exactly
type SupportedOS struct {
	IOS      *OSSupport `yaml:"iOS"`
	MacOS    *OSSupport `yaml:"macOS"`
	TvOS     *OSSupport `yaml:"tvOS"`
	VisionOS *OSSupport `yaml:"visionOS"`
	WatchOS  *OSSupport `yaml:"watchOS"`
}

// OSNames lists the OS keys in a stable order.
var OSNames = []string{"iOS", "macOS", "tvOS", "visionOS", "watchOS"}

// ByName returns the OSSupport for an OS name from OSNames.
func (s SupportedOS) ByName(name string) *OSSupport {
	switch name {
	case "iOS":
		return s.IOS
	case "macOS":
		return s.MacOS
	case "tvOS":
		return s.TvOS
	case "visionOS":
		return s.VisionOS
	case "watchOS":
		return s.WatchOS
	}
	return nil
}

// IsZero reports whether no OS is mentioned.
func (s SupportedOS) IsZero() bool {
	return s.IOS == nil && s.MacOS == nil && s.TvOS == nil && s.VisionOS == nil && s.WatchOS == nil
}

// OSSupport is the per-OS support block. Booleans are pointers so absence
// is distinguishable from false, which matters because per-key blocks
// inherit from the payload block.
//
//nolint:tagliatelle // tags mirror Apple's YAML keys exactly
type OSSupport struct {
	Introduced         string          `yaml:"introduced"`
	Deprecated         string          `yaml:"deprecated"`
	Removed            string          `yaml:"removed"`
	AccessRights       string          `yaml:"accessrights"`
	Multiple           *bool           `yaml:"multiple"`
	DeviceChannel      *bool           `yaml:"devicechannel"`
	UserChannel        *bool           `yaml:"userchannel"`
	Supervised         *bool           `yaml:"supervised"`
	RequiresDEP        *bool           `yaml:"requiresdep"`
	UserApprovedMDM    *bool           `yaml:"userapprovedmdm"`
	AllowManualInstall *bool           `yaml:"allowmanualinstall"`
	AllowedEnrollments []string        `yaml:"allowed-enrollments"`
	AllowedScopes      []string        `yaml:"allowed-scopes"`
	SharedIPad         *SharedIPad     `yaml:"sharedipad"`
	UserEnrollment     *UserEnrollment `yaml:"userenrollment"`
	AlwaysSkippable    *bool           `yaml:"always-skippable"`
	Beta               *bool           `yaml:"beta"`
}

// SharedIPad is the shared iPad behaviour block.
//
//nolint:tagliatelle // tags mirror Apple's YAML keys exactly
type SharedIPad struct {
	Mode          string   `yaml:"mode"`
	DeviceChannel *bool    `yaml:"devicechannel"`
	UserChannel   *bool    `yaml:"userchannel"`
	AllowedScopes []string `yaml:"allowed-scopes"`
}

// UserEnrollment is the user enrollment behaviour block.
type UserEnrollment struct {
	Mode     string `yaml:"mode"`
	Behavior string `yaml:"behavior"`
}

// Key is one payload or response key, possibly with nested subkeys.
//
//nolint:tagliatelle // tags mirror Apple's YAML keys exactly
type Key struct {
	Key               string       `yaml:"key"`
	Title             string       `yaml:"title"`
	SupportedOS       *SupportedOS `yaml:"supportedOS"`
	Type              string       `yaml:"type"`
	Subtype           string       `yaml:"subtype"`
	AssetTypes        []string     `yaml:"assettypes"`
	AssetContentTypes []string     `yaml:"asset-content-types"`
	Presence          string       `yaml:"presence"`
	RangeList         []any        `yaml:"rangelist"`
	Range             *Range       `yaml:"range"`
	Default           any          `yaml:"default"`
	Format            string       `yaml:"format"`
	Repetition        *Repetition  `yaml:"repetition"`
	CombineType       string       `yaml:"combinetype"`
	Content           string       `yaml:"content"`
	SubkeyType        string       `yaml:"subkeytype"`
	Subkeys           []Key        `yaml:"subkeys"`

	// RecursiveTo is set by the loader (never by Apple's YAML) when this
	// key's subkeys were a YAML alias to an enclosing subkeys sequence, that
	// is, a recursive structure such as nested bookmark folders. Its value is
	// the key name of the enclosing key whose subkeys are
	// referenced; Subkeys is left empty. See expand.
	RecursiveTo string `yaml:"x-recursive"`
}

// Required reports whether presence is "required". Apple's schema treats
// absence of presence as optional.
func (k Key) Required() bool { return k.Presence == "required" }

// Range bounds a numeric value.
type Range struct {
	Min *float64 `yaml:"min"`
	Max *float64 `yaml:"max"`
}

// Repetition bounds array cardinality.
type Repetition struct {
	Min int `yaml:"min"`
	Max int `yaml:"max"`
}

// Reason is a declarative management status reason code.
type Reason struct {
	Value       string         `yaml:"value"`
	Description string         `yaml:"description"`
	Details     []ReasonDetail `yaml:"details"`
}

// ReasonDetail is one key in a reason's Details dictionary.
type ReasonDetail struct {
	Key         string `yaml:"key"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
}

// Note is free-form markdown attached to a schema.
type Note struct {
	Title   string `yaml:"title"`
	Content string `yaml:"content"`
}

// RelatedStatusItem links a configuration to status items.
//
//nolint:tagliatelle // tags mirror Apple's YAML keys exactly
type RelatedStatusItem struct {
	StatusItems []string `yaml:"status-items"`
	Note        string   `yaml:"note"`
}

// Tree is every schema loaded from a root directory.
type Tree struct {
	Root    string
	Schemas []*Schema // sorted by Path
}

// ByFamily returns the schemas of one family in path order.
func (t *Tree) ByFamily(f Family) []*Schema {
	var out []*Schema
	for _, s := range t.Schemas {
		if s.Family == f {
			out = append(out, s)
		}
	}
	return out
}

// Errors returned by the loader.
var (
	ErrUnknownFamily = errors.New("schemagen: yaml file outside a known family directory")
	ErrMissingTitle  = errors.New("schemagen: schema has no title")
)

// Classify maps a relative path to its family and kind.
func Classify(rel string) (Family, Kind, error) {
	rel = filepath.ToSlash(rel)
	switch {
	case strings.HasPrefix(rel, "mdm/commands/"):
		return FamilyCommands, KindDefault, nil
	case strings.HasPrefix(rel, "mdm/checkin/"):
		return FamilyCheckin, KindDefault, nil
	case strings.HasPrefix(rel, "mdm/errors/"):
		return FamilyErrors, KindDefault, nil
	case strings.HasPrefix(rel, "mdm/profiles/"):
		return FamilyProfiles, KindDefault, nil
	case rel == "declarative/declarations/declarationbase.yaml":
		return FamilyDDM, KindBase, nil
	case strings.HasPrefix(rel, "declarative/declarations/activations/"):
		return FamilyDDM, KindActivation, nil
	case strings.HasPrefix(rel, "declarative/declarations/assets/credentials/"):
		return FamilyDDM, KindCredential, nil
	case strings.HasPrefix(rel, "declarative/declarations/assets/"):
		return FamilyDDM, KindAsset, nil
	case strings.HasPrefix(rel, "declarative/declarations/configurations/"):
		return FamilyDDM, KindConfiguration, nil
	case strings.HasPrefix(rel, "declarative/declarations/management/"):
		return FamilyDDM, KindManagement, nil
	case strings.HasPrefix(rel, "declarative/protocol/"):
		return FamilyDDMProto, KindDefault, nil
	case strings.HasPrefix(rel, "declarative/status/"):
		return FamilyStatus, KindDefault, nil
	case strings.HasPrefix(rel, "other/"):
		return FamilyOther, KindDefault, nil
	}
	return familyUnknown, KindDefault, fmt.Errorf("%w: %s", ErrUnknownFamily, rel)
}

// Load reads every schema YAML under root (skipping docs/ and dotfiles),
// decoding strictly. Files are read through os.Root so the walk cannot
// escape the schema directory. It returns the first error encountered
// together with the offending path.
func Load(root string) (*Tree, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("schemagen: load %s: %w", root, err)
	}
	defer r.Close()
	tree := &Tree{Root: root}
	err = fs.WalkDir(r.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if rel == "docs" || (strings.HasPrefix(d.Name(), ".") && rel != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}
		data, readErr := r.ReadFile(filepath.FromSlash(rel))
		if readErr != nil {
			return readErr
		}
		s, parseErr := Parse(data)
		if parseErr != nil {
			return fmt.Errorf("%s: %w", rel, parseErr)
		}
		s.Path = rel
		s.Family, s.Kind, parseErr = Classify(rel)
		if parseErr != nil {
			return parseErr
		}
		tree.Schemas = append(tree.Schemas, s)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("schemagen: load %s: %w", root, err)
	}
	sort.Slice(tree.Schemas, func(i, j int) bool { return tree.Schemas[i].Path < tree.Schemas[j].Path })
	return tree, nil
}

// LoadFile decodes one schema file strictly, reading it through an os.Root
// scoped to its directory.
func LoadFile(path string) (*Schema, error) {
	dir, base := filepath.Split(filepath.Clean(path))
	if dir == "" {
		dir = "."
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("schemagen: %w", err)
	}
	defer r.Close()
	data, err := r.ReadFile(base)
	if err != nil {
		return nil, fmt.Errorf("schemagen: %w", err)
	}
	return Parse(data)
}

// Parse decodes schema YAML strictly: unknown keys are errors.
//
// Apple's YAML uses in-file anchors for shared subkey structures. One of them
// (Safari bookmarks) is self-referential, which yaml.v3 refuses to decode into
// structs. Parse therefore loads the document as a node tree, breaks cycles
// with breakCycles, re-encodes, and then decodes strictly.
func Parse(data []byte) (*Schema, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	expanded := expand(&root, nil)
	cleaned, err := yaml.Marshal(expanded)
	if err != nil {
		return nil, fmt.Errorf("re-encode: %w", err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(cleaned)))
	dec.KnownFields(true)
	var s Schema
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if s.Title == "" {
		return nil, ErrMissingTitle
	}
	return &s, nil
}

// pathEntry is one ancestor on the walk: the node and, for mapping nodes,
// the mapping node itself so the owning key's metadata can be read.
type pathEntry struct {
	node  *yaml.Node
	owner *yaml.Node // enclosing mapping node, if any
}

// expand returns a copy of n with every alias expanded in place, so each
// context gets its own copy of shared structures. An alias that points at an
// ancestor of the current position (a recursive structure) is replaced by an
// empty sequence plus an x-recursive key on the enclosing mapping naming the
// owner of the referenced subkeys. Cycles are detected structurally by node
// identity on the ancestor path, not by anchor name. Anchors are dropped from
// the copy so re-encoding never emits aliases.
func expand(n *yaml.Node, path []pathEntry) *yaml.Node {
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
		}
		return expand(n.Alias, path)
	}
	out := *n
	out.Anchor = ""
	out.Alias = nil
	out.Content = nil
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		next := append(append([]pathEntry(nil), path...), pathEntry{node: n, owner: ownerOf(path)})
		for _, c := range n.Content {
			out.Content = append(out.Content, expand(c, next))
		}
	case yaml.MappingNode:
		next := append(append([]pathEntry(nil), path...), pathEntry{node: n, owner: n})
		var recursiveTo string
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if val.Kind == yaml.AliasNode && val.Alias != nil {
				if target := ancestorIndex(path, val.Alias); target >= 0 {
					out.Content = append(
						out.Content,
						expand(key, next),
						&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"},
					)
					recursiveTo = ownerName(path[target].owner)
					continue
				}
			}
			out.Content = append(out.Content, expand(key, next), expand(val, next))
		}
		if recursiveTo != "" {
			out.Content = append(out.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "x-recursive"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: recursiveTo},
			)
		}
	case yaml.ScalarNode, yaml.AliasNode:
	}
	return &out
}

func ownerOf(path []pathEntry) *yaml.Node {
	if len(path) == 0 {
		return nil
	}
	return path[len(path)-1].owner
}

// ancestorIndex returns the path index whose node is target, or -1.
func ancestorIndex(path []pathEntry, target *yaml.Node) int {
	for i := range path {
		if path[i].node == target {
			return i
		}
	}
	return -1
}

// ownerName returns the key name of the mapping that owns an aliased
// subkeys sequence. The type builder resolves it to the nearest ancestor key
// with that name, which is the structure the alias refers to.
func ownerName(m *yaml.Node) string {
	if m == nil || m.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "key" {
			return m.Content[i+1].Value
		}
	}
	return ""
}
