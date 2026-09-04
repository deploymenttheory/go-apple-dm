package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/enroll"
)

// Environment variables read by ParseEnv.
const (
	EnvRole       = "DM_ROLE"
	EnvListen     = "DM_LISTEN"
	EnvStorage    = "DM_STORAGE"
	EnvDSN        = "DM_DSN"
	EnvDDMURL     = "DM_DDM_URL"
	EnvDDMSendKey = "DM_DDM_SEND_KEY"
	EnvDDMRecvKey = "DM_DDM_RECV_KEY"
	// EnvStorageKeys names the keys sealing the secret columns, active
	// first; material comes from DM_STORAGE_KEY_<NAME> or EnvSecretsDir.
	EnvStorageKeys       = "DM_STORAGE_KEYS" // #nosec G101 -- the variable name, not a credential
	EnvStorageKeysStrict = "DM_STORAGE_KEYS_STRICT"
	EnvSecretsDir        = "DM_SECRETS_DIR" // #nosec G101 -- the variable name, not a credential
	// EnvAllowReenroll opts back in to the library's permissive
	// re-enrollment behaviour; see Config.AllowReenroll.
	EnvAllowReenroll = "DM_ALLOW_REENROLL"
	EnvAdminToken    = "DM_ADMIN_TOKEN" // #nosec G101 -- the variable name, not a credential
	// EnvAdminStore opens the admin principal and policy store on the
	// process's own database. Off by default: it mounts the admin API.
	EnvAdminStore = "DM_ADMIN_STORE"
	// EnvAudit writes a projected slog record for every event.
	EnvAudit = "DM_AUDIT_LOG"
	// EnvWebhookURL receives an event per POST in the MicroMDM envelope.
	EnvWebhookURL = "DM_WEBHOOK_URL"
	// EnvAuditStore persists every event to the audit trail.
	EnvAuditStore = "DM_AUDIT_STORE"
	// EnvAuditRetention is how long audit records are kept.
	EnvAuditRetention = "DM_AUDIT_RETENTION"
	// EnvWebhookHMACKey signs the webhook body.
	EnvWebhookHMACKey = "DM_WEBHOOK_HMAC_KEY" // #nosec G101 -- the variable name, not a credential
	EnvCAFile         = "DM_CA_FILE"
	EnvCertHeader     = "DM_CERT_HEADER"
	// Enrollment routes (EnrollConfig).
	EnvPublicURL           = "DM_PUBLIC_URL"
	EnvPushTopic           = "DM_PUSH_TOPIC"
	EnvEnrollCACertFile    = "DM_ENROLL_CA_CERT_FILE"
	EnvEnrollCAKeyFile     = "DM_ENROLL_CA_KEY_FILE"
	EnvSCEPChallenge       = "DM_SCEP_CHALLENGE" // #nosec G101 -- the variable name, not a credential
	EnvSCEPHMACKey         = "DM_SCEP_HMAC_KEY"  // #nosec G101 -- the variable name, not a credential
	EnvProfileIdentifier   = "DM_PROFILE_IDENTIFIER"
	EnvOrganization        = "DM_ORGANIZATION"
	EnvDiscovery           = "DM_DISCOVERY"
	EnvAccountDrivenMethod = "DM_ACCOUNT_DRIVEN_METHOD"
	EnvOIDCIssuer          = "DM_OIDC_ISSUER"
	EnvOIDCClientID        = "DM_OIDC_CLIENT_ID"
	EnvOIDCClientSecret    = "DM_OIDC_CLIENT_SECRET" // #nosec G101 -- the variable name, not a credential
	EnvADEAnchorFile       = "DM_ADE_ANCHOR_FILE"
	EnvADEAudit            = "DM_ADE_AUDIT"
	EnvRequireUserAuth     = "DM_REQUIRE_USER_AUTH"
	// Apple Business Manager (AxMConfig).
	EnvAxMClientID = "DM_AXM_CLIENT_ID"
	EnvAxMKeyID    = "DM_AXM_KEY_ID"
	EnvAxMKeyFile  = "DM_AXM_KEY_FILE"
	EnvAxMScope    = "DM_AXM_SCOPE"
	EnvAxMBaseURL  = "DM_AXM_BASE_URL"
	EnvAxMTokenURL = "DM_AXM_TOKEN_URL" // #nosec G101 -- the variable name, not a credential
	// Device enrollment service (DEPConfig).
	EnvDEPBaseURL        = "DM_DEP_BASE_URL"
	EnvDEPSyncInterval   = "DM_DEP_SYNC_INTERVAL"
	EnvDEPAssignInterval = "DM_DEP_ASSIGN_INTERVAL" // #nosec G101 -- the variable name, not a credential
	EnvDEPProfileURL     = "DM_DEP_PROFILE_URL"
	EnvDEPUsePUT         = "DM_DEP_USE_PUT"

	// Push: where APNs credentials come from, and how pushes are shaped.
	EnvPushSource   = "DM_PUSH_SOURCE"
	EnvPushCertFile = "DM_PUSH_CERT_FILE"
	EnvPushKeyFile  = "DM_PUSH_KEY_FILE"
	EnvPushHost     = "DM_PUSH_HOST"
	EnvPushCoalesce = "DM_PUSH_COALESCE"
	EnvPushCertTTL  = "DM_PUSH_CERT_TTL"
	// ACME and Managed Device Attestation (ACMEConfig).
	EnvIdentity       = "DM_IDENTITY"
	EnvACMEPolicy     = "DM_ACME_POLICY"
	EnvACMEKey        = "DM_ACME_KEY"
	EnvACMEHMACKey    = "DM_ACME_HMAC_KEY" // #nosec G101 -- the variable name, not a credential
	EnvACMEAnchorFile = "DM_ACME_ANCHOR_FILE"
	EnvACMEUnattested = "DM_ACME_ALLOW_UNATTESTED"
	EnvACMEIdentTTL   = "DM_ACME_IDENTIFIER_TTL"

	EnvSubscriptions = "DM_DDM_SUBSCRIPTIONS"
)

// Defaults applied by ParseEnv when a variable is unset.
const (
	DefaultRole    = RoleAll
	DefaultListen  = ":8080"
	DefaultStorage = "sqlite"
	DefaultDSN     = "mdm.db"
)

// ParseEnv builds a Config from DM_* variables through get (os.Getenv in
// the binary). Booleans accept strconv.ParseBool forms.
// keyNames splits a comma separated key list, active first, dropping blanks so
// a trailing comma or a padded list is not a key named "".
func keyNames(v string) []string {
	var out []string
	for name := range strings.SplitSeq(v, ",") {
		if n := strings.TrimSpace(name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

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
		SecretsDir: get(EnvSecretsDir),
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
	cfg.StorageKeys = keyNames(get(EnvStorageKeys))
	if v := get(EnvACMEIdentTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s=%q: %w", ErrConfig, EnvACMEIdentTTL, v, err)
		}
		cfg.Enroll.ACME.IdentifierTTL = d
	}
	for key, dst := range map[string]*bool{
		EnvADEAudit:          &cfg.Enroll.ADEAudit,
		EnvRequireUserAuth:   &cfg.Enroll.RequireUserAuth,
		EnvACMEUnattested:    &cfg.Enroll.ACME.AllowUnattested,
		EnvAllowReenroll:     &cfg.AllowReenroll,
		EnvStorageKeysStrict: &cfg.StorageKeysStrict,
		EnvAdminStore:        &cfg.AdminStoreEnabled,
		EnvAudit:             &cfg.Sinks.Audit,
		EnvAuditStore:        &cfg.Sinks.Persist,
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
