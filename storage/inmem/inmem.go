package inmem

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// Store implements storage.Store in memory.
type Store struct {
	mu          sync.Mutex
	enrollments map[string]*record          // by id
	certs       map[string]mdm.EnrollmentID // cert hash -> device channel id
	bootstrap   map[string][]byte           // device channel id -> token
	history     []storage.CertAssociation   // append-only pin history, oldest first
	pushCerts   map[string]*storage.PushCert
	userAuth    map[string]*storage.UserAuthState // by user channel id
}

type record struct {
	storage.Enrollment
	queue []*storage.QueuedCommand
}

// New returns an empty store.
func New() *Store {
	return &Store{
		enrollments: map[string]*record{}, certs: map[string]mdm.EnrollmentID{}, bootstrap: map[string][]byte{},
		pushCerts: map[string]*storage.PushCert{}, userAuth: map[string]*storage.UserAuthState{},
	}
}

var (
	_ storage.Store          = (*Store)(nil)
	_ storage.PushCertStore  = (*Store)(nil)
	_ storage.UserAuthStore  = (*Store)(nil)
	_ storage.MigrationStore = (*Store)(nil)
)

func (s *Store) get(id mdm.EnrollmentID) (*record, error) {
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", storage.ErrInvalid, err)
	}
	r, ok := s.enrollments[id.ID]
	if !ok {
		return nil, fmt.Errorf("%w: enrollment %s", storage.ErrNotFound, id.ID)
	}
	return r, nil
}

// UpsertAuthenticate implements storage.EnrollmentStore.
func (s *Store) UpsertAuthenticate(_ context.Context, id mdm.EnrollmentID, msg *checkin.Authenticate, raw []byte, at time.Time) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %w", storage.ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.enrollments[id.ID]
	if !ok {
		r = &record{}
		s.enrollments[id.ID] = r
	}
	// Reset everything the previous identity owned.
	for _, q := range r.queue {
		if !q.State.Terminal() {
			q.State = storage.StateCleared
			q.CompletedAt = at
		}
	}
	if !id.Channel.IsUser() {
		s.dropCertLocked(id.ID)
		delete(s.bootstrap, id.ID)
		// User channels of this device are stale once it re-enrolls, and
		// so are their UserAuthenticate sessions.
		s.disableChildrenLocked(id.ID, at)
		s.clearUserAuthOfDeviceLocked(id.ID)
	}
	r.Enrollment = storage.Enrollment{
		ID:              id,
		Device:          storage.DeviceInfoFromAuthenticate(msg),
		AuthenticateRaw: append([]byte(nil), raw...),
		EnrolledAt:      at,
		LastSeenAt:      at,
	}
	return nil
}

// disableChildrenLocked disables every enabled user channel of a device.
func (s *Store) disableChildrenLocked(deviceID string, at time.Time) {
	for _, child := range s.enrollments {
		if child.ID.ParentID == deviceID && child.Enabled {
			child.Enabled = false
			child.DisabledAt = at
		}
	}
}

func (s *Store) dropCertLocked(deviceID string) {
	for h, owner := range s.certs {
		if owner.ID == deviceID {
			delete(s.certs, h)
		}
	}
}

// StoreTokenUpdate implements storage.EnrollmentStore.
func (s *Store) StoreTokenUpdate(_ context.Context, id mdm.EnrollmentID, push mdm.Push, msg *checkin.TokenUpdate, raw []byte, at time.Time) error {
	if !push.Valid() {
		return fmt.Errorf("%w: incomplete push info", storage.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return err
	}
	r.Push = mdm.Push{Topic: push.Topic, Token: append([]byte(nil), push.Token...), Magic: push.Magic}
	if len(raw) > 0 {
		r.TokenUpdateRaw = append([]byte(nil), raw...)
	}
	r.Enabled = true
	r.TokenUpdatedAt = at
	r.LastSeenAt = at
	r.DisabledAt = time.Time{}
	if msg != nil {
		if len(msg.UnlockToken) > 0 {
			r.UnlockToken = append([]byte(nil), msg.UnlockToken...)
		}
		if msg.UserShortName != nil {
			r.UserShortName = *msg.UserShortName
		}
		if msg.UserLongName != "" {
			r.UserLongName = msg.UserLongName
		}
		if msg.EnrollmentUserID != "" {
			r.EnrollmentUserID = msg.EnrollmentUserID
		}
		r.NotOnConsole = msg.NotOnConsole
	}
	return nil
}

// Disable implements storage.EnrollmentStore.
func (s *Store) Disable(_ context.Context, id mdm.EnrollmentID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return err
	}
	r.Enabled = false
	r.DisabledAt = at
	if !id.Channel.IsUser() {
		s.disableChildrenLocked(id.ID, at)
	}
	return nil
}

// Get implements storage.EnrollmentStore.
func (s *Store) Get(_ context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return nil, err
	}
	e := r.Enrollment
	e.Push.Token = append([]byte(nil), e.Push.Token...)
	e.UnlockToken = append([]byte(nil), e.UnlockToken...)
	e.AuthenticateRaw = append([]byte(nil), e.AuthenticateRaw...)
	e.TokenUpdateRaw = append([]byte(nil), e.TokenUpdateRaw...)
	return &e, nil
}

// List implements storage.EnrollmentStore. The cursor is the last id of
// the previous page.
func (s *Store) List(_ context.Context, q storage.EnrollmentQuery, p storage.Page) (storage.Result[storage.Enrollment], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.enrollments))
	for id, r := range s.enrollments {
		if q.Channel != mdm.ChannelUnknown && r.ID.Channel != q.Channel {
			continue
		}
		if q.Enabled != nil && r.Enabled != *q.Enabled {
			continue
		}
		if q.ParentID != "" && r.ID.ParentID != q.ParentID {
			continue
		}
		if p.Cursor != "" && id <= p.Cursor {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	limit := p.Limit
	if limit <= 0 {
		limit = storage.DefaultPageSize
	}
	var out storage.Result[storage.Enrollment]
	for i, id := range ids {
		if i == limit {
			out.NextCursor = ids[i-1]
			break
		}
		out.Items = append(out.Items, s.enrollments[id].Enrollment)
	}
	return out, nil
}

// TouchLastSeen implements storage.EnrollmentStore.
func (s *Store) TouchLastSeen(_ context.Context, id mdm.EnrollmentID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return err
	}
	if at.After(r.LastSeenAt) {
		r.LastSeenAt = at
	}
	return nil
}

// Enqueue implements storage.CommandQueue.
func (s *Store) Enqueue(_ context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, o storage.EnqueueOptions) (storage.EnqueueResult, error) {
	if cmd == nil || cmd.UUID == "" || cmd.RequestType == "" {
		return storage.EnqueueResult{}, fmt.Errorf("%w: command needs a UUID and RequestType", storage.ErrInvalid)
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res := storage.EnqueueResult{Skipped: map[mdm.EnrollmentID]error{}}
	for _, id := range ids {
		r, err := s.get(id)
		if err != nil {
			res.Skipped[id] = err
			continue
		}
		if !r.Enabled {
			res.Skipped[id] = fmt.Errorf("%w: %s", storage.ErrDisabled, id.ID)
			continue
		}
		if o.DedupeKey != "" && hasPendingKey(r, o.DedupeKey) {
			res.Skipped[id] = fmt.Errorf("%w: pending command with dedupe key %q", storage.ErrConflict, o.DedupeKey)
			continue
		}
		c := *cmd
		c.Raw = append([]byte(nil), cmd.Raw...)
		r.queue = append(r.queue, &storage.QueuedCommand{Command: c, State: storage.StatePending, DedupeKey: o.DedupeKey, EnqueuedAt: now})
		res.Queued = append(res.Queued, id)
	}
	return res, nil
}

func hasPendingKey(r *record, key string) bool {
	for _, q := range r.queue {
		if q.DedupeKey == key && !q.State.Terminal() {
			return true
		}
	}
	return false
}

// Next implements storage.CommandQueue.
func (s *Store) Next(_ context.Context, id mdm.EnrollmentID, skipNotNow bool, now time.Time) (*mdm.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return nil, err
	}
	for _, q := range r.queue {
		switch q.State {
		case storage.StatePending, storage.StateSent:
		case storage.StateNotNow:
			if skipNotNow || now.Before(q.NotNowUntil) {
				continue
			}
		case storage.StateAcknowledged, storage.StateError, storage.StateCleared:
			continue
		}
		q.State = storage.StateSent
		q.LastSentAt = now
		q.Attempts++
		c := q.Command
		c.Raw = append([]byte(nil), q.Command.Raw...)
		return &c, nil
	}
	return nil, nil //nolint:nilnil // empty queue is not an error
}

// StoreResult implements storage.CommandQueue.
func (s *Store) StoreResult(_ context.Context, id mdm.EnrollmentID, resp *mdm.Response, now time.Time) error {
	if resp == nil || resp.IsIdle() || resp.CommandUUID == "" {
		return fmt.Errorf("%w: result needs a CommandUUID and a non-Idle status", storage.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return err
	}
	for _, q := range r.queue {
		if q.Command.UUID != resp.CommandUUID || q.State.Terminal() {
			continue
		}
		q.Result = resp
		switch resp.Status {
		case mdm.StatusAcknowledged:
			q.State = storage.StateAcknowledged
			q.CompletedAt = now
		case mdm.StatusNotNow:
			q.NotNowCount++
			q.State = storage.StateNotNow
			q.NotNowUntil = now.Add(storage.NotNowBackoff(q.NotNowCount))
		default:
			// Error and CommandFormatError are terminal; the result is kept.
			q.State = storage.StateError
			q.CompletedAt = now
		}
		return nil
	}
	return fmt.Errorf("%w: no open command %s", storage.ErrNotFound, resp.CommandUUID)
}

// Commands implements storage.CommandQueue with offset cursors.
func (s *Store) Commands(_ context.Context, id mdm.EnrollmentID, q storage.CommandQuery, p storage.Page) (storage.Result[storage.QueuedCommand], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return storage.Result[storage.QueuedCommand]{}, err
	}
	var matches []storage.QueuedCommand
	for _, c := range slices.Backward(r.queue) {

		if q.RequestType != "" && c.Command.RequestType != q.RequestType {
			continue
		}
		if len(q.States) > 0 && !containsState(q.States, c.State) {
			continue
		}
		matches = append(matches, *c)
	}
	offset := 0
	if p.Cursor != "" {
		n, err := strconv.Atoi(p.Cursor)
		if err != nil || n < 0 {
			return storage.Result[storage.QueuedCommand]{}, fmt.Errorf("%w: bad cursor %q", storage.ErrInvalid, p.Cursor)
		}
		offset = n
	}
	limit := p.Limit
	if limit <= 0 {
		limit = storage.DefaultPageSize
	}
	var out storage.Result[storage.QueuedCommand]
	if offset >= len(matches) {
		return out, nil
	}
	end := min(offset+limit, len(matches))
	out.Items = matches[offset:end]
	if end < len(matches) {
		out.NextCursor = strconv.Itoa(end)
	}
	return out, nil
}

func containsState(states []storage.State, s storage.State) bool {
	return slices.Contains(states, s)
}

// Clear implements storage.CommandQueue.
func (s *Store) Clear(_ context.Context, id mdm.EnrollmentID, f storage.ClearFilter) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return 0, err
	}
	var n int64
	now := time.Now()
	for _, q := range r.queue {
		if q.State.Terminal() {
			continue
		}
		if len(f.States) > 0 && !containsState(f.States, q.State) {
			continue
		}
		if f.RequestType != "" && q.Command.RequestType != f.RequestType {
			continue
		}
		if !f.Before.IsZero() && !q.EnqueuedAt.Before(f.Before) {
			continue
		}
		q.State = storage.StateCleared
		q.CompletedAt = now
		n++
	}
	return n, nil
}

// PushInfo implements storage.PushStore.
func (s *Store) PushInfo(_ context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]mdm.Push, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[mdm.EnrollmentID]mdm.Push{}
	for _, id := range ids {
		r, ok := s.enrollments[id.ID]
		if !ok || !r.Enabled || !r.Push.Valid() {
			continue
		}
		out[id] = mdm.Push{Topic: r.Push.Topic, Token: append([]byte(nil), r.Push.Token...), Magic: r.Push.Magic}
	}
	return out, nil
}

// AssociateCert implements storage.CertAuthStore.
func (s *Store) AssociateCert(_ context.Context, id mdm.EnrollmentID, hash string, at time.Time) error {
	if hash == "" {
		return fmt.Errorf("%w: empty certificate hash", storage.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := id.Device()
	r, err := s.get(dev)
	if err != nil {
		return err
	}
	if owner, ok := s.certs[hash]; ok && owner.ID != dev.ID {
		return fmt.Errorf("%w: certificate already associated with %s", storage.ErrConflict, owner.ID)
	}
	s.dropCertLocked(dev.ID)
	s.certs[hash] = dev
	r.CertHash = hash
	r.CertHashAt = at
	s.recordAssociationLocked(dev, hash, at)
	return nil
}

// recordAssociationLocked appends to the history unless the pair is
// already there, keeping the first-seen time.
func (s *Store) recordAssociationLocked(dev mdm.EnrollmentID, hash string, at time.Time) {
	for _, a := range s.history {
		if a.ID.ID == dev.ID && a.Hash == hash {
			return
		}
	}
	s.history = append(s.history, storage.CertAssociation{ID: dev, Hash: hash, At: at})
}

func (s *Store) historyLocked(match func(storage.CertAssociation) bool) []storage.CertAssociation {
	out := []storage.CertAssociation{}
	for _, a := range s.history {
		if match(a) {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		if out[i].Hash != out[j].Hash {
			return out[i].Hash < out[j].Hash
		}
		return out[i].ID.ID < out[j].ID.ID
	})
	return out
}

// CertHistory implements storage.CertAuthStore.
func (s *Store) CertHistory(_ context.Context, id mdm.EnrollmentID) ([]storage.CertAssociation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := id.Device()
	if _, err := s.get(dev); err != nil {
		return nil, err
	}
	return s.historyLocked(func(a storage.CertAssociation) bool { return a.ID.ID == dev.ID }), nil
}

// CertHashHistory implements storage.CertAuthStore.
func (s *Store) CertHashHistory(_ context.Context, hash string) ([]storage.CertAssociation, error) {
	if hash == "" {
		return nil, fmt.Errorf("%w: empty certificate hash", storage.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.historyLocked(func(a storage.CertAssociation) bool { return a.Hash == hash }), nil
}

// CertHash implements storage.CertAuthStore.
func (s *Store) CertHash(_ context.Context, id mdm.EnrollmentID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id.Device())
	if err != nil {
		return "", err
	}
	return r.CertHash, nil
}

// EnrollmentByCertHash implements storage.CertAuthStore.
func (s *Store) EnrollmentByCertHash(_ context.Context, hash string) (mdm.EnrollmentID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.certs[hash]
	if !ok {
		return mdm.EnrollmentID{}, fmt.Errorf("%w: certificate hash", storage.ErrNotFound)
	}
	return id, nil
}

// StoreBootstrapToken implements storage.BootstrapTokenStore.
func (s *Store) StoreBootstrapToken(_ context.Context, id mdm.EnrollmentID, token []byte, at time.Time) error {
	if len(token) == 0 {
		return fmt.Errorf("%w: empty bootstrap token", storage.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := id.Device()
	r, err := s.get(dev)
	if err != nil {
		return err
	}
	s.bootstrap[dev.ID] = append([]byte(nil), token...)
	r.BootstrapTokenAt = at
	return nil
}

// BootstrapToken implements storage.BootstrapTokenStore.
func (s *Store) BootstrapToken(_ context.Context, id mdm.EnrollmentID) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := id.Device()
	if _, err := s.get(dev); err != nil {
		return nil, err
	}
	tok, ok := s.bootstrap[dev.ID]
	if !ok {
		return nil, fmt.Errorf("%w: bootstrap token for %s", storage.ErrNotFound, dev.ID)
	}
	return append([]byte(nil), tok...), nil
}
