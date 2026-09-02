package app

import (
	"fmt"
	"strconv"
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
	EnvSubscriptions       = "MDM_DDM_SUBSCRIPTIONS"
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
	for key, dst := range map[string]*bool{EnvADEAudit: &cfg.Enroll.ADEAudit, EnvRequireUserAuth: &cfg.Enroll.RequireUserAuth} {
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
