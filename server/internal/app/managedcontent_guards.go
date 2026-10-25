package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type nativeContentOwner struct {
	ID         string                `json:"id"`
	Digest     string                `json:"digest"`
	Method     managedcontent.Method `json:"method"`
	Enrollment mdm.EnrollmentID      `json:"enrollment"`
}

// contentServerKey separates server-owned records from caller-selected dispatch IDs.
func contentServerKey(kind, id string) string {
	return fmt.Sprintf("managedcontent/server/%s/%x", kind, sha256.Sum256([]byte(id)))
}

// contentAssetKey reserves one app's native enrollment scope across locations and aliases.
func contentAssetKey(id mdm.EnrollmentID, asset string) string {
	raw, _ := json.Marshal([]any{id, asset})
	return contentServerKey("asset", string(raw))
}

// contentDeclarationKey protects a declaration's native ownership independently of its ID spelling.
func contentDeclarationKey(id string) string { return contentServerKey("declaration", id) }

// nativeContentOwned reports durable ownership without exposing private receipt values.
func (a *App) nativeContentOwned(ctx context.Context, key string) (bool, error) {
	if a.protocol == nil {
		return false, nil
	}
	_, err := a.protocol.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// guardContentCommand prevents generic command administration from adopting an
// owned immutable identity or silently introducing a second management path.
func (a *App) guardContentCommand(ctx context.Context, id mdm.EnrollmentID, cmd *mdm.Command) error {
	if a.AppsBooks == nil {
		return nil
	}
	if err := a.lockNativeContent(ctx); err != nil {
		return err
	}
	owned, err := a.nativeContentOwned(ctx, deliveryKey(cmd.UUID))
	if err != nil {
		return err
	}
	if owned {
		return managedcontent.ErrConflict
	}
	asset := ""
	switch payload := cmd.Payload.(type) {
	case *commands.InstallApplication:
		if payload.ITunesStoreID != nil {
			asset = fmt.Sprint(*payload.ITunesStoreID)
		}
	case *commands.InstallMedia:
		if payload.ITunesStoreID != nil {
			asset = fmt.Sprint(*payload.ITunesStoreID)
		}
	}
	if asset == "" {
		return nil
	}
	owned, err = a.nativeContentOwned(ctx, contentAssetKey(id, asset))
	if err != nil {
		return err
	}
	if owned {
		return managedcontent.ErrConflict
	}
	return nil
}

// guardContentDeclaration prevents direct edits of workflow-owned declarations
// and app publications that conflict with an existing native installation owner.
func (a *App) guardContentDeclaration(ctx context.Context, id string, raw []byte) error {
	if a.AppsBooks == nil {
		return nil
	}
	owned, err := a.nativeContentOwned(ctx, contentDeclarationKey(id))
	if err != nil {
		return err
	}
	if owned {
		return managedcontent.ErrConflict
	}
	var envelope struct {
		Type    string `json:"Type"`
		Payload struct {
			AppStoreID string `json:"AppStoreID"`
		} `json:"Payload"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return managedcontent.ErrInput
	}
	if envelope.Type != "com.apple.configuration.app.managed" || envelope.Payload.AppStoreID == "" {
		return nil
	}
	targets, err := a.Engine.Store().AffectedEnrollments(ctx, []string{id}, nil)
	if err != nil {
		return err
	}
	for _, target := range targets {
		owned, err := a.nativeContentOwned(ctx, contentAssetKey(target, envelope.Payload.AppStoreID))
		if err != nil {
			return err
		}
		if owned {
			return managedcontent.ErrConflict
		}
	}
	return nil
}

// guardContentSetAssignment checks effective native members before a generic
// set assignment can conflict with an owned managed-content installation.
func (a *App) guardContentSetAssignment(ctx context.Context, id mdm.EnrollmentID, set string) error {
	declarations, err := a.Engine.SetDeclarations(ctx, set)
	if err != nil {
		return err
	}
	for _, identifier := range declarations {
		decl, err := a.Engine.GetDeclaration(ctx, identifier)
		if err != nil {
			return err
		}
		var envelope struct {
			Type    string `json:"Type"`
			Payload struct {
				AppStoreID string `json:"AppStoreID"`
			} `json:"Payload"`
		}
		if json.Unmarshal(decl.Canonical, &envelope) != nil {
			return managedcontent.ErrProtocol
		}
		if envelope.Type != "com.apple.configuration.app.managed" {
			continue
		}
		owned, err := a.nativeContentOwned(ctx, contentAssetKey(id, envelope.Payload.AppStoreID))
		if err != nil {
			return err
		}
		if owned {
			return managedcontent.ErrConflict
		}
	}
	return nil
}

// guardContentDDMRoute reserves workflow-owned sets and declaration identities
// across generic DDM administrative routes. SDK adapters bypass these HTTP guards
// only through their atomic immutable ownership boundary.
func (a *App) guardContentDDMRoute(r *http.Request, pattern string) error {
	if a.AppsBooks == nil {
		return nil
	}
	if r.Method == http.MethodGet {
		return nil
	}
	if err := a.lockNativeContent(r.Context()); err != nil {
		return err
	}
	if strings.HasPrefix(r.PathValue("set"), "managedcontent:") {
		return managedcontent.ErrConflict
	}
	if strings.Contains(pattern, "/declarations/") && r.PathValue("id") != "" {
		owned, err := a.nativeContentOwned(r.Context(), contentDeclarationKey(r.PathValue("id")))
		if err != nil {
			return err
		}
		if owned {
			return managedcontent.ErrConflict
		}
	}
	if r.Method == http.MethodPut && strings.HasPrefix(pattern, "PUT /sets/") && r.PathValue("id") != "" {
		decl, err := a.Engine.GetDeclaration(r.Context(), r.PathValue("id"))
		if err != nil {
			return err
		}
		var envelope struct {
			Type    string `json:"Type"`
			Payload struct {
				AppStoreID string `json:"AppStoreID"`
			} `json:"Payload"`
		}
		if json.Unmarshal(decl.Canonical, &envelope) != nil {
			return managedcontent.ErrProtocol
		}
		if envelope.Type == "com.apple.configuration.app.managed" {
			targets, err := a.Engine.Store().AffectedEnrollments(r.Context(), nil, []string{r.PathValue("set")})
			if err != nil {
				return err
			}
			for _, target := range targets {
				owned, err := a.nativeContentOwned(r.Context(), contentAssetKey(target, envelope.Payload.AppStoreID))
				if err != nil {
					return err
				}
				if owned {
					return managedcontent.ErrConflict
				}
			}
		}
	}

	if r.Method == http.MethodPut && strings.HasPrefix(pattern, "PUT /enrollments/") {
		id, err := enrollmentFromPath(r)
		if err != nil {
			return err
		}
		return a.guardContentSetAssignment(r.Context(), id, r.PathValue("set"))
	}
	return nil
}

// lockNativeContent serializes participating generic administration with native
// content dispatch for the lifetime of their existing shared SQL transaction.
// No remote calls run inside this ownership boundary.
func (a *App) lockNativeContent(ctx context.Context) error {
	if a.protocol == nil {
		return nil
	}
	return a.protocol.Update(ctx, []string{contentServerKey("ownership", "native")}, func(state.Tx) error { return nil })
}
