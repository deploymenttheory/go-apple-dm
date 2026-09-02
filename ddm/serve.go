package ddm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/canonjson"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-mdm/schema/ddm"
)

// TimestampLayout is the SyncTokens Timestamp format: whole seconds, UTC.
const TimestampLayout = "2006-01-02T15:04:05Z"

// Response is what a device-facing adapter writes back.
type Response struct {
	Body []byte
	// Status is 200, or 404 when a declaration is not part of the
	// enrollment's manifest (Apple: the device then removes it).
	Status int
}

// refreshSnapshot recomputes the manifest, persists it, and keeps
// TokenChangedAt when the token did not move so the tokens response stays
// byte-stable.
func (e *Engine) refreshSnapshot(ctx context.Context, id mdm.EnrollmentID) (*Snapshot, error) {
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	now := e.clock.Now().UTC()
	var snap *Snapshot
	err := e.store.Update(ctx, func(tx Tx) error {
		token, items, err := e.manifestFor(ctx, tx, id)
		if err != nil {
			return err
		}
		snap = &Snapshot{ID: id, DeclarationsToken: token, Items: items, TokenChangedAt: now, RefreshedAt: now}
		if prev, err := tx.Snapshot(ctx, id); err == nil && prev.DeclarationsToken == token {
			snap.TokenChangedAt = prev.TokenChangedAt
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return tx.PutSnapshot(ctx, snap)
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// Manifest returns the current snapshot, refreshing it first.
func (e *Engine) Manifest(ctx context.Context, id mdm.EnrollmentID) (*Snapshot, error) {
	return e.refreshSnapshot(ctx, id)
}

// Tokens renders the TokensResponse for an enrollment.
func (e *Engine) Tokens(ctx context.Context, id mdm.EnrollmentID) ([]byte, error) {
	snap, err := e.refreshSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	return RenderTokens(snap.DeclarationsToken, snap.TokenChangedAt)
}

// RenderTokens renders {"SyncTokens":{"DeclarationsToken":..,"Timestamp":..}}.
func RenderTokens(token string, at time.Time) ([]byte, error) {
	out, err := canonjson.Marshal(map[string]any{"SyncTokens": map[string]any{
		"DeclarationsToken": token,
		"Timestamp":         at.UTC().Format(TimestampLayout),
	}})
	if err != nil {
		return nil, fmt.Errorf("ddm: render tokens: %w", err)
	}
	return out, nil
}

// DeclarationItems renders the DeclarationItemsResponse for an enrollment
// with all four arrays present, as Apple's schema requires.
func (e *Engine) DeclarationItems(ctx context.Context, id mdm.EnrollmentID) ([]byte, error) {
	snap, err := e.refreshSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	return RenderDeclarationItems(snap)
}

// RenderDeclarationItems renders a snapshot as a DeclarationItemsResponse.
func RenderDeclarationItems(snap *Snapshot) ([]byte, error) {
	groups := map[schemaddm.Kind]string{
		schemaddm.KindActivation: "Activations", schemaddm.KindAsset: "Assets",
		schemaddm.KindConfiguration: "Configurations", schemaddm.KindManagement: "Management",
	}
	decls := map[string]any{"Activations": []any{}, "Assets": []any{}, "Configurations": []any{}, "Management": []any{}}
	for _, item := range SortRefsItems(snap.Items) {
		key, ok := groups[item.Kind]
		if !ok {
			return nil, fmt.Errorf("%w: kind %q in snapshot", ErrInvalid, item.Kind)
		}
		decls[key] = append(decls[key].([]any), map[string]any{"Identifier": item.Identifier, "ServerToken": item.ServerToken}) //nolint:forcetypeassert // populated above with []any
	}
	out, err := canonjson.Marshal(map[string]any{"Declarations": decls, "DeclarationsToken": snap.DeclarationsToken})
	if err != nil {
		return nil, fmt.Errorf("ddm: render declaration items: %w", err)
	}
	return out, nil
}

// SortRefsItems orders snapshot items the way manifests are rendered.
func SortRefsItems(items []SnapshotItem) []SnapshotItem {
	out := make([]SnapshotItem, len(items))
	copy(out, items)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && compareRefs(out[j-1].DeclarationRef, out[j].DeclarationRef) > 0; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Declaration serves one declaration as the enrollment's manifest
// advertised it. ErrNotFound (404 on the wire) when the declaration is not
// in the manifest, has the wrong kind, or was deleted since.
func (e *Engine) Declaration(ctx context.Context, id mdm.EnrollmentID, kind schemaddm.Kind, identifier string) ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	snap, err := e.store.Snapshot(ctx, id)
	if errors.Is(err, ErrNotFound) {
		if snap, err = e.refreshSnapshot(ctx, id); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	for _, item := range snap.Items {
		if item.Identifier != identifier || item.Kind != kind {
			continue
		}
		if item.Expanded != nil {
			return RenderDeclaration(item.Expanded, item.ServerToken)
		}
		v, err := e.store.GetDeclarationVersion(ctx, identifier, item.BaseToken)
		if err != nil {
			return nil, err
		}
		return RenderDeclaration(v.Canonical, item.ServerToken)
	}
	return nil, fmt.Errorf("%w: %s/%s not in the manifest of %s", ErrNotFound, kind, identifier, id.ID)
}

// Handle serves one DeclarativeManagement check-in for the adapters.
// Malformed endpoints are ErrBadEndpoint; a status endpoint needs data.
func (e *Engine) Handle(ctx context.Context, id mdm.EnrollmentID, endpoint string, data []byte) (Response, error) {
	ep, err := ParseEndpoint(endpoint)
	if err != nil {
		return Response{}, err
	}
	switch ep.Op {
	case OpTokens:
		body, err := e.Tokens(ctx, id)
		return Response{Body: body, Status: http.StatusOK}, err
	case OpDeclarationItems:
		body, err := e.DeclarationItems(ctx, id)
		return Response{Body: body, Status: http.StatusOK}, err
	case OpDeclaration:
		body, err := e.Declaration(ctx, id, ep.Kind, ep.Identifier)
		if errors.Is(err, ErrNotFound) {
			return Response{Status: http.StatusNotFound}, nil
		}
		return Response{Body: body, Status: http.StatusOK}, err
	case OpStatus:
		if len(data) == 0 {
			return Response{}, fmt.Errorf("%w: status without data", ErrBadEndpoint)
		}
		if _, err := e.Status(ctx, id, data); err != nil {
			return Response{}, err
		}
		return Response{Status: http.StatusOK}, nil
	default:
		return Response{}, fmt.Errorf("%w: %q", ErrBadEndpoint, endpoint)
	}
}
