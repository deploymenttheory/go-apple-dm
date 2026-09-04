package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/app"
)

// TestDDMHopNeedsACredential holds both ends of the declarative management hop
// to an authenticated channel. The hop forwards a check-in verbatim and the
// ddm role resolves the enrollment from that body, so without a credential any
// caller reaching the listener names any enrollment and reads its declarations
// or writes its status reports.
func TestDDMHopNeedsACredential(t *testing.T) {
	t.Parallel()
	for name, cfg := range map[string]app.Config{
		"Serving":    {Role: app.RoleDDM, Storage: "inmem"},
		"Forwarding": {Role: app.RoleMDM, Storage: "inmem", DDMURL: "https://ddm.example"},
		"OneSided":   {Role: app.RoleDDM, Storage: "inmem", DDMRecvKey: []byte("recv")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg.Logger = quiet
			a, err := app.Build(context.Background(), cfg)
			if err == nil {
				_ = a.Close()
				t.Fatal("Build accepted an unauthenticated hop")
			}
			if !errors.Is(err, app.ErrConfig) {
				t.Fatalf("Build = %v, want a configuration error", err)
			}
		})
	}
}
