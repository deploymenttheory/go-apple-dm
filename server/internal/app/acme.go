package app

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/pki/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
	acmesql "github.com/deploymenttheory/go-apple-dm/server/acmestore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/storage"
	acmeinmem "github.com/deploymenttheory/go-apple-dm/storage/acme/inmem"
)

// Identity methods for the enrollment profile.
const (
	// IdentitySCEP is the default: the profile carries a SCEP payload and
	// a challenge password.
	IdentitySCEP = "scep"
	// IdentityACME issues the identity through ACME with Managed Device
	// Attestation and an issuance identifier bound to the requested assurance.
	IdentityACME = "acme"
)

// ACME policies selectable from the environment.
const (
	// ACMEPolicyAny issues to any device that produced a valid attestation
	// for a recognised client identifier.
	ACMEPolicyAny = "any"
	// ACMEPolicyDEP issues only to devices assigned to this organisation in
	// the device enrollment service.
	ACMEPolicyDEP = "dep"
	// ACMEPolicySIP additionally requires System Integrity Protection.
	ACMEPolicySIP = "sip"
)

// ErrBadACMERequest is a malformed admin request.
var ErrBadACMERequest = errors.New("app: invalid ACME request")

// ACMEConfig configures the ACME server and the identities it issues.
type ACMEConfig struct {
	// MacHardware resolves trusted inventory capabilities. Unknown leaves initial
	// profile flags explicit; secondary credentials use software keys until known.
	// T2 supports hardware keys, but only Apple silicon supports attestation.
	MacHardware func(context.Context, acme.Binding) (enroll.MacHardware, error)
	// Policy is which devices may enroll: ACMEPolicyAny, ACMEPolicyDEP, or
	// ACMEPolicySIP. Empty means ACMEPolicyAny.
	Policy string
	// AllowUnattested issues on the client identifier alone to a device
	// that produced no attestation. Off by default.
	AllowUnattested bool
	// KeyType and KeySize are what the profile asks the device to generate.
	// Empty means an attestable elliptic curve key of 384 bits.
	KeyType string
	KeySize int64
	// HMACKey mints client identifiers. Empty falls back to the SCEP HMAC
	// key, and then to a key generated at startup, which is fine for one
	// process and wrong for several.
	HMACKey []byte
	// IdentifierTTL is how long a client identifier in a profile stays
	// usable. It has to cover the gap between handing a device its profile
	// and the device acting on it.
	IdentifierTTL time.Duration
	// AnchorFile is a PEM bundle of attestation anchors, for a lab with a
	// simulated device. Empty trusts Apple alone.
	AnchorFile string
	// Anchors is the parsed AnchorFile; tests set it directly.
	Anchors []*x509.Certificate
	// Store overrides the ACME store; default follows the storage backend.
	Store acme.Store
	// NonceTTL and OrderTTL default to the acme package's values.
	NonceTTL, OrderTTL time.Duration
}

// acmeService is the ACME server and the state behind it.
type acmeService struct {
	app         *App
	store       acme.Store
	server      *acme.Server
	identifiers *acme.HMACIdentifiers
	cfg         ACMEConfig
}

// newACME builds the ACME server on the enrollment certificate authority.
func (a *App) newACME(ctx context.Context, e *enrollment) (*acmeService, error) {
	cfg := a.cfg.Enroll.ACME
	store := cfg.Store
	if store == nil {
		if a.db == nil {
			store = acmeinmem.New()
		} else {
			s, err := acmesql.Open(ctx, a.db, a.dialect, acmesql.Options{})
			if err != nil {
				return nil, fmt.Errorf("app: ACME store: %w", err)
			}
			store = s
		}
	}
	key, err := a.acmeIdentifierKey()
	if err != nil {
		return nil, err
	}
	ttl := cfg.IdentifierTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	identifiers, err := acme.NewHMACIdentifiers(key, ttl, a.cfg.Clock)
	if err != nil {
		return nil, fmt.Errorf("app: ACME identifiers: %w", err)
	}
	anchors := cfg.Anchors
	if len(anchors) == 0 && cfg.AnchorFile != "" {
		anchors, err = readCertsPEM(cfg.AnchorFile)
		if err != nil {
			return nil, fmt.Errorf("%w: ACME anchors: %w", ErrConfig, err)
		}
	}
	svc := &acmeService{app: a, store: store, identifiers: identifiers, cfg: cfg}
	policy, err := svc.policy()
	if err != nil {
		return nil, err
	}
	pure, err := ca.NewLocal(e.caCert, e.caKey, ca.WithClock(a.cfg.Clock))
	if err != nil {
		return nil, fmt.Errorf("app: ACME signer: %w", err)
	}
	server, err := acme.New(acme.Config{
		BaseURL:     e.base,
		Prefix:      PathACME,
		Store:       store,
		Signer:      pure,
		Register:    e.depot.Put,
		CAPolicy:    a.issuancePolicy(e),
		Revocations: a.acmeRevocations(),
		Identifiers: identifiers,
		Authorize: acme.Chain(
			acme.PolicyFunc(func(ctx context.Context, d *acme.Decision) error {
				if d.Binding.EnrollmentID != "" {
					return svc.authorizeCredential(ctx, d, false)
				}
				return nil
			}),
			policy,
			acme.PolicyFunc(func(ctx context.Context, d *acme.Decision) error {
				_, err := e.admit(ctx, d.Binding)
				if errors.Is(err, webauth.ErrDenied) {
					return acme.ErrUnauthorized
				}
				return err
			}),
		),
		// A device that cannot attest is worth knowing about, so the
		// default refuses it rather than quietly issuing on the strength of
		// a client identifier alone.
		AllowUnattested: cfg.AllowUnattested,
		AuthorizeUnattested: acme.PolicyFunc(func(ctx context.Context, d *acme.Decision) error {
			if d.Binding.EnrollmentID == "" {
				return acme.NewProblem(
					acme.ProblemBadAttestationStatement,
					"initial enrollment requires an attestation",
				)
			}
			return svc.authorizeCredential(ctx, d, true)
		}),
		Anchors:  anchors,
		Clock:    a.cfg.Clock,
		Bus:      a.cfg.Bus,
		Logger:   a.cfg.Logger,
		NonceTTL: cfg.NonceTTL,
		OrderTTL: cfg.OrderTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("app: ACME server: %w", err)
	}
	svc.server = server
	return svc, nil
}

// acmeIdentifierKey finds the key that mints client identifiers. Sharing
// the SCEP key is deliberate: both are the same kind of secret, held by the
// same server, and a deployment that has configured one has said what it
// means to.
func (a *App) acmeIdentifierKey() ([]byte, error) {
	if key := a.cfg.Enroll.ACME.HMACKey; len(key) >= acme.MinIdentifierKey {
		return key, nil
	}
	if key := a.cfg.Enroll.SCEPHMACKey; len(key) >= acme.MinIdentifierKey {
		return key, nil
	}
	// A generated key works for one process and fails the moment a second
	// one has to verify an identifier the first minted, so it is loud.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("app: ACME identifier key: %w", err)
	}
	a.cfg.Logger.Warn(
		"app: no ACME identifier key configured, generating one",
		"variable", EnvACMEHMACKey,
		"consequence", "profiles issued by this process cannot be used against another",
	)
	return key, nil
}

// policy turns the configured name into the rule that decides who enrolls.
func (s *acmeService) policy() (acme.Policy, error) {
	switch s.cfg.Policy {
	case "", ACMEPolicyAny:
		return acme.AllowAll(), nil
	case ACMEPolicyDEP, ACMEPolicySIP:
		// The DEP ownership policy requires a device store. Reject missing
		// configuration during Build rather than on each enrollment request.
		if s.app.dep == nil {
			return nil, fmt.Errorf(
				"%w: %s=%s needs the device enrollment service, which the %s role does not run",
				ErrConfig, EnvACMEPolicy, s.cfg.Policy, s.app.cfg.Role,
			)
		}
		if s.cfg.Policy == ACMEPolicyDEP {
			return acme.DeviceLookup(s.depLookup), nil
		}
		return acme.Chain(acme.DeviceLookup(s.depLookup), acme.RequireSIP()), nil
	default:
		return nil, fmt.Errorf(
			"%w: %s must be one of %s, %s, or %s",
			ErrConfig, EnvACMEPolicy, ACMEPolicyAny, ACMEPolicyDEP, ACMEPolicySIP,
		)
	}
}

// depLookup asks the device enrollment service store whether a serial
// number belongs to this organisation.
func (s *acmeService) depLookup(ctx context.Context, serial string) (bool, error) {
	if s.app.dep == nil {
		return false, errDEPAdmissionUnavailable
	}
	page := paging.Page{Limit: 1000}
	for {
		res, err := s.app.dep.store.ListAccounts(ctx, page)
		if err != nil {
			return false, fmt.Errorf("app: DEP accounts: %w", err)
		}
		for _, account := range res.Items {
			d, err := s.app.dep.store.GetDevice(ctx, account.Name, serial)
			if errors.Is(err, dep.ErrNotFound) {
				continue
			}
			if err != nil {
				return false, fmt.Errorf("app: DEP device ownership: %w", err)
			}
			if admittedDEPDevice(d, &account) {
				return true, nil
			}
		}
		if res.NextCursor == "" {
			return false, nil
		}
		page.Cursor = res.NextCursor
	}
}

// deviceBinding keeps MDM admission separate from hardware attestation.
// Initial Mac enrollment supplies no ProvisioningUDID, so its serial number
// is the hardware binding. Unknown products also require a serial number.
func deviceBinding(udid, serial, product, cn string) acme.Binding {
	b := acme.Binding{MDMUDID: udid, Serial: serial, CommonName: cn}
	if os := support.OSFromProduct(product); os != "" && os != support.MacOS {
		b.UDID = udid
	}
	return b
}

// acmePayload builds the ACME enrollment payload for one device.
func (s *acmeService) acmePayload(
	b acme.Binding,
	directoryURL string,
	targets ...acmeTarget,
) (*enroll.ACME, error) {
	if !b.Identified() && !b.AllowUnidentified {
		return nil, fmt.Errorf(
			"%w: ACME needs a serial number or attestation UDID",
			ErrBadACMERequest,
		)
	}
	keyType, keySize := s.cfg.KeyType, s.cfg.KeySize
	if keyType == "" {
		// An attestable key: Apple only attests hardware bound elliptic
		// curve keys, and only at 256 or 384 bits.
		keyType, keySize = enroll.KeyTypeEC, 384
	}
	attested := keyType == enroll.KeyTypeEC && (keySize == 256 || keySize == 384)
	// Apple requires a Subject in the payload even though the server may
	// override it, so the device is told the name it should expect and the
	// server issues that same name.
	subject := pkix.Name{CommonName: b.CommonName, Organization: b.Organization}
	if subject.CommonName == "" {
		subject.CommonName = b.Serial
	}
	if subject.CommonName == "" {
		// A user channel has no hardware of its own, so the enrollment it
		// belongs to is the most specific name there is. Apple requires a
		// Subject, so leaving it empty would produce a payload the device
		// refuses.
		subject.CommonName = b.EnrollmentID
	}
	payload := &enroll.ACME{
		DirectoryURL:     directoryURL,
		ClientIdentifier: "pending",
		KeyType:          keyType,
		KeySize:          keySize,
		HardwareBound:    attested,
		Attest:           attested,
		Subject:          subject,
	}
	if len(targets) > 0 {
		if err := targets[0].apply(payload); err != nil {
			return nil, err
		}
	}
	b.RequireAttestation = payload.Attest
	identifier, err := s.identifiers.Issue(b)
	if err != nil {
		return nil, fmt.Errorf("app: ACME identifier: %w", err)
	}
	payload.ClientIdentifier = identifier
	return payload, nil
}

// credentialHandler serves the com.apple.credential.acme document that a
// declarative com.apple.asset.credential.acme asset points at, so a
// security.identity declaration can give an enrolled device a second
// identity through ACME.
//
// The asset is fetched with MDM authentication, which means the device
// presents its existing identity certificate. That certificate is how the
// server knows which device is asking, and therefore which device the
// client identifier it mints should be bound to.
func (s *acmeService) credentialHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		cert := httpapi.CertFromContext(r.Context())
		if cert == nil {
			writeError(
				w,
				http.StatusUnauthorized,
				fmt.Errorf("%w: the request carries no device identity", ErrBadACMERequest),
			)
			return
		}
		id, err := s.app.Store.EnrollmentByCertHash(r.Context(), cms.Fingerprint(cert))
		if err != nil {
			writeError(
				w,
				http.StatusUnauthorized,
				fmt.Errorf("%w: the certificate is not an enrolled device", ErrBadACMERequest),
			)
			return
		}
		enrollment, err := s.app.Store.Get(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !enrollment.Enabled || !enrollment.DisabledAt.IsZero() {
			writeError(w, http.StatusForbidden, storage.ErrDisabled)
			return
		}
		target := support.Target{OS: support.OSFromProduct(enrollment.Device.ProductName)}
		version, versionErr := support.ParseVersion(enrollment.Device.OSVersion)
		if target.OS == "" || versionErr != nil {
			writeError(
				w,
				http.StatusForbidden,
				fmt.Errorf(
					"%w: credential requires a known device platform and version",
					ErrBadACMERequest,
				),
			)
			return
		}
		target.Version = version
		binding := acme.Binding{
			Serial:       enrollment.Device.SerialNumber,
			EnrollmentID: string(id.ID),
			MDMUDID:      id.ID,
			CommonName:   cert.Subject.CommonName,
			// A user channel has no hardware of its own to attest.
			AllowUnidentified: enrollment.Device.SerialNumber == "",
		}
		hardware := enroll.MacHardwareUnknown
		if enrollment.Capabilities.AppleSilicon == storage.CapabilityTrue {
			hardware = enroll.MacAppleSilicon
		}
		if target.OS == support.MacOS {
			hardware, err = s.resolveMac(r.Context(), binding, hardware)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		payload, err := s.acmePayload(
			binding,
			s.server.DirectoryURL(),
			acmeTarget{target: target, hardware: hardware, credential: true},
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		credential := ddm.ACMECredential{
			DirectoryURL:     payload.DirectoryURL,
			ClientIdentifier: payload.ClientIdentifier,
			KeyType:          payload.KeyType,
			KeySize:          payload.KeySize,
			HardwareBound:    payload.HardwareBound,
			Subject:          enroll.SubjectFromName(payload.Subject),
		}
		credential.Attest = new(payload.Attest)
		if err := credential.Validate(target); err != nil {
			writeError(
				w,
				http.StatusForbidden,
				fmt.Errorf("%w: unsupported credential: %w", ErrBadACMERequest, err),
			)
			return
		}
		if err := s.recordCredentialGrant(
			r.Context(),
			credential.ClientIdentifier,
			id,
			cert,
			payload.Attest,
		); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, credential)
	})
}

// handler exposes ACME certificate and order metadata under the admin prefix:
//
//	GET /acme/certificates?serial=&udid=&account=&cursor=
//	GET /acme/orders?account=&cursor=
//
// Certificate records include properties verified during attestation.
func (s *acmeService) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /acme/certificates", func(w http.ResponseWriter, r *http.Request) {
		q := acme.CertificateQuery{
			DeviceSerial: r.URL.Query().Get("serial"),
			UDID:         r.URL.Query().Get("udid"),
			AccountID:    r.URL.Query().Get("account"),
		}
		page := paging.Page{Cursor: r.URL.Query().Get("cursor")}
		if v := r.URL.Query().Get("limit"); v != "" {
			_, _ = fmt.Sscanf(v, "%d", &page.Limit)
		}
		res, err := s.store.ListCertificates(r.Context(), q, page)
		if err != nil {
			writeError(w, acmeStatus(err), err)
			return
		}
		type row struct {
			ID, Serial, Identifier string
			Device                 attest.Properties
			NotAfter, IssuedAt     time.Time
			PendingRegistration    bool
		}
		rows := make([]row, 0, len(res.Items))
		for _, c := range res.Items {
			rows = append(rows, row{
				ID: c.ID, Serial: c.Serial, Identifier: c.Binding.EnrollmentID,
				Device: c.Device, NotAfter: c.NotAfter, IssuedAt: c.IssuedAt,
				PendingRegistration: c.PendingRegistration,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"Items": rows, "NextCursor": res.NextCursor})
	})
	mux.HandleFunc("GET /acme/orders", func(w http.ResponseWriter, r *http.Request) {
		account := r.URL.Query().Get("account")
		if account == "" {
			writeError(
				w,
				http.StatusBadRequest,
				fmt.Errorf("%w: an account is required", ErrBadACMERequest),
			)
			return
		}
		res, err := s.store.ListOrders(
			r.Context(), account, paging.Page{Cursor: r.URL.Query().Get("cursor")},
		)
		if err != nil {
			writeError(w, acmeStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	return mux
}

// acmeStatus maps store errors to admin API statuses.
func acmeStatus(err error) int {
	switch {
	case errors.Is(err, acme.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, acme.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, acme.ErrInvalid), errors.Is(err, ErrBadACMERequest):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// readACMEKey reads an HMAC key given directly or as a file path, the same
// way the SCEP HMAC key is configured.
func readACMEKey(v string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	if strings.HasPrefix(v, "@") {
		data, err := os.ReadFile(
			v[1:],
		) // #nosec G304 -- an operator-supplied path from the configuration
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrConfig, EnvACMEHMACKey, err)
		}
		return data, nil
	}
	return []byte(v), nil
}

// ACMEStoreForTests exposes the ACME store to tests of the wiring.
func (a *App) ACMEStoreForTests() acme.Store { return a.acme.store }

// ACMEIdentifierForTests mints a client identifier, so a test can order
// without composing a whole enrollment profile.
func (a *App) ACMEIdentifierForTests(b acme.Binding) (string, error) {
	id, err := a.acme.identifiers.Issue(b)
	if err != nil {
		return "", fmt.Errorf("app: ACME identifier: %w", err)
	}
	return id, nil
}

// ACMEStatusForTests exposes the error mapping to tests of the wiring.
func ACMEStatusForTests(err error) int { return acmeStatus(err) }

// ACMEKeyFromEnvForTests exposes the key reader to tests of the wiring.
func ACMEKeyFromEnvForTests(v string) ([]byte, error) { return readACMEKey(v) }
