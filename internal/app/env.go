package app

import (
	"fmt"
	"github.com/deploymenttheory/go-apple-mdm/enroll"
	"strconv"
	"time"
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
	EnvAdminToken = "MDM_ADMIN_TOKEN" // #nosec G101 -- the variable name, not a credential
	EnvCAFile     = "MDM_CA_FILE"
	EnvCertHeader = "MDM_CERT_HEADER"
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
		Role:          Role(pick(EnvRole, string(DefaultRole))),
		Listen:        pick(EnvListen, DefaultListen),
		Storage:       pick(EnvStorage, DefaultStorage),
		DSN:           pick(EnvDSN, DefaultDSN),
		DDMURL:        get(EnvDDMURL),
		AdminToken:    get(EnvAdminToken),
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
