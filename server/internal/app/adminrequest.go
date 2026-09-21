package app

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

type (
	decodedCommandKey struct{}
	authorizationKey  struct{}
	actorKey          struct{}
)

type authorizationRequest struct {
	Resource types.EntityUID
	Context  map[string]types.Value
	Decision adminauth.Decision
	Outcome  string
	Status   int
}

// resolvePermission builds a typed request before any handler can mutate state.
func (a *App) resolvePermission(r *http.Request, rt *adminRoute) (*http.Request, error) {
	var command *mdm.Command
	if rt.Command {
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
		if err != nil || len(body) > MaxAdminBody {
			return r, ErrBodyTooLarge
		}
		command, err = mdm.DecodeCommand(body)
		if err != nil {
			return r, fmt.Errorf("%w: malformed command", mdm.ErrInvalidCommand)
		}
		rt.RequestType = command.RequestType
		if len(commands.ByID(command.RequestType)) > 0 {
			rt.Action = "enqueueCommand." + command.RequestType
		}
	}
	action, ok := a.admin.Registry().Lookup(rt.Action)
	if !ok {
		return r, adminauth.ErrUnknownAction
	}
	rt.Sensitive = action.Sensitive
	resource := adminauth.SystemResource
	switch action.Resource {
	case adminauth.EntityEnrollment:
		id, err := enrollmentFromPath(r)
		if err != nil {
			return r, err
		}
		resource = types.NewEntityUID(adminauth.EntityEnrollment, types.String(id.Channel.String()+"/"+url.PathEscape(id.ID)+"/"+url.PathEscape(id.ParentID)))
	case adminauth.EntityDeclaration, adminauth.EntityBlueprint:
		name := r.PathValue(rt.ResourceParam)
		if name == "" {
			body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
			if err != nil || len(body) > MaxAdminBody {
				return r, ErrBodyTooLarge
			}
			var envelope struct{ Identifier string }
			if json.Unmarshal(body, &envelope) != nil || envelope.Identifier == "" {
				return r, fmt.Errorf("%w: missing Identifier", adminauth.ErrInvalid)
			}
			name = envelope.Identifier
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		resource = types.NewEntityUID(action.Resource, types.String(name))
	case adminauth.EntityDEPAccount, adminauth.EntityConfigurationProfile, adminauth.EntitySet:
		name := r.PathValue(rt.ResourceParam)
		if name == "" {
			return r, fmt.Errorf("%w: missing resource", adminauth.ErrInvalid)
		}
		resource = types.NewEntityUID(action.Resource, types.String(name))
	}
	ctx := map[string]types.Value{"method": types.String(r.Method)}
	for _, key := range []string{"channel", "set", "blueprint"} {
		if _, declared := action.Context[key]; declared {
			if value := r.PathValue(key); value != "" {
				ctx[key] = types.String(value)
			}
		}
	}
	if rt.RequestType != "" {
		ctx["requestType"] = types.String(rt.RequestType)
	}
	request := &authorizationRequest{Resource: resource, Context: ctx}
	r = r.WithContext(context.WithValue(r.Context(), authorizationKey{}, request))
	if command != nil {
		r = r.WithContext(context.WithValue(r.Context(), decodedCommandKey{}, command))
	}
	return r, nil
}

// resourceParameter is chosen from the declared action type at registration,
// rather than guessing an entity type from arbitrary URL variable names.
func resourceParameter(kind types.EntityType) string {
	switch kind {
	case adminauth.EntityDeclaration:
		return "id"
	case adminauth.EntityBlueprint:
		return "blueprint"
	case adminauth.EntityDEPAccount:
		return "name"
	case adminauth.EntityConfigurationProfile:
		return "revision"
	case adminauth.EntitySet:
		return "set"
	}
	return ""
}

// authorityMutation identifies principal, role and policy mutations that require root
// authority independently of Cedar grants.
func authorityMutation(rt adminRoute) bool {
	return rt.Action == ActionManagePolicies || rt.Action == ActionManagePrincipals || rt.Action == ActionManageRoles
}

// commandActionIDs collects explicit per-command permissions and the unknown-command
// permission from the action catalogue.
func commandActionIDs() []string {
	var out []string
	for _, action := range AdminActions() {
		if strings.HasPrefix(action.ID, "enqueueCommand.") || action.ID == ActionEnqueueUnknownCommand {
			out = append(out, action.ID)
		}
	}
	return out
}
