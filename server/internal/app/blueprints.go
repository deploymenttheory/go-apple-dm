package app

import (
	"context"

	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// wireBlueprints constructs the Blueprint manager with shared protocol state, profile
// hosting and SQL transaction coordination when available.
func (a *App) wireBlueprints(ctx context.Context) error {
	st, err := a.protocolState(ctx)
	if err != nil {
		return err
	}
	cfg := blueprints.Config{Engine: a.Engine, State: st, ConfigurationProfiles: a.ConfigurationProfiles}
	if a.db != nil {
		cfg.Run = (sqlcommon.UnitOfWork{DB: a.db, Dialect: a.dialect}).Run
	}
	a.Blueprints, err = blueprints.New(cfg)
	return err
}
