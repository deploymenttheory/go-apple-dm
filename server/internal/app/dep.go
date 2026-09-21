package app

import (
	"context"
	"crypto/x509"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	depinmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
	depsql "github.com/deploymenttheory/go-apple-dm/server/depstore/sqlstore"
)

// DEPConfig connects the reference server to Apple's device enrollment
// service (decision record 0026). It is always available: accounts are
// created through the admin API, so nothing is required to enable it.
type DEPConfig struct {
	// BaseURL overrides https://mdmenrollment.apple.com (tests point it
	// at the fake service).
	BaseURL string
	// SyncInterval and AssignInterval independently schedule inventory sync
	// and profile assignment. Zero disables that background operation; the
	// admin API can still sync and assign.
	SyncInterval, AssignInterval time.Duration
	// ProfileURL is the DEP profile url; default PublicURL + /enroll/ade.
	ProfileURL string
	// UsePUT sends PUT for profile assignment (simulators).
	UsePUT     bool
	HTTPClient *http.Client
	// RootCAFile supplies private HTTPS trust instead of HTTPClient.
	RootCAFile string
	// Store overrides the DEP store (embedders with their own backend,
	// tests with a failing one); default follows Storage.
	Store dep.Store
}

// DEP errors.
var (
	ErrBadDEPRequest = errors.New("app: invalid DEP request")
)

// depService holds the client, store, and per-account loops.
type depService struct {
	app    *App
	store  dep.Store
	client *dep.Client
}

// newDEP selects injected, in-memory or SQL DEP storage and constructs the shared outbound
// client with configured trust and event delivery.
func (a *App) newDEP(ctx context.Context) (*depService, error) {
	var st dep.Store
	switch {
	case a.cfg.DEP.Store != nil:
		st = a.cfg.DEP.Store
	case a.db == nil:
		st = depinmem.New()
	default:
		s, err := depsql.Open(ctx, a.db, a.dialect, depsql.Options{Keyring: a.keyring})
		if err != nil {
			return nil, fmt.Errorf("app: DEP store: %w", err)
		}
		st = s
	}
	httpClient, err := outboundClient(a.cfg.DEP.HTTPClient, a.cfg.DEP.RootCAFile)
	if err != nil {
		return nil, err
	}
	client, err := dep.NewClient(
		dep.ClientConfig{
			Store:      st,
			BaseURL:    a.cfg.DEP.BaseURL,
			HTTPClient: httpClient,
			Clock:      a.cfg.Clock,
			Bus:        a.cfg.publisher(),
			Logger:     a.cfg.Logger,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("app: DEP client: %w", err)
	}
	return &depService{app: a, store: st, client: client}, nil
}

// syncer constructs an account-scoped inventory worker using the shared client, persistent
// state and application clock.
func (d *depService) syncer(account string) (*dep.Syncer, error) {
	s, err := dep.NewSyncer(
		dep.SyncerConfig{
			Client:  d.client,
			Store:   d.store,
			Account: account,
			Clock:   d.app.cfg.Clock,
			Bus:     d.app.cfg.publisher(),
			Logger:  d.app.cfg.Logger,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("app: DEP syncer: %w", err)
	}
	return s, nil
}

// assigner constructs an account-scoped assignment worker with readback enabled and the
// configured assignment HTTP method.
func (d *depService) assigner(account string) (*dep.Assigner, error) {
	a, err := dep.NewAssigner(
		dep.AssignerConfig{
			Client:   d.client,
			Store:    d.store,
			Account:  account,
			Clock:    d.app.cfg.Clock,
			Bus:      d.app.cfg.publisher(),
			Logger:   d.app.cfg.Logger,
			ReadBack: true,
			UsePUT:   d.app.cfg.DEP.UsePUT,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("app: DEP assigner: %w", err)
	}
	return a, nil
}

// runOnce syncs then assigns one account.
func (d *depService) runOnce(
	ctx context.Context,
	account string,
) (dep.SyncResult, dep.AssignResult, error) {
	s, err := d.syncer(account)
	if err != nil {
		return dep.SyncResult{}, dep.AssignResult{}, err
	}
	sres, err := s.RunOnce(ctx)
	if err != nil {
		return sres, dep.AssignResult{}, fmt.Errorf("app: DEP sync %s: %w", account, err)
	}
	a, err := d.assigner(account)
	if err != nil {
		return sres, dep.AssignResult{}, err
	}
	ares, err := a.RunOnce(ctx)
	if err != nil {
		return sres, ares, fmt.Errorf("app: DEP assign %s: %w", account, err)
	}
	return sres, ares, nil
}

// Run independently schedules sync and assignment until cancellation.
func (d *depService) Run(ctx context.Context) error {
	var workers sync.WaitGroup
	for _, job := range []struct {
		interval time.Duration
		assign   bool
	}{{d.app.cfg.DEP.SyncInterval, false}, {d.app.cfg.DEP.AssignInterval, true}} {
		if job.interval <= 0 {
			continue
		}
		workers.Go(func() { d.runScheduled(ctx, job.interval, job.assign) })
	}
	<-ctx.Done()
	workers.Wait()
	return nil
}

// runScheduled runs periodic inventory or assignment passes until cancellation, logging
// pass failures without terminating the schedule.
func (d *depService) runScheduled(ctx context.Context, interval time.Duration, assign bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.app.cfg.Clock.After(interval):
		}
		if err := d.runAccounts(ctx, assign); err != nil && ctx.Err() == nil {
			d.app.cfg.Logger.WarnContext(ctx, "app: DEP worker", "assignment", assign, "error", err)
		}
	}
}

// runAccounts visits credentialed accounts and runs the selected worker; account failures
// are logged while listing and cancellation errors stop the pass.
func (d *depService) runAccounts(ctx context.Context, assign bool) error {
	p := paging.Page{Limit: 1000}
	for {
		res, err := d.store.ListAccounts(ctx, p)
		if err != nil {
			return err
		}
		for _, acct := range res.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !acct.HasTokens() {
				continue
			}
			if assign {
				worker, e := d.assigner(acct.Name)
				err = e
				if err == nil {
					_, err = worker.RunOnce(ctx)
				}
			} else {
				worker, e := d.syncer(acct.Name)
				err = e
				if err == nil {
					_, err = worker.RunOnce(ctx)
				}
			}
			if err != nil && !errors.Is(err, dep.ErrBackoff) && ctx.Err() == nil {
				d.app.cfg.Logger.WarnContext(ctx, "app: DEP account worker", "account", acct.Name, "assignment", assign, "error", err)
			}
		}
		if res.NextCursor == "" {
			return nil
		}
		p.Cursor = res.NextCursor
	}
}

// profileURL is the DEP profile url for this server.
func (d *depService) profileURL() string {
	if d.app.cfg.DEP.ProfileURL != "" {
		return d.app.cfg.DEP.ProfileURL
	}
	return d.app.cfg.Enroll.PublicURL + PathADE
}

// handler is the admin API for DEP (mounted under the admin prefix as
// /dep/...):
//
//	GET  /dep/accounts
//	PUT  /dep/accounts/{name}/keypair            generate the token PKI; returns the certificate PEM for the portal
//	PUT  /dep/accounts/{name}/token              body: the portal's .p7m; imports the tokens
//	PUT  /dep/accounts/{name}/tokens             body: JSON tokens (development and tests)
//	GET  /dep/accounts/{name}/devices?cursor=&limit=
//	PUT  /dep/accounts/{name}/profile            body: DEP profile JSON; url defaults to this server
//	POST /dep/accounts/{name}/sync               sync then assign once
func (d *depService) routes() []adminRoute {
	var routes []adminRoute
	// #nosec G101 -- Values are permission identifiers, never credentials.
	actions := map[string]string{"GET /dep/accounts": ActionListDEP, "GET /dep/accounts/{name}/devices": ActionReadDEP, "PUT /dep/accounts/{name}/keypair": "manageDEPCredentials", "PUT /dep/accounts/{name}/token": "manageDEPCredentials", "PUT /dep/accounts/{name}/tokens": "manageDEPCredentials", "PUT /dep/accounts/{name}/profile": "manageDEPProfiles", "POST /dep/accounts/{name}/sync": "syncDEPDevices"}
	add := func(pattern string, handler http.HandlerFunc) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: actions[pattern], Family: "dep", Handler: handler})
	}
	add("GET /dep/accounts", func(w http.ResponseWriter, r *http.Request) {
		res, err := d.store.ListAccounts(
			r.Context(),
			paging.Page{Cursor: r.URL.Query().Get("cursor")},
		)
		if err != nil {
			writeError(w, depStatus(err), err)
			return
		}
		type row struct {
			Name, OrgName, ServerUUID, ProfileUUID string
			HasTokens                              bool
			State                                  dep.AccountState
			AccessTokenExpiry                      *time.Time
		}
		rows := make([]row, 0, len(res.Items))
		for _, a := range res.Items {
			rows = append(
				rows,
				row{
					Name:              a.Name,
					OrgName:           a.OrgName,
					ServerUUID:        a.ServerUUID,
					ProfileUUID:       a.ProfileUUID,
					HasTokens:         a.HasTokens(),
					State:             a.State,
					AccessTokenExpiry: a.AccessTokenExpiry,
				},
			)
		}
		writeJSON(w, http.StatusOK, map[string]any{"Items": rows, "NextCursor": res.NextCursor})
	})
	add(
		"PUT /dep/accounts/{name}/keypair",
		func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("name")
			kp, err := dep.GenerateTokenPKI(name, 365*24*time.Hour, d.app.cfg.Clock.Now())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			if err := d.store.PutKeypair(r.Context(), name, dep.StageStaged, kp); err != nil {
				writeError(w, depStatus(err), err)
				return
			}
			w.Header().Set("Content-Type", "application/x-pem-file")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(kp.CertPEM) // #nosec G705 -- a PEM certificate this server generated
		},
	)
	add("PUT /dep/accounts/{name}/token", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
		if err != nil || len(body) > MaxAdminBody || len(body) == 0 {
			writeError(
				w,
				http.StatusBadRequest,
				fmt.Errorf("%w: a .p7m body is required", ErrBadDEPRequest),
			)
			return
		}
		force := r.URL.Query().Get("force") == "true"
		detail, err := d.client.ImportToken(
			r.Context(),
			r.PathValue("name"),
			body,
			dep.ImportOptions{Force: force},
		)
		if err != nil {
			writeError(w, depStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	})
	add("PUT /dep/accounts/{name}/tokens", func(w http.ResponseWriter, r *http.Request) {
		var tokens dep.Tokens
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
		if err != nil || len(body) > MaxAdminBody {
			writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
			return
		}
		if err := json.Unmarshal(body, &tokens); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%w: %w", ErrBadDEPRequest, err))
			return
		}
		detail, err := d.client.StoreTokens(r.Context(), r.PathValue("name"), tokens)
		if err != nil {
			writeError(w, depStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	})
	add(
		"GET /dep/accounts/{name}/devices",
		func(w http.ResponseWriter, r *http.Request) {
			page := paging.Page{Cursor: r.URL.Query().Get("cursor")}
			if v := r.URL.Query().Get("limit"); v != "" {
				_, _ = fmt.Sscanf(v, "%d", &page.Limit)
			}
			res, err := d.store.ListDevices(
				r.Context(),
				r.PathValue("name"),
				dep.DeviceQuery{},
				page,
			)
			if err != nil {
				writeError(w, depStatus(err), err)
				return
			}
			writeJSON(w, http.StatusOK, res)
		},
	)
	add(
		"PUT /dep/accounts/{name}/profile",
		func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("name")
			body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
			if err != nil || len(body) > MaxAdminBody {
				writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
				return
			}
			var p dep.Profile
			if err := json.Unmarshal(body, &p); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("%w: %w", ErrBadDEPRequest, err))
				return
			}
			if p.URL == "" {
				p.URL = d.profileURL()
			}
			if p.ProfileName == "" {
				p.ProfileName = "go-apple-dm"
			}
			resp, err := d.client.DefineProfile(r.Context(), name, &p)
			if err != nil {
				writeError(w, depStatus(err), err)
				return
			}
			ctx := r.Context()
			err = d.store.Update(ctx, func(tx dep.Tx) error {
				if err := tx.PutProfile(ctx, name, &p); err != nil {
					return fmt.Errorf("app: DEP profile: %w", err)
				}
				acct, err := tx.GetAccount(ctx, name)
				if err != nil {
					return fmt.Errorf("app: DEP account: %w", err)
				}
				acct.ProfileUUID = resp.ProfileUUID
				if err := tx.PutAccount(ctx, acct); err != nil {
					return fmt.Errorf("app: DEP account: %w", err)
				}
				return nil
			})
			if err != nil {
				writeError(w, depStatus(err), err)
				return
			}
			writeJSON(w, http.StatusOK, resp)
		},
	)
	add("POST /dep/accounts/{name}/sync", func(w http.ResponseWriter, r *http.Request) {
		sres, ares, err := d.runOnce(r.Context(), r.PathValue("name"))
		if err != nil {
			writeError(w, depStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"Sync": sres, "Assign": ares})
	})
	return routes
}

// depStatus maps client and store errors to admin API statuses.
func depStatus(err error) int {
	var derr *dep.Error
	var perr *dep.ProfileError
	switch {
	case errors.As(err, &perr):
		return http.StatusBadRequest
	case errors.Is(err, dep.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, dep.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, dep.ErrInvalid),
		errors.Is(err, dep.ErrTokenExpired),
		errors.Is(err, dep.ErrTokenInvalid),
		errors.Is(err, dep.ErrTermsNotSigned):
		return http.StatusBadRequest
	case errors.As(err, &derr):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// CertificateFromPEM reads the first certificate of a PEM bundle; the
// admin keypair route returns one for the portal upload.
func CertificateFromPEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errPEM
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	return c, nil
}

// DEPStoreForTests exposes the DEP store to tests of the wiring.
func (a *App) DEPStoreForTests() dep.Store { return a.dep.store }

// DEPStatusForTests exposes the error mapping to tests of the wiring.
func DEPStatusForTests(err error) int { return depStatus(err) }
