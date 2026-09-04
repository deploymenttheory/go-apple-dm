package app

import (
	"fmt"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/enroll"
)

// Environment variables read by ParseEnv.
const (
	EnvRole       = "MDM_ROLE"
	EnvListen     = "MDM_LISTEN"
	EnvStorage    = "MDM_STORAGE"
	EnvDSN        = "MDM_DSN"
	EnvDDMURL     = "MDM_DDM_URL"
	EnvDDMSendKey = "MDM_DDM_SEND_KEY"
	EnvDDMRecvKey = "MDM_DDM_RECV_KEY"
	// EnvAllowReenroll opts back in to the library's permissive
	// re-enrollment behaviour; see Config.AllowReenroll.
	EnvAllowReenroll = "MDM_ALLOW_REENROLL"
	EnvAdminToken    = "MDM_ADMIN_TOKEN" // #nosec G101 -- the variable name, not a credential
	// EnvAdminStore opens the admin principal and policy store on the
	// process's own database. Off by default: it mounts the admin API.
	EnvAdminStore = "MDM_ADMIN_STORE"
	// EnvAudit writes a projected slog record for every event.
	EnvAudit = "MDM_AUDIT_LOG"
	// EnvWebhookURL receives an event per POST in the MicroMDM envelope.
	EnvWebhookURL = "MDM_WEBHOOK_URL"
	// EnvAuditStore persists every event to the audit trail.
	EnvAuditStore = "MDM_AUDIT_STORE"
	// EnvAuditRetention is how long audit records are kept.
	EnvAuditRetention = "MDM_AUDIT_RETENTION"
	// EnvWebhookHMACKey signs the webhook body.
	EnvWebhookHMACKey = "MDM_WEBHOOK_HMAC_KEY" // #nosec G101 -- the variable name, not a credential
	EnvCAFile         = "MDM_CA_FILE"
	EnvCertHeader     = "MDM_CERT_HEADER"
	// Enrollment routes (EnrollConfig).
	EnvPublicURL           = "MDM_PUBLIC_URL"
	EnvPushTopic           = "MDM_PUSH_TOPIC"
	EnvEnrollCACertFile    = "MDM_ENROLL_CA_CERT_FILE"
	EnvEnrollCAKeyFile     = "MDM_ENROLL_CA_KEY_FILE"
	EnvSCEPChallenge       = "MDM_SCEP_CHALLENGE" // #nosec G101 -- the variable name, not a credential
	EnvSCEPHMACKey         = "MDM_SCEP_HMAC_KEY"  // #nosec G101 -- the variable name, not a credential
	EnvProfileIdentifier   = "MDM_PROFILE_IDENTIFIER"
	EnvOrganization        = "MDM_ORGANIZATION"
	EnvDiscovery           = "MDM_DISCOVERY"
	EnvAccountDrivenMethod = "MDM_ACCOUNT_DRIVEN_METHOD"
	EnvOIDCIssuer          = "MDM_OIDC_ISSUER"
	EnvOIDCClientID        = "MDM_OIDC_CLIENT_ID"
	EnvOIDCClientSecret    = "MDM_OIDC_CLIENT_SECRET" // #nosec G101 -- the variable name, not a credential
	EnvADEAnchorFile       = "MDM_ADE_ANCHOR_FILE"
	EnvADEAudit            = "MDM_ADE_AUDIT"
	EnvRequireUserAuth     = "MDM_REQUIRE_USER_AUTH"
	// Apple Business Manager (AxMConfig).
	EnvAxMClientID = "MDM_AXM_CLIENT_ID"
	EnvAxMKeyID    = "MDM_AXM_KEY_ID"
	EnvAxMKeyFile  = "MDM_AXM_KEY_FILE"
	EnvAxMScope    = "MDM_AXM_SCOPE"
	EnvAxMBaseURL  = "MDM_AXM_BASE_URL"
	EnvAxMTokenURL = "MDM_AXM_TOKEN_URL" // #nosec G101 -- the variable name, not a credential
	// Device enrollment service (DEPConfig).
	EnvDEPBaseURL        = "MDM_DEP_BASE_URL"
	EnvDEPSyncInterval   = "MDM_DEP_SYNC_INTERVAL"
	EnvDEPAssignInterval = "MDM_DEP_ASSIGN_INTERVAL" // #nosec G101 -- the variable name, not a credential
	EnvDEPProfileURL     = "MDM_DEP_PROFILE_URL"
	EnvDEPUsePUT         = "MDM_DEP_USE_PUT"

	// Push: where APNs credentials come from, and how pushes are shaped.
	EnvPushSource   = "MDM_PUSH_SOURCE"
	EnvPushCertFile = "MDM_PUSH_CERT_FILE"
	EnvPushKeyFile  = "MDM_PUSH_KEY_FILE"
	EnvPushHost     = "MDM_PUSH_HOST"
	EnvPushCoalesce = "MDM_PUSH_COALESCE"
	EnvPushCertTTL  = "MDM_PUSH_CERT_TTL"
	// ACME and Managed Device Attestation (ACMEConfig).
	EnvIdentity       = "MDM_IDENTITY"
	EnvACMEPolicy     = "MDM_ACME_POLICY"
	EnvACMEKey        = "MDM_ACME_KEY"
	EnvACMEHMACKey    = "MDM_ACME_HMAC_KEY" // #nosec G101 -- the variable name, not a credential
	EnvACMEAnchorFile = "MDM_ACME_ANCHOR_FILE"
	EnvACMEUnattested = "MDM_ACME_ALLOW_UNATTESTED"
	EnvACMEIdentTTL   = "MDM_ACME_IDENTIFIER_TTL"

	EnvSubscriptions = "MDM_DDM_SUBSCRIPTIONS"
)

// Defaults applied by ParseEnv when a variable is unset.
const (
	DefaultRole    = RoleAll
	DefaultListen  = ":8080"
	DefaultStorage = "sqlite"
	DefaultDSN     = "mdm.db"
)

// ParseEnv builds a Config from MDM_* variables through get (os.Getenv in
// the binary). Booleans accept strconv.ParseBool forms.
func ParseEnv(get func(string) string) (Config, error) {
	pick := func(key, def string) string {
		if v := get(key); v != "" {
			return v
		}
		return def
	}
	cfg := Config{
		Role:       Role(pick(EnvRole, string(DefaultRole))),
		Listen:     pick(EnvListen, DefaultListen),
		Storage:    pick(EnvStorage, DefaultStorage),
		DSN:        pick(EnvDSN, DefaultDSN),
		DDMURL:     get(EnvDDMURL),
		AdminToken: get(EnvAdminToken),
		Sinks: SinkConfig{
			WebhookURL:     get(EnvWebhookURL),
			WebhookHMACKey: []byte(get(EnvWebhookHMACKey)),
		},
		CAFile:        get(EnvCAFile),
		CertHeader:    get(EnvCertHeader),
		Subscriptions: true,
	}
	if v := get(EnvDDMSendKey); v != "" {
		cfg.DDMSendKey = []byte(v)
	}
	if v := get(EnvDDMRecvKey); v != "" {
		cfg.DDMRecvKey = []byte(v)
	}
	if v := get(EnvSubscriptions); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s=%q: %w", ErrConfig, EnvSubscriptions, v, err)
		}
		cfg.Subscriptions = b
	}
	if cfg.Storage == "inmem" {
		cfg.DSN = ""
	}
	cfg.Push = PushConfig{
		Source:   get(EnvPushSource),
		CertFile: get(EnvPushCertFile),
		KeyFile:  get(EnvPushKeyFile),
		Host:     get(EnvPushHost),
		// The topic comes from the certificate; there is deliberately no
		// variable for it.
		Topic: get(EnvPushTopic),
	}
	// A file pair with no explicit source is the source: an operator who
	// pointed at a certificate meant to send pushes.
	if cfg.Push.Source == "" && cfg.Push.CertFile != "" {
		cfg.Push.Source = PushSourceFile
	}
	for _, d := range []struct {
		name string
		dst  *time.Duration
	}{
		{EnvPushCoalesce, &cfg.Push.Coalesce},
		{EnvPushCertTTL, &cfg.Push.CertTTL},
	} {
		if v := get(d.name); v != "" {
			parsed, err := time.ParseDuration(v)
			if err != nil {
				return Config{}, fmt.Errorf("%w: %s=%q: %w", ErrConfig, d.name, v, err)
			}
			*d.dst = parsed
		}
	}
	cfg.Enroll = EnrollConfig{
		PublicURL: get(
			EnvPublicURL,
		),
		Topic:      get(EnvPushTopic),
		CACertFile: get(EnvEnrollCACertFile),
		CAKeyFile:  get(EnvEnrollCAKeyFile),
		SCEPChallenge: get(
			EnvSCEPChallenge,
		),
		ProfileIdentifier:   get(EnvProfileIdentifier),
		Organization:        get(EnvOrganization),
		AccountDrivenMethod: get(EnvAccountDrivenMethod),
		ADEAnchorFile:       get(EnvADEAnchorFile),
		OIDC: OIDCConfig{
			Issuer:       get(EnvOIDCIssuer),
			ClientID:     get(EnvOIDCClientID),
			ClientSecret: get(EnvOIDCClientSecret),
		},
	}
	if v := get(EnvSCEPHMACKey); v != "" {
		cfg.Enroll.SCEPHMACKey = []byte(v)
	}
	if v := get(EnvDiscovery); v != "" {
		d, err := ParseDiscovery(v)
		if err != nil {
			return Config{}, err
		}
		cfg.Enroll.Discovery = d
	}
	cfg.DEP = DEPConfig{BaseURL: get(EnvDEPBaseURL), ProfileURL: get(EnvDEPProfileURL)}
	for key, dst := range map[string]*time.Duration{EnvDEPSyncInterval: &cfg.DEP.SyncInterval, EnvDEPAssignInterval: &cfg.DEP.AssignInterval} {
		if v := get(key); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return Config{}, fmt.Errorf("%w: %s=%q: %w", ErrConfig, key, v, err)
			}
			*dst = d
		}
	}
	if v := get(EnvDEPUsePUT); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s=%q: %w", ErrConfig, EnvDEPUsePUT, v, err)
		}
		cfg.DEP.UsePUT = b
	}
	cfg.AxM = AxMConfig{
		ClientID: get(EnvAxMClientID),
		KeyID:    get(EnvAxMKeyID),
		KeyFile:  get(EnvAxMKeyFile),
		Scope:    get(EnvAxMScope),
		BaseURL:  get(EnvAxMBaseURL),
		TokenURL: get(EnvAxMTokenURL),
	}
	cfg.Enroll.Identity = get(EnvIdentity)
	cfg.Enroll.ACME.Policy = get(EnvACMEPolicy)
	cfg.Enroll.ACME.AnchorFile = get(EnvACMEAnchorFile)
	acmeKey, err := readACMEKey(get(EnvACMEHMACKey))
	if err != nil {
		return Config{}, err
	}
	cfg.Enroll.ACME.HMACKey = acmeKey
	if v := get(EnvACMEKey); v != "" {
		keyType, keySize, err := parseACMEKey(v)
		if err != nil {
			return Config{}, err
		}
		cfg.Enroll.ACME.KeyType, cfg.Enroll.ACME.KeySize = keyType, keySize
	}
	if v := get(EnvAuditRetention); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s=%q: %w", ErrConfig, EnvAuditRetention, v, err)
		}
		cfg.Sinks.Retention = d
	}
	if v := get(EnvACMEIdentTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s=%q: %w", ErrConfig, EnvACMEIdentTTL, v, err)
		}
		cfg.Enroll.ACME.IdentifierTTL = d
	}
	for key, dst := range map[string]*bool{
		EnvADEAudit:        &cfg.Enroll.ADEAudit,
		EnvRequireUserAuth: &cfg.Enroll.RequireUserAuth,
		EnvACMEUnattested:  &cfg.Enroll.ACME.AllowUnattested,
		EnvAllowReenroll:   &cfg.AllowReenroll,
		EnvAdminStore:      &cfg.AdminStoreEnabled,
		EnvAudit:           &cfg.Sinks.Audit,
		EnvAuditStore:      &cfg.Sinks.Persist,
	} {
		if v := get(key); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return Config{}, fmt.Errorf("%w: %s=%q: %w", ErrConfig, key, v, err)
			}
			*dst = b
		}
	}
	return cfg, cfg.validate()
}

// parseACMEKey reads the key a device should generate, named the way an
// operator thinks of it rather than as Apple's two separate fields. Only
// the elliptic curve sizes can be attested, so those are the ones an
// attesting deployment wants.
func parseACMEKey(v string) (string, int64, error) {
	switch v {
	case "ec256":
		return enroll.KeyTypeEC, 256, nil
	case "ec384":
		return enroll.KeyTypeEC, 384, nil
	case "rsa2048":
		return enroll.KeyTypeRSA, 2048, nil
	case "rsa4096":
		return enroll.KeyTypeRSA, 4096, nil
	default:
		return "", 0, fmt.Errorf(
			"%w: %s=%q must be ec256, ec384, rsa2048, or rsa4096", ErrConfig, EnvACMEKey, v,
		)
	}
}
