package app

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/adminauth"
	"github.com/deploymenttheory/go-apple-mdm/axm"
	"github.com/deploymenttheory/go-apple-mdm/cms"
	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/ddm/adapter/inproc"
	"github.com/deploymenttheory/go-apple-mdm/ddm/adapter/proxyclient"
	"github.com/deploymenttheory/go-apple-mdm/ddm/adapter/proxyserver"
	ddminmem "github.com/deploymenttheory/go-apple-mdm/ddm/inmem"
	"github.com/deploymenttheory/go-apple-mdm/ddm/sqlstore"
	"github.com/deploymenttheory/go-apple-mdm/dep"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/httpapi"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/push"
	"github.com/deploymenttheory/go-apple-mdm/service"
	"github.com/deploymenttheory/go-apple-mdm/storage"
	"github.com/deploymenttheory/go-apple-mdm/storage/inmem"
	"github.com/deploymenttheory/go-apple-mdm/storage/mysql"
	"github.com/deploymenttheory/go-apple-mdm/storage/postgres"
	"github.com/deploymenttheory/go-apple-mdm/storage/sqlcommon"
	"github.com/deploymenttheory/go-apple-mdm/storage/sqlite"
)

// Role selects what a process serves.
type Role string

// Roles.
const (
	RoleMDM Role = "mdm"
	RoleDDM Role = "ddm"
	RoleAll Role = "all"
)

// Paths served by the handler.
const (
	PathMDM     = "/mdm"
	PathDDM     = "/ddm"
	PathHealthz = "/healthz"
	PathAdmin   = "/admin/v1/"
)

// Config is the process configuration; see ParseEnv for the MDM_*
// variables and cmd/mdmserver for the flags.
type Config struct {
	Role    Role
	Listen  string
	Storage string // sqlite, postgres, mysql, inmem
	DSN     string // file path for sqlite
	// DDMURL, on the mdm role, forwards DeclarativeManagement check-ins to
	// a ddm role through proxyclient; empty means the local engine.
	DDMURL string
	// DDMSendKey signs what this role sends across the hop; DDMRecvKey
	// verifies what it receives.
	DDMSendKey, DDMRecvKey []byte
	// AdminToken enables the admin API on the ddm and all roles with a single
	// static credential that bypasses policy. It is the development opt-out;
	// AdminStore is what a deployment uses.
	AdminToken string
	// AdminStore holds admin principals and Cedar policies. When set, every
	// admin request is authenticated against it and authorized by policy
	// (decision record 0034). It takes precedence over AdminToken.
	AdminStore adminauth.Store
	// CAFile is a PEM bundle of roots that device identities chain to;
	// the mdm role then verifies Mdm-Signature on every check-in and
	// connect. CARoots is the parsed form (tests set it directly).
	CAFile  string
	CARoots *x509.CertPool
	// CertHeader names a header carrying the client certificate from a
	// TLS-terminating proxy (httpapi.CertFromHeader). Used when no CA is
	// configured. With neither, the certificate must come from TLS on
	// this process (httpapi.CertFromTLS).
	CertHeader string
	// Subscriptions enables the synthesised status-subscriptions
	// declaration (decision record 0021).
	Subscriptions bool
	// Enroll turns the enrollment routes on (SCEP, discovery,
	// account-driven, ADE).
	Enroll EnrollConfig
	// AxM connects Apple Business Manager or Apple School Manager; its
	// admin routes live under the admin API on the ddm and all roles.
	AxM AxMConfig
	// DEP configures the device enrollment service client and worker;
	// its admin routes live under the admin API too.
	DEP DEPConfig
	// Push selects where APNs credentials come from. With no source the
	// server queues commands and never wakes a device, which is what it did
	// before phase 8.
	Push   PushConfig
	Logger *slog.Logger
	Clock  clock.Clock
	Bus    *event.Bus
}

// ErrConfig reports an invalid configuration.
var ErrConfig = errors.New("app: invalid configuration")

// App is a built process.
type App struct {
	Handler  http.Handler
	Core     *service.Core
	Engine   *ddm.Engine
	Notifier *ddm.Notifier
	Store    storage.Store
	// AxM is the Business Manager client when configured.
	AxM *axm.Client
	// DEP is the device enrollment service; nil on the mdm role.
	DEP *dep.Client
	// Push wakes devices; nil when no push source is configured.
	Push *push.Notifier
	// admin authorizes admin callers against the stored Cedar policies. It
	// is nil when the deployment configured the static MDM_ADMIN_TOKEN
	// instead, which bypasses policy by design (decision record 0034).
	admin *adminauth.Manager
	// adminTable is the mounted admin route table, served by GET /routes.
	adminTable []adminRoute
	acme       *acmeService
	dep        *depService
	cfg        Config
	enroll     *enrollment
	db         *sql.DB
	dialect    sqlcommon.Dialect
	closers    []func() error
}

// Build validates cfg, opens storage, and wires the role.
func Build(ctx context.Context, cfg Config) (*App, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if err := cfg.roots(); err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	a := &App{cfg: cfg}
	if err := a.openStorage(ctx); err != nil {
		return nil, err
	}
	if err := a.wire(ctx); err != nil {
		_ = a.Close()
		return nil, err
	}
	return a, nil
}

func (c Config) validate() error {
	switch c.Role {
	case RoleMDM, RoleDDM, RoleAll:
	default:
		return fmt.Errorf("%w: role %q (want mdm, ddm, or all)", ErrConfig, c.Role)
	}
	switch c.Storage {
	case "sqlite", "postgres", "mysql":
		if c.DSN == "" {
			return fmt.Errorf("%w: %s storage needs a DSN", ErrConfig, c.Storage)
		}
	case "inmem":
	default:
		return fmt.Errorf(
			"%w: storage %q (want sqlite, postgres, mysql, or inmem)",
			ErrConfig,
			c.Storage,
		)
	}
	if c.DDMURL != "" && c.Role != RoleMDM {
		return fmt.Errorf("%w: DDM URL is only for the mdm role", ErrConfig)
	}
	if err := c.Enroll.validate(); err != nil {
		return err
	}
	if err := c.Push.validate(); err != nil {
		return err
	}
	return c.AxM.validate()
}

// roots loads CAFile into CARoots when set.
func (c *Config) roots() error {
	if c.CAFile == "" {
		return nil
	}
	pem, err := os.ReadFile(c.CAFile)
	if err != nil {
		return fmt.Errorf("%w: CA file: %w", ErrConfig, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("%w: CA file %s holds no certificates", ErrConfig, c.CAFile)
	}
	c.CARoots = pool
	return nil
}

// certSource picks how the mdm role learns the device certificate.
func (a *App) certSource() func(http.Handler) http.Handler {
	switch {
	case a.cfg.CARoots != nil:
		return httpapi.CertFromMdmSignature(
			cms.VerifyOptions{
				Roots:     a.cfg.CARoots,
				ClockSkew: 5 * time.Minute,
				Now:       a.cfg.Clock.Now,
			},
			0,
		)
	case a.cfg.CertHeader != "":
		return httpapi.CertFromHeader(a.cfg.CertHeader)
	default:
		a.cfg.Logger.Warn(
			"app: no CA or certificate header configured; device certificates must arrive over TLS on this process",
		)
		return httpapi.CertFromTLS
	}
}

func (a *App) openStorage(ctx context.Context) error {
	var (
		dialect sqlcommon.Dialect
		db      *sql.DB
	)
	switch a.cfg.Storage {
	case "inmem":
		a.Store = inmem.New()
		return nil
	case "sqlite":
		s, err := sqlite.Open(ctx, a.cfg.DSN, sqlite.Options{})
		if err != nil {
			return fmt.Errorf("app: sqlite: %w", err)
		}
		a.Store, db, dialect = s, s.DB(), sqlite.Dialect
		a.closers = append(a.closers, s.Close)
	case "postgres":
		s, err := postgres.Open(ctx, a.cfg.DSN, postgres.Options{})
		if err != nil {
			return fmt.Errorf("app: postgres: %w", err)
		}
		a.Store, db, dialect = s, s.DB(), postgres.Dialect
		a.closers = append(a.closers, s.Close)
	default:
		s, err := mysql.Open(ctx, a.cfg.DSN, mysql.Options{})
		if err != nil {
			return fmt.Errorf("app: mysql: %w", err)
		}
		a.Store, db, dialect = s, s.DB(), mysql.Dialect
		a.closers = append(a.closers, s.Close)
	}
	a.db = db
	a.dialect = dialect
	return nil
}

func (a *App) ddmStore(ctx context.Context) (ddm.Store, error) {
	if a.db == nil {
		return ddminmem.New(), nil
	}
	st, err := sqlstore.Open(ctx, a.db, a.dialect, sqlstore.Options{})
	if err != nil {
		return nil, fmt.Errorf("app: ddm store: %w", err)
	}
	return st, nil
}

// wire builds the engine, core, adapters, and routes for the role.
func (a *App) wire(ctx context.Context) error {
	cfg := a.cfg
	st, err := a.ddmStore(ctx)
	if err != nil {
		return err
	}
	engine, err := ddm.New(ddm.Config{
		Store: st, Bus: cfg.Bus, Clock: cfg.Clock, Logger: cfg.Logger,
		Wake: func() {
			if a.Notifier != nil {
				a.Notifier.Kick()
			}
		},
		Subscriptions: ddm.Subscriptions{Enabled: cfg.Subscriptions},
	})
	if err != nil {
		return fmt.Errorf("app: engine: %w", err)
	}
	a.Engine = engine
	// Without a Pusher the notifier treats every group as delivered, so a
	// declaration change queues a command and never wakes the device. That
	// was the reference server's behaviour before phase 8.
	a.Push, err = a.wirePush()
	if err != nil {
		return err
	}
	var pusher ddm.Pusher
	if a.Push != nil {
		pusher = a.Push
	}
	a.Notifier, err = ddm.NewNotifier(
		ddm.NotifierConfig{
			Store:    st,
			Tokens:   engine,
			Enqueuer: a.Store,
			Pusher:   pusher,
			Bus:      cfg.Bus,
			Clock:    cfg.Clock,
			Logger:   cfg.Logger,
		},
	)
	if err != nil {
		return fmt.Errorf("app: notifier: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+PathHealthz, a.healthz)
	if cfg.Role == RoleMDM || cfg.Role == RoleAll {
		dm := inproc.Handler(engine)
		if cfg.DDMURL != "" {
			dm, err = proxyclient.Handler(
				proxyclient.Config{
					URL:     cfg.DDMURL,
					SendKey: cfg.DDMSendKey,
					RecvKey: cfg.DDMRecvKey,
				},
			)
			if err != nil {
				return fmt.Errorf("app: proxyclient: %w", err)
			}
		}
		// The device enrollment service is built before enrollment,
		// because an ACME policy can make an assignment in it a condition
		// of issuing an identity, and a policy that cannot reach its store
		// should fail here rather than answer every device with a server
		// error.
		if cfg.Role != RoleMDM {
			if err := a.wireDEP(ctx); err != nil {
				return err
			}
		}
		enrollHooks, err := a.wireEnrollment(ctx, mux)
		if err != nil {
			return err
		}
		core, err := service.New(service.Config{
			Store:  a.Store,
			Bus:    cfg.Bus,
			Clock:  cfg.Clock,
			Logger: cfg.Logger,
			Hooks: append(
				[]service.Hook{ddm.NewServiceHook(engine, a.Store, cfg.Logger)},
				enrollHooks...),
			DeclarativeManagement: dm,
			RequireUserAuth:       cfg.Enroll.RequireUserAuth,
		})
		if err != nil {
			return fmt.Errorf("app: core: %w", err)
		}
		a.Core = core
		api := httpapi.Handler(
			httpapi.Config{Checkin: core, Connect: core, Logger: cfg.Logger, Now: cfg.Clock.Now},
		)
		mux.Handle(
			PathMDM,
			a.certSource()(api),
		) // check-in is PUT, connect is PUT; httpapi enforces methods
	}
	if cfg.Role == RoleDDM {
		ps, err := proxyserver.Handler(
			proxyserver.Config{
				Backend: engine,
				RecvKey: cfg.DDMRecvKey,
				SendKey: cfg.DDMSendKey,
				Logger:  cfg.Logger,
			},
		)
		if err != nil {
			return fmt.Errorf("app: proxyserver: %w", err)
		}
		mux.Handle(PathDDM+"/", http.StripPrefix(PathDDM, ps))
	}
	if cfg.Role != RoleMDM && a.adminEnabled() {
		if cfg.AdminStore != nil {
			m, err := adminauth.New(cfg.AdminStore, mustAdminRegistry(), adminauth.WithClock(cfg.Clock))
			if err != nil {
				return fmt.Errorf("app: admin authorization: %w", err)
			}
			a.admin = m
		}
		var routes []adminRoute
		routes = append(routes, a.introspectionRoutes()...)
		routes = append(routes, a.ddmAdminRoutes()...)
		if a.admin != nil {
			routes = append(routes, a.principalRoutes()...)
		}
		if cfg.AxM.Enabled() {
			client, err := a.newAxM(ctx)
			if err != nil {
				return err
			}
			a.AxM = client
			routes = append(routes, adminRoute{Pattern: "/axm/", Action: ActionManageBusinessMgr, Family: "axm", Handler: a.axmHandler(client)})
		}
		if a.dep == nil {
			if err := a.wireDEP(ctx); err != nil {
				return err
			}
		}
		routes = append(routes, adminRoute{Pattern: "/dep/", Action: ActionManageDEP, Family: "dep", Handler: a.dep.handler()})
		if a.acme != nil {
			routes = append(routes, adminRoute{Pattern: "/acme/", Action: ActionReadACME, Family: "acme", Handler: a.acme.handler()})
		}
		admin, err := a.buildAdminMux(routes)
		if err != nil {
			return err
		}
		mux.Handle(PathAdmin, http.StripPrefix(PathAdmin[:len(PathAdmin)-1], admin))
	}
	a.Handler = mux
	return nil
}

// Run drives the notifier until ctx is cancelled. The HTTP listener is the
// caller's (cmd/mdmserver, or httptest in tests).
func (a *App) Run(ctx context.Context) error {
	errc := make(chan error, 2)
	go func() { errc <- a.Notifier.Run(ctx) }()
	if a.dep != nil {
		go func() { errc <- a.dep.Run(ctx) }()
	} else {
		errc <- nil
	}
	var first error
	for range 2 {
		if err := <-errc; err != nil && !errors.Is(err, context.Canceled) && first == nil {
			first = err
		}
	}
	if first != nil {
		return fmt.Errorf("app: worker: %w", first)
	}
	return nil
}

// Close releases storage.
func (a *App) Close() error {
	var errs []error
	for _, c := range a.closers {
		if err := c(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// healthz answers 200 when storage answers, 503 otherwise (E2E-015 in
// minimal form).
func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	if a.db != nil {
		if err := a.db.PingContext(r.Context()); err != nil {
			a.cfg.Logger.WarnContext(r.Context(), "app: healthz", "error", err)
			http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// wireDEP builds the device enrollment service once.
func (a *App) wireDEP(ctx context.Context) error {
	if a.dep != nil {
		return nil
	}
	svc, err := a.newDEP(ctx)
	if err != nil {
		return err
	}
	a.dep, a.DEP = svc, svc.client
	return nil
}
