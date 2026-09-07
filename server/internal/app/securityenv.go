package app

import (
	json "encoding/json/v2"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Optional security service environment variables. Empty leaves the feature off.
const (
	EnvPKIRevocation       = "DM_PKI_REVOCATION"
	EnvPKICRLTTL           = "DM_PKI_CRL_TTL"
	EnvPKICRLRefresh       = "DM_PKI_CRL_REFRESH"
	EnvPKIOCSPTTL          = "DM_PKI_OCSP_TTL"
	EnvPKIRetiredIssuers   = "DM_PKI_RETIRED_ISSUERS"
	EnvRateLimits          = "DM_RATE_LIMITS"
	EnvRateLimitMaxEntries = "DM_RATE_LIMIT_MAX_ENTRIES"
	EnvTrustedProxies      = "DM_TRUSTED_PROXIES"
)

func parseSecurityEnv(get func(string) string, c *Config) error {
	if raw := get(EnvPKIRevocation); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrConfig, EnvPKIRevocation, err)
		}
		c.PKI.Enabled = v
	}
	for name, dst := range map[string]*time.Duration{EnvPKICRLTTL: &c.PKI.CRLTTL, EnvPKICRLRefresh: &c.PKI.CRLRefresh, EnvPKIOCSPTTL: &c.PKI.OCSPTTL} {
		if raw := get(name); raw != "" {
			v, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("%w: %s: %w", ErrConfig, name, err)
			}
			*dst = v
		}
	}
	if raw := get(EnvPKIRetiredIssuers); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.PKI.Retired, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrConfig, EnvPKIRetiredIssuers, err)
		}
	}
	if raw := get(EnvRateLimitMaxEntries); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrConfig, EnvRateLimitMaxEntries, err)
		}
		c.RateLimits.MaxEntries = n
	}
	if raw := get(EnvTrustedProxies); raw != "" {
		for _, v := range strings.Split(raw, ",") {
			p, err := netip.ParsePrefix(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("%w: %s: %w", ErrConfig, EnvTrustedProxies, err)
			}
			c.RateLimits.TrustedProxies = append(c.RateLimits.TrustedProxies, p)
		}
	}
	if raw := get(EnvRateLimits); raw != "" {
		var quotas map[string]struct {
			Interval       string `json:"interval"`
			Burst          int    `json:"burst"`
			GlobalInterval string `json:"global_interval"`
			GlobalBurst    int    `json:"global_burst"`
		}
		if err := json.Unmarshal([]byte(raw), &quotas, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrConfig, EnvRateLimits, err)
		}
		c.RateLimits.Routes = map[string]RouteQuota{}
		for name, q := range quotas {
			interval, err := time.ParseDuration(q.Interval)
			if err != nil {
				return fmt.Errorf("%w: %s interval: %w", ErrConfig, name, err)
			}
			global, err := time.ParseDuration(q.GlobalInterval)
			if err != nil {
				return fmt.Errorf("%w: %s global interval: %w", ErrConfig, name, err)
			}
			c.RateLimits.Routes[name] = RouteQuota{Interval: interval, Burst: q.Burst, GlobalInterval: global, GlobalBurst: q.GlobalBurst}
		}
	}
	return nil
}
