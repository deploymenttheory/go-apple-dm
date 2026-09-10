package app

import (
	"context"
	"crypto/x509"
	json "encoding/json/v2"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/ratelimit"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
	"github.com/deploymenttheory/go-apple-dm/state"
)

// PKIConfig enables issuer status registration, enforcement and publication.
// All lifetimes are required when enabled. Existing DER must be imported before
// enabling enforcement on devices issued before the registry existed.
type PKIConfig struct {
	// Disabled explicitly opts out of issuer status enforcement.
	Disabled                    bool
	Enabled                     bool
	CRLTTL, CRLRefresh, OCSPTTL time.Duration
	// Retired holds old issuer keys while their issued certificates remain valid.
	Retired []IssuerFiles
}

// IssuerFiles names an operator-managed CA certificate and private key.
type IssuerFiles struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

// RouteQuota is explicit per-peer and aggregate admission for one route family.
type RouteQuota struct {
	Interval       time.Duration
	Burst          int
	GlobalInterval time.Duration
	GlobalBurst    int
}

// RateLimitConfig is off when Routes is empty. TrustedProxies is opt-in.
type RateLimitConfig struct {
	Routes     map[string]RouteQuota
	MaxEntries int
}

func (a *App) protocolState(ctx context.Context) (state.Store, error) {
	if a.protocol != nil {
		return a.protocol, nil
	}
	if a.db == nil {
		m := state.NewMemory()
		m.Now = a.cfg.Clock.Now
		a.protocol = m
	} else {
		s, err := statestore.Open(ctx, a.db, a.dialect, a.keyring)
		if err != nil {
			return nil, fmt.Errorf("app: open protocol state: %w", err)
		}
		a.protocol = s
	}
	a.addWorker("protocol-state-retention", a.pruneProtocolState)
	return a.protocol, nil
}

// enrollmentDepot records account association and, when enabled, revocation
// state before ca.Local returns a newly signed certificate.
type enrollmentDepot struct {
	ca.Depot
	associations *accountdriven.Associations
	registry     *revocation.Registry
	issuer       string
	app          *App
}

func (d *enrollmentDepot) Put(ctx context.Context, c *x509.Certificate) error {
	if d.app != nil {
		if err := d.app.replacementIssuance(ctx, c); err != nil {
			return err
		}
		if err := d.app.recordIssuedIdentity(ctx, c); err != nil {
			return err
		}
	}
	if d.registry != nil {
		p := revocation.ProvenanceFromContext(ctx)
		if p.Source == "" {
			p.Source = "scep"
		}
		if ref, ok := strings.CutPrefix(
			c.Subject.CommonName,
			accountdriven.CertificateSubjectPrefix,
		); ok {
			p.EnrollmentReference = ref
		}
		if err := d.registry.Register(ctx, d.issuer, c, p); err != nil {
			return fmt.Errorf("app: register issuance: %w", err)
		}
	}
	if err := d.associations.RegisterCertificate(ctx, c); err != nil {
		return fmt.Errorf("app: register account certificate: %w", err)
	}
	if err := d.Depot.Put(ctx, c); err != nil {
		return fmt.Errorf("app: persist certificate: %w", err)
	}
	return nil
}

type enrollmentChallenge struct {
	base         scep.Challenge
	associations *accountdriven.Associations
	app          *App
}

func (c enrollmentChallenge) Verify(
	ctx context.Context,
	password string,
	csr *x509.CertificateRequest,
) error {
	if c.app != nil && csr != nil &&
		strings.HasPrefix(csr.Subject.CommonName, replacementSubjectPrefix) {
		return c.app.replacementChallenge(ctx, password, csr)
	}
	if csr != nil &&
		strings.HasPrefix(csr.Subject.CommonName, accountdriven.CertificateSubjectPrefix) {
		if c.app != nil && c.app.enroll != nil {
			if _, err := c.app.enroll.admit(
				ctx,
				acme.Binding{CommonName: csr.Subject.CommonName},
			); err != nil {
				return err
			}
		}
		if err := c.associations.VerifySCEPChallenge(ctx, password, csr); err != nil {
			return fmt.Errorf("app: account SCEP challenge: %w", err)
		}
		return nil
	}
	if c.app != nil && c.app.enroll != nil {
		return c.app.enroll.verifySCEPGrant(ctx, password, csr)
	}
	if err := c.base.Verify(ctx, password, csr); err != nil {
		return fmt.Errorf("app: SCEP challenge: %w", err)
	}
	return nil
}

func (a *App) wirePKI(ctx context.Context, e *enrollment, mux *http.ServeMux) error {
	cfg := a.cfg.PKI
	if !cfg.Enabled {
		return nil
	}
	issuers := []revocation.Issuer{
		{
			Certificate: e.caCert,
			Signer:      e.caKey,
			CRLTTL:      cfg.CRLTTL,
			CRLRefresh:  cfg.CRLRefresh,
			OCSPTTL:     cfg.OCSPTTL,
		},
	}
	for _, files := range cfg.Retired {
		certs, err := readCertsPEM(files.Certificate)
		if err != nil {
			return err
		}
		keyPEM, err := os.ReadFile(files.Key)
		if err != nil {
			return fmt.Errorf("app: read retired issuer key: %w", err)
		}
		key, err := parseSignerPEM(keyPEM)
		if err != nil {
			return err
		}
		issuers = append(
			issuers,
			revocation.Issuer{
				Certificate: certs[0],
				Signer:      key,
				CRLTTL:      cfg.CRLTTL,
				CRLRefresh:  cfg.CRLRefresh,
				OCSPTTL:     cfg.OCSPTTL,
			},
		)
	}
	reg, err := revocation.New(a.protocol, issuers...)
	if err != nil {
		return fmt.Errorf("app: configure revocation registry: %w", err)
	}
	reg.Now = a.cfg.Clock.Now
	a.revocations = reg
	mux.Handle("/pki/", reg.Handler("/pki"))
	return nil
}

func (a *App) certificateStatus() func(context.Context, *x509.Certificate) error {
	if a.revocations == nil {
		return nil
	}
	return a.revocations.Check
}

func (a *App) issuancePolicy(e *enrollment) ca.Policy {
	p := ca.Policy{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if a.revocations != nil {
		id := cms.Fingerprint(e.caCert)
		p.CRLDistributionPoints = []string{e.base + "/pki/crl/" + id}
		p.OCSPServer = []string{e.base + "/pki/ocsp/" + id}
	}
	return p
}

func (a *App) acmeRevocations() acme.Revocations {
	if a.revocations == nil {
		return nil
	}
	return a.revocations
}

func (a *App) credentialSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cert := httpapi.CertFromContext(r.Context())
		if a.revocations != nil {
			if err := a.revocations.Check(r.Context(), cert); err != nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func routeFamily(path string) string {
	switch {
	case path == PathHealthz:
		return ""
	case strings.HasPrefix(path, PathAdmin):
		return "admin"
	case strings.HasPrefix(path, PathACME+"/"):
		return "acme"
	case path == PathSCEP:
		return "scep"
	case strings.HasPrefix(path, "/pki/"):
		return "pki"
	case path == PathAuthenticate || strings.HasPrefix(path, "/enroll/oauth2/") || path == PathOIDCCallback:
		return "auth"
	case strings.HasPrefix(path, PathEnroll) || path == PathWellKnown || path == "/ota" || strings.HasPrefix(path, "/ota/"):
		return "enroll"
	case path == PathMDM || path == "/v1/declarative-management" || path == PathDDM+"/v1/declarative-management":
		return "mdm"
	}
	return ""
}

func (a *App) withRateLimits(ctx context.Context, next http.Handler) (http.Handler, error) {
	cfg := a.cfg.RateLimits
	if len(cfg.Routes) == 0 {
		return next, nil
	}
	st, err := a.protocolState(ctx)
	if err != nil {
		return nil, err
	}
	limiter := &ratelimit.Limiter{
		Store:      st,
		MaxEntries: cfg.MaxEntries,
		Namespace:  "reference-http",
	}
	return ratelimit.Middleware(
		ratelimit.HTTPConfig{Limiter: limiter, Buckets: func(r *http.Request) []ratelimit.Bucket {
			family := routeFamily(r.URL.Path)
			q, ok := cfg.Routes[family]
			if !ok {
				return nil
			}
			return []ratelimit.Bucket{
				{Key: family + "/global", Interval: q.GlobalInterval, Burst: q.GlobalBurst},
				{
					Key:      family + "/peer/" + ratelimit.PeerKey(r, a.cfg.TrustedProxies),
					Interval: q.Interval,
					Burst:    q.Burst,
				},
			}
		}, Reject: func(w http.ResponseWriter, r *http.Request, status int) {
			if routeFamily(r.URL.Path) == "acme" {
				code := acme.ProblemRateLimited
				if status == 503 {
					code = acme.ProblemServerInternal
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(status)
				_ = json.MarshalWrite(
					w,
					map[string]any{
						"type":   acme.ProblemPrefix + code,
						"status": status,
						"detail": http.StatusText(status),
					},
				)
				return
			}
			http.Error(w, http.StatusText(status), status)
		}},
		next,
	), nil
}

func (c Config) validateSecurity() error {
	if c.PKI.Enabled &&
		(!c.Enroll.Enabled() || c.PKI.CRLTTL <= 0 || c.PKI.CRLRefresh <= 0 || c.PKI.CRLRefresh >= c.PKI.CRLTTL || c.PKI.OCSPTTL <= 0) {
		return fmt.Errorf(
			"%w: PKI requires enrollment and explicit valid publication lifetimes",
			ErrConfig,
		)
	}
	if c.RateLimits.MaxEntries < 0 || c.RateLimits.MaxEntries > 10000 {
		return fmt.Errorf("%w: rate limit capacity must be at most 10000", ErrConfig)
	}
	for family, q := range c.RateLimits.Routes {
		switch family {
		case "enroll", "auth", "scep", "acme", "admin", "pki", "mdm":
		default:
			return fmt.Errorf("%w: unknown rate-limit route family %q", ErrConfig, family)
		}
		for _, b := range []ratelimit.Bucket{{Key: "peer", Interval: q.Interval, Burst: q.Burst}, {Key: "global", Interval: q.GlobalInterval, Burst: q.GlobalBurst}} {
			if err := b.Validate(); err != nil {
				return fmt.Errorf("%w: quota %s: %w", ErrConfig, family, err)
			}
		}
	}
	return nil
}

// serialFromPath accepts the hexadecimal serial format used by registry records.
func serialFromPath(r *http.Request) (*big.Int, error) {
	s, ok := new(big.Int).SetString(r.PathValue("serial"), 16)
	if !ok || s.Sign() <= 0 || len(s.Bytes()) > 20 {
		return nil, revocation.ErrInvalid
	}
	return s, nil
}

func (a *App) pruneProtocolState(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.cfg.Clock.After(time.Minute):
		}
		if _, err := a.protocol.Prune(ctx, 1000); err != nil {
			a.cfg.Logger.WarnContext(ctx, "protocol state retention failed", "error", err)
		}
	}
}
