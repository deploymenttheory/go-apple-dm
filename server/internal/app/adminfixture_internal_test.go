package app

import (
	"context"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// FixtureAdminCredential seeds operational tests without adding audit events.
// Security boundary tests exercise the HTTP bootstrap endpoint instead.
func FixtureAdminCredential(a *App) (adminauth.Token, error) {
	ctx := context.Background()
	_, token, err := a.admin.Bootstrap(ctx, "fixture-root", time.Time{})
	if errors.Is(err, adminauth.ErrConflict) {
		_, token, err = a.admin.Rotate(ctx, adminauth.Root, "fixture-root", time.Time{})
	}
	if err != nil {
		return "", err
	}
	_, err = a.admin.PutPolicy(ctx, adminauth.Root, adminauth.Policy{Name: "fixture-root", Source: `permit(principal == MDM::Principal::"fixture-root", action, resource);`})
	return token, err
}
