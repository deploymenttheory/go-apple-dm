package adminauth

// Keep the pinned Cedar experimental schema APIs behind this adapter.
import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"
	expast "github.com/cedar-policy/cedar-go/x/exp/ast"
	"github.com/cedar-policy/cedar-go/x/exp/schema"
	"github.com/cedar-policy/cedar-go/x/exp/schema/validate"
)

// ContextAttribute describes a server-derived Cedar context field.
type ContextAttribute struct {
	Type     string
	Required bool
}

type policySchema struct {
	validator *validate.Validator
	json      []byte
}

// buildSchema builds Cedar action and entity constraints from the registered administrative
// catalogue.
func (r *Registry) buildSchema() error {
	entities := map[string]any{
		"Principal": map[string]any{"memberOfTypes": []string{"Role"}},
		"Role":      map[string]any{}, "System": map[string]any{},
	}
	actions := map[string]any{}
	for _, a := range r.Actions() {
		resource := a.Resource
		if resource == "" {
			resource = EntitySystem
		}
		entities[strings.TrimPrefix(string(resource), "MDM::")] = map[string]any{}
		attrs := map[string]any{}
		for name, attr := range a.Context {
			attrs[name] = map[string]any{"type": attr.Type, "required": attr.Required}
		}
		entry := map[string]any{"appliesTo": map[string]any{"principalTypes": []string{"Principal"}, "resourceTypes": []string{strings.TrimPrefix(string(resource), "MDM::")}, "context": map[string]any{"type": "Record", "attributes": attrs}}}
		var parents []map[string]string
		for _, group := range a.Groups {
			parents = append(parents, map[string]string{"id": group})
			actions[group] = map[string]any{}
		}
		if len(parents) > 0 {
			entry["memberOf"] = parents
		}
		actions[a.ID] = entry
	}
	data, err := json.Marshal(map[string]any{"MDM": map[string]any{"entityTypes": entities, "actions": actions}})
	if err != nil {
		return err
	}
	var s schema.Schema
	if err := s.UnmarshalJSON(data); err != nil {
		return fmt.Errorf("%w: schema: %w", ErrInvalid, err)
	}
	resolved, err := s.Resolve()
	if err != nil {
		return fmt.Errorf("%w: schema: %w", ErrInvalid, err)
	}
	r.schema = &policySchema{validator: validate.New(resolved), json: data}
	return nil
}

// Schema returns the exact schema used to validate policies and requests.
func (r *Registry) Schema() json.RawMessage { return append(json.RawMessage(nil), r.schema.json...) }

// validatePolicy checks a parsed policy against the registry's Cedar schema.
func (r *Registry) validatePolicy(id string, p *cedar.Policy) error {
	if err := r.schema.validator.Policy(id, (*expast.Policy)(p.AST())); err != nil {
		return fmt.Errorf("%w: policy %s: %w", ErrInvalid, id, err)
	}
	return nil
}

// actionEntities constructs Cedar action entities and their action-group parents.
func (r *Registry) actionEntities() types.EntityMap {
	out := types.EntityMap{}
	for _, a := range r.Actions() {
		var parents []types.EntityUID
		for _, group := range a.Groups {
			uid := ActionUID(group)
			parents = append(parents, uid)
			out[uid] = types.Entity{UID: uid}
		}
		uid := ActionUID(a.ID)
		out[uid] = types.Entity{UID: uid, Parents: types.NewEntityUIDSet(parents...)}
	}
	return out
}
