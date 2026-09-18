package blueprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/predicate"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// Spec is the local authoring format for one Blueprint. Declaration and
// activation identifiers are stable within this source. Compile derives the
// identifiers delivered to devices; Spec is not an Apple wire declaration.
type Spec struct {
	Identifier   string
	Name         string
	Description  string
	Declarations []Declaration
	Activations  []Activation
}

// Declaration supplies a known Apple declaration payload or a configuration
// profile reference that Compile converts to LegacyProfile. Asset references in
// Payload use other declarations' source identifiers.
type Declaration struct {
	Identifier           string
	Type                 string                         `json:",omitempty"`
	Payload              jsontext.Value                 `json:",omitempty"`
	ConfigurationProfile *ConfigurationProfileReference `json:",omitempty"`
}

// ConfigurationProfileReference pins immutable uploaded configuration profile
// bytes. UseProfileAssetReference is an authoring option that selects Apple's
// ProfileAssetReference field instead of ProfileURL; it is not a wire key.
type ConfigurationProfileReference struct {
	Revision                 string
	UseProfileAssetReference bool `json:",omitempty"`
}

// Activation supplies an identifier and the payload fields of ActivationSimple.
// StandardConfigurations names configurations by their source identifiers.
type Activation struct {
	Identifier             string
	StandardConfigurations []string
	Predicate              string `json:",omitempty"`
}

// ConfigurationProfileDescriptor supplies upload metadata to the compiler.
// The caller verifies it; the compiler does not download or inspect the file.
type ConfigurationProfileDescriptor struct {
	URL         string
	ContentType string
	Size        int64
	SHA256      string
	Signed      bool
}

// Options supplies optional target validation and hosted profile descriptors,
// indexed by immutable revision.
type Options struct {
	Target                support.Target
	ConfigurationProfiles map[string]ConfigurationProfileDescriptor
}

// Compiled contains the publication and mappings for diagnostics and status.
type Compiled struct {
	Publication ddm.SetPublication
	Identifiers map[string]string
	Activations map[string]string
}

// NewDeclaration encodes a generated, typed Apple payload for compilation.
func NewDeclaration(identifier string, payload schema.Declaration) (Declaration, error) {
	if payload == nil {
		return Declaration{}, invalid("nil declaration payload")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Declaration{}, invalid("cannot encode declaration payload")
	}
	if string(raw) == "null" {
		return Declaration{}, invalid("nil declaration payload")
	}
	return Declaration{Identifier: identifier, Type: payload.DeclarationTypeName(), Payload: raw}, nil
}

// ValidIdentifier checks a source identifier or Blueprint URL path segment.
// Device declaration identifiers are generated separately with Apple's limits.
func ValidIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return s != "." && s != ".."
}

// SetName is the stable backing set name for a Blueprint identifier.
func SetName(identifier string) string { return "blueprint." + digest([]string{identifier}) }

// OwnsIdentifier identifies the namespace reserved for compiled declarations.
func OwnsIdentifier(identifier string) bool {
	return strings.HasPrefix(identifier, "com.deploymenttheory.blueprint.")
}

// OwnsSet identifies the namespace reserved for Blueprint publication.
func OwnsSet(name string) bool { return strings.HasPrefix(name, "blueprint.") }

func digest(parts []string) string {
	raw, _ := json.Marshal(parts)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func identifier(owner string, kind schema.Kind, key string) string {
	// Apple limits declaration identifiers to 64 bytes. Retain 128 hash bits.
	return "com.deploymenttheory.blueprint." + digest([]string{owner, string(kind), key})[:32]
}

func invalid(message string) error {
	return fmt.Errorf("%w: blueprint: %s", ddm.ErrInvalidDeclaration, message)
}

type entry struct {
	key, typ, id string
	kind         schema.Kind
	payload      map[string]any
}

// Compile resolves references, validates the complete graph, and derives stable
// identifiers. Input payloads are copied; the caller's specification is unchanged.
func Compile(spec Spec, options Options) (*Compiled, error) {
	if !ValidIdentifier(spec.Identifier) {
		return nil, invalid("invalid Blueprint identifier")
	}
	out := &Compiled{Publication: ddm.SetPublication{Name: SetName(spec.Identifier), Declarations: []jsontext.Value{}}, Identifiers: map[string]string{}, Activations: map[string]string{}}
	entries := map[string]*entry{}
	for _, c := range spec.Declarations {
		if !ValidIdentifier(c.Identifier) || entries[c.Identifier] != nil {
			return nil, invalid("invalid or duplicate declaration identifier")
		}
		if c.ConfigurationProfile != nil {
			if c.Type != "" || len(c.Payload) != 0 {
				return nil, invalid("profile and declaration payload are mutually exclusive")
			}
			profile, asset, err := profileEntries(c, options)
			if err != nil {
				return nil, err
			}
			entries[c.Identifier] = profile
			if asset != nil {
				entries[asset.key] = asset
			}
			continue
		}
		kind, err := declarationKind(c.Type)
		if err != nil {
			return nil, err
		}
		p := map[string]any{}
		if len(c.Payload) != 0 {
			if err := json.Unmarshal(c.Payload, &p); err != nil || p == nil {
				return nil, invalid("declaration payload must be an object")
			}
		}
		entries[c.Identifier] = &entry{key: c.Identifier, typ: c.Type, kind: kind, payload: p}
	}
	keys := make([]string, 0, len(entries))
	var configurations []string
	for key, e := range entries {
		e.id = identifier(spec.Identifier, e.kind, key)
		out.Identifiers[key] = e.id
		keys = append(keys, key)
		if e.kind == schema.KindConfiguration {
			configurations = append(configurations, key)
		}
	}
	slices.Sort(keys)
	slices.Sort(configurations)
	edges := map[string][]string{}
	for _, key := range keys {
		e := entries[key]
		for _, ref := range schema.AssetReferences[e.typ] {
			err := rewrite(e.payload, ref.Path, func(local string) (string, error) {
				asset := entries[local]
				if asset == nil || asset.kind != schema.KindAsset || !slices.Contains(ref.Types, asset.typ) {
					return "", invalid("missing or incompatible asset reference in declaration " + key)
				}
				edges[key] = append(edges[key], local)
				return asset.id, nil
			})
			if err != nil {
				return nil, err
			}
		}
		if err := out.append(e, options.Target); err != nil {
			return nil, err
		}
	}
	if cyclic(edges) {
		return nil, invalid("cyclic declaration references")
	}
	activations := slices.Clone(spec.Activations)
	if len(activations) == 0 && len(configurations) != 0 {
		activations = []Activation{{Identifier: "default", StandardConfigurations: configurations}}
	}
	used := map[string]bool{}
	for _, act := range activations {
		if !ValidIdentifier(act.Identifier) || out.Activations[act.Identifier] != "" || len(act.StandardConfigurations) == 0 {
			return nil, invalid("invalid, empty or duplicate activation")
		}
		if act.Predicate != "" {
			if err := predicate.Validate(act.Predicate); err != nil {
				return nil, fmt.Errorf("%w: activation %s: %w", ddm.ErrInvalidDeclaration, act.Identifier, err)
			}
		}
		refs := []string{}
		for _, key := range act.StandardConfigurations {
			e := entries[key]
			if e == nil || e.kind != schema.KindConfiguration {
				return nil, invalid("activation must reference local configurations")
			}
			refs = append(refs, e.id)
			used[key] = true
		}
		slices.Sort(refs)
		refs = slices.Compact(refs)
		p := map[string]any{"StandardConfigurations": refs}
		if act.Predicate != "" {
			p["Predicate"] = act.Predicate
		}
		id := identifier(spec.Identifier, schema.KindActivation, act.Identifier)
		out.Activations[act.Identifier] = id
		if err := out.append(&entry{key: act.Identifier, id: id, typ: schema.DeclarationTypeActivationSimple, payload: p}, options.Target); err != nil {
			return nil, err
		}
	}
	for _, key := range configurations {
		if !used[key] {
			return nil, invalid("configuration is not referenced by an activation: " + key)
		}
	}
	slices.SortFunc(out.Publication.Declarations, func(a, b jsontext.Value) int { return strings.Compare(string(a), string(b)) })
	return out, nil
}

func declarationKind(typ string) (schema.Kind, error) {
	for _, e := range schema.ByID(typ) {
		if e.Kind == schema.KindConfiguration || e.Kind == schema.KindAsset || e.Kind == schema.KindManagement {
			return e.Kind, nil
		}
	}
	return "", invalid("unsupported declaration declaration type")
}

func (c *Compiled) append(e *entry, target support.Target) error {
	raw, err := json.Marshal(map[string]any{"Identifier": e.id, "Type": e.typ, "Payload": e.payload})
	if err != nil {
		return invalid("cannot encode declaration")
	}
	d, err := ddm.ParseDeclaration(raw, target)
	if err != nil {
		return fmt.Errorf("blueprint declaration %s: %w", e.key, err)
	}
	c.Publication.Declarations = append(c.Publication.Declarations, jsontext.Value(d.Canonical))
	return nil
}

func profileEntries(c Declaration, o Options) (*entry, *entry, error) {
	d, ok := o.ConfigurationProfiles[c.ConfigurationProfile.Revision]
	u, err := url.Parse(d.URL)
	if !ok || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, nil, invalid("hosted profile needs a resolved HTTPS descriptor")
	}
	e := &entry{key: c.Identifier, typ: schema.DeclarationTypeLegacyProfile, kind: schema.KindConfiguration, payload: map[string]any{"ProfileURL": d.URL}}
	if !c.ConfigurationProfile.UseProfileAssetReference {
		return e, nil, nil
	}
	if d.Signed || !slices.Contains([]string{"application/plist", "application/x-plist", "application/xml", "text/xml"}, d.ContentType) || d.Size <= 0 || len(d.SHA256) != 64 {
		return nil, nil, invalid("profile asset needs unsigned plist data, media type, size and SHA-256")
	}
	if _, err := hex.DecodeString(d.SHA256); err != nil {
		return nil, nil, invalid("invalid profile digest")
	}
	key := "@profile:" + c.Identifier
	e.payload = map[string]any{"ProfileAssetReference": key}
	a := &entry{key: key, typ: schema.DeclarationTypeAssetData, kind: schema.KindAsset, payload: map[string]any{
		"Reference":      map[string]any{"DataURL": d.URL, "ContentType": d.ContentType, "Size": d.Size, "Hash-SHA-256": d.SHA256},
		"Authentication": map[string]any{"Type": "MDM"},
	}}
	return e, a, nil
}

func rewrite(value any, path []string, resolve func(string) (string, error)) error {
	if len(path) == 0 {
		return nil
	}
	if path[0] == "*" {
		child := func(v any) (any, error) {
			if len(path) == 1 {
				s, ok := v.(string)
				if !ok {
					return nil, invalid("asset reference must be a string")
				}
				return resolve(s)
			}
			return v, rewrite(v, path[1:], resolve)
		}
		switch v := value.(type) {
		case map[string]any:
			for k, old := range v {
				next, err := child(old)
				if err != nil {
					return err
				}
				v[k] = next
			}
		case []any:
			for i, old := range v {
				next, err := child(old)
				if err != nil {
					return err
				}
				v[i] = next
			}
		}
		return nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	v, exists := m[path[0]]
	if !exists {
		return nil
	}
	if len(path) > 1 {
		return rewrite(v, path[1:], resolve)
	}
	s, ok := v.(string)
	if !ok {
		return invalid("asset reference must be a string")
	}
	resolved, err := resolve(s)
	if err != nil {
		return err
	}
	m[path[0]] = resolved
	return nil
}

func cyclic(edges map[string][]string) bool {
	state := map[string]int{}
	var visit func(string) bool
	visit = func(key string) bool {
		if state[key] == 1 {
			return true
		}
		if state[key] == 2 {
			return false
		}
		state[key] = 1
		for _, next := range edges[key] {
			if visit(next) {
				return true
			}
		}
		state[key] = 2
		return false
	}
	for key := range edges {
		if visit(key) {
			return true
		}
	}
	return false
}
