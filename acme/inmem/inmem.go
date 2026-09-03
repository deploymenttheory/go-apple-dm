package inmem

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// Store implements acme.Store in memory.
//
// One mutex guards everything. Update holds it for the whole callback, so
// a callback that reaches back into the same Store deadlocks; a callback
// is given a Tx and has no reason to.
type Store struct {
	mu sync.Mutex
	st *state
}

var _ acme.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store { return &Store{st: newState()} }

// state is every table of the store. Values are whole records, replaced
// rather than mutated in place, so cloning the maps is enough to copy the
// state.
type state struct {
	accounts map[string]acme.Account
	// thumbprints indexes accounts by key thumbprint. RFC 8555 makes a key
	// the identity of an account, so this is an index and a uniqueness
	// constraint at once.
	thumbprints map[string]string
	orders      map[string]acme.Order
	authzs      map[string]acme.Authorization
	challenges  map[string]acme.Challenge
	certs       map[string]acme.Certificate
	// claims maps a client identifier to the order that took it. Apple
	// calls the ClientIdentifier a one-time code, so a value here is never
	// released, not even when the order that took it is pruned.
	claims map[string]string
	nonces map[string]acme.Nonce
}

func newState() *state {
	return &state{
		accounts:    map[string]acme.Account{},
		thumbprints: map[string]string{},
		orders:      map[string]acme.Order{},
		authzs:      map[string]acme.Authorization{},
		challenges:  map[string]acme.Challenge{},
		certs:       map[string]acme.Certificate{},
		claims:      map[string]string{},
		nonces:      map[string]acme.Nonce{},
	}
}

// clone returns a copy sharing no map with the original.
func (st *state) clone() *state {
	return &state{
		accounts:    maps.Clone(st.accounts),
		thumbprints: maps.Clone(st.thumbprints),
		orders:      maps.Clone(st.orders),
		authzs:      maps.Clone(st.authzs),
		challenges:  maps.Clone(st.challenges),
		certs:       maps.Clone(st.certs),
		claims:      maps.Clone(st.claims),
		nonces:      maps.Clone(st.nonces),
	}
}

// tx is the view every method runs against: the live state, under the
// store lock in both cases, since Update rolls back rather than swapping
// a copy in.
type tx struct{ st *state }

var _ acme.Tx = (*tx)(nil)

// Update implements acme.Store. fn runs against the live state under the
// store lock; a copy taken on entry is put back when fn fails, so a
// failed transaction leaves nothing behind.
func (s *Store) Update(_ context.Context, fn func(acme.Tx) error) error {
	if fn == nil {
		return fmt.Errorf("%w: nil Update callback", acme.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.st.clone()
	if err := fn(&tx{st: s.st}); err != nil {
		s.st = before
		return err
	}
	return nil
}

// view runs a read against the live state under the lock.
func (s *Store) view() (*tx, func()) {
	s.mu.Lock()
	return &tx{st: s.st}, s.mu.Unlock
}

func validID(what, id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty %s", acme.ErrInvalid, what)
	}
	return nil
}

func notFound(what, id string) error {
	return fmt.Errorf("%w: %s %q", acme.ErrNotFound, what, id)
}

// Deep copies. A caller that mutates a record it was given, or one it
// handed over, must not reach the stored copy, so every pointer and every
// slice on a record is copied on the way in and on the way out.

// cloneBytes copies a byte slice, keeping nil distinct from empty: a
// challenge that was answered with a zero-length attestation is not the
// same as one that was never answered.
func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func cloneBool(b *bool) *bool {
	if b == nil {
		return nil
	}
	v := *b
	return &v
}

// cloneProblem copies a problem document. The struct is copied whole so
// the cause it carries for the log survives with it.
func cloneProblem(p *acme.Problem) *acme.Problem {
	if p == nil {
		return nil
	}
	out := *p
	out.Algorithms = slices.Clone(p.Algorithms)
	out.Subproblems = nil
	for _, sp := range p.Subproblems {
		if sp == nil {
			out.Subproblems = append(out.Subproblems, nil)
			continue
		}
		c := *sp
		if sp.Identifier != nil {
			id := *sp.Identifier
			c.Identifier = &id
		}
		out.Subproblems = append(out.Subproblems, &c)
	}
	return &out
}

func cloneBinding(b acme.Binding) acme.Binding {
	b.Organization = slices.Clone(b.Organization)
	b.NotAfter = b.NotAfter.UTC()
	return b
}

func cloneAccount(a acme.Account) acme.Account {
	a.Contact = slices.Clone(a.Contact)
	if a.Key != nil {
		k := *a.Key
		a.Key = &k
	}
	a.CreatedAt = a.CreatedAt.UTC()
	return a
}

func cloneOrder(o acme.Order) acme.Order {
	o.Binding = cloneBinding(o.Binding)
	o.Error = cloneProblem(o.Error)
	o.Expires, o.CreatedAt = o.Expires.UTC(), o.CreatedAt.UTC()
	return o
}

// cloneAuthorization is a plain copy: an authorization holds no pointer
// and no slice. It exists so every record is copied the same way.
func cloneAuthorization(a acme.Authorization) acme.Authorization {
	a.Expires = a.Expires.UTC()
	return a
}

func cloneChallenge(c acme.Challenge) acme.Challenge {
	c.Attestation = cloneBytes(c.Attestation)
	c.Error = cloneProblem(c.Error)
	c.ValidatedAt = c.ValidatedAt.UTC()
	return c
}

func cloneProperties(p attest.Properties) attest.Properties {
	p.Freshness = cloneBytes(p.Freshness)
	p.SIPEnabled = cloneBool(p.SIPEnabled)
	p.KextsAllowed = cloneBool(p.KextsAllowed)
	return p
}

func cloneCertificate(c acme.Certificate) acme.Certificate {
	c.ChainPEM = cloneBytes(c.ChainPEM)
	c.Device = cloneProperties(c.Device)
	c.Binding = cloneBinding(c.Binding)
	c.NotAfter, c.IssuedAt = c.NotAfter.UTC(), c.IssuedAt.UTC()
	return c
}

// Reads, on the transaction view.

// GetAccount implements acme.Reader.
func (t *tx) GetAccount(_ context.Context, id string) (*acme.Account, error) {
	if err := validID("account id", id); err != nil {
		return nil, err
	}
	a, ok := t.st.accounts[id]
	if !ok {
		return nil, notFound("account", id)
	}
	out := cloneAccount(a)
	return &out, nil
}

// AccountByThumbprint implements acme.Reader.
func (t *tx) AccountByThumbprint(ctx context.Context, thumbprint string) (*acme.Account, error) {
	if err := validID("account thumbprint", thumbprint); err != nil {
		return nil, err
	}
	id, ok := t.st.thumbprints[thumbprint]
	if !ok {
		return nil, notFound("account for thumbprint", thumbprint)
	}
	return t.GetAccount(ctx, id)
}

// GetOrder implements acme.Reader.
func (t *tx) GetOrder(_ context.Context, id string) (*acme.Order, error) {
	if err := validID("order id", id); err != nil {
		return nil, err
	}
	o, ok := t.st.orders[id]
	if !ok {
		return nil, notFound("order", id)
	}
	out := cloneOrder(o)
	return &out, nil
}

// GetAuthorization implements acme.Reader.
func (t *tx) GetAuthorization(_ context.Context, id string) (*acme.Authorization, error) {
	if err := validID("authorization id", id); err != nil {
		return nil, err
	}
	a, ok := t.st.authzs[id]
	if !ok {
		return nil, notFound("authorization", id)
	}
	out := cloneAuthorization(a)
	return &out, nil
}

// GetChallenge implements acme.Reader.
func (t *tx) GetChallenge(_ context.Context, id string) (*acme.Challenge, error) {
	if err := validID("challenge id", id); err != nil {
		return nil, err
	}
	c, ok := t.st.challenges[id]
	if !ok {
		return nil, notFound("challenge", id)
	}
	out := cloneChallenge(c)
	return &out, nil
}

// GetCertificate implements acme.Reader.
func (t *tx) GetCertificate(_ context.Context, id string) (*acme.Certificate, error) {
	if err := validID("certificate id", id); err != nil {
		return nil, err
	}
	c, ok := t.st.certs[id]
	if !ok {
		return nil, notFound("certificate", id)
	}
	out := cloneCertificate(c)
	return &out, nil
}

// ListOrders implements acme.Reader.
func (t *tx) ListOrders(
	_ context.Context,
	accountID string,
	p storage.Page,
) (storage.Result[acme.Order], error) {
	if err := validID("account id", accountID); err != nil {
		return storage.Result[acme.Order]{}, err
	}
	var ids []string
	for id, o := range t.st.orders {
		if o.AccountID == accountID {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return page(ids, p, func(id string) acme.Order { return cloneOrder(t.st.orders[id]) })
}

// ListCertificates implements acme.Reader.
func (t *tx) ListCertificates(
	_ context.Context,
	q acme.CertificateQuery,
	p storage.Page,
) (storage.Result[acme.Certificate], error) {
	var ids []string
	for id, c := range t.st.certs {
		if matches(c, q) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return page(ids, p, func(id string) acme.Certificate { return cloneCertificate(t.st.certs[id]) })
}

// matches reports whether a certificate satisfies every non-empty field
// of the query.
func matches(c acme.Certificate, q acme.CertificateQuery) bool {
	switch {
	case q.DeviceSerial != "" && c.Device.SerialNumber != q.DeviceSerial:
		return false
	case q.UDID != "" && c.Device.UDID != q.UDID:
		return false
	case q.AccountID != "" && c.AccountID != q.AccountID:
		return false
	default:
		return true
	}
}

// Writes, only on the transaction view.

// PutAccount implements acme.Writer.
func (t *tx) PutAccount(_ context.Context, a *acme.Account) error {
	if a == nil {
		return fmt.Errorf("%w: nil account", acme.ErrInvalid)
	}
	if err := validID("account id", a.ID); err != nil {
		return err
	}
	if err := validID("account thumbprint", a.Thumbprint); err != nil {
		return err
	}
	// A thumbprint is the account's identity, so one already held by
	// another account is a conflict rather than a silent re-binding.
	if owner, ok := t.st.thumbprints[a.Thumbprint]; ok && owner != a.ID {
		return fmt.Errorf("%w: thumbprint %q is account %q", acme.ErrConflict, a.Thumbprint, owner)
	}
	if prev, ok := t.st.accounts[a.ID]; ok && prev.Thumbprint != a.Thumbprint {
		delete(t.st.thumbprints, prev.Thumbprint)
	}
	t.st.accounts[a.ID] = cloneAccount(*a)
	t.st.thumbprints[a.Thumbprint] = a.ID
	return nil
}

// PutOrder implements acme.Writer.
func (t *tx) PutOrder(_ context.Context, o *acme.Order) error {
	if o == nil {
		return fmt.Errorf("%w: nil order", acme.ErrInvalid)
	}
	if err := validID("order id", o.ID); err != nil {
		return err
	}
	if err := validID("order account id", o.AccountID); err != nil {
		return err
	}
	t.st.orders[o.ID] = cloneOrder(*o)
	return nil
}

// PutAuthorization implements acme.Writer.
func (t *tx) PutAuthorization(_ context.Context, a *acme.Authorization) error {
	if a == nil {
		return fmt.Errorf("%w: nil authorization", acme.ErrInvalid)
	}
	if err := validID("authorization id", a.ID); err != nil {
		return err
	}
	t.st.authzs[a.ID] = cloneAuthorization(*a)
	return nil
}

// PutChallenge implements acme.Writer.
func (t *tx) PutChallenge(_ context.Context, c *acme.Challenge) error {
	if c == nil {
		return fmt.Errorf("%w: nil challenge", acme.ErrInvalid)
	}
	if err := validID("challenge id", c.ID); err != nil {
		return err
	}
	t.st.challenges[c.ID] = cloneChallenge(*c)
	return nil
}

// PutCertificate implements acme.Writer.
func (t *tx) PutCertificate(_ context.Context, c *acme.Certificate) error {
	if c == nil {
		return fmt.Errorf("%w: nil certificate", acme.ErrInvalid)
	}
	if err := validID("certificate id", c.ID); err != nil {
		return err
	}
	t.st.certs[c.ID] = cloneCertificate(*c)
	return nil
}

// ClaimIdentifier implements acme.Writer.
func (t *tx) ClaimIdentifier(_ context.Context, identifier, orderID string) error {
	if err := validID("client identifier", identifier); err != nil {
		return err
	}
	if err := validID("order id", orderID); err != nil {
		return err
	}
	// The claim is what makes the client identifier one-time, so a second
	// claim loses even when it names the order that already holds it: a
	// retry that got this far has already had its order created.
	if owner, ok := t.st.claims[identifier]; ok {
		return fmt.Errorf("%w: identifier %q is order %q", acme.ErrConflict, identifier, owner)
	}
	t.st.claims[identifier] = orderID
	return nil
}

// Pagination. The cursor is the last id of the page, encoded so a caller
// cannot build one out of a record id and depend on an ordering the
// backend chose for itself.

func encodeCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func decodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", fmt.Errorf("%w: page cursor %q", acme.ErrInvalid, c)
	}
	return string(b), nil
}

// page applies keyset pagination over sorted ids. NextCursor is empty on
// the last page, so a caller stops without a trailing empty read.
func page[T any](ids []string, p storage.Page, load func(string) T) (storage.Result[T], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return storage.Result[T]{}, err
	}
	limit := p.Limit
	if limit <= 0 {
		limit = storage.DefaultPageSize
	}
	var out storage.Result[T]
	last := ""
	for _, id := range ids {
		if p.Cursor != "" && id <= after {
			continue
		}
		if len(out.Items) == limit {
			out.NextCursor = encodeCursor(last)
			return out, nil
		}
		out.Items = append(out.Items, load(id))
		last = id
	}
	return out, nil
}

// Nonces and pruning, on the store: neither belongs to a transaction.

// PutNonce implements acme.Store.
func (s *Store) PutNonce(_ context.Context, n acme.Nonce) error {
	if err := validID("nonce value", n.Value); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n.IssuedAt = n.IssuedAt.UTC()
	s.st.nonces[n.Value] = n
	return nil
}

// TakeNonce implements acme.Store. Removing and returning under one lock
// is what makes the second use of a nonce a miss rather than a race.
func (s *Store) TakeNonce(_ context.Context, value string) (*acme.Nonce, error) {
	if err := validID("nonce value", value); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.st.nonces[value]
	if !ok {
		return nil, notFound("nonce", value)
	}
	delete(s.st.nonces, value)
	return &n, nil
}

// Prune implements acme.Store.
func (s *Store) Prune(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	removed := 0
	for value, n := range st.nonces {
		if n.IssuedAt.Before(before) {
			delete(st.nonces, value)
			removed++
		}
	}
	// An order owns its authorization and its challenge, so pruning one
	// takes the chain with it: an authorization outliving its order names
	// something no request could ever reach again.
	for id, o := range st.orders {
		if !expired(o.Expires, before) {
			continue
		}
		delete(st.orders, id)
		removed += 1 + st.removeAuthorization(o.AuthzID)
	}
	for id, a := range st.authzs {
		if expired(a.Expires, before) {
			removed += st.removeAuthorization(id)
		}
	}
	return removed, nil
}

// removeAuthorization deletes an authorization and its challenge, and
// returns how many records went. An id naming nothing removes nothing,
// which is what an order pruned after its authorization already was
// looks like.
func (st *state) removeAuthorization(id string) int {
	a, ok := st.authzs[id]
	if !ok {
		return 0
	}
	delete(st.authzs, id)
	removed := 1
	if _, ok := st.challenges[a.ChallengeID]; ok {
		delete(st.challenges, a.ChallengeID)
		removed++
	}
	return removed
}

// expired reports whether an expiry has passed the cutoff. A zero expiry
// is a record that never expires, and is kept: pruning it would read the
// zero time as the distant past.
func expired(exp, before time.Time) bool {
	return !exp.IsZero() && exp.Before(before)
}

// Reads on the store run against the live state under the lock.

// GetAccount implements acme.Reader.
func (s *Store) GetAccount(ctx context.Context, id string) (*acme.Account, error) {
	t, done := s.view()
	defer done()
	return t.GetAccount(ctx, id)
}

// AccountByThumbprint implements acme.Reader.
func (s *Store) AccountByThumbprint(ctx context.Context, thumbprint string) (*acme.Account, error) {
	t, done := s.view()
	defer done()
	return t.AccountByThumbprint(ctx, thumbprint)
}

// GetOrder implements acme.Reader.
func (s *Store) GetOrder(ctx context.Context, id string) (*acme.Order, error) {
	t, done := s.view()
	defer done()
	return t.GetOrder(ctx, id)
}

// GetAuthorization implements acme.Reader.
func (s *Store) GetAuthorization(ctx context.Context, id string) (*acme.Authorization, error) {
	t, done := s.view()
	defer done()
	return t.GetAuthorization(ctx, id)
}

// GetChallenge implements acme.Reader.
func (s *Store) GetChallenge(ctx context.Context, id string) (*acme.Challenge, error) {
	t, done := s.view()
	defer done()
	return t.GetChallenge(ctx, id)
}

// GetCertificate implements acme.Reader.
func (s *Store) GetCertificate(ctx context.Context, id string) (*acme.Certificate, error) {
	t, done := s.view()
	defer done()
	return t.GetCertificate(ctx, id)
}

// ListOrders implements acme.Reader.
func (s *Store) ListOrders(
	ctx context.Context,
	accountID string,
	p storage.Page,
) (storage.Result[acme.Order], error) {
	t, done := s.view()
	defer done()
	return t.ListOrders(ctx, accountID, p)
}

// ListCertificates implements acme.Reader.
func (s *Store) ListCertificates(
	ctx context.Context,
	q acme.CertificateQuery,
	p storage.Page,
) (storage.Result[acme.Certificate], error) {
	t, done := s.view()
	defer done()
	return t.ListCertificates(ctx, q, p)
}
