package dmctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	adminsql "github.com/deploymenttheory/go-apple-dm/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/recovery"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// runRoles parses and executes the roles subcommand, reporting argument and operation
// failures to the CLI caller.
func runRoles(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: roles needs list, get, put, or delete", ErrUsage)
	}
	fs := e.verbFlags("roles " + args[0])
	description := fs.String("description", "", "role description")
	cursor := fs.String("cursor", "", "pagination cursor")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(args) > 1 && args[1] == "-h" {
		return nil
	}
	method, path := http.MethodGet, "/roles"
	var body any
	query := url.Values{}
	switch args[0] {
	case "list":
		if len(rest) != 0 {
			return fmt.Errorf("%w: roles list takes no arguments", ErrUsage)
		}
		query.Set("cursor", *cursor)
	case "get", "put", "delete":
		if len(rest) != 1 {
			return fmt.Errorf("%w: roles %s needs a name", ErrUsage, args[0])
		}
		path += "/" + url.PathEscape(rest[0])
		if args[0] == "put" {
			method = http.MethodPut
			body = map[string]string{"Description": *description}
		}
		if args[0] == "delete" {
			method = http.MethodDelete
		}
	default:
		return fmt.Errorf("%w: unknown roles command", ErrUsage)
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	return e.emit(resp, nil)
}

// runAuth parses and executes the auth subcommand, reporting argument and operation
// failures to the CLI caller.
func runAuth(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: auth needs me, schema, bootstrap, or recover-root", ErrUsage)
	}
	if args[0] == "recover-root" {
		return recoverRoot(ctx, e, args[1:])
	}
	fs := e.verbFlags("auth " + args[0])
	bootstrap := fs.String("bootstrap-token", "", "bootstrap credential, @file, or env:NAME")
	expires := fs.String("expires", "", "RFC3339 token expiration")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(args) > 1 && args[1] == "-h" {
		return nil
	}
	if *bootstrap != "" {
		e.opts.token = *bootstrap
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	switch args[0] {
	case "me", "schema":
		if len(rest) != 0 {
			return fmt.Errorf("%w: auth %s takes no arguments", ErrUsage, args[0])
		}
		path := "/auth/me"
		if args[0] == "schema" {
			path = "/schema"
		}
		resp, err := c.Do(ctx, http.MethodGet, path, nil, nil)
		if err != nil {
			return err
		}
		return e.emit(resp, nil)
	case "bootstrap":
		if len(rest) != 1 {
			return fmt.Errorf("%w: auth bootstrap needs a principal name", ErrUsage)
		}
		body := map[string]any{"Name": rest[0]}
		if *expires != "" {
			when, err := time.Parse(time.RFC3339, *expires)
			if err != nil {
				return err
			}
			body["ExpiresAt"] = when
		}
		resp, err := c.Do(ctx, http.MethodPost, "/auth/bootstrap", nil, body)
		if err != nil {
			return err
		}
		return e.emitToken(resp)
	default:
		return fmt.Errorf("%w: unknown auth command", ErrUsage)
	}
}

// recoverRoot creates a recovery root principal through the local database under an
// owned, drained maintenance fence and writes its credential to a new private file.
func recoverRoot(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("auth recover-root")
	setup := fs.String("setup-file", e.getenv("DM_SETUP_FILE"), "local setup file")
	ticketFile := fs.String("ticket-file", "", "owned, drained maintenance ticket")
	output := fs.String("token-file", "", "new private output file for the recovery credential")
	rest, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "-h" {
		return nil
	}
	if len(rest) != 1 || *setup == "" || *ticketFile == "" || *output == "" {
		return fmt.Errorf("%w: recover-root requires a new principal name, -setup-file, -ticket-file, and -token-file", ErrUsage)
	}
	cfg, err := app.LoadSetupFile(*setup, e.getenv)
	if err != nil {
		return err
	}
	db, err := recovery.OpenDatabase(ctx, cfg.Storage, cfg.DSN)
	if err != nil {
		return err
	}
	defer func() { _ = db.DB.Close() }()
	control, err := maintenance.Open(ctx, db.DB, db.Dialect, false)
	if err != nil {
		return err
	}
	ticket, err := readRecoveryIdentityFile(*ticketFile)
	if err != nil {
		return err
	}
	if err := control.WaitDrained(ctx, ticket); err != nil {
		return err
	}
	store, err := adminsql.Open(ctx, db.DB, db.Dialect, adminsql.Options{SkipMigrate: true})
	if err != nil {
		return err
	}
	trail, err := eventstore.Open(ctx, db.DB, db.Dialect)
	if err != nil {
		return err
	}
	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		return err
	}
	manager, err := adminauth.New(store, reg)
	if err != nil {
		return err
	}
	var principal adminauth.Principal
	var token adminauth.Token
	err = (sqlcommon.UnitOfWork{DB: db.DB, Dialect: db.Dialect}).Run(ctx, func(ctx context.Context) error {
		q := sqlcommon.Query(ctx, db.DB)
		if _, err := q.ExecContext(ctx, "UPDATE maintenance_state SET token = token WHERE id = 1"); err != nil {
			return err
		}
		state, err := control.Status(ctx)
		if err != nil {
			return err
		}
		if state.Token != ticket || !state.Ready() {
			return maintenance.ErrOwner
		}
		principal, token, err = manager.CreatePrincipal(ctx, adminauth.Root, adminauth.Principal{Name: rest[0], Root: true}, time.Time{})
		if err != nil {
			return err
		}
		err = trail.Capture(ctx, eventsink.Record{EventID: "recover-root-" + principal.TokenID, At: time.Now().UTC(), Type: "admin-action", Actor: "local-recovery", Fields: map[string]any{"Action": "recoverRoot", "Resource": principal.Name, "TokenID": principal.TokenID, "Outcome": "succeeded"}}, nil)
		return err
	})
	if err != nil {
		return err
	}
	if err := writeNewPrivateFile(*output, []byte(string(token)+"\n")); err != nil {
		return fmt.Errorf("recovery principal %q was created but credential output failed; recover with a new name: %w", principal.Name, err)
	}
	return json.NewEncoder(e.stdout).Encode(map[string]string{"Principal": principal.Name, "TokenFile": *output, "Maintenance": "paused"})
}
