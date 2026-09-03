package ddm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/deploymenttheory/go-apple-dm/internal/canonjson"
	"github.com/deploymenttheory/go-apple-dm/mdm"
)

// manifestFor computes the manifest an enrollment should see right now:
// static membership, resolver additions, the synthesised subscriptions
// declaration, then per-enrollment expansion. Items come back sorted by
// (kind, identifier) with the DeclarationsToken over them.
func (e *Engine) manifestFor(ctx context.Context, tx Tx, id mdm.EnrollmentID) (string, []SnapshotItem, error) {
	decls, err := tx.StaticDeclarations(ctx, id)
	if err != nil {
		return "", nil, err
	}
	seen := make(map[string]bool, len(decls))
	for i := range decls {
		seen[decls[i].Identifier] = true
	}
	for _, r := range e.resolvers {
		extra, err := r.Resolve(ctx, id)
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", ErrResolver, err)
		}
		for _, identifier := range extra {
			if seen[identifier] {
				continue
			}
			d, err := tx.GetDeclaration(ctx, identifier)
			if errors.Is(err, ErrNotFound) {
				e.log.WarnContext(ctx, "ddm: resolver named an unknown declaration", "enrollment", id.ID, "identifier", identifier)
				continue
			}
			if err != nil {
				return "", nil, err
			}
			decls = append(decls, *d)
			seen[identifier] = true
		}
	}
	items := make([]SnapshotItem, 0, len(decls)+1)
	for i := range decls {
		item, err := e.expand(ctx, id, &decls[i])
		if err != nil {
			return "", nil, err
		}
		items = append(items, item)
	}
	if e.subs.Enabled && !seen[SubscriptionIdentifier] {
		item, err := e.subscriptionItem(ctx, tx, id)
		if err != nil {
			return "", nil, err
		}
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b SnapshotItem) int { return compareRefs(a.DeclarationRef, b.DeclarationRef) })
	refs := make([]DeclarationRef, len(items))
	for i := range items {
		refs[i] = items[i].DeclarationRef
	}
	return DeclarationsToken(refs), items, nil
}

// expand applies the Expander to one declaration for one enrollment.
func (e *Engine) expand(ctx context.Context, id mdm.EnrollmentID, d *Declaration) (SnapshotItem, error) {
	item := SnapshotItem{
		DeclarationRef: DeclarationRef{Kind: d.Kind, Identifier: d.Identifier, ServerToken: d.ServerToken},
		BaseToken:      d.ServerToken,
	}
	if e.expander == nil {
		return item, nil
	}
	out, err := e.expander.Expand(ctx, id, d)
	if err != nil {
		return item, fmt.Errorf("%w: %s: %w", ErrExpander, d.Identifier, err)
	}
	if len(out) == 0 || bytes.Equal(out, d.Canonical) {
		return item, nil
	}
	canonical, err := canonjson.Canonicalize(out)
	if err != nil {
		return item, fmt.Errorf("%w: %s: %w", ErrExpander, d.Identifier, err)
	}
	if bytes.Equal(canonical, d.Canonical) {
		return item, nil
	}
	// The expansion must still be this declaration: an object whose
	// Identifier and Type match, or the manifest would advertise one thing
	// and serve another.
	env, err := splitCanonical(canonical)
	if err != nil {
		return item, fmt.Errorf("%w: %s: %w", ErrExpander, d.Identifier, err)
	}
	if env.Identifier != d.Identifier || env.Type != d.Type {
		return item, fmt.Errorf("%w: %s: expansion changed Identifier or Type", ErrExpander, d.Identifier)
	}
	item.Expanded = canonical
	item.ServerToken = TokenFor(canonical)
	return item, nil
}
