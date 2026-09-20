package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// authorizeBench uses the same first-root handoff as a normal deployment.
func (e *Environment) authorizeBench(ctx context.Context) error {
	saved, err := e.Workspace.token()
	if err != nil {
		return err
	}
	if _, _, err := HTTP(ctx, e.Client, e.URL, saved, http.MethodGet, "/auth/me", nil); err == nil {
		e.Token = saved
	} else {
		body, _, err := HTTP(ctx, e.Client, e.URL, e.Token, http.MethodPost, "/auth/bootstrap", strings.NewReader(`{"Name":"bench-root"}`))
		if err != nil {
			return err
		}
		var result struct {
			Token string `json:"Token"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}
		e.Token = result.Token
		if err := privateFile(e.Workspace.path("mdm", "admin-credential"), []byte(e.Token+"\n")); err != nil {
			return err
		}
	}
	// An explicit policy, retained for review, authorizes the bench's scenarios.
	policy, err := json.Marshal(map[string]string{"Source": `permit(principal == MDM::Principal::"bench-root", action, resource);`, "Description": "Explicit authority for the local reference-server bench."})
	if err != nil {
		return err
	}
	_, _, err = HTTP(ctx, e.Client, e.URL, e.Token, http.MethodPut, "/policies/bench-root", bytes.NewReader(policy))
	return err
}
