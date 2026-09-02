package adminauth

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"
)

// Action is an operation an admin route declares. The set is closed and owned
// by the route table, which is what lets a policy naming an action nobody
// serves be refused when it is written rather than silently never granting.
type Action struct {
	// ID is the Cedar action id, `MDM::Action::"<ID>"`.
	ID string
	// Help is operator-facing prose naming the consequence, shown by
	// `mdmctl policy actions`. Zentral writes the same for its secret-reveal
	// actions, and it is the difference between an operator granting an
	// action knowingly and granting it by name.
	Help string
	// Resource is the entity type this action acts on, EntitySystem when the
	// action is deployment-wide.
	Resource types.EntityType
}

// Registry is the set of actions the server serves. It is built once from the
// route table and then read-only, so there is no global mutable state and no
// init-order dependence.
type Registry struct {
	byID map[string]Action
}

// NewRegistry returns a registry over actions, refusing duplicates and
// malformed ids.
func NewRegistry(actions ...Action) (*Registry, error) {
	r := &Registry{byID: make(map[string]Action, len(actions))}
	for _, a := range actions {
		if !ValidName(a.ID) {
			return nil, fmt.Errorf("%w: action id %q", ErrInvalid, a.ID)
		}
		if _, dup := r.byID[a.ID]; dup {
			return nil, fmt.Errorf("%w: action %q declared twice", ErrConflict, a.ID)
		}
		r.byID[a.ID] = a
	}
	return r, nil
}

// Lookup returns the action with id.
func (r *Registry) Lookup(id string) (Action, bool) {
	a, ok := r.byID[id]
	return a, ok
}

// IDs returns every action id, sorted.
func (r *Registry) IDs() []string {
	out := make([]string, 0, len(r.byID))
	for id := range r.byID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Actions returns every action, sorted by id.
func (r *Registry) Actions() []Action {
	out := make([]Action, 0, len(r.byID))
	for _, id := range r.IDs() {
		out = append(out, r.byID[id])
	}
	return out
}

// PolicySet is a compiled, immutable set of policies ready to answer
// decisions. Compile once per policy version and share it across requests.
type PolicySet struct {
	set     *cedar.PolicySet
	version int64
}

// Version is the store version this set was compiled from.
func (p *PolicySet) Version() int64 { return p.version }

// Compile parses and validates policy documents into a decision-ready set.
//
// Validation is two steps. Cedar's parser rejects malformed syntax, which is
// the stable half. It does not reject a policy naming an action that does not
// exist -- such a policy compiles and then silently never grants, which is
// exactly the failure Zentral's schema validation exists to prevent -- so
// every action id referenced is additionally checked against the registry.
// The result is a typo refused at write time without depending on cedar-go's
// experimental schema package.
func Compile(reg *Registry, version int64, docs []Policy) (*PolicySet, error) {
	set := cedar.NewPolicySet()
	for _, doc := range docs {
		parsed, err := cedar.NewPolicySetFromBytes(doc.Name+".cedar", []byte(doc.Source))
		if err != nil {
			return nil, fmt.Errorf("%w: policy %q: %w", ErrInvalid, doc.Name, err)
		}
		for id, pol := range parsed.All() {
			if err := checkActions(reg, doc.Name, pol); err != nil {
				return nil, err
			}
			// Namespace the id so two documents cannot collide, and so a
			// diagnostic names the document an operator can edit.
			set.Add(cedar.PolicyID(doc.Name+"/"+string(id)), pol)
		}
	}
	return &PolicySet{set: set, version: version}, nil
}

// Validate parses one document and checks its action ids without adding it to
// a set, for the write path.
func Validate(reg *Registry, doc Policy) error {
	if !ValidName(doc.Name) {
		return fmt.Errorf("%w: policy name %q", ErrInvalid, doc.Name)
	}
	_, err := Compile(reg, 0, []Policy{doc})
	return err
}

// Decision is the outcome of an authorization check, carrying the policy that
// decided so an audit line can name it.
type Decision struct {
	Allowed bool
	// Policy is the document and statement that decided, empty when nothing
	// matched and the default deny applied.
	Policy string
	// Errors are per-policy evaluation errors. A policy that errors neither
	// permits nor forbids, so these are surfaced rather than swallowed.
	Errors []string
}

// Authorize evaluates one request. The default is deny: with no policy set,
// no matching policy, or an unknown action, the answer is no.
func (p *PolicySet) Authorize(principal Principal, action string, resource types.EntityUID, ctx map[string]types.Value) Decision {
	if p == nil || p.set == nil {
		return Decision{}
	}
	if resource.IsZero() {
		resource = SystemResource
	}
	rec := types.RecordMap{}
	for k, v := range ctx {
		rec[types.String(k)] = v
	}
	entities := types.EntityMap{principal.UID(): principal.Entity()}
	// Roles are referenced as entity parents; Cedar resolves `in` against the
	// principal's parent set without needing the role entities themselves.
	d, diag := cedar.Authorize(p.set, entities, types.Request{
		Principal: principal.UID(),
		Action:    ActionUID(action),
		Resource:  resource,
		Context:   types.NewRecord(rec),
	})
	out := Decision{Allowed: d == cedar.Allow}
	for _, r := range diag.Reasons {
		out.Policy = string(r.PolicyID)
	}
	for _, e := range diag.Errors {
		out.Errors = append(out.Errors, fmt.Sprintf("%s: %s", e.PolicyID, e.Message))
	}
	return out
}

// checkActions refuses a policy that names an action the registry does not
// declare. Cedar policies reference actions in the scope (`action ==` or
// `action in [...]`), so the check reads the rendered statement rather than
// walking an experimental AST package.
func checkActions(reg *Registry, doc string, pol *cedar.Policy) error {
	for _, id := range actionIDs(string(pol.MarshalCedar())) {
		if _, ok := reg.Lookup(id); !ok {
			return fmt.Errorf("%w: policy %q names %q; known actions: %s",
				ErrUnknownAction, doc, id, strings.Join(reg.IDs(), ", "))
		}
	}
	return nil
}

// actionIDs extracts every `MDM::Action::"id"` literal from rendered Cedar.
func actionIDs(src string) []string {
	const marker = string(EntityAction) + `::"`
	var out []string
	for rest := src; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return out
		}
		rest = rest[i+len(marker):]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			return out
		}
		if id := rest[:j]; !slices.Contains(out, id) {
			out = append(out, id)
		}
		rest = rest[j+1:]
	}
}
