package adminauth

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"
)

// Action describes an operation from the route registry. Policy validation
// rejects references outside this registry.
type Action struct {
	// ID is the Cedar action id, `MDM::Action::"<ID>"`.
	ID string
	// Help is operator-facing prose naming the consequence, shown by
	// `dmctl policy actions`. Zentral writes the same for its secret-reveal
	// actions, and it is the difference between an operator granting an
	// action knowingly and granting it by name.
	Help string
	// Resource is the entity type this action acts on, EntitySystem when the
	// action is deployment-wide.
	Resource  types.EntityType
	Group     string
	Groups    []string
	Context   map[string]ContextAttribute
	Sensitive bool
	RootOnly  bool
}

// Registry is the set of actions the server serves. It is built once from the
// route table and then read-only, so there is no global mutable state and no
// init-order dependence.
type Registry struct {
	byID   map[string]Action
	schema *policySchema
	groups map[string]bool
}

// NewRegistry returns a registry over actions, refusing duplicates and
// malformed ids.
func NewRegistry(actions ...Action) (*Registry, error) {
	r := &Registry{byID: make(map[string]Action, len(actions)), groups: make(map[string]bool)}
	for _, a := range actions {
		if !ValidName(a.ID) {
			return nil, fmt.Errorf("%w: action id %q", ErrInvalid, a.ID)
		}
		if _, dup := r.byID[a.ID]; dup {
			return nil, fmt.Errorf("%w: action %q declared twice", ErrConflict, a.ID)
		}
		if a.Resource == "" {
			a.Resource = EntitySystem
		}
		for _, group := range a.Groups {
			if !ValidName(group) {
				return nil, ErrInvalid
			}
			r.groups[group] = true
		}
		r.byID[a.ID] = cloneAction(a)
	}
	for name := range r.groups {
		if _, ok := r.byID[name]; ok {
			return nil, fmt.Errorf("%w: group shadows action %q", ErrConflict, name)
		}
	}
	if err := r.buildSchema(); err != nil {
		return nil, err
	}
	return r, nil
}

func cloneAction(a Action) Action {
	a.Groups = slices.Clone(a.Groups)
	a.Context = maps.Clone(a.Context)
	return a
}

// Lookup returns the action with id.
func (r *Registry) Lookup(id string) (Action, bool) {
	a, ok := r.byID[id]
	return cloneAction(a), ok
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
		out = append(out, cloneAction(r.byID[id]))
	}
	return out
}

// PolicySet is a compiled, immutable set of policies ready to answer
// decisions. Compile once per policy version and share it across requests.
type PolicySet struct {
	set     *cedar.PolicySet
	version int64
	reg     *Registry
}

// Version is the store version this set was compiled from.
func (p *PolicySet) Version() int64 { return p.version }

// Compile parses policy documents with Cedar and checks referenced actions
// against the supplied registry and Cedar schema. Every active document must
// validate before any part of the set can authorize requests.
func Compile(reg *Registry, version int64, docs []Policy) (*PolicySet, error) {
	set := cedar.NewPolicySet()
	for _, doc := range docs {
		if !doc.Enabled() {
			continue
		}
		parsed, err := cedar.NewPolicySetFromBytes(doc.Name+".cedar", []byte(doc.Source))
		if err != nil {
			return nil, fmt.Errorf("%w: policy %q: %w", ErrInvalid, doc.Name, err)
		}
		for id, pol := range parsed.All() {
			if err := checkActions(reg, doc.Name, pol); err != nil {
				return nil, err
			}
			if err := reg.validatePolicy(doc.Name+"/"+string(id), pol); err != nil {
				return nil, err
			}
			// Namespace the id so two documents cannot collide, and so a
			// diagnostic names the document an operator can edit.
			set.Add(cedar.PolicyID(doc.Name+"/"+string(id)), pol)
		}
	}
	return &PolicySet{set: set, version: version, reg: reg}, nil
}

// Validate parses one document and checks its action ids without adding it to
// a set, for the write path.
func Validate(reg *Registry, doc Policy) error {
	if !ValidName(doc.Name) {
		return fmt.Errorf("%w: policy name %q", ErrInvalid, doc.Name)
	}
	doc.Active = nil
	_, err := Compile(reg, 0, []Policy{doc})
	return err
}

// Decision is the outcome of an authorization check, carrying the policy that
// decided so an audit line can name it.
type Decision struct {
	Allowed bool
	// Policy is the document and statement that decided, empty when nothing
	// matched and the default deny applied.
	Policy   string
	Policies []string
	Version  int64
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
	entities := p.reg.actionEntities()
	entities[principal.UID()] = principal.Entity()
	// Roles are referenced as entity parents; Cedar resolves `in` against the
	// principal's parent set without needing the role entities themselves.
	request := types.Request{
		Principal: principal.UID(),
		Action:    ActionUID(action),
		Resource:  resource,
		Context:   types.NewRecord(rec),
	}
	if err := p.reg.schema.validator.Request(request); err != nil {
		return Decision{Errors: []string{err.Error()}, Version: p.version}
	}
	d, diag := cedar.Authorize(p.set, entities, request)
	out := Decision{Allowed: d == cedar.Allow && len(diag.Errors) == 0, Version: p.version}
	for _, r := range diag.Reasons {
		out.Policies = append(out.Policies, string(r.PolicyID))
	}
	for _, e := range diag.Errors {
		out.Errors = append(out.Errors, fmt.Sprintf("%s: %s", e.PolicyID, e.Message))
	}
	slices.Sort(out.Policies)
	if len(out.Policies) > 0 {
		out.Policy = out.Policies[0]
	}

	return out
}

// checkActions refuses a policy that names an action the registry does not
// declare. Cedar policies reference actions in the scope (`action ==` or
// `action in [...]`), so the check reads the rendered statement rather than
// walking an experimental AST package.
func checkActions(reg *Registry, doc string, pol *cedar.Policy) error {
	for _, id := range actionIDs(string(pol.MarshalCedar())) {
		if _, ok := reg.Lookup(id); !ok && !reg.groups[id] {
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
