//go:build e2e

package e2e

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
)

// TestE2E_BlueprintPublication checks e2 e blueprint publication.
func TestE2E_BlueprintPublication(t *testing.T) {
	h := newDDMHarness(t, false)
	ctx := t.Context()
	dev := h.ddmDevice("BLUEPRINT-DEVICE", map[string]any{"team": "engineering"})
	id := deviceID("BLUEPRINT-DEVICE")
	spec := blueprint.Spec{Identifier: "engineering", Declarations: []blueprint.Declaration{
		{Identifier: "first", Type: "com.apple.configuration.management.test", Payload: jsontext.Value(`{"Echo":"first"}`)},
		{Identifier: "second", Type: "com.apple.configuration.management.test", Payload: jsontext.Value(`{"Echo":"second"}`)},
	}, Activations: []blueprint.Activation{
		{Identifier: "baseline", StandardConfigurations: []string{"first"}},
		{Identifier: "conditional", StandardConfigurations: []string{"second"}, Predicate: "@property(team) == 'engineering'"},
	}}
	compiled, err := blueprint.Compile(spec, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.PublishSet(ctx, compiled.Publication); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.AssignSet(ctx, id, compiled.Publication.Name); err != nil {
		t.Fatal(err)
	}
	if r := h.drain(); r.Queued != 1 || r.Pushed != 1 {
		t.Fatal("publication wake", r)
	}
	if _, err := dev.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"first", "second"} {
		d := dev.DDM().Declarations["configuration/"+compiled.Identifiers[key]]
		if d == nil || !d.Active || d.Valid != "valid" {
			t.Fatal("declaration not active", key, d)
		}
	}
	if len(h.status(id)) != 4 {
		t.Fatal("status did not include full Blueprint")
	}
	if r, err := h.engine.PublishSet(ctx, compiled.Publication); err != nil || r.Changed {
		t.Fatal("idempotence", r, err)
	}
	if r := h.drain(); r.Queued != 0 {
		t.Fatal("duplicate wake", r)
	}
	// Replacement removes the first declaration without disturbing assignment.
	spec.Declarations = spec.Declarations[1:]
	spec.Activations = spec.Activations[1:]
	spec.Activations[0].Predicate = "@property(team) == 'marketing'"
	next, err := blueprint.Compile(spec, blueprint.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.PublishSet(ctx, next.Publication); err != nil {
		t.Fatal(err)
	}
	if r := h.drain(); r.Queued != 1 {
		t.Fatal("replacement wake", r)
	}
	if _, err := dev.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if d := dev.DDM().Declarations["configuration/"+compiled.Identifiers["first"]]; d != nil {
		t.Fatal("omitted declaration still delivered", d)
	}
	if d := dev.DDM().Declarations["configuration/"+compiled.Identifiers["second"]]; d == nil || d.Active {
		t.Fatal("predicate not reevaluated", d)
	}
}
