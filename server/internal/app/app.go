package app

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push/apns"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	ddminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	admininmem "github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
	adminsql "github.com/deploymenttheory/go-apple-dm/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/apppush"
	"github.com/deploymenttheory/go-apple-dm/server/audit"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/inproc"
	sqlstore "github.com/deploymenttheory/go-apple-dm/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/ddmsync"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/pushnotify"
	"github.com/deploymenttheory/go-apple-dm/server/replycerts"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/webhook"
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
	Webhooks              webhook.Config
	ApplicationIdentities ApplicationIdentityConfig
	persistentEvents      event.Publisher
	Setup                 *SetupConfig

	PKI                     PKIConfig
	RateLimits              RateLimitConfig
	TLSCertFile, TLSKeyFile string
	Listen                  string
	Storage                 string // sqlite, postgres, mysql, inmem
	DSN                     string // file path for sqlite
	// AllowReenroll accepts Authenticate with a different certificate and replaces
	// the enrollment pin. The library defaults to allowing this; the reference
	// server defaults to DenyReenroll.
	//
	// Enable only with appropriate enrollment admission. CA trust alone does not
	// bind a certificate to an enrollment identifier; account-driven issuance
	// associations provide an additional binding for those sessions.
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
	// BootstrapToken is accepted only for the first-root bootstrap endpoint.
	BootstrapToken string
	// AdminStore overrides the process database for custom compositions and tests.
	AdminStore adminauth.Store
	// CAFile is a PEM bundle of roots that device identities chain to;
	// the server then verifies Mdm-Signature on every check-in and
	// connect. CARoots is the parsed form (tests set it directly).
	CAFile  string
	CARoots *x509.CertPool
	// CertHeader names a header carrying the client certificate from a
	// TLS-terminating proxy (httpapi.CertFromHeader). Used when no CA is
	// configured. With neither, the certificate must come from TLS on
	// this process (httpapi.CertFromTLS).
	CertHeader string
	// TrustedProxies are socket peers allowed to assert client certificates and IPs.
	TrustedProxies []netip.Prefix
	// Subscriptions enables the synthesised status-subscriptions
	// declaration (decision record 0021).
	Subscriptions bool
	ContentCache  ContentCacheConfig
	// Enroll turns the enrollment routes on (SCEP, discovery,
	// account-driven, ADE).
	Enroll EnrollConfig
	// AxM connects Apple Business Manager or Apple School Manager; its
	// administrative routes live under the admin API.
	AxM AxMConfig
	// DEP configures the device enrollment service client and worker;
	// its admin routes live under the admin API too.
	DEP DEPConfig
	// Push selects where APNs credentials come from. With no source the
	// server queues commands and never wakes a device.
	AppPush AppPushConfig
	Push    PushConfig
	Logger  *slog.Logger
	Clock   clock.Clock
	// Bus carries the typed events every state change publishes. When nil,
	// Build creates one so the sinks below have something to subscribe to;
	// pass one to observe events from outside the process.
	Bus *event.Bus
	// Sinks configures what subscribes to the bus.
	Sinks SinkConfig
}

// SinkConfig enables event logging, webhooks and audit delivery. SQL deployments
// always capture projected occurrences, with independent destination retries.
type SinkConfig struct {
	// Dispatch bounds the application-owned in-process event bus.
	// It does not configure DDM synchronization or device command workers.
	Dispatch event.AsyncConfig
	// Audit writes a projected slog record for each event delivered to the sink.
	// Retention depends on the configured log destination.
	Audit bool
	// WebhookURL is obsolete. Nonempty legacy settings fail startup with
	// migration guidance; configure Webhooks and managed subscriptions instead.
	WebhookURL string
	// WebhookRootCAFile configures private HTTPS trust for the receiver.
	WebhookRootCAFile string
	// WebhookHMACKey is obsolete and causes startup to fail when set.
	WebhookHMACKey []byte
	// Persist delivers captured occurrences to the SQL audit trail. The event
	// store retains pending deliveries until this destination acknowledges them.
	Persist bool
	// AuditStore overrides Persist with a caller's own trail.
	AuditStore audit.Store
	// Retention is the maximum record age. Zero disables age-based pruning.
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
	webhooks          *webhook.Store
	eventStore        *eventstore.Store
	eventPublisher    *eventstore.Publisher
	issuerMu          sync.Mutex
	issuerServices    map[string]*managedIssuerService
	Certificates      *lifecycle.Manager
	ReplyCertificates *replycerts.Manager

	appPushStore          *apppush.Store
	contentCache          contentcache.ReportStore
	appPushClients        map[string]*apns.AppClient
	Handler               http.Handler
	Core                  *service.Core
	Engine                *ddm.Engine
	Blueprints            *blueprints.Manager
	ConfigurationProfiles *configurationprofile.Manager
	Notifier              *ddmsync.Notifier
	Store                 storage.Store
	keyring               *crypt.Keyring
	// AxM is the Business Manager client when configured.
	AxM *axm.Client
	// DEP is the device enrollment service when configured.
	DEP *dep.Client
	// Push wakes devices; nil when no push source is configured.
	Push *pushnotify.Notifier
	// admin authenticates stored principals and evaluates their Cedar policies.
	admin *adminauth.Manager
	// adminTable is the mounted admin route table, served by GET /routes.
	adminTable  []adminRoute
	acme        *acmeService
	dep         *depService
	cfg         Config
	enroll      *enrollment
	db          *sql.DB
	dialect     sqlcommon.Dialect
	maintenance *maintenance.Participant
	protocol    state.Store
	revocations *revocation.Registry
	closers     []func() error
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
	if a.running == nil {
		a.running = make(map[string]bool, len(a.workers))
	}
	a.running[name] = up
	a.mu.Unlock()
	if a.webhooks != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.webhooks.Capture(ctx, webhook.Event{Type: "server.worker.state", Data: map[string]any{"worker": name, "running": up}})
	}
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
	if cfg.Enroll.Enabled() && !cfg.PKI.Disabled {
		cfg.PKI.Enabled = true
		if cfg.PKI.CRLTTL == 0 {
			cfg.PKI.CRLTTL = 24 * time.Hour
		}
		if cfg.PKI.CRLRefresh == 0 {
			cfg.PKI.CRLRefresh = time.Hour
		}
		if cfg.PKI.OCSPTTL == 0 {
			cfg.PKI.OCSPTTL = 15 * time.Minute
		}
	}
	if cfg.Enroll.Enabled() && cfg.Storage != "inmem" && cfg.Enroll.CACertFile == "" &&
		cfg.Setup == nil {
		return nil, fmt.Errorf("%w: persistent enrollment requires CA files", ErrConfig)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.CertHeader != "" && len(cfg.TrustedProxies) == 0 {
		return nil, fmt.Errorf("%w: certificate headers require trusted proxy networks", ErrConfig)
	}
	if err := cfg.roots(); err != nil {
		return nil, err
	}
	if cfg.CertHeader != "" && cfg.CARoots == nil && !cfg.Enroll.Enabled() && cfg.Setup == nil {
		return nil, fmt.Errorf("%w: certificate headers require client CA roots", ErrConfig)
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
		var err error
		// the bus owns a lifetime independent of the construction/request context
		cfg.Bus, err = event.NewAsync(
			cfg.Sinks.Dispatch,
			event.WithErrorHandler(eventReporter(cfg.Logger, cfg.Clock)),
		)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrConfig, err)
		}
		ownBus = true
	}
	a := &App{cfg: cfg, ownBus: ownBus}
	built := false
	// failure cleanup owns its bounded drain context
	defer func() {
		if !built {
			// Close owns bounded teardown after construction fails.
			_ = a.Close()
		}
	}()
	if err := a.openStorage(ctx); err != nil {
		return nil, err
	}
	if err := a.openCertificates(ctx); err != nil {
		return nil, err
	}
	if err := a.configureManagedIdentities(ctx); err != nil {
		return nil, err
	}
	if a.Certificates != nil {
		a.addWorker("certificate-renewal", a.renewCertificates)
		a.addWorker("device-identity-renewal", a.renewDeviceIdentities)
	}
	if err := a.wireSinks(ctx); err != nil {
		return nil, err
	}
	if err := a.wire(ctx); err != nil {
		return nil, err
	}
	built = true
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
	if c.Sinks.WebhookURL != "" || len(c.Sinks.WebhookHMACKey) != 0 {
		return fmt.Errorf("%w: legacy webhook settings require migration to managed subscriptions", ErrConfig)
	}
	if c.Webhooks.Enabled && (c.Storage == "inmem" || len(c.StorageKeys) == 0) {
		return fmt.Errorf("%w: managed webhooks require SQL, encryption, and administration", ErrConfig)
	}
	if o := c.ApplicationIdentities.Artifacts; o.MaxBytes < 0 || o.MaxBytes > 1<<40 || o.MaxExpandedBytes < 0 || o.MaxExpandedBytes > 1<<40 || o.MaxEntries < 0 || o.MaxApplications < 0 || o.MaxDepth < 0 || o.Timeout < 0 {
		return fmt.Errorf("%w: invalid application artifact inspection limits", ErrConfig)
	}
	if d := c.Sinks.Dispatch; d.Workers < 0 || d.QueueCapacity < 0 || d.DeliveryTimeout < 0 {
		return fmt.Errorf("%w: event dispatch limits must be non-negative", ErrConfig)
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return fmt.Errorf("%w: TLS certificate and key must be configured together", ErrConfig)
	}
	if err := c.validateSecurity(); err != nil {
		return err
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
	if c.Storage != "inmem" && len(c.StorageKeys) == 0 {
		return fmt.Errorf(
			"%w: %s storage seals unlock tokens, bootstrap tokens and push keys, so it needs %s",
			ErrConfig, c.Storage, EnvStorageKeys,
		)
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

// certSource picks how the server learns the device certificate.
func (a *App) certSource() func(http.Handler) http.Handler {
	if a.Certificates != nil {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				roots, err := a.managedRoots(r.Context())
				if err != nil {
					http.Error(w, "certificate trust unavailable", http.StatusServiceUnavailable)
					return
				}
				a.certSourceWithRoots(roots)(next).ServeHTTP(w, r)
			})
		}
	}
	return a.certSourceWithRoots(a.cfg.CARoots)
}

func (a *App) certSourceWithRoots(roots *x509.CertPool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		signed := next
		if roots != nil {
			signed = httpapi.CertFromMdmSignature(
				cms.VerifyOptions{
					Roots:     roots,
					ClockSkew: 5 * time.Minute,
					Now:       a.cfg.Clock.Now,
				},
				0,
			)(
				signed,
			)
		}
		direct := httpapi.CertFromTLS(signed)
		if a.cfg.CertHeader == "" {
			return direct
		}
		forwarded := httpapi.CertFromHeader(
			a.cfg.CertHeader,
			httpapi.WithHeaderRoots(roots),
			httpapi.WithHeaderPeers(a.cfg.TrustedProxies...),
		)(
			signed,
		)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(a.cfg.CertHeader) != "" {
				forwarded.ServeHTTP(w, r)
			} else {
				direct.ServeHTTP(w, r)
			}
		})
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
		s, err := sqlite.Open(ctx, a.cfg.DSN, sqlite.Options{Keyring: a.keyring, SkipMigrate: true})
		if err != nil {
			return fmt.Errorf("app: sqlite: %w", err)
		}
		a.Store, db, dialect = s, s.DB(), sqlite.Dialect
		a.closers = append(a.closers, s.Close)
	case "postgres":
		s, err := postgres.Open(
			ctx,
			a.cfg.DSN,
			postgres.Options{Keyring: a.keyring, SkipMigrate: true},
		)
		if err != nil {
			return fmt.Errorf("app: postgres: %w", err)
		}
		a.Store, db, dialect = s, s.DB(), postgres.Dialect
		a.closers = append(a.closers, s.Close)
	default:
		s, err := mysql.Open(ctx, a.cfg.DSN, mysql.Options{Keyring: a.keyring, SkipMigrate: true})
		if err != nil {
			return fmt.Errorf("app: mysql: %w", err)
		}
		a.Store, db, dialect = s, s.DB(), mysql.Dialect
		a.closers = append(a.closers, s.Close)
	}
	a.db = db
	a.dialect = dialect
	control, err := maintenance.Open(ctx, db, dialect, true)
	if err != nil {
		return wrapError(err)
	}
	a.maintenance, err = control.Register(ctx, "device-management")
	if err != nil {
		return wrapError(err)
	}
	if _, err := sqlcommon.Migrate(ctx, db, dialect); err != nil {
		return wrapError(err)
	}
	return nil
}

func (a *App) ddmStore(ctx context.Context) (ddm.Store, error) {
	if a.db == nil {
		return ddminmem.New(), nil
	}
	st, err := sqlstore.Open(ctx, a.db, a.dialect, sqlstore.Options{Keyring: a.keyring})
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
		Store: st, Bus: cfg.publisher(), Clock: cfg.Clock, Logger: cfg.Logger,
		Expander:      configurationprofile.Expander{BaseURL: cfg.Enroll.PublicURL},
		Subscriptions: ddm.Subscriptions{Enabled: cfg.Subscriptions},
		EnrollmentTarget: func(ctx context.Context, id mdm.EnrollmentID) (support.Target, error) {
			target, err := service.EnrollmentTarget(ctx, a.Store, id)
			if errors.Is(err, storage.ErrNotFound) {
				return support.Target{}, nil
			}
			return target, err
		},
	})
	if err != nil {
		return fmt.Errorf("app: engine: %w", err)
	}
	a.Engine = engine
	if err := a.wireConfigurationProfiles(ctx); err != nil {
		return err
	}
	if err := a.wireBlueprints(ctx); err != nil {
		return err
	}
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
	if a.webhooks != nil {
		mux.Handle(webhook.PayloadPath, a.webhooks.PayloadHandler())
	}
	if err := a.wireContentCache(ctx, mux); err != nil {
		return err
	}
	if a.Certificates != nil {
		mux.Handle("/.well-known/acme-challenge/", a.Certificates.HTTP01Handler())
	}
	mux.HandleFunc("GET "+PathHealthz, a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	{
		dm := inproc.Handler(engine)
		if err := a.wireDEP(ctx); err != nil {
			return err
		}
		enrollHooks, err := a.wireEnrollment(ctx, mux)
		if err != nil {
			return err
		}
		userAuth, err := a.userAuthenticator()
		if err != nil {
			return err
		}
		core, err := service.New(service.Config{
			Store:              a.Store,
			EnableReplacements: a.replacementStore() != nil && a.enroll != nil,
			Bus:                cfg.publisher(),
			Clock:              cfg.Clock,
			Logger:             cfg.Logger,
			CertificateStatus:  a.certificateStatus(),
			Hooks: append(
				[]service.Hook{ddmsync.NewServiceHook(engine, a.Store, cfg.Logger)},
				enrollHooks...),
			DeclarativeManagement: dm,
			ReturnToService:       returnToService(cfg.Enroll.ReturnToService),
			RequireUserAuth:       cfg.Enroll.RequireUserAuth,
			UserAuthenticate:      userAuth,
			Reenroll:              reenrollPolicy(cfg.AllowReenroll),
		})
		if err != nil {
			return fmt.Errorf("app: core: %w", err)
		}
		a.Core = core
		a.wireConfigurationProfileDownloads(mux, nil)
		api := httpapi.Handler(
			httpapi.Config{Checkin: core, Connect: core, Logger: cfg.Logger, Now: cfg.Clock.Now},
		)
		mux.Handle(
			PathMDM,
			a.certSource()(api),
		) // check-in is PUT, connect is PUT; httpapi enforces methods
	}

	// Build the notifier after Core so DeclarativeManagement commands pass through
	// hooks, target validation and CommandQueued events. The engine writes change rows
	// transactionally; the notifier reads them without an engine callback. Admin routes
	// also kick the notifier to reduce delay between polls.
	//
	// The reference server explicitly deduplicates pending DeclarativeManagement
	// commands.
	dedupe := ddmsync.DefaultDedupeKey
	a.Notifier, err = ddmsync.NewNotifier(
		ddmsync.NotifierConfig{
			Store:     st,
			Tokens:    engine,
			Enqueuer:  a.Core,
			Pusher:    pusher,
			Bus:       cfg.publisher(),
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
	// Mount one administrative API for all device-management capabilities.
	if err := a.wireAdmin(ctx, mux); err != nil {
		return err
	}
	a.Handler, err = a.withRateLimits(ctx, mux)
	if err == nil && a.maintenance != nil {
		a.Handler = a.maintenance.Wrap(a.Handler)
	}
	if err == nil {
		a.Handler = redactContentCacheURL(a.Handler)
		if a.webhooks != nil {
			a.Handler = a.webhooks.Observe(a.Handler, mux)
		}
	}
	return err
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
	if a.maintenance != nil {
		err := a.maintenance.Run(ctx, a.runWorkers)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return wrapError(err)
	}
	return a.runWorkers(ctx)
}

func (a *App) runWorkers(ctx context.Context) error {
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

// Close releases storage after a bounded drain of a bus created by Build.
// A drain timeout cancels active deliveries and abandons queued events.
func (a *App) Close() error {
	var errs []error
	if a.ownBus && a.cfg.Bus != nil {
		ctx, cancel := context.WithTimeout(context.Background(), busDrainTimeout)
		defer cancel()
		if err := a.cfg.Bus.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("app: drain events: %w", err))
		}
	}
	if a.maintenance != nil {
		ctx, cancel := context.WithTimeout(context.Background(), busDrainTimeout)
		defer cancel()
		if err := a.maintenance.Close(ctx); err != nil {
			errs = append(errs, wrapError(err))
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
	if a.db != nil {
		return a.wirePersistentSinks(ctx)
	}
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
	return nil
}

// adminStore uses the shared database, or memory for ephemeral compositions.
func (a *App) adminStore(ctx context.Context) (adminauth.Store, error) {
	switch {
	case a.cfg.AdminStore != nil:
		return a.cfg.AdminStore, nil
	case a.db == nil:
		// The in-memory principal store supports administration without persisted state.
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

// TLSClientRoots returns the configured device identity trust roots.
func (a *App) TLSClientRoots() *x509.CertPool { return a.cfg.CARoots }

// readyz requires storage and all configured background loops to be running.
func (a *App) readyz(w http.ResponseWriter, r *http.Request) {
	if a.eventPublisher != nil {
		health := a.eventPublisher.Health()
		if health.LastFailure.After(health.LastSuccess) {
			http.Error(w, "event recording unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	for _, worker := range a.Workers() {
		if !worker.Running {
			http.Error(w, "worker unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	a.healthz(w, r)
}

func (a *App) wireAdmin(ctx context.Context, mux *http.ServeMux) error {
	cfg := a.cfg
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
	routes = append(routes, a.eventRoutes()...)
	routes = append(routes, a.webhookRoutes()...)
	routes = append(routes, a.setupRoutes()...)
	routes = append(routes, a.ddmAdminRoutes()...)
	routes = append(routes, a.blueprintAdminRoutes()...)
	routes = append(routes, a.applicationIdentityRoutes()...)
	routes = append(routes, a.configurationProfileAdminRoutes()...)
	routes = append(routes, a.mdmAdminRoutes()...)
	routes = append(routes, a.contentCacheRoutes()...)
	extras, err := a.operatorRoutes(ctx)
	if err != nil {
		return err
	}
	routes = append(routes, extras...)
	if a.admin != nil {
		routes = append(routes, a.principalRoutes()...)
		routes = append(routes, a.roleRoutes()...)
		mux.Handle("POST "+PathAdmin+"auth/bootstrap", http.HandlerFunc(a.bootstrapAdmin))
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
		routes = append(routes, a.axmRoutes(client)...)
	}
	if a.dep == nil {
		if err := a.wireDEP(ctx); err != nil {
			return err
		}
	}
	routes = append(routes, a.dep.routes()...)
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
	routes = append(routes, a.pkiAdminRoutes()...)
	admin, err := a.buildAdminMux(routes)
	if err != nil {
		return err
	}
	mux.Handle(PathAdmin, http.StripPrefix(PathAdmin[:len(PathAdmin)-1], admin))
	return nil
}
