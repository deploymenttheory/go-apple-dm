package acme

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/ca"
	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// directoryBody is RFC 8555 section 7.1.1. The absent members are the
// endpoints this server does not implement: a device never revokes a
// certificate or rolls an account key, and advertising an endpoint that
// answers nothing helps nobody.
type directoryBody struct {
	NewNonce   string        `json:"newNonce"`
	NewAccount string        `json:"newAccount"`
	NewOrder   string        `json:"newOrder"`
	Meta       directoryMeta `json:"meta"`
}

type directoryMeta struct {
	Website string `json:"website,omitempty"`
}

func (s *Server) directory(e *exchange) error {
	return s.write(e, http.StatusOK, directoryBody{
		NewNonce:   s.url(pathNewNonce),
		NewAccount: s.url(pathNewAccount),
		NewOrder:   s.url(pathNewOrder),
	})
}

func (s *Server) newNonce(e *exchange) error {
	// The nonce is already on the response. RFC 8555 section 7.2 wants 200
	// for HEAD and 204 for GET.
	status := http.StatusNoContent
	if e.r.Method == http.MethodHead {
		status = http.StatusOK
	}
	e.w.WriteHeader(status)
	return nil
}

// accountRequest is the new-account payload.
type accountRequest struct {
	Contact              []string `json:"contact,omitempty"`
	TermsOfServiceAgreed bool     `json:"termsOfServiceAgreed,omitempty"`
	OnlyReturnExisting   bool     `json:"onlyReturnExisting,omitempty"`
}

// accountBody is an account as RFC 8555 section 7.1.2 renders it.
type accountBody struct {
	Status  string   `json:"status"`
	Contact []string `json:"contact,omitempty"`
	Orders  string   `json:"orders"`
}

func (s *Server) newAccount(e *exchange) error {
	var req accountRequest
	if err := e.decode(&req); err != nil {
		return err
	}
	thumbprint, err := e.jws.Header.JWK.Thumbprint()
	if err != nil {
		return WrapProblem(ProblemBadPublicKey, err, "the account key cannot be identified")
	}
	// A key is an account. A client that has registered before and asks
	// again gets the account it already has, which is what lets a device
	// that lost its state carry on.
	existing, err := s.cfg.Store.AccountByThumbprint(e.ctx(), thumbprint)
	switch {
	case err == nil:
		e.w.Header().Set("Location", s.url(pathAccount, existing.ID))
		return s.write(e, http.StatusOK, s.accountBody(existing))
	case !errors.Is(err, ErrNotFound):
		return WrapProblem(ProblemServerInternal, err, "the account could not be read")
	case req.OnlyReturnExisting:
		return NewProblem(ProblemAccountDoesNotExist, "no account exists for this key")
	}
	id, err := newID()
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the account could not be created")
	}
	account := &Account{
		ID:         id,
		Thumbprint: thumbprint,
		Key:        e.jws.Header.JWK,
		Status:     StatusValid,
		Contact:    req.Contact,
		CreatedAt:  s.cfg.Clock.Now(),
	}
	err = s.cfg.Store.Update(e.ctx(), func(tx Tx) error { return tx.PutAccount(e.ctx(), account) })
	if errors.Is(err, ErrConflict) {
		// Two registrations of one key raced. The other won, and its
		// account is the answer.
		existing, err := s.cfg.Store.AccountByThumbprint(e.ctx(), thumbprint)
		if err != nil {
			return WrapProblem(ProblemServerInternal, err, "the account could not be read")
		}
		e.w.Header().Set("Location", s.url(pathAccount, existing.ID))
		return s.write(e, http.StatusOK, s.accountBody(existing))
	}
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the account could not be stored")
	}
	e.w.Header().Set("Location", s.url(pathAccount, account.ID))
	return s.write(e, http.StatusCreated, s.accountBody(account))
}

func (s *Server) accountBody(a *Account) accountBody {
	return accountBody{
		Status:  a.Status,
		Contact: a.Contact,
		Orders:  s.url(pathAccount, a.ID, "/orders"),
	}
}

// account answers a POST-as-GET for the account itself. Updates are not
// supported: Apple's client does not make them.
func (s *Server) account(e *exchange) error {
	if e.r.PathValue("id") != e.account.ID {
		return NewProblem(ProblemUnauthorized, "the account may only read itself")
	}
	if !e.jws.PayloadIsEmpty() {
		return NewProblem(ProblemMalformed, "account updates are not supported")
	}
	return s.write(e, http.StatusOK, s.accountBody(e.account))
}

// ordersBody is RFC 8555 section 7.1.2.1.
type ordersBody struct {
	Orders []string `json:"orders"`
}

func (s *Server) accountOrders(e *exchange) error {
	if e.r.PathValue("id") != e.account.ID {
		return NewProblem(ProblemUnauthorized, "the account may only read its own orders")
	}
	page := storage.Page{Cursor: e.r.URL.Query().Get("cursor")}
	res, err := s.cfg.Store.ListOrders(e.ctx(), e.account.ID, page)
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the orders could not be read")
	}
	body := ordersBody{Orders: make([]string, 0, len(res.Items))}
	for _, o := range res.Items {
		body.Orders = append(body.Orders, s.url(pathOrder, o.ID))
	}
	if res.NextCursor != "" {
		next := s.url(pathAccount, e.account.ID, "/orders") + "?cursor=" + res.NextCursor
		e.w.Header().Add("Link", `<`+next+`>;rel="next"`)
	}
	return s.write(e, http.StatusOK, body)
}

// orderRequest is the new-order payload.
type orderRequest struct {
	Identifiers []Identifier `json:"identifiers"`
	NotBefore   string       `json:"notBefore,omitempty"`
	NotAfter    string       `json:"notAfter,omitempty"`
}

// orderBody is an order as RFC 8555 section 7.1.3 renders it.
type orderBody struct {
	Status         string       `json:"status"`
	Expires        string       `json:"expires"`
	Identifiers    []Identifier `json:"identifiers"`
	Authorizations []string     `json:"authorizations"`
	Finalize       string       `json:"finalize"`
	Certificate    string       `json:"certificate,omitempty"`
	Error          *Problem     `json:"error,omitempty"`
}

func (s *Server) newOrder(e *exchange) error {
	var req orderRequest
	if err := e.decode(&req); err != nil {
		return err
	}
	// Apple orders one permanent-identifier. Any other shape would produce
	// an authorization with no challenge that nothing could satisfy, so it
	// is refused rather than accepted and left to rot.
	if len(req.Identifiers) != 1 {
		return NewProblem(
			ProblemMalformed, "an order must carry exactly one identifier, got %d", len(req.Identifiers),
		)
	}
	id := req.Identifiers[0]
	if id.Type != IdentifierPermanent {
		return NewProblem(
			ProblemUnsupportedIdentifier, "identifiers of type %q are not issued for", id.Type,
		).WithSubproblem(ProblemUnsupportedIdentifier, "unsupported type", id)
	}
	if id.Value == "" {
		return NewProblem(ProblemMalformed, "the identifier has no value")
	}
	if req.NotBefore != "" || req.NotAfter != "" {
		// The validity of a device identity is the server's decision.
		return NewProblem(ProblemMalformed, "notBefore and notAfter are not accepted")
	}
	binding, err := s.cfg.Identifiers.Verify(e.ctx(), id.Value)
	if err != nil {
		return err
	}
	now := s.cfg.Clock.Now()
	expires := now.Add(s.cfg.OrderTTL)
	orderID, err := newID()
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the order could not be created")
	}
	authzID, err := newID()
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the order could not be created")
	}
	challengeID, err := newID()
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the order could not be created")
	}
	token, err := newToken()
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the order could not be created")
	}
	order := &Order{
		ID: orderID, AccountID: e.account.ID, Identifier: id, Binding: binding,
		Status: StatusPending, AuthzID: authzID, Expires: expires, CreatedAt: now,
	}
	authz := &Authorization{
		ID: authzID, OrderID: orderID, AccountID: e.account.ID, Identifier: id,
		Status: StatusPending, ChallengeID: challengeID, Expires: expires,
	}
	challenge := &Challenge{
		ID: challengeID, AuthzID: authzID, AccountID: e.account.ID,
		Type: ChallengeDeviceAttest, Token: token, Status: StatusPending,
	}
	// The claim and the three records go in together, so two orders racing
	// for one client identifier cannot both leave an order behind.
	err = s.cfg.Store.Update(e.ctx(), func(tx Tx) error {
		if err := tx.ClaimIdentifier(e.ctx(), id.Value, orderID); err != nil {
			return err
		}
		if err := tx.PutChallenge(e.ctx(), challenge); err != nil {
			return err
		}
		if err := tx.PutAuthorization(e.ctx(), authz); err != nil {
			return err
		}
		return tx.PutOrder(e.ctx(), order)
	})
	if errors.Is(err, ErrConflict) {
		// Apple calls the ClientIdentifier an anti-replay code, and this is
		// where that means something: it buys exactly one certificate.
		return WrapProblem(
			ProblemRejectedIdentifier, err, "the identifier has already been used",
		).WithSubproblem(ProblemRejectedIdentifier, "already used", id)
	}
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the order could not be stored")
	}
	e.w.Header().Set("Location", s.url(pathOrder, order.ID))
	return s.write(e, http.StatusCreated, s.orderBody(order))
}

func (s *Server) orderBody(o *Order) orderBody {
	body := orderBody{
		Status:         o.Status,
		Expires:        o.Expires.UTC().Format(time.RFC3339),
		Identifiers:    []Identifier{o.Identifier},
		Authorizations: []string{s.url(pathAuthz, o.AuthzID)},
		Finalize:       s.url(pathOrder, o.ID, "/finalize"),
		Error:          o.Error,
	}
	if o.CertificateID != "" {
		body.Certificate = s.url(pathCert, o.CertificateID)
	}
	return body
}

// order answers a POST-as-GET for an order.
func (s *Server) order(e *exchange) error {
	o, err := s.loadOrder(e)
	if err != nil {
		return err
	}
	return s.write(e, http.StatusOK, s.orderBody(o))
}

// authorizationBody is RFC 8555 section 7.1.4.
type authorizationBody struct {
	Status     string          `json:"status"`
	Expires    string          `json:"expires"`
	Identifier Identifier      `json:"identifier"`
	Challenges []challengeBody `json:"challenges"`
}

// challengeBody is RFC 8555 section 7.1.5 with the device-attest-01 token.
type challengeBody struct {
	Type      string   `json:"type"`
	URL       string   `json:"url"`
	Status    string   `json:"status"`
	Token     string   `json:"token"`
	Validated string   `json:"validated,omitempty"`
	Error     *Problem `json:"error,omitempty"`
}

func (s *Server) authorization(e *exchange) error {
	authz, err := s.cfg.Store.GetAuthorization(e.ctx(), e.r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		return NewProblem(ProblemMalformed, "no such authorization")
	}
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the authorization could not be read")
	}
	if authz.AccountID != e.account.ID {
		return NewProblem(ProblemUnauthorized, "the authorization belongs to another account")
	}
	challenge, err := s.cfg.Store.GetChallenge(e.ctx(), authz.ChallengeID)
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the challenge could not be read")
	}
	return s.write(e, http.StatusOK, authorizationBody{
		Status:     s.authzStatus(authz),
		Expires:    authz.Expires.UTC().Format(time.RFC3339),
		Identifier: authz.Identifier,
		Challenges: []challengeBody{s.challengeBody(challenge)},
	})
}

// authzStatus reports expiry without needing a sweeper to have run.
func (s *Server) authzStatus(a *Authorization) string {
	if a.Status == StatusPending && s.cfg.Clock.Now().After(a.Expires) {
		return StatusExpired
	}
	return a.Status
}

func (s *Server) challengeBody(c *Challenge) challengeBody {
	body := challengeBody{
		Type:   c.Type,
		URL:    s.url(pathChallenge, c.ID),
		Status: c.Status,
		Token:  c.Token,
		Error:  c.Error,
	}
	if !c.ValidatedAt.IsZero() {
		body.Validated = c.ValidatedAt.UTC().Format(time.RFC3339)
	}
	return body
}

// challengeRequest is the device-attest-01 response of the ACME device
// attestation draft.
type challengeRequest struct {
	AttObj string `json:"attObj"`
}

func (s *Server) challenge(e *exchange) error {
	challenge, err := s.cfg.Store.GetChallenge(e.ctx(), e.r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		return NewProblem(ProblemMalformed, "no such challenge")
	}
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the challenge could not be read")
	}
	if challenge.AccountID != e.account.ID {
		return NewProblem(ProblemUnauthorized, "the challenge belongs to another account")
	}
	// A repeated post, or a POST-as-GET, reports where things stand rather
	// than validating again.
	if e.jws.PayloadIsEmpty() || challenge.Status != StatusPending {
		return s.write(e, http.StatusOK, s.challengeBody(challenge))
	}
	authz, err := s.cfg.Store.GetAuthorization(e.ctx(), challenge.AuthzID)
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the authorization could not be read")
	}
	order, err := s.cfg.Store.GetOrder(e.ctx(), authz.OrderID)
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the order could not be read")
	}
	if s.cfg.Clock.Now().After(authz.Expires) {
		return NewProblem(ProblemMalformed, "the authorization has expired")
	}
	raw, err := s.attestationFrom(e)
	if err != nil {
		return s.settleChallenge(e, challenge, authz, order, err)
	}
	if err := s.validate(e, challenge, order, raw); err != nil {
		return s.settleChallenge(e, challenge, authz, order, err)
	}
	now := s.cfg.Clock.Now()
	challenge.Status = StatusValid
	challenge.ValidatedAt = now
	challenge.Attestation = raw
	challenge.Error = nil
	authz.Status = StatusValid
	order.Status = StatusReady
	if err := s.saveTriple(e, challenge, authz, order); err != nil {
		return err
	}
	s.publish(e.ctx(), event.ACMEChallengeValid, map[string]any{
		"identifier": order.Identifier.Value,
		"serial":     order.Binding.Serial,
	})
	return s.write(e, http.StatusOK, s.challengeBody(challenge))
}

// attestationFrom reads the attestation object out of the challenge
// response.
func (s *Server) attestationFrom(e *exchange) ([]byte, error) {
	var req challengeRequest
	if err := e.decode(&req); err != nil {
		return nil, err
	}
	if req.AttObj == "" {
		return nil, NewProblem(ProblemMalformed, "the challenge response carries no attestation object")
	}
	raw, err := base64.RawURLEncoding.DecodeString(req.AttObj)
	if err != nil {
		return nil, WrapProblem(
			ProblemMalformed, err, "the attestation object is not base64url",
		)
	}
	return raw, nil
}

// validate is the whole of the trust decision: the attestation is genuine,
// it answers this challenge, it describes the device the identifier was
// issued for, and the policy accepts that device.
func (s *Server) validate(e *exchange, c *Challenge, o *Order, raw []byte) error {
	a, err := attest.ParseObject(raw)
	switch {
	case errors.Is(err, attest.ErrNoAttestation):
		// The device produced no attestation. Apple does this when the
		// profile did not ask for one or the hardware cannot. Whether that
		// is acceptable is a deployment's decision, not a protocol one.
		if !s.cfg.AllowUnattested {
			return WrapProblem(
				ProblemBadAttestationStatement, err,
				"this server requires an attestation, and the device sent none",
			)
		}
		return s.authorize(e, o, nil)
	case err != nil:
		return WrapProblem(
			ProblemBadAttestationStatement, err, "the attestation object could not be read",
		)
	}
	err = a.Verify(attest.VerifyOptions{
		Anchors:   s.cfg.Anchors,
		Now:       s.cfg.Clock.Now,
		Freshness: attest.FreshnessForToken(c.Token),
	})
	if err != nil {
		s.publish(e.ctx(), event.AttestationRejected, map[string]any{
			"identifier": o.Identifier.Value,
			"reason":     err.Error(),
		})
		return WrapProblem(
			ProblemBadAttestationStatement, err, "the attestation does not verify",
		)
	}
	if err := s.matchBinding(o, a); err != nil {
		s.publish(e.ctx(), event.AttestationRejected, map[string]any{
			"identifier": o.Identifier.Value,
			"reason":     err.Error(),
		})
		return err
	}
	return s.authorize(e, o, a)
}

// matchBinding compares the device the identifier was issued for with the
// device Apple attested. An identifier that has been intercepted is not
// enough on its own: it has to be presented by the device it names.
func (s *Server) matchBinding(o *Order, a *attest.Attestation) error {
	p := a.Properties
	b := o.Binding
	mismatch := func(what, want, got string) error {
		return NewProblem(
			ProblemBadAttestationStatement,
			"the identifier was issued for %s %s, and the attestation describes %q",
			what, want, got,
		).WithSubproblem(ProblemRejectedIdentifier, "wrong device", o.Identifier)
	}
	if b.Serial != "" && b.Serial != p.SerialNumber {
		return mismatch("serial number", b.Serial, p.SerialNumber)
	}
	if b.UDID != "" && b.UDID != p.UDID {
		return mismatch("UDID", b.UDID, p.UDID)
	}
	if !b.Identified() && !p.Identified() && !b.AllowUnidentified {
		// A user enrollment attests to genuine hardware without naming it.
		// Accepting one is a deliberate setting, because there is then
		// nothing to tie the certificate to a particular device.
		return NewProblem(
			ProblemBadAttestationStatement,
			"the attestation names no device, and this identifier requires one",
		)
	}
	return nil
}

// authorize runs the deployment's policy.
func (s *Server) authorize(e *exchange, o *Order, a *attest.Attestation) error {
	d := &Decision{
		Account:     e.account,
		Order:       o,
		Binding:     o.Binding,
		Identifier:  o.Identifier,
		Attestation: a,
	}
	if err := s.cfg.Authorize.Authorize(e.ctx(), d); err != nil {
		p := AsProblem(err)
		if p.Terminal() {
			s.publish(e.ctx(), event.AttestationRejected, map[string]any{
				"identifier": o.Identifier.Value,
				"reason":     p.Detail,
			})
		}
		return p
	}
	return nil
}

// settleChallenge records a failed validation. A fault that is the client's
// settles the challenge, its authorization, and its order invalid, because
// repeating the request would fail the same way. A fault that is ours
// leaves everything pending so a retry can succeed.
func (s *Server) settleChallenge(
	e *exchange,
	c *Challenge,
	a *Authorization,
	o *Order,
	cause error,
) error {
	p := AsProblem(cause)
	if !p.Terminal() {
		return p
	}
	c.Status = StatusInvalid
	c.Error = p
	a.Status = StatusInvalid
	o.Status = StatusInvalid
	o.Error = p
	if err := s.saveTriple(e, c, a, o); err != nil {
		return err
	}
	return p
}

func (s *Server) saveTriple(e *exchange, c *Challenge, a *Authorization, o *Order) error {
	err := s.cfg.Store.Update(e.ctx(), func(tx Tx) error {
		if err := tx.PutChallenge(e.ctx(), c); err != nil {
			return err
		}
		if err := tx.PutAuthorization(e.ctx(), a); err != nil {
			return err
		}
		return tx.PutOrder(e.ctx(), o)
	})
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the challenge could not be stored")
	}
	return nil
}

// finalizeRequest is RFC 8555 section 7.4.
type finalizeRequest struct {
	CSR string `json:"csr"`
}

func (s *Server) finalize(e *exchange) error {
	o, err := s.loadOrder(e)
	if err != nil {
		return err
	}
	switch o.Status {
	case StatusValid:
		// Already issued: RFC 8555 wants the order back, not a second
		// certificate.
		return s.write(e, http.StatusOK, s.orderBody(o))
	case StatusInvalid:
		return NewProblem(ProblemOrderNotReady, "the order failed and cannot be finalized")
	case StatusPending:
		return NewProblem(ProblemOrderNotReady, "the authorization is not yet valid")
	case StatusReady:
	default:
		return NewProblem(ProblemOrderNotReady, "the order is %s", o.Status)
	}
	if s.cfg.Clock.Now().After(o.Expires) {
		return NewProblem(ProblemOrderNotReady, "the order has expired")
	}
	var req finalizeRequest
	if err := e.decode(&req); err != nil {
		return err
	}
	csr, err := parseCSR(req.CSR)
	if err != nil {
		return s.settleOrder(e, o, err)
	}
	if err := s.checkAttestedKey(e, o, csr); err != nil {
		return s.settleOrder(e, o, err)
	}
	cert, err := s.issue(e, o, csr)
	if err != nil {
		return s.settleOrder(e, o, err)
	}
	e.w.Header().Set("Location", s.url(pathOrder, o.ID))
	return s.write(e, http.StatusOK, s.orderBody(cert.order))
}

// parseCSR reads and checks the certificate request.
func parseCSR(encoded string) (*x509.CertificateRequest, error) {
	if encoded == "" {
		return nil, NewProblem(ProblemBadCSR, "the request carries no certificate request")
	}
	der, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, WrapProblem(ProblemBadCSR, err, "the certificate request is not base64url")
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, WrapProblem(ProblemBadCSR, err, "the certificate request could not be parsed")
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, WrapProblem(
			ProblemBadCSR, err, "the certificate request is not signed by its own key",
		)
	}
	return csr, nil
}

// checkAttestedKey is the binding Apple's guidance asks for: the key in the
// certificate request must be the key the attestation covered. The stored
// attestation is verified again from its original bytes, because the key
// was not known when the challenge was answered and a decoded copy would
// not prove anything.
func (s *Server) checkAttestedKey(e *exchange, o *Order, csr *x509.CertificateRequest) error {
	challenge, err := s.challengeOf(e, o)
	if err != nil {
		return err
	}
	a, err := attest.ParseObject(challenge.Attestation)
	if len(challenge.Attestation) == 0 || errors.Is(err, attest.ErrNoAttestation) {
		// The challenge was answered without an attestation, which only
		// reached this point because the deployment allows it. There is no
		// attested key, so there is nothing to bind the request's key to.
		if !s.cfg.AllowUnattested {
			return NewProblem(
				ProblemUnauthorized, "the order was authorized without an attestation",
			)
		}
		return nil
	}
	if err != nil {
		return WrapProblem(
			ProblemServerInternal, err, "the stored attestation could not be read",
		)
	}
	err = a.Verify(attest.VerifyOptions{
		Anchors:   s.cfg.Anchors,
		Now:       s.cfg.Clock.Now,
		Freshness: attest.FreshnessForToken(challenge.Token),
		PublicKey: csr.PublicKey,
	})
	if errors.Is(err, attest.ErrKeyMismatch) {
		return WrapProblem(
			ProblemBadCSR, err,
			"the certificate request is for a different key than the one attested",
		)
	}
	if err != nil {
		return WrapProblem(
			ProblemBadAttestationStatement, err, "the attestation no longer verifies",
		)
	}
	return nil
}

func (s *Server) challengeOf(e *exchange, o *Order) (*Challenge, error) {
	authz, err := s.cfg.Store.GetAuthorization(e.ctx(), o.AuthzID)
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the authorization could not be read")
	}
	challenge, err := s.cfg.Store.GetChallenge(e.ctx(), authz.ChallengeID)
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the challenge could not be read")
	}
	return challenge, nil
}

// issued carries what issuance produced.
type issued struct {
	order *Order
	cert  *Certificate
}

// issue signs the certificate and records it. The subject and the subject
// alternative name come from the binding the server decided at order time,
// never from the request: Apple's documentation says the server may
// override the Subject the profile asked for, and a subject a device chose
// for itself is not evidence of anything.
func (s *Server) issue(e *exchange, o *Order, csr *x509.CertificateRequest) (*issued, error) {
	otherName, err := ca.PermanentIdentifier(o.Identifier.Value)
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the identifier could not be encoded")
	}
	policy := s.cfg.CAPolicy
	policy.OtherNames = append(append([]ca.OtherName(nil), policy.OtherNames...), otherName)
	subject := pkix.Name{
		CommonName:   o.Binding.CommonName,
		Organization: o.Binding.Organization,
	}
	if subject.CommonName == "" {
		subject.CommonName = o.Binding.Serial
	}
	if subject.CommonName == "" {
		subject.CommonName = o.Identifier.Value
	}
	policy.Subject = &subject
	// The binding's deadline is absolute, so it is handed to the authority
	// as one. Turning it into a duration here would let the certificate
	// outlive it by however long signing took.
	if !o.Binding.NotAfter.IsZero() {
		if !o.Binding.NotAfter.After(s.cfg.Clock.Now()) {
			return nil, NewProblem(
				ProblemRejectedIdentifier,
				"the identifier's certificate deadline has already passed",
			)
		}
		policy.NotAfter = o.Binding.NotAfter
	}
	cert, err := s.cfg.Signer.Sign(e.ctx(), csr, policy)
	if err != nil {
		if errors.Is(err, ca.ErrPolicy) || errors.Is(err, ca.ErrCSR) {
			return nil, WrapProblem(ProblemBadCSR, err, "the certificate request was refused")
		}
		return nil, WrapProblem(ProblemServerInternal, err, "the certificate could not be signed")
	}
	chain := encodeChain(cert, s.cfg.Signer.Chain())
	challenge, err := s.challengeOf(e, o)
	if err != nil {
		return nil, err
	}
	var device attest.Properties
	if len(challenge.Attestation) > 0 {
		if a, err := attest.ParseObject(challenge.Attestation); err == nil {
			device = a.Properties
		}
	}
	certID, err := newID()
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the certificate could not be recorded")
	}
	record := &Certificate{
		ID: certID, OrderID: o.ID, AccountID: o.AccountID,
		Serial: cert.SerialNumber.String(), ChainPEM: chain, Device: device,
		Binding: o.Binding, NotAfter: cert.NotAfter, IssuedAt: s.cfg.Clock.Now(),
	}
	o.Status = StatusValid
	o.CertificateID = certID
	o.Error = nil
	err = s.cfg.Store.Update(e.ctx(), func(tx Tx) error {
		if err := tx.PutCertificate(e.ctx(), record); err != nil {
			return err
		}
		return tx.PutOrder(e.ctx(), o)
	})
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the certificate could not be stored")
	}
	s.publish(e.ctx(), event.ACMEIssued, map[string]any{
		"serial":      record.Serial,
		"identifier":  o.Identifier.Value,
		"device":      device.SerialNumber,
		"udid":        device.UDID,
		"enrollment":  o.Binding.EnrollmentID,
		"not_after":   record.NotAfter,
		"certificate": certID,
	})
	return &issued{order: o, cert: record}, nil
}

func encodeChain(leaf *x509.Certificate, issuers []*x509.Certificate) []byte {
	var out []byte
	for _, c := range append([]*x509.Certificate{leaf}, issuers...) {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return out
}

// settleOrder records a failed finalize. A bad certificate request leaves
// the order ready, as RFC 8555 section 7.4 requires, so an amended request
// can still finalize it; anything else terminal makes the order invalid.
func (s *Server) settleOrder(e *exchange, o *Order, cause error) error {
	p := AsProblem(cause)
	if !p.Terminal() || errors.Is(p, ErrBadCSR) {
		return p
	}
	o.Status = StatusInvalid
	o.Error = p
	if err := s.cfg.Store.Update(e.ctx(), func(tx Tx) error { return tx.PutOrder(e.ctx(), o) }); err != nil {
		return WrapProblem(ProblemServerInternal, err, "the order could not be stored")
	}
	return p
}

func (s *Server) certificate(e *exchange) error {
	record, err := s.cfg.Store.GetCertificate(e.ctx(), e.r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		return NewProblem(ProblemMalformed, "no such certificate")
	}
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the certificate could not be read")
	}
	if record.AccountID != e.account.ID {
		return NewProblem(ProblemUnauthorized, "the certificate belongs to another account")
	}
	e.w.Header().Set("Content-Type", ContentTypePEMChain)
	e.w.Header().Set("X-Content-Type-Options", "nosniff")
	e.w.WriteHeader(http.StatusOK)
	_, _ = e.w.Write(record.ChainPEM)
	return nil
}

// loadOrder reads the order named in the path and checks it belongs to the
// account that signed the request.
func (s *Server) loadOrder(e *exchange) (*Order, error) {
	o, err := s.cfg.Store.GetOrder(e.ctx(), e.r.PathValue("id"))
	if errors.Is(err, ErrNotFound) {
		return nil, NewProblem(ProblemMalformed, "no such order")
	}
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the order could not be read")
	}
	if o.AccountID != e.account.ID {
		return nil, NewProblem(ProblemUnauthorized, "the order belongs to another account")
	}
	return o, nil
}

// decode reads the request payload. An empty payload is a POST-as-GET,
// which no endpoint that calls this accepts.
func (e *exchange) decode(v any) error {
	if e.jws.PayloadIsEmpty() {
		return NewProblem(ProblemMalformed, "the request carries no payload")
	}
	if err := json.Unmarshal(e.payload, v); err != nil {
		// The decoder's message can quote the payload, so it goes to the
		// log through the wrapped cause rather than to the device.
		return WrapProblem(ProblemMalformed, err, "the payload could not be read")
	}
	return nil
}
