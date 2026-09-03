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
	"time"

	"github.com/deploymenttheory/go-apple-dm/dep"
	depinmem "github.com/deploymenttheory/go-apple-dm/dep/inmem"
	depsql "github.com/deploymenttheory/go-apple-dm/dep/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// DEPConfig connects the reference server to Apple's device enrollment
// service (decision record 0026). It is always available: accounts are
// created through the admin API, so nothing is required to enable it.
type DEPConfig struct {
	// BaseURL overrides https://mdmenrollment.apple.com (tests point it
	// at the fake service).
	BaseURL string
	// SyncInterval and AssignInterval drive the background worker;
	// zero disables it (the admin API can still sync and assign).
	SyncInterval, AssignInterval time.Duration
	// ProfileURL is the DEP profile url; default PublicURL + /enroll/ade.
	ProfileURL string
	// UsePUT sends PUT for profile assignment (simulators).
	UsePUT     bool
	HTTPClient *http.Client
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

func (a *App) newDEP(ctx context.Context) (*depService, error) {
	var st dep.Store
	switch {
	case a.cfg.DEP.Store != nil:
		st = a.cfg.DEP.Store
	case a.db == nil:
		st = depinmem.New()
	default:
		s, err := depsql.Open(ctx, a.db, a.dialect, depsql.Options{})
		if err != nil {
			return nil, fmt.Errorf("app: DEP store: %w", err)
		}
		st = s
	}
	client, err := dep.NewClient(
		dep.ClientConfig{
			Store:      st,
			BaseURL:    a.cfg.DEP.BaseURL,
			HTTPClient: a.cfg.DEP.HTTPClient,
			Clock:      a.cfg.Clock,
			Bus:        a.cfg.Bus,
			Logger:     a.cfg.Logger,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("app: DEP client: %w", err)
	}
	return &depService{app: a, store: st, client: client}, nil
}

func (d *depService) syncer(account string) (*dep.Syncer, error) {
	s, err := dep.NewSyncer(
		dep.SyncerConfig{
			Client:  d.client,
			Store:   d.store,
			Account: account,
			Clock:   d.app.cfg.Clock,
			Bus:     d.app.cfg.Bus,
			Logger:  d.app.cfg.Logger,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("app: DEP syncer: %w", err)
	}
	return s, nil
}

func (d *depService) assigner(account string) (*dep.Assigner, error) {
	a, err := dep.NewAssigner(
		dep.AssignerConfig{
			Client:   d.client,
			Store:    d.store,
			Account:  account,
			Clock:    d.app.cfg.Clock,
			Bus:      d.app.cfg.Bus,
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

// Run is the background worker: every SyncInterval it syncs and assigns
// every account with tokens. Disabled when the interval is zero. It
// returns nil once the context ends: a stopped worker is not a failure.
func (d *depService) Run(ctx context.Context) error {
	interval := d.app.cfg.DEP.SyncInterval
	if interval <= 0 {
		<-ctx.Done()
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-d.app.cfg.Clock.After(interval):
		}
		res, err := d.store.ListAccounts(ctx, storage.Page{Limit: 1000})
		if err != nil {
			d.app.cfg.Logger.WarnContext(ctx, "app: DEP accounts", "error", err)
			continue
		}
		for _, acct := range res.Items {
			if !acct.HasTokens() {
				continue
			}
			if _, _, err := d.runOnce(ctx, acct.Name); err != nil && ctx.Err() == nil {
				d.app.cfg.Logger.WarnContext(
					ctx,
					"app: DEP sync",
					"account",
					acct.Name,
					"error",
					err,
				)
			}
		}
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
func (d *depService) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dep/accounts", func(w http.ResponseWriter, r *http.Request) {
		res, err := d.store.ListAccounts(
			r.Context(),
			storage.Page{Cursor: r.URL.Query().Get("cursor")},
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
	mux.HandleFunc(
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
	mux.HandleFunc("PUT /dep/accounts/{name}/token", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("PUT /dep/accounts/{name}/tokens", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc(
		"GET /dep/accounts/{name}/devices",
		func(w http.ResponseWriter, r *http.Request) {
			page := storage.Page{Cursor: r.URL.Query().Get("cursor")}
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
	mux.HandleFunc(
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
	mux.HandleFunc("POST /dep/accounts/{name}/sync", func(w http.ResponseWriter, r *http.Request) {
		sres, ares, err := d.runOnce(r.Context(), r.PathValue("name"))
		if err != nil {
			writeError(w, depStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"Sync": sres, "Assign": ares})
	})
	return mux
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
