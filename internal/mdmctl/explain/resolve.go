package explain

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
	"github.com/deploymenttheory/go-apple-mdm/schema/ddm"
	"github.com/deploymenttheory/go-apple-mdm/schema/ddmproto"
	schemaerrors "github.com/deploymenttheory/go-apple-mdm/schema/errors"
	"github.com/deploymenttheory/go-apple-mdm/schema/other"
	"github.com/deploymenttheory/go-apple-mdm/schema/profiles"
	"github.com/deploymenttheory/go-apple-mdm/schema/status"
	"github.com/deploymenttheory/go-apple-mdm/schema/support"
)

// Errors.
var (
	// ErrNotFound is an argument that matches no type, id, or key path.
	ErrNotFound = errors.New("explain: not found")
	// ErrUnknownFamily is a -family value no schema package registers.
	ErrUnknownFamily = errors.New("explain: unknown family")
)

// Match is one thing an argument resolved to.
type Match struct {
	// Family is the schema package: commands, ddm, profiles, and so on.
	Family string
	// TypeName is the Go type, which is also the root of its support path.
	TypeName string
	// ID is the wire identifier: RequestType, PayloadType, DeclarationType,
	// the dotted status path, and so on.
	ID string
	// Title is Apple's title for the schema.
	Title string
	// Schema is the YAML path under third_party/device-management.
	Schema string
	// Kind is the declaration family, for schema/ddm only.
	Kind string
	// Path is the support key path this match is rooted at. For a type it is
	// TypeName; for a dotted key it is the key itself.
	Path string
	// Key is set when the argument named one key rather than a whole type.
	Key bool
}

// entry is one row of the flattened family index.
type entry struct {
	typeName, id, title, schema, kind string
}

// families is the fixed search order. commands first because a RequestType is
// what an operator asks about most, then declarations and profiles.
var families = []string{"commands", "ddm", "profiles", "status", "checkin", "ddmproto", "errors", "other"}

// index is built once from the generated registries. Every schema package is
// imported here, which is also what registers its support table, so no
// question is silently unanswerable because a family was not linked in.
var index = buildIndex()

func buildIndex() map[string][]entry {
	out := make(map[string][]entry, len(families))
	for name, e := range commands.Registry {
		out["commands"] = append(out["commands"], entry{name, e.ID, e.Title, e.Schema, ""})
	}
	for name, e := range ddm.Registry {
		out["ddm"] = append(out["ddm"], entry{name, e.ID, e.Title, e.Schema, string(e.Kind)})
	}
	for name, e := range profiles.Registry {
		out["profiles"] = append(out["profiles"], entry{name, e.ID, e.Title, e.Schema, ""})
	}
	for name, e := range status.Registry {
		out["status"] = append(out["status"], entry{name, e.ID, e.Title, e.Schema, ""})
	}
	for name, e := range checkin.Registry {
		out["checkin"] = append(out["checkin"], entry{name, e.ID, e.Title, e.Schema, ""})
	}
	for name, e := range ddmproto.Registry {
		out["ddmproto"] = append(out["ddmproto"], entry{name, e.ID, e.Title, e.Schema, ""})
	}
	for name, e := range schemaerrors.Registry {
		out["errors"] = append(out["errors"], entry{name, e.ID, e.Title, e.Schema, ""})
	}
	for name, e := range other.Registry {
		out["other"] = append(out["other"], entry{name, e.ID, e.Title, e.Schema, ""})
	}
	for _, f := range families {
		sort.Slice(out[f], func(i, j int) bool { return out[f][i].typeName < out[f][j].typeName })
	}
	return out
}

// Families returns the searchable families, in search order.
func Families() []string { return append([]string(nil), families...) }

// IDs returns every wire identifier in a family, sorted and deduplicated.
func IDs(family string) ([]string, error) {
	rows, ok := index[family]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownFamily, family)
	}
	seen := make(map[string]bool, len(rows))
	var out []string
	for _, r := range rows {
		if r.id != "" && !seen[r.id] {
			seen[r.id] = true
			out = append(out, r.id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Paths returns every support key path in a family.
func Paths(family string) ([]string, error) {
	if _, ok := index[family]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownFamily, family)
	}
	return support.Paths(family), nil
}

// Resolve finds everything arg names, searching family when given and every
// family in order otherwise.
//
// The order is deliberate: an exact Go type name, then a wire identifier, then
// a dotted support path. A wire identifier may name several types -- six
// schema/profiles types report com.apple.MCX -- so the result is a slice and
// every match is returned rather than one being chosen.
func Resolve(arg, family string) ([]Match, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, fmt.Errorf("%w: empty argument", ErrNotFound)
	}
	search := families
	if family != "" {
		if _, ok := index[family]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownFamily, family)
		}
		search = []string{family}
	}

	// A Go type name is unique within a family and is what roots a support
	// path, so it wins over a wire id that happens to read the same.
	for _, f := range search {
		for _, r := range index[f] {
			if r.typeName == arg {
				return []Match{match(f, r, arg, false)}, nil
			}
		}
	}
	// A wire identifier may name several types.
	for _, f := range search {
		var out []Match
		for _, r := range index[f] {
			if r.id == arg {
				out = append(out, match(f, r, r.typeName, false))
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	// A dotted key path, rooted at a type: DeviceLock.Message.
	for _, f := range search {
		if support.Lookup(f, arg) == nil {
			continue
		}
		root, _, _ := strings.Cut(arg, ".")
		for _, r := range index[f] {
			if r.typeName == root {
				return []Match{match(f, r, arg, true)}, nil
			}
		}
		// A path whose root is not a registered type is still answerable.
		return []Match{{Family: f, TypeName: root, Path: arg, Key: true}}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, arg)
}

func match(family string, r entry, path string, key bool) Match {
	return Match{
		Family: family, TypeName: r.typeName, ID: r.id, Title: r.title,
		Schema: r.schema, Kind: r.kind, Path: path, Key: key,
	}
}

// minPrefix is the shortest shared prefix that counts as a near miss.
const minPrefix = 4

// Suggest returns up to limit identifiers and key paths close to arg, for the
// message printed when nothing resolved.
//
// It tries a case-insensitive substring first, then falls back to a shared
// prefix. The fallback is what catches a dropped or transposed letter:
// "DeviceLok" is not a substring of "DeviceLock", so substring matching alone
// would answer a typo with silence.
func Suggest(arg, family string, limit int) []string {
	needle := strings.ToLower(strings.TrimSpace(arg))
	if needle == "" || limit <= 0 {
		return nil
	}
	search := families
	if family != "" {
		search = []string{family}
	}
	candidates := func() []string {
		var all []string
		for _, f := range search {
			for _, r := range index[f] {
				all = append(all, r.typeName)
				if r.id != "" {
					all = append(all, r.id)
				}
			}
			all = append(all, support.Paths(f)...)
		}
		return all
	}()

	for _, match := range []func(string) bool{
		func(s string) bool { return strings.Contains(strings.ToLower(s), needle) },
		func(s string) bool { return sharedPrefix(strings.ToLower(s), needle) >= minPrefix },
	} {
		var out []string
		seen := make(map[string]bool)
		for _, c := range candidates {
			if seen[c] || !match(c) {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
		if len(out) == 0 {
			continue
		}
		// Closest first: a longer shared prefix is a better guess than an
		// earlier alphabet, so the name the operator meant leads the list
		// rather than being buried under its siblings.
		sort.Slice(out, func(i, j int) bool {
			pi, pj := sharedPrefix(strings.ToLower(out[i]), needle), sharedPrefix(strings.ToLower(out[j]), needle)
			if pi != pj {
				return pi > pj
			}
			if len(out[i]) != len(out[j]) {
				return len(out[i]) < len(out[j])
			}
			return out[i] < out[j]
		})
		return out[:min(limit, len(out))]
	}
	return nil
}

// sharedPrefix returns how many leading characters a and b have in common.
func sharedPrefix(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// Keys returns the support key paths under a match, sorted.
func Keys(m Match) []string {
	prefix := m.Path + "."
	var out []string
	for _, p := range support.Paths(m.Family) {
		if strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
