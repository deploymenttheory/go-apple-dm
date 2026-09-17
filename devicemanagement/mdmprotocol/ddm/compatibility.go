package ddm

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"slices"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/internal/canonjson"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/status"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// CompatibilityIssue identifies a withheld or adjusted declaration without
// disclosing settings, credentials, or arbitrary validation error values.
type CompatibilityIssue struct {
	Identifier string
	Reason     string
	Withheld   bool
}

// CompatibilityReport is a preview; requesting it never changes snapshots,
// tokens, assignments, or notification state.
type CompatibilityReport struct {
	Enforced bool
	Target   support.Target
	Eligible []DeclarationRef
	Issues   []CompatibilityIssue
}

func (e *Engine) deliveryTarget(ctx context.Context, id mdm.EnrollmentID) (*support.Target, error) {
	if e.enrollmentTarget == nil {
		return nil, nil
	}
	target, err := e.enrollmentTarget(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: enrollment target: %w", ErrResolver, err)
	}
	return &target, nil
}

// Compatibility evaluates current membership without advertising or persisting it.
func (e *Engine) Compatibility(ctx context.Context, id mdm.EnrollmentID) (CompatibilityReport, error) {
	if err := id.Validate(); err != nil {
		return CompatibilityReport{}, err
	}
	target, err := e.deliveryTarget(ctx, id)
	if err != nil {
		return CompatibilityReport{}, err
	}
	report := CompatibilityReport{Enforced: target != nil, Eligible: []DeclarationRef{}, Issues: []CompatibilityIssue{}}
	if target != nil {
		report.Target = *target
	}
	err = e.store.Update(ctx, func(tx Tx) error {
		_, items, err := e.manifestFor(ctx, tx, id, target, &report.Issues)
		for _, item := range items {
			report.Eligible = append(report.Eligible, item.DeclarationRef)
		}
		return err
	})
	return report, err
}

type compatibleItem struct {
	item    SnapshotItem
	typ     string
	payload map[string]any
	reason  string
}

func filterCompatible(ctx context.Context, store DeclarationStore, items []SnapshotItem, target support.Target) ([]SnapshotItem, []CompatibilityIssue, error) {
	unknown := target.OS == "" || target.Version.IsZero()
	byID := make(map[string]*compatibleItem, len(items))
	var ordered []*compatibleItem
	for _, item := range items {
		canonical := item.Expanded
		if canonical == nil {
			version, err := store.GetDeclarationVersion(ctx, item.Identifier, item.BaseToken)
			if err != nil {
				return nil, nil, err
			}
			canonical = version.Canonical
		}
		env, err := splitCanonical(canonical)
		if err != nil {
			return nil, nil, err
		}
		entry := &compatibleItem{item: item, typ: env.Type}
		if err := json.Unmarshal(env.Payload, &entry.payload); err != nil {
			return nil, nil, err
		}
		if unknown && env.Type != schemaddm.DeclarationTypeManagementStatusSubscriptions && env.Type != schemaddm.DeclarationTypeActivationSimple {
			entry.reason = "inventory-required"
		} else if _, err := ParseDeclaration(canonical, target); err != nil {
			entry.reason = "unsupported-target"
		}
		if entry.reason == "" && env.Type == schemaddm.DeclarationTypeManagementStatusSubscriptions {
			pruneStatusSubscriptions(entry, target, unknown)
		}
		byID[item.Identifier] = entry
		ordered = append(ordered, entry)
	}
	// Iterate to a fixed point: an unavailable asset can invalidate another
	// asset and every configuration depending on it. Never fetch asset URLs.
	for changed := true; changed; {
		changed = false
		for _, entry := range ordered {
			if entry.reason != "" {
				continue
			}
			for _, ref := range schemaddm.AssetReferences[entry.typ] {
				for _, id := range referencedStrings(entry.payload, ref.Path) {
					asset := byID[id]
					if asset == nil || asset.reason != "" || asset.item.Kind != schemaddm.KindAsset || !slices.Contains(ref.Types, asset.typ) {
						entry.reason = "asset-unavailable"
						changed = true
					}
				}
			}
		}
	}
	issues := []CompatibilityIssue{}
	result := make([]SnapshotItem, 0, len(items))
	for _, entry := range ordered {
		modified := false
		if entry.reason == "" && entry.typ == schemaddm.DeclarationTypeActivationSimple {
			refs, _ := entry.payload["StandardConfigurations"].([]any)
			kept := make([]any, 0, len(refs))
			for _, ref := range refs {
				id, _ := ref.(string)
				if dep := byID[id]; dep != nil && dep.reason == "" && dep.item.Kind == schemaddm.KindConfiguration {
					kept = append(kept, ref)
				}
			}
			if len(kept) == 0 {
				entry.reason = "no-compatible-configurations"
			} else if len(kept) != len(refs) {
				entry.payload["StandardConfigurations"] = kept
				modified = true
			}
		}
		if entry.reason != "" {
			issues = append(issues, CompatibilityIssue{Identifier: entry.item.Identifier, Reason: entry.reason, Withheld: true})
			continue
		}
		payload, err := canonjson.Marshal(entry.payload)
		if err != nil {
			return nil, nil, err
		}
		canonical, err := canonicalDeclaration(entry.item.Identifier, entry.typ, payload)
		if err != nil {
			return nil, nil, err
		}
		if token := TokenFor(canonical); token != entry.item.ServerToken {
			entry.item.Expanded, entry.item.ServerToken = canonical, token
			modified = true
		}
		if modified {
			issues = append(issues, CompatibilityIssue{Identifier: entry.item.Identifier, Reason: "references-filtered"})
		}
		result = append(result, entry.item)
	}
	slices.SortFunc(issues, func(a, b CompatibilityIssue) int {
		if a.Identifier < b.Identifier {
			return -1
		}
		if a.Identifier > b.Identifier {
			return 1
		}
		return 0
	})
	return result, issues, nil
}

func pruneStatusSubscriptions(entry *compatibleItem, target support.Target, unknown bool) {
	items, _ := entry.payload["StatusItems"].([]any)
	kept := make([]any, 0, len(items))
	for _, item := range items {
		m, _ := item.(map[string]any)
		name, _ := m["Name"].(string)
		allowed := unknown && slices.Contains(DefaultSubscriptionBaseline, name)
		if !unknown {
			for goName, meta := range status.Registry {
				if meta.ID == name && status.Support(goName).Check(target).Supported {
					allowed = true
					break
				}
			}
		}
		if allowed {
			kept = append(kept, item)
		}
	}
	entry.payload["StatusItems"] = kept
	if len(kept) == 0 {
		entry.reason = "no-compatible-status-items"
	}
}

func referencedStrings(value any, path []string) []string {
	if len(path) == 0 {
		if s, ok := value.(string); ok {
			return []string{s}
		}
		return nil
	}
	var result []string
	if path[0] == "*" {
		switch values := value.(type) {
		case []any:
			for _, v := range values {
				result = append(result, referencedStrings(v, path[1:])...)
			}
		case map[string]any:
			for _, v := range values {
				result = append(result, referencedStrings(v, path[1:])...)
			}
		}
	} else if values, ok := value.(map[string]any); ok {
		result = referencedStrings(values[path[0]], path[1:])
	}
	return result
}

func sameItems(a, b []SnapshotItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].DeclarationRef != b[i].DeclarationRef {
			return false
		}
	}
	return true
}
