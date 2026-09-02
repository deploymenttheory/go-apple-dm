package app

import (
	"fmt"
	"strconv"
)

// Environment variables read by ParseEnv.
const (
	EnvRole          = "MDM_ROLE"
	EnvListen        = "MDM_LISTEN"
	EnvStorage       = "MDM_STORAGE"
	EnvDSN           = "MDM_DSN"
	EnvDDMURL        = "MDM_DDM_URL"
	EnvDDMSendKey    = "MDM_DDM_SEND_KEY"
	EnvDDMRecvKey    = "MDM_DDM_RECV_KEY"
	EnvAdminToken    = "MDM_ADMIN_TOKEN" // #nosec G101 -- the variable name, not a credential
	EnvCAFile        = "MDM_CA_FILE"
	EnvCertHeader    = "MDM_CERT_HEADER"
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
	return cfg, cfg.validate()
}
