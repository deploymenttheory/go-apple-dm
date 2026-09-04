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
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/secrets"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
	admininmem "github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/inmem"
	adminsql "github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmadapter/inproc"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmadapter/proxyclient"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmadapter/proxyserver"
	ddminmem "github.com/deploymenttheory/go-apple-dm/v3/server/ddmstore/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmsync"
	"github.com/deploymenttheory/go-apple-dm/v3/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/v3/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/v3/server/pushnotify"
	"github.com/deploymenttheory/go-apple-dm/v3/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/mysql"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/postgres"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/sqlite"
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

// Config is the process configuration; see ParseEnv for the DM_*
// variables and cmd/dmserver for the flags.
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
	// AllowReenroll accepts an Authenticate whose identity certificate
	// differs from the one the enrollment pins, and replaces the pin with it.
	// It is service.AllowReenroll, the library default.
	//
	// The reference server defaults to service.DenyReenroll. A certificate
	// carries no binding to an enrollment id, and chaining to the enrollment
	// CA establishes only that the certificate is one we issued, so a
	// permissive policy makes every certificate the CA issues a key to every
	// enrollment. Deployments that need devices to re-enrol themselves after
	// a wipe set DM_ALLOW_REENROLL=true.
	AllowReenroll bool
	// StorageKeys names the keys that seal the secret columns of a persistent
	// store: unlock tokens, bootstrap tokens, APNs push keys and user auth
	// tokens. The first is the active key every write seals under, and the
	// rest are retired keys reads still accept, so a rotation is a prepended
	// name followed by Rewrap.
	//
	// Without a keyring those columns are written in clear, and a stolen
	// backup, replica or volume yields the push key, which wakes and
	// impersonates the whole fleet. A persistent store therefore requires one.
	StorageKeys []string
	// StorageKeysStrict refuses to read a secret column that is not sealed.
	// It belongs on once Rewrap has run everywhere; before that it would
	// reject rows written before the keyring existed.
	StorageKeysStrict bool
	// SecretsDir resolves StorageKeys from files in one directory, the shape
	// Docker and Kubernetes secret mounts take. Empty reads them from the
	// environment as DM_STORAGE_KEY_<NAME>.
	SecretsDir string
	// Secrets overrides both, for tests and embedding.
	Secrets secrets.Provider
	// AdminToken enables the admin API on the ddm and all roles with a single
	// static credential that authenticates as root and bypasses policy.
	//
	// Alongside a principal store it is the break-glass credential, and it
	// keeps working rather than being superseded: an empty principal store
	// authenticates nobody, and the route that creates the first principal is
	// itself authorized, so without it there is no way in. Its use is audited
	// under the actor "break-glass" and logged at warn on every request.
	//
	// It has no expiry and cannot be revoked without restarting the process.
	// While it is set, every least-privilege property record 0034 claims is
	// void for whoever holds it, so a deployment sets it to create real
	// principals and then unsets it. An audit record with the actor
	// "break-glass" after that point is an incident. See
	// docs/operations/deployment.md.
	AdminToken string
	// AdminStore holds admin principals and Cedar policies. When set, an admin
	// request that does not present AdminToken is authenticated against it and
	// authorized by policy (decision record 0034). Injecting a store here
	// overrides AdminStoreEnabled, which is how tests supply a fake.
	AdminStore adminauth.Store
	// AdminStoreEnabled opens the principal and policy store on the process's
	// own database, so principals work in the shipped binary rather than only
	// where a caller injects AdminStore. Off by default: turning it on mounts
	// the admin API, which is a security change rather than a convenience.
	AdminStoreEnabled bool
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
	// server queues commands and never wakes a device.
	Push   PushConfig
	Logger *slog.Logger
	Clock  clock.Clock
	// Bus carries the typed events every state change publishes. When nil,
	// Build creates one so the sinks below have something to subscribe to;
	// pass one to observe events from outside the process.
	Bus *event.Bus
	// Sinks configures what subscribes to the bus.
	Sinks SinkConfig
}

// SinkConfig turns on the event sinks. Both are off by default: an audit log
// and a webhook are deployment choices, and a library consumer supplies its
// own subscribers.
type SinkConfig struct {
	// Audit writes a projected slog record for every event. It is the
	// cheapest form of the threat model's repudiation control: attributable,
	// but only as persistent as the log stream it is shipped to.
	Audit bool
	// WebhookURL receives a POST per event in the MicroMDM envelope, minus
	// the raw payload those servers include (event/sink explains why).
	WebhookURL string
	// WebhookHMACKey signs the webhook body when set.
	WebhookHMACKey []byte
	// Persist writes every event to the audit trail on the process's own
	// database. This is what makes the threat model's repudiation control
	// real: an slog record is only as persistent as the log stream someone
	// remembered to ship, and proving who erased a device three weeks ago
	// needs a table.
	Persist bool
	// AuditStore overrides Persist with a caller's own trail.
	AuditStore audit.Store
	// Retention is how long records are kept. Zero keeps them forever, which
	// is a choice a deployment should make deliberately rather than inherit.
	Retention time.Duration
	// PruneInterval is how often retention runs; DefaultAuditPruneInterval
	// when unset.
	PruneInterval time.Duration
}

// Enabled reports whether anything subscribes.
func (s SinkConfig) Enabled() bool {
	return s.Audit || s.WebhookURL != "" || s.Persist || s.AuditStore != nil
}

// ErrConfig reports an invalid configuration.
var ErrConfig = errors.New("app: invalid configuration")

// App is a built process.
type App struct {
	Handler  http.Handler
	Core     *service.Core
	Engine   *ddm.Engine
	Notifier *ddmsync.Notifier
	Store    storage.Store
	keyring  *crypt.Keyring
	// AxM is the Business Manager client when configured.
	AxM *axm.Client
	// DEP is the device enrollment service; nil on the mdm role.
	DEP *dep.Client
	// Push wakes devices; nil when no push source is configured.
	Push *pushnotify.Notifier
	// admin authorizes admin callers against the stored Cedar policies. It
	// is nil when the deployment configured the static DM_ADMIN_TOKEN
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
	// workers are the supervised background loops, in registration order.
	// Run starts every one of them; nothing here is started by Build.
	workers []worker
	// mu guards running.
	mu sync.Mutex
	// running records which workers are currently in their Run func, so
	// readiness can report a loop that has stopped or never started.
	running map[string]bool
	// ownBus is set when Build created the event bus, and so has to drain it
	// on Close. A bus passed in belongs to the caller.
	ownBus bool
	// audit is the persisted trail, nil when none is configured.
	audit audit.Store
}

// worker is one supervised background loop. The name is a fixed identifier
// chosen here, never derived from configuration, so it is safe to report and
// to use as a metric label.
type worker struct {
	name string
	run  func(context.Context) error
}

// WorkerState reports one supervised loop and whether it is running.
type WorkerState struct {
	Name    string
	Running bool
}

// addWorker registers a background loop for Run to supervise. Callers are
// the wire functions, so the set is fixed by the time Build returns.
func (a *App) addWorker(name string, run func(context.Context) error) {
	a.workers = append(a.workers, worker{name: name, run: run})
}

// setRunning records a worker entering or leaving its loop.
func (a *App) setRunning(name string, up bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running == nil {
		a.running = make(map[string]bool, len(a.workers))
	}
	a.running[name] = up
}

// Workers reports every supervised loop and whether it is running, in
// registration order. Readiness reads this; a worker that has stopped while
// the process keeps serving is exactly the state /healthz could not see.
func (a *App) Workers() []WorkerState {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]WorkerState, 0, len(a.workers))
	for _, w := range a.workers {
		out = append(out, WorkerState{Name: w.name, Running: a.running[w.name]})
	}
	return out
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
	// The bus is only created when something is configured to listen, so a
	// server with no sinks pays nothing per state change. It is asynchronous
	// because a webhook receiver must never sit on the check-in path: a slow
	// receiver would delay every device.
	ownBus := false
	if cfg.Bus == nil && cfg.Sinks.Enabled() {
		log := cfg.Logger
		cfg.Bus = event.New(
			event.WithAsync(),
			event.WithErrorHandler(func(e event.Event, err error) {
				log.Warn("app: event sink failed", "event", string(e.Type), "error", err)
			}),
		)
		ownBus = true
	}
	a := &App{cfg: cfg, ownBus: ownBus}
	if err := a.openStorage(ctx); err != nil {
		return nil, err
	}
	if err := a.wireSinks(ctx); err != nil {
		return nil, err
	}
	if err := a.wire(ctx); err != nil {
		//nolint:contextcheck // teardown of a half-built App; Close releases
		// resources already opened and takes no context.
		_ = a.Close()
		return nil, err
	}
	return a, nil
}

// reenrollPolicy maps the configuration flag to the service policy. The
// secure default is deny; see Config.AllowReenroll for why.
func reenrollPolicy(allow bool) service.ReenrollPolicy {
	if allow {
		return service.AllowReenroll
	}
	return service.DenyReenroll
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
	// The hop forwards a check-in verbatim and the ddm role resolves the
	// enrollment from that body, so an unauthenticated hop hands any caller
	// every enrollment's declarations and its status reports. proxyserver
	// treats each of its caller checks as optional, which makes requiring one
	// this package's job: the ddm role exists to serve the hop, and an mdm
	// role forwarding to it is the other end of the same trust boundary.
	if c.Storage != "inmem" && len(c.StorageKeys) == 0 {
		return fmt.Errorf(
			"%w: %s storage seals unlock tokens, bootstrap tokens and push keys, so it needs %s",
			ErrConfig, c.Storage, EnvStorageKeys,
		)
	}
	if c.Role == RoleDDM || c.DDMURL != "" {
		if len(c.DDMSendKey) == 0 || len(c.DDMRecvKey) == 0 {
			return fmt.Errorf(
				"%w: the declarative management hop needs %s and %s on both roles",
				ErrConfig, EnvDDMSendKey, EnvDDMRecvKey,
			)
		}
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
	// A configured header names where certificates come from, so it is
	// honoured ahead of the signature: an mTLS-only enrollment profile sends
	// no Mdm-Signature, and the signature source would leave those requests
	// with no identity at all. CARoots, when present, verifies the chain.
	case a.cfg.CertHeader != "":
		if a.cfg.CARoots == nil {
			a.cfg.Logger.Warn(
				"app: certificate header trusted without a CA to verify it; "+
					"set DM_CA_FILE, and keep this listener reachable only through the proxy",
				"header", a.cfg.CertHeader,
			)
			return httpapi.CertFromHeader(a.cfg.CertHeader)
		}
		return httpapi.CertFromHeader(a.cfg.CertHeader, httpapi.WithHeaderRoots(a.cfg.CARoots))
	case a.cfg.CARoots != nil:
		return httpapi.CertFromMdmSignature(
			cms.VerifyOptions{
				Roots:     a.cfg.CARoots,
				ClockSkew: 5 * time.Minute,
				Now:       a.cfg.Clock.Now,
			},
			0,
		)
	default:
		a.cfg.Logger.Warn(
			"app: no CA or certificate header configured; device certificates must arrive over TLS on this process",
		)
		return httpapi.CertFromTLS
	}
}

// openKeyring resolves StorageKeys once, so a missing or malformed key is a
// startup failure rather than the first write of an unlock token.
func (a *App) openKeyring(ctx context.Context) error {
	if len(a.cfg.StorageKeys) == 0 {
		return nil
	}
	provider := a.cfg.Secrets
	switch {
	case provider != nil:
	case a.cfg.SecretsDir != "":
		d, err := secrets.NewDir(a.cfg.SecretsDir)
		if err != nil {
			return fmt.Errorf("app: secrets directory: %w", err)
		}
		a.closers = append(a.closers, d.Close)
		provider = d
	default:
		provider = secrets.Env{Prefix: "DM_STORAGE_KEY_"}
	}
	k, err := crypt.NewKeyring(ctx, crypt.Options{
		Keys: crypt.Keys{
			Active:   a.cfg.StorageKeys[0],
			Accepted: a.cfg.StorageKeys[1:],
			Strict:   a.cfg.StorageKeysStrict,
		},
		Provider: provider,
	})
	if err != nil {
		return fmt.Errorf("app: storage keyring: %w", err)
	}
	a.keyring = k
	return nil
}

func (a *App) openStorage(ctx context.Context) error {
	var (
		dialect sqlcommon.Dialect
		db      *sql.DB
	)
	if err := a.openKeyring(ctx); err != nil {
		return err
	}
	switch a.cfg.Storage {
	case "inmem":
		a.Store = inmem.New()
		return nil
	case "sqlite":
		s, err := sqlite.Open(ctx, a.cfg.DSN, sqlite.Options{Keyring: a.keyring})
		if err != nil {
			return fmt.Errorf("app: sqlite: %w", err)
		}
		a.Store, db, dialect = s, s.DB(), sqlite.Dialect
		a.closers = append(a.closers, s.Close)
	case "postgres":
		s, err := postgres.Open(ctx, a.cfg.DSN, postgres.Options{Keyring: a.keyring})
		if err != nil {
			return fmt.Errorf("app: postgres: %w", err)
		}
		a.Store, db, dialect = s, s.DB(), postgres.Dialect
		a.closers = append(a.closers, s.Close)
	default:
		s, err := mysql.Open(ctx, a.cfg.DSN, mysql.Options{Keyring: a.keyring})
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
		Subscriptions: ddm.Subscriptions{Enabled: cfg.Subscriptions},
	})
	if err != nil {
		return fmt.Errorf("app: engine: %w", err)
	}
	a.Engine = engine
	// Without a Pusher the notifier treats every group as delivered, so a
	// declaration change queues a command and never wakes the device.
	a.Push, err = a.wirePush()
	if err != nil {
		return err
	}
	var pusher ddmsync.Pusher
	if a.Push != nil {
		pusher = a.Push
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
				[]service.Hook{ddmsync.NewServiceHook(engine, a.Store, cfg.Logger)},
				enrollHooks...),
			DeclarativeManagement: dm,
			RequireUserAuth:       cfg.Enroll.RequireUserAuth,
			Reenroll:              reenrollPolicy(cfg.AllowReenroll),
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
		// The ddm role serves no device channel, but its notifier still
		// enqueues DeclarativeManagement into the shared command queue that
		// the mdm role delivers from. Building a core here means those
		// commands are screened, hooked and audited on this role too.
		core, err := service.New(service.Config{
			Store: a.Store, Bus: cfg.Bus, Clock: cfg.Clock, Logger: cfg.Logger,
		})
		if err != nil {
			return fmt.Errorf("app: core: %w", err)
		}
		a.Core = core
	}

	// The notifier is built after the core because DeclarativeManagement is
	// an MDM command and travels the MDM command path: Core.Enqueue runs the
	// hook chain, screens the target against schema/support, and publishes
	// CommandQueued. Enqueueing straight into storage skipped all three,
	// which kept every DDM-driven command out of the event bus and so out of
	// the audit trail.
	//
	// This ordering is only possible because the engine no longer calls back
	// into the notifier. The persistent signal is the change rows recordAffected
	// writes inside the transaction; the admin route wrapper kicks the
	// notifier after a change so the 1s poll is not the only trigger.
	// The reference server suppresses a second DeclarativeManagement while
	// one is pending, and says so here rather than inheriting it: whether to
	// suppress is a deployment's decision, and ddm defaults to this only
	// because a nil key means "not set".
	dedupe := ddmsync.DefaultDedupeKey
	a.Notifier, err = ddmsync.NewNotifier(
		ddmsync.NotifierConfig{
			Store:     st,
			Tokens:    engine,
			Enqueuer:  a.Core,
			Pusher:    pusher,
			Bus:       cfg.Bus,
			Clock:     cfg.Clock,
			Logger:    cfg.Logger,
			DedupeKey: &dedupe,
		},
	)
	if err != nil {
		return fmt.Errorf("app: notifier: %w", err)
	}
	a.addWorker("ddm-notifier", a.Notifier.Run)
	a.addWorker("audit-retention", a.runAuditRetention)
	// Every role that has a credential serves the admin API. Withholding it
	// from the mdm role would leave the half that owns enrollments, commands
	// and push with no administrative surface.
	if a.adminEnabled() {
		store, err := a.adminStore(ctx)
		if err != nil {
			return err
		}
		if store != nil {
			m, err := adminauth.New(store, mustAdminRegistry(), adminauth.WithClock(cfg.Clock))
			if err != nil {
				return fmt.Errorf("app: admin authorization: %w", err)
			}
			a.admin = m
		}
		var routes []adminRoute
		routes = append(routes, a.introspectionRoutes()...)
		routes = append(routes, a.ddmAdminRoutes()...)
		routes = append(routes, a.mdmAdminRoutes()...)
		if a.admin != nil {
			routes = append(routes, a.principalRoutes()...)
		}
		if a.audit != nil {
			routes = append(routes, a.auditRoutes()...)
		}
		if cfg.AxM.Enabled() {
			client, err := a.newAxM(ctx)
			if err != nil {
				return err
			}
			a.AxM = client
			routes = append(
				routes,
				adminRoute{
					Pattern: "/axm/",
					Action:  ActionManageBusinessMgr,
					Family:  "axm",
					Handler: a.axmHandler(client),
				},
			)
		}
		if a.dep == nil {
			if err := a.wireDEP(ctx); err != nil {
				return err
			}
		}
		routes = append(
			routes,
			adminRoute{
				Pattern: "/dep/",
				Action:  ActionManageDEP,
				Family:  "dep",
				Handler: a.dep.handler(),
			},
		)
		if a.acme != nil {
			routes = append(
				routes,
				adminRoute{
					Pattern: "/acme/",
					Action:  ActionReadACME,
					Family:  "acme",
					Handler: a.acme.handler(),
				},
			)
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

// Run supervises every registered background loop until ctx is cancelled or
// one of them fails, whichever comes first. The HTTP listener is the
// caller's (cmd/dmserver, or httptest in tests).
//
// The first failure cancels its siblings so Run returns promptly rather than
// waiting for loops that only stop on cancellation. A loop that stops because
// the context ended is not a failure, and the two existing loops disagree on
// how they say so -- ddmsync.Notifier.Run returns ctx.Err(), depService.Run
// returns nil -- so cancellation is normalised here rather than in each loop.
func (a *App) Run(ctx context.Context) error {
	if len(a.workers) == 0 {
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, len(a.workers))
	var wg sync.WaitGroup
	for _, w := range a.workers {
		// Marked running before the goroutine is scheduled so readiness
		// never observes a registered worker as down before it starts.
		a.setRunning(w.name, true)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer a.setRunning(w.name, false)
			if err := w.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				errc <- fmt.Errorf("app: worker %s: %w", w.name, err)
				cancel()
			}
		}()
	}
	wg.Wait()
	close(errc)
	return <-errc
}

// Close releases storage, draining the event bus first when Build created
// it, so an asynchronous sink finishes delivering before the process exits.
func (a *App) Close() error {
	var errs []error
	if a.ownBus && a.cfg.Bus != nil {
		ctx, cancel := context.WithTimeout(context.Background(), busDrainTimeout)
		defer cancel()
		if err := a.cfg.Bus.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("app: drain events: %w", err))
		}
	}
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

// busDrainTimeout bounds how long Close waits for asynchronous sinks.
const busDrainTimeout = 5 * time.Second

// wireSinks subscribes the configured sinks to the bus. Both are off unless
// asked for: an audit log and a webhook are deployment choices, and a library
// consumer subscribes its own handlers instead.
func (a *App) wireSinks(ctx context.Context) error {
	if !a.cfg.Sinks.Enabled() || a.cfg.Bus == nil {
		return nil
	}
	reg := eventsink.Default()
	if a.cfg.Sinks.Audit {
		a.cfg.Bus.Subscribe(event.All, eventsink.Slog(a.cfg.Logger, reg))
	}
	store, err := a.auditStore(ctx)
	if err != nil {
		return err
	}
	if store != nil {
		a.audit = store
		a.cfg.Bus.Subscribe(event.All, auditSink(store, reg))
	}
	if a.cfg.Sinks.WebhookURL != "" {
		h, err := eventsink.Webhook(eventsink.WebhookConfig{
			URL:      a.cfg.Sinks.WebhookURL,
			Registry: reg,
			HMACKey:  a.cfg.Sinks.WebhookHMACKey,
			Clock:    a.cfg.Clock,
			Logger:   a.cfg.Logger,
		})
		if err != nil {
			return fmt.Errorf("app: webhook sink: %w", err)
		}
		a.cfg.Bus.Subscribe(event.All, h)
	}
	return nil
}

// adminStore resolves the admin principal and policy store, following the
// same three-way choice as the other satellite stores: an injected store
// wins, then the process's own database, then memory when there is no
// database to share. It returns a nil store when neither an injection nor
// AdminStoreEnabled asked for one, which leaves the static token as the only
// credential.
//
// Before this existed, adminauth/sqlstore was imported only by its own tests:
// cmd/dmserver never set AdminStore, so the principals, policies and
// revocable tokens of record 0034 were unreachable from the shipped binary.
func (a *App) adminStore(ctx context.Context) (adminauth.Store, error) {
	switch {
	case a.cfg.AdminStore != nil:
		return a.cfg.AdminStore, nil
	case !a.cfg.AdminStoreEnabled:
		//nolint:nilnil // a nil store is the documented "no principal store"
		// answer, not a failure: the static token stays the only credential.
		return nil, nil
	case a.db == nil:
		// An in-memory deployment has nowhere persistent to put principals; the
		// store still works so the admin API behaves the same way in tests
		// and in a throwaway run.
		return admininmem.New(), nil
	default:
		s, err := adminsql.Open(ctx, a.db, a.dialect, adminsql.Options{})
		if err != nil {
			return nil, fmt.Errorf("app: admin store: %w", err)
		}
		return s, nil
	}
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
	a.addWorker("dep-syncer", svc.Run)
	return nil
}
