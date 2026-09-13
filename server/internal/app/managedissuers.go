package app

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/scep"
)

type issuedByKey struct{}

func issuerFromContext(ctx context.Context) string {
	s, _ := ctx.Value(issuedByKey{}).(string)
	return s
}

type targetIssuerKey struct{}

type managedIssuerService struct {
	enrollment *enrollment
	scep       http.Handler
}

func (e *enrollment) scepPath() string {
	if e.scepRoute != "" {
		return e.scepRoute
	}
	return PathSCEP
}

func (e *enrollment) acmePath() string {
	if e.acmeRoute != "" {
		return e.acmeRoute
	}
	return PathACME
}

func (a *App) managedIssuerPairs(ctx context.Context) ([]tls.Certificate, error) {
	return a.managedPairs(ctx, false)
}

func (a *App) managedPairs(ctx context.Context, includeRetired bool) ([]tls.Certificate, error) {
	return a.certificatePairs(ctx, a.cfg.Setup.IssuerID, includeRetired)
}

func (a *App) certificatePairs(
	ctx context.Context,
	id string,
	includeRetired bool,
) ([]tls.Certificate, error) {
	item, err := a.Certificates.Get(ctx, id)
	if err != nil {
		return nil, wrapError(err)
	}
	var out []tls.Certificate
	for _, rev := range item.Revisions {
		if rev.Fingerprint == "" || rev.Phase == "cancelled" ||
			(rev.Phase == "retired" && !includeRetired) {
			continue
		}
		material, err := a.Certificates.LoadMaterial(ctx, item.ID, rev.ID)
		if err != nil {
			return nil, wrapError(err)
		}
		pair, err := tls.X509KeyPair(material.Certificate, material.Key)
		if err != nil {
			return nil, wrapError(err)
		}
		out = append(out, pair)
	}
	return out, nil
}

func (a *App) managedRoots(ctx context.Context) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	pairs, err := a.managedIssuerPairs(ctx)
	if err != nil {
		return nil, wrapError(err)
	}
	for _, pair := range pairs {
		pool.AddCert(pair.Leaf)
	}
	return pool, nil
}

func (a *App) managedRegistry(ctx context.Context) (*revocation.Registry, error) {
	pairs, err := a.managedPairs(ctx, true)
	if err != nil {
		return nil, wrapError(err)
	}
	var issuers []revocation.Issuer
	for _, pair := range pairs {
		issuers = append(
			issuers,
			revocation.Issuer{
				Certificate: pair.Leaf,
				Signer:      pair.PrivateKey.(crypto.Signer),
				CRLTTL:      a.cfg.PKI.CRLTTL,
				CRLRefresh:  a.cfg.PKI.CRLRefresh,
				OCSPTTL:     a.cfg.PKI.OCSPTTL,
			},
		)
	}
	registry, err := revocation.New(a.protocol, issuers...)
	if err != nil {
		return nil, wrapError(err)
	}
	registry.Now = a.cfg.Clock.Now
	return registry, nil
}

func (a *App) certificateRegistry(ctx context.Context) (*revocation.Registry, error) {
	if a.Certificates != nil {
		return a.managedRegistry(ctx)
	}
	return a.revocations, nil
}

// Legacy URLs keep their original issuing key while it is retained. Retiring
// that issuer closes issuance without removing its CRL or OCSP service.
func (a *App) legacyIssuance(next http.Handler) http.Handler {
	if a.Certificates == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.managedIssuer(r.Context(), "1"); err != nil {
			http.Error(w, "enrollment issuer unavailable", http.StatusGone)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ManagedTLSConfig refreshes device trust for new connections without mutating a
// certificate pool already in use by another TLS handshake.
func (a *App) ManagedTLSConfig(hello *tls.ClientHelloInfo) (*tls.Config, error) {
	roots, err := a.managedRoots(hello.Context())
	if err != nil {
		// A setup-only server can serve HTTPS before enrollment is configured.
		if a.enroll != nil {
			return nil, wrapError(err)
		}
		roots = x509.NewCertPool()
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		ClientAuth:     tls.VerifyClientCertIfGiven,
		ClientCAs:      roots,
		GetCertificate: a.TLSCertificate,
		NextProtos:     []string{"h2", "http/1.1"},
	}, nil
}

func (a *App) profileIssuer(ctx context.Context) (*managedIssuerService, error) {
	rev, _ := ctx.Value(targetIssuerKey{}).(string)
	if rev == "" {
		item, err := a.Certificates.Get(ctx, a.cfg.Setup.IssuerID)
		if err != nil {
			return nil, wrapError(err)
		}
		rev = item.Active
	}
	return a.managedIssuer(ctx, rev)
}

func (a *App) managedIssuer(ctx context.Context, rev string) (*managedIssuerService, error) {
	if a.enroll == nil {
		return nil, fmt.Errorf("%w: enrollment is not configured", lifecycle.ErrConflict)
	}
	item, err := a.Certificates.Get(ctx, a.cfg.Setup.IssuerID)
	if err != nil {
		return nil, wrapError(err)
	}
	allowed := false
	for _, v := range item.Revisions {
		if v.ID == rev && v.Fingerprint != "" && v.Phase != "cancelled" && v.Phase != "retired" {
			allowed = true
		}
	}
	if !allowed {
		return nil, lifecycle.ErrNotFound
	}
	a.issuerMu.Lock()
	defer a.issuerMu.Unlock()
	if service := a.issuerServices[rev]; service != nil {
		return service, nil
	}
	material, err := a.Certificates.LoadMaterial(ctx, item.ID, rev)
	if err != nil {
		return nil, wrapError(err)
	}
	pair, err := tls.X509KeyPair(material.Certificate, material.Key)
	if err != nil {
		return nil, wrapError(err)
	}
	copy := *a.enroll
	copy.issuerRevision = rev
	copy.caCert = pair.Leaf
	copy.caKey = pair.PrivateKey.(crypto.Signer)
	copy.scepRoute = PathSCEP + "/issuers/" + rev
	copy.acmeRoute = PathACME + "/issuers/" + rev
	copy.trust = append(append([]*x509.Certificate(nil), copy.trust...), pair.Leaf)
	registry, err := a.managedRegistry(ctx)
	if err != nil {
		return nil, wrapError(err)
	}
	copy.depot = &enrollmentDepot{
		app:          a,
		Depot:        ca.NewMemoryDepot(),
		associations: copy.tokens.AssociationStore(),
		registry:     registry,
		issuer:       cms.Fingerprint(pair.Leaf),
	}
	copy.local, err = ca.NewLocal(
		copy.caCert,
		copy.caKey,
		ca.WithDepot(copy.depot),
		ca.WithClock(a.cfg.Clock),
	)
	if err != nil {
		return nil, wrapError(err)
	}
	copy.acme, err = a.newACME(ctx, &copy)
	if err != nil {
		return nil, wrapError(err)
	}
	server, err := scep.NewServer(
		copy.local,
		copy.caCert,
		copy.caKey,
		scep.WithChallenge(
			enrollmentChallenge{
				base:         copy.challenge,
				associations: copy.tokens.AssociationStore(),
				app:          a,
			},
		),
		scep.WithPolicy(
			a.issuancePolicy(&copy),
		),
		scep.WithIssuance(copy.issueSCEP),
		scep.WithCertificateStatus(a.certificateStatus()),
		scep.WithLogger(a.cfg.Logger),
	)
	if err != nil {
		return nil, wrapError(err)
	}
	service := &managedIssuerService{enrollment: &copy, scep: server.Handler()}
	if a.issuerServices == nil {
		a.issuerServices = map[string]*managedIssuerService{}
	}
	a.issuerServices[rev] = service
	return service, nil
}

func (a *App) managedIssuanceHandler(isACME bool) http.Handler {
	prefix := PathSCEP + "/issuers/"
	if isACME {
		prefix = PathACME + "/issuers/"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		rev, _, _ := strings.Cut(rest, "/")
		service, err := a.managedIssuer(r.Context(), rev)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if isACME {
			service.enrollment.acme.server.Handler().ServeHTTP(w, r)
		} else {
			service.scep.ServeHTTP(w, r)
		}
	})
}

func (a *App) profileSigningIdentity(
	e *enrollment,
) func(context.Context) (*x509.Certificate, crypto.Signer, error) {
	if a.Certificates == nil {
		return nil
	}
	return func(ctx context.Context) (*x509.Certificate, crypto.Signer, error) {
		issuer, err := a.profileIssuer(ctx)
		if err != nil {
			return nil, nil, wrapError(err)
		}
		return issuer.enrollment.caCert, issuer.enrollment.caKey, nil
	}
}

func (a *App) managedProfileTrust(ctx context.Context) ([]*x509.Certificate, error) {
	seen := map[string]bool{}
	out := []*x509.Certificate{}
	for _, id := range []string{a.cfg.Setup.HTTPSCAID, a.cfg.Setup.IssuerID} {
		if id == "" {
			continue
		}
		pairs, err := a.certificatePairs(ctx, id, false)
		if errors.Is(err, lifecycle.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, wrapError(err)
		}
		for _, pair := range pairs {
			hash := cms.Fingerprint(pair.Leaf)
			if !seen[hash] {
				out = append(out, pair.Leaf)
				seen[hash] = true
			}
		}
	}
	return out, nil
}
