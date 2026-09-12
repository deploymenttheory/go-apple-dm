package app

import (
	"context"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/discovery"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// Enrollment routes on the mdm and all roles (decision records 0027 to 0029).
const (
	PathSCEP            = "/scep"
	PathACME            = "/acme"
	PathACMECredential  = "/enroll/acme-credential" // #nosec G101 -- a route, not a credential
	PathWellKnown       = discovery.WellKnownPath
	PathEnroll          = "/enroll/"             // + discovery version (mdm-byod, mdm-adde)
	PathADE             = "/enroll/ade"          // DEP profile url and configuration_web_url
	PathAuthenticate    = "/enroll/authenticate" // apple-as-web web-auth URL
	PathOAuth2Authorize = "/enroll/oauth2/authorize"
	PathOAuth2Token     = "/enroll/oauth2/token" // #nosec G101 -- a route, not a credential
	PathOIDCCallback    = "/enroll/oidc/callback"
	OAuth2ClientID      = "mdm"
	OAuth2RedirectURL   = accountdriven.CallbackScheme + ":/oauth2/redirection"
	OAuth2Scope         = "MDM"
)

// EnrollConfig turns the enrollment routes on. It is inactive until
// PublicURL and Topic are set.
type EnrollConfig struct {
	Admission                   EnrollmentAdmission
	AdmissionFile               string
	UserAuthHA1File             string
	OTAAnchorFile, OTAChallenge string
	// PublicURL is the https base devices reach (profiles, discovery,
	// redirects).
	PublicURL string
	// Topic is the APNs topic written into enrollment profiles.
	Topic string
	// CACertFile and CAKeyFile are the PEM files of the enrollment CA that
	// issues device identities through SCEP; both empty means a
	// self-signed CA is generated at start (development only) and logged.
	CACertFile, CAKeyFile string
	// TLSAnchorFile contains public CA certificates needed to trust the HTTPS
	// enrollment endpoints. Empty means publicly trusted HTTPS. It is independent
	// of the identity issuer and the incoming ADE/OTA signing anchors.
	TLSAnchorFile string
	// SCEPChallenge does not authorize reference-server certificate issuance.
	// SCEPHMACKey also supplies the fallback key for ACME client identifiers.
	SCEPChallenge     string
	SCEPHMACKey       []byte
	ProfileIdentifier string
	Organization      string
	// Discovery maps a model family to a discovery version, for example
	// Mac=mdm-adde,iPhone=mdm-byod. Families absent are rejected.
	Discovery map[discovery.ModelFamily]string
	// AccountDrivenMethod is apple-as-web (default) or apple-oauth2.
	AccountDrivenMethod string
	// OIDC is the identity provider behind the web view, the apple-as-web
	// page, and the apple-oauth2 sign-in. Unset leaves only the
	// token-based ADE lane.
	OIDC OIDCConfig
	// ADEAnchorFile is a PEM bundle of MachineInfo signing anchors; empty
	// uses Apple's. ADEAudit logs verification failures and continues.
	ADEAnchorFile string
	ADEAudit      bool
	// RequireUserAuth gates user channels on UserAuthenticate (0029).
	RequireUserAuth bool
	// ReturnToService allows a supervised Automated Device Enrollment device
	// that asks to erase itself and re-enrol. Off by default: the answer to a
	// return-to-service request erases a device, so it is not something a
	// server should agree to because nobody said otherwise (0045).
	ReturnToService bool
	// Identity is where an enrolled device's identity certificate comes
	// from: IdentitySCEP (the default) or IdentityACME. The ACME endpoints
	// are mounted either way, so a declarative credential can use them even
	// when enrollment profiles still carry SCEP.
	Identity string
	// ACME configures the ACME server and the identities it issues.
	ACME ACMEConfig
	// Anchors is the parsed ADEAnchorFile (tests set it directly).
	Anchors []*x509.Certificate
}

// OIDCConfig is the relying-party configuration.
type OIDCConfig struct {
	RootCAFile                     string
	Issuer, ClientID, ClientSecret string
	// HTTPClient reaches the provider; tests point it at a fake.
	HTTPClient *http.Client
}

// Enabled reports whether enrollment routes are configured.
func (e EnrollConfig) Enabled() bool { return e.PublicURL != "" && e.Topic != "" }

func (e EnrollConfig) validate() error {
	if !e.Enabled() {
		return nil
	}
	if !strings.HasPrefix(e.PublicURL, "https://") {
		return fmt.Errorf("%w: public URL must be https", ErrConfig)
	}
	if (e.CACertFile == "") != (e.CAKeyFile == "") {
		return fmt.Errorf("%w: CA certificate and key files go together", ErrConfig)
	}
	switch e.Identity {
	case "", IdentitySCEP:
	case IdentityACME:
	default:
		return fmt.Errorf(
			"%w: %s must be %s or %s, got %q",
			ErrConfig, EnvIdentity, IdentitySCEP, IdentityACME, e.Identity,
		)
	}
	switch e.AccountDrivenMethod {
	case "", accountdriven.MethodAppleAsWeb, accountdriven.MethodAppleOAuth2:
	default:
		return fmt.Errorf("%w: account-driven method %q", ErrConfig, e.AccountDrivenMethod)
	}
	for family, version := range e.Discovery {
		if _, known := discovery.ParseModelFamily(string(family)); !known {
			return fmt.Errorf("%w: unknown model family %q", ErrConfig, family)
		}
		if version != discovery.VersionBYOD && version != discovery.VersionADDE {
			return fmt.Errorf("%w: discovery version %q for %s", ErrConfig, version, family)
		}
	}
	return nil
}

// ParseDiscovery reads "Mac=mdm-adde,iPhone=mdm-byod".
func ParseDiscovery(s string) (map[discovery.ModelFamily]string, error) {
	out := map[discovery.ModelFamily]string{}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		family, version, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("%w: discovery entry %q (want family=version)", ErrConfig, part)
		}
		out[discovery.ModelFamily(strings.TrimSpace(family))] = strings.TrimSpace(version)
	}
	return out, nil
}

// enrollment holds what the routes share.
type enrollment struct {
	depot     *enrollmentDepot
	admission EnrollmentAdmission
	now       func() time.Time
	app       *App
	cfg       EnrollConfig
	base      string
	caCert    *x509.Certificate
	caKey     crypto.Signer
	local     *ca.Local
	tokens    *accountdriven.Tokens
	asweb     *accountdriven.AppleAsWeb
	oauth     *accountdriven.OAuth2
	acme      *acmeService
	ade       *ade.Handler
	flow      *webauth.Flow
	challenge scep.Challenge
	trust     []*x509.Certificate
	state     state.Store
}

// wireEnrollment mounts the routes; it returns the hooks the core needs.
func (a *App) wireEnrollment(ctx context.Context, mux *http.ServeMux) ([]service.Hook, error) {
	cfg := a.cfg.Enroll
	if !cfg.Enabled() {
		return nil, nil
	}
	e := &enrollment{
		cfg:  cfg,
		app:  a,
		now:  a.cfg.Clock.Now,
		base: strings.TrimSuffix(cfg.PublicURL, "/"),
	}
	var admissionErr error
	e.admission, admissionErr = a.enrollmentAdmission(e)
	if admissionErr != nil {
		return nil, admissionErr
	}
	if err := e.loadCA(a); err != nil {
		return nil, err
	}
	st, err := a.protocolState(ctx)
	if err != nil {
		return nil, err
	}
	e.state = st
	if err := a.wireServiceConfig(e, mux); err != nil {
		return nil, err
	}
	e.tokens = &accountdriven.Tokens{
		Store: &accountdriven.StateTokenStore{Backend: st},
		Now:   a.cfg.Clock.Now,
	}
	if err := a.wirePKI(ctx, e, mux); err != nil {
		return nil, err
	}
	e.depot = &enrollmentDepot{
		app:          a,
		Depot:        ca.NewMemoryDepot(),
		associations: e.tokens.AssociationStore(),
		registry:     a.revocations,
		issuer:       cms.Fingerprint(e.caCert),
	}
	e.local, err = ca.NewLocal(e.caCert, e.caKey, ca.WithDepot(e.depot), ca.WithClock(a.cfg.Clock))
	if err != nil {
		return nil, fmt.Errorf("app: enrollment CA: %w", err)
	}
	if len(cfg.SCEPHMACKey) > 0 {
		if e.challenge, err = scep.NewHMACChallenge(
			cfg.SCEPHMACKey,
			time.Hour,
			a.cfg.Clock,
		); err != nil {
			return nil, fmt.Errorf("app: SCEP challenge: %w", err)
		}
	} else {
		e.challenge = scep.StaticChallenge(cfg.SCEPChallenge)
	}
	scepServer, err := scep.NewServer(
		e.local,
		e.caCert,
		e.caKey,
		scep.WithChallenge(
			enrollmentChallenge{
				base:         e.challenge,
				associations: e.tokens.AssociationStore(),
				app:          a,
			},
		),
		scep.WithPolicy(a.issuancePolicy(e)),
		scep.WithIssuance(e.issueSCEP),
		scep.WithCertificateStatus(a.certificateStatus()),
		scep.WithLogger(a.cfg.Logger),
	)
	if err != nil {
		return nil, fmt.Errorf("app: SCEP: %w", err)
	}
	mux.Handle(PathSCEP, scepServer.Handler())

	// ACME is mounted whether or not enrollment profiles use it, because a
	// declarative credential can ask an enrolled device to obtain a second
	// identity through it.
	if e.acme, err = a.newACME(ctx, e); err != nil {
		return nil, err
	}
	a.acme = e.acme
	mux.Handle(PathACME+"/", e.acme.server.Handler())
	// The credential document identifies the device by the certificate it
	// presents, so it goes behind the same certificate source as the MDM
	// endpoints rather than being readable by anyone with the URL.
	mux.Handle(
		PathACMECredential,
		noStore(a.certSource()(a.credentialSecurity(e.acme.credentialHandler()))),
	)

	e.asweb = &accountdriven.AppleAsWeb{URL: e.base + PathAuthenticate, Tokens: e.tokens}
	e.oauth = &accountdriven.OAuth2{
		AuthorizationURL: e.base + PathOAuth2Authorize,
		TokenURL:         e.base + PathOAuth2Token,
		RedirectURL:      OAuth2RedirectURL,
		ClientID:         OAuth2ClientID,
		Scope:            OAuth2Scope,
		Tokens:           e.tokens,
	}
	mux.Handle("POST "+PathOAuth2Token, e.oauth.TokenHandler())

	// Service discovery.
	routes := map[discovery.ModelFamily]discovery.Server{}
	for family, version := range cfg.Discovery {
		routes[family] = discovery.Server{Version: version, BaseURL: e.base + PathEnroll + version}
	}
	mux.Handle(
		PathWellKnown,
		discovery.Handler(
			discovery.Config{Router: discovery.StaticRouter(routes), Logger: a.cfg.Logger},
		),
	)

	// ADE.
	anchors := cfg.Anchors
	if len(anchors) == 0 && cfg.ADEAnchorFile != "" {
		if anchors, err = readCertsPEM(cfg.ADEAnchorFile); err != nil {
			return nil, fmt.Errorf("%w: ADE anchors: %w", ErrConfig, err)
		}
	}
	e.ade = ade.New(ade.Config{
		Parse:  ade.ParseOptions{Anchors: anchors, Audit: cfg.ADEAudit, Logger: a.cfg.Logger},
		Signer: ade.Signer{Cert: e.caCert, Key: e.caKey},
		Profile: func(ctx context.Context, p *ade.Parsed, id ade.Identity) (*enroll.Profile, error) {
			cn := p.SERIAL
			if email, _ := id.Claims["email"].(string); email != "" {
				cn = email + "/" + p.SERIAL
			}
			return e.profileForDevice(
				ctx,
				deviceBinding(p.UDID, p.SERIAL, p.PRODUCT, cn),
				e.cfg.Identity,
				p.PRODUCT,
				p.OSVERSION,
				enroll.MacHardwareUnknown,
			)
		},
		WebAuth: ade.WebAuthFunc(func(w http.ResponseWriter, r *http.Request, b ade.Bound) {
			if e.flow == nil {
				http.Error(
					w,
					"web view authentication is not configured",
					http.StatusNotImplemented,
				)
				return
			}
			if err := e.flow.Begin(
				w,
				r,
				webauth.Bound{
					Serial: b.Serial,
					UDID:   b.UDID,
					Extra:  map[string]string{"flow": "ade"},
				},
			); err != nil {
				a.cfg.Logger.Error("app: ADE web view", "error", err)
				http.Error(
					w,
					http.StatusText(http.StatusInternalServerError),
					http.StatusInternalServerError,
				)
			}
		}),
		Logger: a.cfg.Logger,
		Now:    a.cfg.Clock.Now,
	})
	mux.Handle(PathADE, e.ade)

	// Account-driven enrollment, one endpoint per discovery version.
	var auth accountdriven.Authenticator = e.asweb
	if cfg.AccountDrivenMethod == accountdriven.MethodAppleOAuth2 {
		auth = e.oauth
	}
	parse := e.parseDeviceInfo(anchors)
	for _, version := range []string{discovery.VersionBYOD, discovery.VersionADDE} {
		h, err := accountdriven.New(accountdriven.Config{
			Version: version,
			Parse:   parse,
			Auth:    auth,
			Tokens:  e.tokens,
			Profile: func(ctx context.Context, id accountdriven.Identity, info *accountdriven.DeviceInfo) (*enroll.Profile, error) {
				association, ok := accountdriven.AssociationFromContext(ctx)
				if !ok {
					return nil, accountdriven.ErrAssociation
				}
				p, err := e.profileForDevice(
					ctx,
					acme.Binding{
						CommonName:        accountdriven.CertificateSubjectPrefix + association.Reference,
						AllowUnidentified: true,
					},
					e.cfg.Identity, info.Product, info.OSVersion, enroll.MacHardwareUnknown,
				)
				if err != nil {
					return nil, err
				}
				if p.SCEP != nil {
					p.SCEP.Challenge, err = e.tokens.AssociationStore().
						IssueSCEPChallenge(ctx, association.Reference, time.Hour)
				}
				if err != nil {
					return nil, fmt.Errorf("app: issue profile SCEP challenge: %w", err)
				}
				return p, nil
			},
			SignCert: e.caCert,
			SignKey:  e.caKey,
			Logger:   a.cfg.Logger,
		})
		if err != nil {
			return nil, fmt.Errorf("app: account-driven %s: %w", version, err)
		}
		mux.Handle(PathEnroll+version, h)
	}

	if err := a.wireOIDC(e, mux); err != nil {
		return nil, err
	}
	hooks := []service.Hook{
		issuanceAdmission{app: a},
		replacementAdmission{app: a},
		&accountdriven.CheckinHook{Tokens: e.tokens, Auth: auth},
	}
	a.enroll = e
	if err := a.wireOTA(mux); err != nil {
		return nil, err
	}
	return hooks, nil
}

// complete finishes whichever web flow the state belongs to.
func (e *enrollment) complete(
	ctx context.Context,
	bound webauth.Bound,
	claims webauth.Claims,
	decision webauth.Decision,
	w http.ResponseWriter,
	r *http.Request,
) {
	identity := accountdriven.Identity{
		UserIdentifier:      bound.LoginHint,
		ManagedAppleAccount: decision.Profile,
		Subject:             claims.Subject,
		Issuer:              e.cfg.OIDC.Issuer,
		Claims:              claims.Raw,
	}
	switch bound.Extra["flow"] {
	case "ade":
		p, id, err := e.ade.Resume(ctx, bound.Serial)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		id.Subject, id.Claims = claims.Subject, map[string]any{
			"email":  claims.Email,
			"name":   claims.Name,
			"groups": claims.Groups,
		}
		e.ade.Finish(w, r, p, id)
	case "asweb":
		if err := e.asweb.Finish(w, r, identity); err != nil {
			http.Error(
				w,
				http.StatusText(http.StatusInternalServerError),
				http.StatusInternalServerError,
			)
		}
	case "oauth2":
		req := &accountdriven.AuthorizationRequest{
			State:     bound.Extra["state"],
			LoginHint: bound.LoginHint,
			Scope:     bound.Extra["scope"],
		}
		if err := e.oauth.Grant(w, r, req, identity); err != nil {
			http.Error(
				w,
				http.StatusText(http.StatusInternalServerError),
				http.StatusInternalServerError,
			)
		}
	default:
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	}
}

// profile builds the enrollment profile for one device. The binding says
// what the server knows about that device, which becomes the certificate
// subject and, for an ACME identity, the device the client identifier is
// issued for.
func (e *enrollment) profile(ctx context.Context, b acme.Binding) (*enroll.Profile, error) {
	return e.profileWithIdentity(ctx, b, e.cfg.Identity)
}

func (e *enrollment) profileWithIdentity(
	ctx context.Context,
	b acme.Binding,
	identity string,
	targets ...acmeTarget,
) (*enroll.Profile, error) {
	if identity == "" {
		identity = IdentitySCEP
	}
	if identity != IdentitySCEP && identity != IdentityACME {
		return nil, fmt.Errorf("%w: identity must be scep or acme", ErrConfig)
	}
	grant, err := e.admit(ctx, b)
	if err != nil {
		return nil, err
	}
	subjectCN := b.CommonName
	challenge := ""
	if identity == IdentitySCEP {
		challenge, err = e.issueSCEPGrant(ctx, b, grant)
		if err != nil {
			return nil, err
		}
	}
	id := e.cfg.ProfileIdentifier
	if id == "" {
		id = "com.deploymenttheory.mdm.enrollment"
	}
	org := e.cfg.Organization
	if org == "" {
		org = "go-apple-dm"
	}
	out := &enroll.Profile{
		Identifier:   id,
		DisplayName:  org + " MDM enrollment",
		Organization: org,
		Topic:        e.cfg.Topic,
		ServerURL:    e.base + PathMDM,
		CheckInURL:   e.base + PathMDM,
		Roots:        e.trust,
		ServerCapabilities: []string{
			enroll.CapabilityPerUserConnections,
			enroll.CapabilityBootstrapToken,
		},
	}
	if len(targets) > 0 {
		out.Target = targets[0].target
		out.MacHardware = targets[0].hardware
	}
	if err := e.stabilizeProfile(ctx, b, out); err != nil {
		return nil, err
	}
	if identity == IdentityACME {
		b.CommonName, b.Organization = subjectCN, []string{org}
		payload, err := e.acme.acmePayload(b, e.acme.server.DirectoryURL(), targets...)
		if err != nil {
			return nil, err
		}
		if !payload.Attest && !e.acme.cfg.AllowUnattested {
			return nil, fmt.Errorf(
				"%w: this ACME enrollment requires attestation; use SCEP or explicitly authorize unattested enrollment",
				ErrBadACMERequest,
			)
		}
		out.ACME = payload
		return out, nil
	}
	out.SCEP = &enroll.SCEP{
		KeyIsExtractable: new(false), AllowAllAppsAccess: new(false),
		URL:       e.base + PathSCEP,
		Challenge: challenge,
		Subject:   pkix.Name{CommonName: subjectCN, Organization: []string{org}},
	}
	return out, nil
}

// parseDeviceInfo adapts the ADE parser to the account-driven body: the
// same CMS verification, then the documented keys.
func (e *enrollment) parseDeviceInfo(anchors []*x509.Certificate) accountdriven.Parser {
	return func(r *http.Request) (*accountdriven.DeviceInfo, error) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, accountdriven.MaxBody))
		if err != nil {
			return nil, fmt.Errorf("app: read body: %w", err)
		}
		content, _, err := ade.Verify(
			raw,
			ade.ParseOptions{Anchors: anchors, Audit: e.cfg.ADEAudit},
		)
		if err != nil {
			return nil, fmt.Errorf("app: device info: %w", err)
		}
		var body struct {
			Language  string `plist:"LANGUAGE"`
			Product   string `plist:"PRODUCT"`
			Version   string `plist:"VERSION"`
			OSVersion string `plist:"OS_VERSION"`
		}
		if err := plist.Unmarshal(content, &body); err != nil {
			return nil, fmt.Errorf("app: device info plist: %w", err)
		}
		return &accountdriven.DeviceInfo{
			Language:  body.Language,
			Product:   body.Product,
			Version:   body.Version,
			OSVersion: body.OSVersion,
			Raw:       content,
		}, nil
	}
}

// loadCA reads the CA files or generates a self-signed CA.
func (e *enrollment) loadCA(a *App) error {
	if e.cfg.CACertFile == "" {
		cert, key, err := ca.NewSelfSigned(
			ca.SelfSignedOptions{
				Subject: pkix.Name{
					CommonName:   "go-apple-dm enrollment CA",
					Organization: []string{"go-apple-dm"},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("app: self-signed CA: %w", err)
		}
		a.cfg.Logger.Warn(
			"app: generated a self-signed enrollment CA; identities will not survive a restart",
			"subject",
			cert.Subject.CommonName,
		)
		e.caCert, e.caKey = cert, key
		return nil
	}
	certs, err := readCertsPEM(e.cfg.CACertFile)
	if err != nil || len(certs) == 0 {
		return fmt.Errorf("%w: CA certificate file: %w", ErrConfig, err)
	}
	keyPEM, err := os.ReadFile(e.cfg.CAKeyFile)
	if err != nil {
		return fmt.Errorf("%w: CA key file: %w", ErrConfig, err)
	}
	key, err := parseSignerPEM(keyPEM)
	if err != nil {
		return fmt.Errorf("%w: CA key: %w", ErrConfig, err)
	}
	e.caCert, e.caKey = certs[0], key
	return nil
}

var errPEM = errors.New("no usable PEM block")

func readCertsPEM(path string) ([]*x509.Certificate, error) {
	data, err := os.ReadFile(
		path,
	) // #nosec G304 -- an operator-supplied path from the configuration
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, errPEM
	}
	return certs, nil
}

func parseSignerPEM(data []byte) (crypto.Signer, error) {
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return nil, errPEM
		}
		switch block.Type {
		case "RSA PRIVATE KEY":
			k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse RSA key: %w", err)
			}
			return k, nil
		case "EC PRIVATE KEY":
			k, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse EC key: %w", err)
			}
			return k, nil
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse PKCS#8 key: %w", err)
			}
			s, ok := k.(crypto.Signer)
			if !ok {
				return nil, errPEM
			}
			return s, nil
		}
	}
}

// returnToService builds the response when the operator enables the feature.
// Deployment-specific eligibility and post-erasure configuration require
// additional policy. The service attaches an available escrowed bootstrap token
// when the response omits it.
func returnToService(enabled bool) service.ReturnToServiceHandler {
	if !enabled {
		return nil
	}
	return func(context.Context, *mdm.Request, *checkin.ReturnToService) (*checkin.ReturnToServiceResponse, error) {
		return &checkin.ReturnToServiceResponse{
			ReturnToService: checkin.ReturnToServiceResponseReturnToService{Enabled: true},
		}, nil
	}
}

func (c OIDCConfig) client() (*http.Client, error) {
	if c.RootCAFile != "" {
		return trustedHTTPClient(c.RootCAFile)
	}
	if c.HTTPClient != nil {
		return c.HTTPClient, nil
	}
	return http.DefaultClient, nil
}

func (a *App) wireOIDC(e *enrollment, mux *http.ServeMux) error {
	cfg := a.cfg.Enroll
	var err error
	// The identity provider behind every web flow.
	if cfg.OIDC.Issuer != "" {
		cfg.OIDC.HTTPClient, err = cfg.OIDC.client()
		if err != nil {
			return err
		}
		e.flow, err = webauth.New(webauth.Config{
			Issuer:       cfg.OIDC.Issuer,
			ClientID:     cfg.OIDC.ClientID,
			ClientSecret: cfg.OIDC.ClientSecret,
			RedirectURL:  e.base + PathOIDCCallback,
			StateStore:   &webauth.SharedStore{Backend: e.state},
			HTTPClient:   cfg.OIDC.HTTPClient,
			Clock:        a.cfg.Clock,
			Logger:       a.cfg.Logger,
			Authorizer: func(ctx context.Context, _ webauth.Bound, c webauth.Claims) (webauth.Decision, error) {
				g, err := e.admission(
					ctx,
					AdmissionRequest{
						Issuer:        cfg.OIDC.Issuer,
						Subject:       c.Subject,
						Email:         c.Email,
						EmailVerified: c.EmailVerified,
						Groups:        c.Groups,
					},
				)
				if err != nil {
					a.securityEvent(ctx, event.EnrollmentDenied)
					return webauth.Decision{}, err
				}
				if g.Account == "" || g.ExpiresAt.IsZero() ||
					!a.cfg.Clock.Now().Before(g.ExpiresAt) {
					a.securityEvent(ctx, event.EnrollmentDenied)
					return webauth.Decision{}, webauth.ErrDenied
				}
				return webauth.Decision{Profile: g.Account}, nil
			},
			Complete: e.complete,
		})
		if err != nil {
			return fmt.Errorf("app: OIDC: %w", err)
		}
		mux.Handle(PathOIDCCallback, e.flow.Callback())
		mux.HandleFunc("GET "+PathAuthenticate, func(w http.ResponseWriter, r *http.Request) {
			hint := accountdriven.UserIdentifier(r)
			if err := e.flow.Begin(
				w,
				r,
				webauth.Bound{LoginHint: hint, Extra: map[string]string{"flow": "asweb"}},
			); err != nil {
				http.Error(
					w,
					http.StatusText(http.StatusInternalServerError),
					http.StatusInternalServerError,
				)
			}
		})
		mux.HandleFunc("GET "+PathOAuth2Authorize, func(w http.ResponseWriter, r *http.Request) {
			req, err := e.oauth.ParseAuthorization(r)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			if err := e.flow.Begin(
				w,
				r,
				webauth.Bound{
					LoginHint: req.LoginHint,
					Extra: map[string]string{
						"flow":  "oauth2",
						"state": req.State,
						"scope": req.Scope,
					},
				},
			); err != nil {
				http.Error(
					w,
					http.StatusText(http.StatusInternalServerError),
					http.StatusInternalServerError,
				)
			}
		})
	} else {
		a.cfg.Logger.Warn("app: no OIDC issuer configured; only the token-based ADE lane can enrol")
	}
	return nil
}
