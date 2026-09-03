package inmem

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

// Purposes name the sealed values; each is the AAD prefix binding a
// ciphertext to its field, the same strings dep/sqlstore uses.
const (
	PurposeConsumerSecret = "dep_accounts.consumer_secret" // #nosec G101 -- a column name, not a credential
	PurposeAccessToken    = "dep_accounts.access_token"    // #nosec G101 -- a column name, not a credential
	PurposeAccessSecret   = "dep_accounts.access_secret"   // #nosec G101 -- a column name, not a credential
	PurposeSession        = "dep_sessions.token"           // #nosec G101 -- a column name, not a credential
	PurposeKeyPEM         = "dep_keypairs.key_pem"         // #nosec G101 -- a column name, not a credential
)

// Option configures New.
type Option func(*Store)

// WithKeyring seals the secrets (OAuth secrets, session tokens, private
// keys) in memory the way the SQL store seals its columns, so the
// contract suite exercises the same code path.
func WithKeyring(k *crypt.Keyring) Option { return func(s *Store) { s.keyring = k } }

// Store implements dep.Store in memory.
type Store struct {
	mu      sync.Mutex
	st      *state
	keyring *crypt.Keyring
}

var _ dep.Store = (*Store)(nil)

// New returns an empty store.
func New(opts ...Option) *Store {
	s := &Store{st: newState()}
	for _, o := range opts {
		o(s)
	}
	return s
}

type keypairKey struct {
	account string
	stage   dep.Stage
}

type deviceKey struct {
	account string
	serial  string
}

// state is every table of the store. Secret bytes are stored sealed when
// a keyring is configured; byte slices are never mutated in place.
type state struct {
	accounts map[string]dep.Account
	// secrets holds the three OAuth secrets per account, sealed or not.
	secrets  map[string]map[string][]byte
	sessions map[string][]byte
	cursors  map[string]dep.Cursor
	keypairs map[keypairKey]dep.Keypair
	devices  map[deviceKey]dep.StoredDevice
	profiles map[deviceKey]dep.Profile
	assigns  map[deviceKey]dep.Assignment
}

func newState() *state {
	return &state{
		accounts: map[string]dep.Account{},
		secrets:  map[string]map[string][]byte{},
		sessions: map[string][]byte{},
		cursors:  map[string]dep.Cursor{},
		keypairs: map[keypairKey]dep.Keypair{},
		devices:  map[deviceKey]dep.StoredDevice{},
		profiles: map[deviceKey]dep.Profile{},
		assigns:  map[deviceKey]dep.Assignment{},
	}
}

// clone returns a copy sharing no map with the original.
func (st *state) clone() *state {
	secrets := make(map[string]map[string][]byte, len(st.secrets))
	for k, v := range st.secrets {
		secrets[k] = maps.Clone(v)
	}
	return &state{
		accounts: maps.Clone(st.accounts),
		secrets:  secrets,
		sessions: maps.Clone(st.sessions),
		cursors:  maps.Clone(st.cursors),
		keypairs: maps.Clone(st.keypairs),
		devices:  maps.Clone(st.devices),
		profiles: maps.Clone(st.profiles),
		assigns:  maps.Clone(st.assigns),
	}
}

// tx is the view every method runs against: the live state under the
// store lock, or a private copy inside Update.
type tx struct {
	s  *Store
	st *state
}

var _ dep.Store = (*tx)(nil)

// Update implements dep.Store on the transaction view: nested transactions
// are not supported.
func (t *tx) Update(context.Context, func(dep.Tx) error) error {
	return fmt.Errorf("%w: nested Update", dep.ErrInvalid)
}

// Update implements dep.Store. fn runs against a copy of the state under
// the store lock; the copy replaces the live state only when fn returns
// nil. fn must use the Tx it is given.
func (s *Store) Update(_ context.Context, fn func(dep.Tx) error) error {
	if fn == nil {
		return fmt.Errorf("%w: nil Update callback", dep.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := s.st.clone()
	if err := fn(&tx{s: s, st: cp}); err != nil {
		return err
	}
	s.st = cp
	return nil
}

func (s *Store) view() (*tx, func()) {
	s.mu.Lock()
	return &tx{s: s, st: s.st}, s.mu.Unlock
}

// RawSecrets returns the stored bytes of every secret of the account as
// they rest in memory (sealed when a keyring is configured), keyed
// consumer_secret, access_token, access_secret, session, and
// key_pem:<stage>. It lets the contract suite prove sealing.
func (s *Store) RawSecrets(_ context.Context, name string) (map[string][]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty account name", dep.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]byte{}
	for k, v := range s.st.secrets[name] {
		out[k] = slices.Clone(v)
	}
	if v, ok := s.st.sessions[name]; ok {
		out["session"] = slices.Clone(v)
	}
	for _, stage := range []dep.Stage{dep.StageStaged, dep.StageCurrent} {
		if kp, ok := s.st.keypairs[keypairKey{name, stage}]; ok {
			out["key_pem:"+string(stage)] = slices.Clone(kp.KeyPEM)
		}
	}
	return out, nil
}

// seal encrypts b for the purpose and row; empty input stays nil.
func (s *Store) seal(purpose, rowID string, b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if s.keyring == nil {
		return slices.Clone(b), nil
	}
	out, err := s.keyring.Seal(b, crypt.AAD(purpose, rowID))
	if err != nil {
		return nil, fmt.Errorf("inmem: seal %s: %w", purpose, err)
	}
	return out, nil
}

// open decrypts a stored value; plaintext passes unless the keyring is
// strict, and a sealed value without a keyring is an error.
func (s *Store) open(purpose, rowID string, b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if !crypt.IsSealed(b) {
		if s.keyring != nil && s.keyring.Strict() {
			return nil, fmt.Errorf("%w: %s for %s", crypt.ErrUnsealed, purpose, rowID)
		}
		return slices.Clone(b), nil
	}
	if s.keyring == nil {
		return nil, fmt.Errorf("%w: %s for %s is sealed", crypt.ErrNoKeyring, purpose, rowID)
	}
	pt, _, err := s.keyring.Open(b, crypt.AAD(purpose, rowID))
	if err != nil {
		return nil, fmt.Errorf("inmem: open %s: %w", purpose, err)
	}
	return pt, nil
}

func validName(what, name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty %s", dep.ErrInvalid, what)
	}
	return nil
}

func notFound(what, name string) error {
	return fmt.Errorf("%w: %s %q", dep.ErrNotFound, what, name)
}

// PutAccount implements dep.AccountStore.
func (t *tx) PutAccount(_ context.Context, a *dep.Account) error {
	if a == nil {
		return fmt.Errorf("%w: nil account", dep.ErrInvalid)
	}
	if err := validName("account name", a.Name); err != nil {
		return err
	}
	secrets := map[string][]byte{}
	for _, f := range []struct {
		key, purpose, value string
	}{
		{"consumer_secret", PurposeConsumerSecret, a.ConsumerSecret},
		{"access_token", PurposeAccessToken, a.AccessToken},
		{"access_secret", PurposeAccessSecret, a.AccessSecret},
	} {
		sealed, err := t.s.seal(f.purpose, a.Name, []byte(f.value))
		if err != nil {
			return err
		}
		secrets[f.key] = sealed
	}
	stored := *a.Clone()
	stored.ConsumerSecret, stored.AccessToken, stored.AccessSecret = "", "", ""
	stored.CreatedAt, stored.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	if prev, ok := t.st.accounts[a.Name]; ok {
		stored.CreatedAt = prev.CreatedAt
	}
	t.st.accounts[a.Name] = stored
	t.st.secrets[a.Name] = secrets
	return nil
}

// GetAccount implements dep.AccountStore.
func (t *tx) GetAccount(_ context.Context, name string) (*dep.Account, error) {
	if err := validName("account name", name); err != nil {
		return nil, err
	}
	stored, ok := t.st.accounts[name]
	if !ok {
		return nil, notFound("account", name)
	}
	out := stored.Clone()
	secrets := t.st.secrets[name]
	for _, f := range []struct {
		key, purpose string
		dst          *string
	}{
		{"consumer_secret", PurposeConsumerSecret, &out.ConsumerSecret},
		{"access_token", PurposeAccessToken, &out.AccessToken},
		{"access_secret", PurposeAccessSecret, &out.AccessSecret},
	} {
		pt, err := t.s.open(f.purpose, name, secrets[f.key])
		if err != nil {
			return nil, err
		}
		*f.dst = string(pt)
	}
	return out, nil
}

// DeleteAccount implements dep.AccountStore.
func (t *tx) DeleteAccount(_ context.Context, name string) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	if _, ok := t.st.accounts[name]; !ok {
		return notFound("account", name)
	}
	delete(t.st.accounts, name)
	delete(t.st.secrets, name)
	delete(t.st.sessions, name)
	delete(t.st.cursors, name)
	for _, stage := range []dep.Stage{dep.StageStaged, dep.StageCurrent} {
		delete(t.st.keypairs, keypairKey{name, stage})
	}
	maps.DeleteFunc(t.st.devices, func(k deviceKey, _ dep.StoredDevice) bool { return k.account == name })
	maps.DeleteFunc(t.st.profiles, func(k deviceKey, _ dep.Profile) bool { return k.account == name })
	maps.DeleteFunc(t.st.assigns, func(k deviceKey, _ dep.Assignment) bool { return k.account == name })
	return nil
}

// ListAccounts implements dep.AccountStore.
func (t *tx) ListAccounts(ctx context.Context, p storage.Page) (storage.Result[dep.Account], error) {
	names := slices.Sorted(maps.Keys(t.st.accounts))
	return page(names, p, func(name string) (dep.Account, error) {
		a, err := t.GetAccount(ctx, name)
		if err != nil {
			return dep.Account{}, err
		}
		return *a, nil
	})
}

// SetAccountState implements dep.AccountStore.
func (t *tx) SetAccountState(_ context.Context, name string, s dep.AccountState) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	a, ok := t.st.accounts[name]
	if !ok {
		return notFound("account", name)
	}
	a.State = s
	t.st.accounts[name] = a
	return nil
}

func validStage(stage dep.Stage) error {
	if stage != dep.StageStaged && stage != dep.StageCurrent {
		return fmt.Errorf("%w: keypair stage %q", dep.ErrInvalid, stage)
	}
	return nil
}

// PutKeypair implements dep.AccountStore.
func (t *tx) PutKeypair(_ context.Context, name string, stage dep.Stage, kp *dep.Keypair) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	if err := validStage(stage); err != nil {
		return err
	}
	if kp == nil || len(kp.CertPEM) == 0 || len(kp.KeyPEM) == 0 {
		return fmt.Errorf("%w: keypair needs certificate and key PEM", dep.ErrInvalid)
	}
	sealed, err := t.s.seal(PurposeKeyPEM, name+"/"+string(stage), kp.KeyPEM)
	if err != nil {
		return err
	}
	t.st.keypairs[keypairKey{name, stage}] = dep.Keypair{CertPEM: slices.Clone(kp.CertPEM), KeyPEM: sealed, CreatedAt: kp.CreatedAt.UTC()}
	return nil
}

// Keypair implements dep.AccountStore.
func (t *tx) Keypair(_ context.Context, name string, stage dep.Stage) (*dep.Keypair, error) {
	if err := validName("account name", name); err != nil {
		return nil, err
	}
	if err := validStage(stage); err != nil {
		return nil, err
	}
	kp, ok := t.st.keypairs[keypairKey{name, stage}]
	if !ok {
		return nil, fmt.Errorf("%w: %s keypair for %q", dep.ErrNotFound, stage, name)
	}
	key, err := t.s.open(PurposeKeyPEM, name+"/"+string(stage), kp.KeyPEM)
	if err != nil {
		return nil, err
	}
	return &dep.Keypair{CertPEM: slices.Clone(kp.CertPEM), KeyPEM: key, CreatedAt: kp.CreatedAt}, nil
}

// UpstageKeypair implements dep.AccountStore. The key is re-sealed for
// its new row identity.
func (t *tx) UpstageKeypair(ctx context.Context, name string) error {
	kp, err := t.Keypair(ctx, name, dep.StageStaged)
	if err != nil {
		return err
	}
	if err := t.PutKeypair(ctx, name, dep.StageCurrent, kp); err != nil {
		return err
	}
	delete(t.st.keypairs, keypairKey{name, dep.StageStaged})
	return nil
}

// Session implements dep.SessionStore.
func (t *tx) Session(_ context.Context, name string) (string, error) {
	if err := validName("account name", name); err != nil {
		return "", err
	}
	pt, err := t.s.open(PurposeSession, name, t.st.sessions[name])
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// SetSession implements dep.SessionStore.
func (t *tx) SetSession(_ context.Context, name, token string) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	if token == "" {
		delete(t.st.sessions, name)
		return nil
	}
	sealed, err := t.s.seal(PurposeSession, name, []byte(token))
	if err != nil {
		return err
	}
	t.st.sessions[name] = sealed
	return nil
}

// Cursor implements dep.CursorStore.
func (t *tx) Cursor(_ context.Context, name string) (dep.Cursor, error) {
	if err := validName("account name", name); err != nil {
		return dep.Cursor{}, err
	}
	c := t.st.cursors[name]
	if c.FetchedUntil != nil {
		u := *c.FetchedUntil
		c.FetchedUntil = &u
	}
	return c, nil
}

// SetCursor implements dep.CursorStore.
func (t *tx) SetCursor(_ context.Context, name string, c dep.Cursor) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	if c.IsZero() {
		delete(t.st.cursors, name)
		return nil
	}
	c.UpdatedAt = c.UpdatedAt.UTC()
	if c.FetchedUntil != nil {
		u := c.FetchedUntil.UTC()
		c.FetchedUntil = &u
	}
	t.st.cursors[name] = c
	return nil
}

// PutDevices implements dep.DeviceStore.
func (t *tx) PutDevices(_ context.Context, account string, devs []dep.Device, at time.Time) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	for _, d := range devs {
		if d.SerialNumber == "" {
			return fmt.Errorf("%w: device without serial_number", dep.ErrInvalid)
		}
	}
	at = at.UTC()
	for _, d := range devs {
		k := deviceKey{account, d.SerialNumber}
		sd := dep.StoredDevice{Account: account, Device: d.Clone(), Deleted: d.OpType == dep.OpDeleted, FirstSeen: at, UpdatedAt: at}
		if prev, ok := t.st.devices[k]; ok {
			sd.FirstSeen = prev.FirstSeen
		}
		t.st.devices[k] = sd
	}
	return nil
}

// GetDevice implements dep.DeviceStore.
func (t *tx) GetDevice(_ context.Context, account, serial string) (*dep.StoredDevice, error) {
	if err := validName("account name", account); err != nil {
		return nil, err
	}
	if err := validName("serial", serial); err != nil {
		return nil, err
	}
	sd, ok := t.st.devices[deviceKey{account, serial}]
	if !ok {
		return nil, notFound("device", serial)
	}
	out := sd
	out.Device = sd.Device.Clone()
	return &out, nil
}

// ListDevices implements dep.DeviceStore.
func (t *tx) ListDevices(_ context.Context, account string, q dep.DeviceQuery, p storage.Page) (storage.Result[dep.StoredDevice], error) {
	if err := validName("account name", account); err != nil {
		return storage.Result[dep.StoredDevice]{}, err
	}
	var serials []string
	for k, sd := range t.st.devices {
		if k.account != account || (sd.Deleted && !q.IncludeDeleted) || (q.ProfileUUID != "" && sd.ProfileUUID != q.ProfileUUID) {
			continue
		}
		serials = append(serials, k.serial)
	}
	slices.Sort(serials)
	return page(serials, p, func(serial string) (dep.StoredDevice, error) {
		sd := t.st.devices[deviceKey{account, serial}]
		sd.Device = sd.Device.Clone()
		return sd, nil
	})
}

// PutProfile implements dep.ProfileStore.
func (t *tx) PutProfile(_ context.Context, account string, p *dep.Profile) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	if p == nil || p.ProfileUUID == "" {
		return fmt.Errorf("%w: profile needs a profile_uuid", dep.ErrInvalid)
	}
	t.st.profiles[deviceKey{account, p.ProfileUUID}] = p.Clone()
	return nil
}

// GetProfile implements dep.ProfileStore.
func (t *tx) GetProfile(_ context.Context, account, uuid string) (*dep.Profile, error) {
	if err := validName("account name", account); err != nil {
		return nil, err
	}
	if err := validName("profile uuid", uuid); err != nil {
		return nil, err
	}
	p, ok := t.st.profiles[deviceKey{account, uuid}]
	if !ok {
		return nil, notFound("profile", uuid)
	}
	out := p.Clone()
	return &out, nil
}

// DeleteProfile implements dep.ProfileStore.
func (t *tx) DeleteProfile(_ context.Context, account, uuid string) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	if err := validName("profile uuid", uuid); err != nil {
		return err
	}
	if _, ok := t.st.profiles[deviceKey{account, uuid}]; !ok {
		return notFound("profile", uuid)
	}
	delete(t.st.profiles, deviceKey{account, uuid})
	return nil
}

// ListProfiles implements dep.ProfileStore.
func (t *tx) ListProfiles(_ context.Context, account string, p storage.Page) (storage.Result[dep.Profile], error) {
	if err := validName("account name", account); err != nil {
		return storage.Result[dep.Profile]{}, err
	}
	var uuids []string
	for k := range t.st.profiles {
		if k.account == account {
			uuids = append(uuids, k.serial)
		}
	}
	slices.Sort(uuids)
	return page(uuids, p, func(uuid string) (dep.Profile, error) {
		return t.st.profiles[deviceKey{account, uuid}].Clone(), nil
	})
}

// PutAssignment implements dep.AssignmentStore.
func (t *tx) PutAssignment(_ context.Context, a *dep.Assignment) error {
	if a == nil {
		return fmt.Errorf("%w: nil assignment", dep.ErrInvalid)
	}
	if err := validName("account name", a.Account); err != nil {
		return err
	}
	if err := validName("serial", a.SerialNumber); err != nil {
		return err
	}
	stored := *a
	stored.AttemptedAt, stored.NextAttemptAt = a.AttemptedAt.UTC(), a.NextAttemptAt.UTC()
	if a.NextAttemptAt.IsZero() {
		stored.NextAttemptAt = time.Time{}
	}
	t.st.assigns[deviceKey{a.Account, a.SerialNumber}] = stored
	return nil
}

// GetAssignment implements dep.AssignmentStore.
func (t *tx) GetAssignment(_ context.Context, account, serial string) (*dep.Assignment, error) {
	if err := validName("account name", account); err != nil {
		return nil, err
	}
	if err := validName("serial", serial); err != nil {
		return nil, err
	}
	a, ok := t.st.assigns[deviceKey{account, serial}]
	if !ok {
		return nil, notFound("assignment", serial)
	}
	return &a, nil
}

// ListAssignments implements dep.AssignmentStore.
func (t *tx) ListAssignments(_ context.Context, account string, q dep.AssignmentQuery, p storage.Page) (storage.Result[dep.Assignment], error) {
	if err := validName("account name", account); err != nil {
		return storage.Result[dep.Assignment]{}, err
	}
	var serials []string
	for k, a := range t.st.assigns {
		if k.account == account && (q.Status == "" || a.Status == q.Status) {
			serials = append(serials, k.serial)
		}
	}
	slices.Sort(serials)
	return page(serials, p, func(serial string) (dep.Assignment, error) {
		return t.st.assigns[deviceKey{account, serial}], nil
	})
}

// page applies keyset pagination over sorted keys.
func page[T any](keys []string, p storage.Page, load func(string) (T, error)) (storage.Result[T], error) {
	limit := p.Limit
	if limit <= 0 {
		limit = storage.DefaultPageSize
	}
	var out storage.Result[T]
	last := ""
	for _, k := range keys {
		if p.Cursor != "" && strings.Compare(k, p.Cursor) <= 0 {
			continue
		}
		if len(out.Items) == limit {
			out.NextCursor = last
			return out, nil
		}
		item, err := load(k)
		if err != nil {
			return storage.Result[T]{}, err
		}
		out.Items = append(out.Items, item)
		last = k
	}
	return out, nil
}

// Store methods outside Update run against the live state under the lock.

// PutAccount implements dep.AccountStore.
func (s *Store) PutAccount(ctx context.Context, a *dep.Account) error {
	t, done := s.view()
	defer done()
	return t.PutAccount(ctx, a)
}

// GetAccount implements dep.AccountStore.
func (s *Store) GetAccount(ctx context.Context, name string) (*dep.Account, error) {
	t, done := s.view()
	defer done()
	return t.GetAccount(ctx, name)
}

// DeleteAccount implements dep.AccountStore.
func (s *Store) DeleteAccount(ctx context.Context, name string) error {
	t, done := s.view()
	defer done()
	return t.DeleteAccount(ctx, name)
}

// ListAccounts implements dep.AccountStore.
func (s *Store) ListAccounts(ctx context.Context, p storage.Page) (storage.Result[dep.Account], error) {
	t, done := s.view()
	defer done()
	return t.ListAccounts(ctx, p)
}

// SetAccountState implements dep.AccountStore.
func (s *Store) SetAccountState(ctx context.Context, name string, st dep.AccountState) error {
	t, done := s.view()
	defer done()
	return t.SetAccountState(ctx, name, st)
}

// PutKeypair implements dep.AccountStore.
func (s *Store) PutKeypair(ctx context.Context, name string, stage dep.Stage, kp *dep.Keypair) error {
	t, done := s.view()
	defer done()
	return t.PutKeypair(ctx, name, stage, kp)
}

// Keypair implements dep.AccountStore.
func (s *Store) Keypair(ctx context.Context, name string, stage dep.Stage) (*dep.Keypair, error) {
	t, done := s.view()
	defer done()
	return t.Keypair(ctx, name, stage)
}

// UpstageKeypair implements dep.AccountStore atomically: it runs inside
// Update so a failure leaves both slots untouched.
func (s *Store) UpstageKeypair(ctx context.Context, name string) error {
	return s.Update(ctx, func(tx dep.Tx) error { return tx.UpstageKeypair(ctx, name) })
}

// Session implements dep.SessionStore.
func (s *Store) Session(ctx context.Context, name string) (string, error) {
	t, done := s.view()
	defer done()
	return t.Session(ctx, name)
}

// SetSession implements dep.SessionStore.
func (s *Store) SetSession(ctx context.Context, name, token string) error {
	t, done := s.view()
	defer done()
	return t.SetSession(ctx, name, token)
}

// Cursor implements dep.CursorStore.
func (s *Store) Cursor(ctx context.Context, name string) (dep.Cursor, error) {
	t, done := s.view()
	defer done()
	return t.Cursor(ctx, name)
}

// SetCursor implements dep.CursorStore.
func (s *Store) SetCursor(ctx context.Context, name string, c dep.Cursor) error {
	t, done := s.view()
	defer done()
	return t.SetCursor(ctx, name, c)
}

// PutDevices implements dep.DeviceStore.
func (s *Store) PutDevices(ctx context.Context, account string, devs []dep.Device, at time.Time) error {
	t, done := s.view()
	defer done()
	return t.PutDevices(ctx, account, devs, at)
}

// GetDevice implements dep.DeviceStore.
func (s *Store) GetDevice(ctx context.Context, account, serial string) (*dep.StoredDevice, error) {
	t, done := s.view()
	defer done()
	return t.GetDevice(ctx, account, serial)
}

// ListDevices implements dep.DeviceStore.
func (s *Store) ListDevices(ctx context.Context, account string, q dep.DeviceQuery, p storage.Page) (storage.Result[dep.StoredDevice], error) {
	t, done := s.view()
	defer done()
	return t.ListDevices(ctx, account, q, p)
}

// PutProfile implements dep.ProfileStore.
func (s *Store) PutProfile(ctx context.Context, account string, p *dep.Profile) error {
	t, done := s.view()
	defer done()
	return t.PutProfile(ctx, account, p)
}

// GetProfile implements dep.ProfileStore.
func (s *Store) GetProfile(ctx context.Context, account, uuid string) (*dep.Profile, error) {
	t, done := s.view()
	defer done()
	return t.GetProfile(ctx, account, uuid)
}

// DeleteProfile implements dep.ProfileStore.
func (s *Store) DeleteProfile(ctx context.Context, account, uuid string) error {
	t, done := s.view()
	defer done()
	return t.DeleteProfile(ctx, account, uuid)
}

// ListProfiles implements dep.ProfileStore.
func (s *Store) ListProfiles(ctx context.Context, account string, p storage.Page) (storage.Result[dep.Profile], error) {
	t, done := s.view()
	defer done()
	return t.ListProfiles(ctx, account, p)
}

// PutAssignment implements dep.AssignmentStore.
func (s *Store) PutAssignment(ctx context.Context, a *dep.Assignment) error {
	t, done := s.view()
	defer done()
	return t.PutAssignment(ctx, a)
}

// GetAssignment implements dep.AssignmentStore.
func (s *Store) GetAssignment(ctx context.Context, account, serial string) (*dep.Assignment, error) {
	t, done := s.view()
	defer done()
	return t.GetAssignment(ctx, account, serial)
}

// ListAssignments implements dep.AssignmentStore.
func (s *Store) ListAssignments(ctx context.Context, account string, q dep.AssignmentQuery, p storage.Page) (storage.Result[dep.Assignment], error) {
	t, done := s.view()
	defer done()
	return t.ListAssignments(ctx, account, q, p)
}
