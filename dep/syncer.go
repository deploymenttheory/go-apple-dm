package dep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
)

// Defaults for SyncerConfig.
const (
	DefaultSyncInterval = 30 * time.Minute
	// DefaultMaxCursorAge is Apple's documented cursor lifetime: a sync
	// cursor older than 7 days answers EXPIRED_CURSOR.
	DefaultMaxCursorAge    = 7 * 24 * time.Hour
	DefaultSyncMaxAttempts = 5
	DefaultSyncBackoffBase = 2 * time.Second
	DefaultSyncBackoffMax  = 5 * time.Minute
)

// ErrSync wraps syncer failures that are neither service errors nor
// sentinels: store failures during a page commit.
var ErrSync = errors.New("dep: sync")

// SyncerConfig configures NewSyncer. Client, Store, and Account are
// required.
type SyncerConfig struct {
	Client  *Client
	Store   Store
	Account string
	Clock   clock.Clock
	Bus     *event.Bus
	Logger  *slog.Logger
	// Interval is how often Run syncs without a SyncNow. Default 30m.
	Interval time.Duration
	// Limit is the page size; 0 takes it from the account detail, falling
	// back to FallbackLimit.
	Limit int
	// MaxCursorAge discards a persisted cursor before the first call.
	// Default 7 days.
	MaxCursorAge time.Duration
	// MaxAttempts bounds retries of one call on a transient error.
	// Default 5.
	MaxAttempts int
	// Backoff spaces transient retries and failed runs. Default 2s doubling
	// to 5m with jitter.
	Backoff Backoff
	// Wake is called after every committed page, for the assigner's Kick.
	Wake func()
}

// Syncer keeps one account's device list current: it fetches the full
// list page by page, then syncs changes from the cursor, committing each
// page with its cursor in one transaction and publishing one event per
// device after the commit (decision record 0026).
type Syncer struct {
	cfg  SyncerConfig
	kick chan struct{}
}

// SyncResult counts what one run did.
type SyncResult struct {
	Pages    int
	Added    int
	Modified int
	Deleted  int
	// Restarted is set when an expired, invalid, or missing cursor forced a
	// full fetch.
	Restarted bool
	// Phase is the phase the cursor was left in.
	Phase Phase
}

// NewSyncer validates the configuration and applies defaults.
func NewSyncer(cfg SyncerConfig) (*Syncer, error) {
	if cfg.Client == nil || cfg.Store == nil || cfg.Account == "" {
		return nil, fmt.Errorf("%w: syncer needs Client, Store, and Account", ErrConfig)
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultSyncInterval
	}
	if cfg.MaxCursorAge <= 0 {
		cfg.MaxCursorAge = DefaultMaxCursorAge
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultSyncMaxAttempts
	}
	cfg.Backoff = cfg.Backoff.withDefaults(DefaultSyncBackoffBase, DefaultSyncBackoffMax)
	return &Syncer{cfg: cfg, kick: make(chan struct{}, 1)}, nil
}

// SyncNow wakes Run so the next run happens now. It never blocks.
func (s *Syncer) SyncNow() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run syncs until ctx is cancelled: every Interval, on SyncNow, or after
// a backoff when the previous run failed.
func (s *Syncer) Run(ctx context.Context) error {
	failures := 0
	for {
		wait := s.cfg.Interval
		if _, err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
			failures++
			wait = s.cfg.Backoff.Delay(failures)
			s.cfg.Logger.WarnContext(ctx, "dep: sync failed", "account", s.cfg.Account, "attempt", failures, "retry_in", wait, "error", err)
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.kick:
		case <-s.cfg.Clock.After(wait):
		}
	}
}

// RunOnce performs one complete pass: fetch pages until the list is
// complete, then sync pages until none follow. A cursor older than
// MaxCursorAge is discarded first; EXPIRED_CURSOR, INVALID_CURSOR, and
// CURSOR_REQUIRED restart the fetch once; EXHAUSTED_CURSOR ends the fetch
// phase or an empty sync; a repeated cursor with more_to_follow is
// ErrSameCursor; transient errors retry inside the run.
func (s *Syncer) RunOnce(ctx context.Context) (SyncResult, error) {
	var res SyncResult
	acct, err := s.cfg.Store.GetAccount(ctx, s.cfg.Account)
	if err != nil {
		return res, err
	}
	cur, err := s.cfg.Store.Cursor(ctx, s.cfg.Account)
	if err != nil {
		return res, err
	}
	if age := s.cfg.Clock.Now().Sub(cur.UpdatedAt); !cur.IsZero() && age > s.cfg.MaxCursorAge {
		s.cfg.Logger.InfoContext(ctx, "dep: cursor stale, fetching", "account", s.cfg.Account, "age", age)
		cur = Cursor{}
		res.Restarted = true
	}
	if cur.Value == "" || cur.Phase == "" {
		cur = Cursor{Phase: PhaseFetch}
	}
	restarted := false
	for {
		res.Phase = cur.Phase
		page, err := s.call(ctx, acct, cur)
		if err != nil {
			switch {
			case codeIs(err, CodeExhaustedCursor) && cur.Phase == PhaseFetch:
				cur.Phase = PhaseSync
				if err := s.cfg.Store.SetCursor(ctx, s.cfg.Account, cur); err != nil {
					return res, err
				}
				continue
			case codeIs(err, CodeExhaustedCursor):
				return res, nil
			case codeIs(err, CodeExpiredCursor), codeIs(err, CodeInvalidCursor), codeIs(err, CodeCursorRequired):
				if restarted {
					return res, err
				}
				restarted, res.Restarted = true, true
				s.cfg.Logger.InfoContext(ctx, "dep: cursor rejected, fetching", "account", s.cfg.Account, "error", err)
				cur = Cursor{Phase: PhaseFetch}
				continue
			default:
				return res, err
			}
		}
		if page.Cursor == "" {
			return res, fmt.Errorf("%w: page without a cursor", ErrInvalid)
		}
		if page.MoreToFollow && page.Cursor == cur.Value {
			return res, fmt.Errorf("%w: %q", ErrSameCursor, page.Cursor)
		}
		next := Cursor{Value: page.Cursor, Phase: PhaseSync, FetchedUntil: page.FetchedUntil, UpdatedAt: s.cfg.Clock.Now()}
		if cur.Phase == PhaseFetch && page.MoreToFollow {
			next.Phase = PhaseFetch
		}
		added, modified, deleted, err := s.commit(ctx, cur.Phase, page, next)
		if err != nil {
			return res, err
		}
		res.Pages++
		res.Added += added
		res.Modified += modified
		res.Deleted += deleted
		if s.cfg.Wake != nil {
			s.cfg.Wake()
		}
		done := cur.Phase == PhaseSync && !page.MoreToFollow
		cur = next
		res.Phase = cur.Phase
		if done {
			return res, nil
		}
	}
}

// call performs one fetch or sync request, retrying transient failures
// with backoff and honouring Retry-After.
func (s *Syncer) call(ctx context.Context, acct *Account, cur Cursor) (*DevicePage, error) {
	for attempt := 1; ; attempt++ {
		var page *DevicePage
		var err error
		if cur.Phase == PhaseFetch {
			page, err = s.cfg.Client.FetchDevices(ctx, s.cfg.Account, cur.Value, s.limit(acct, PathFetchDevices))
		} else {
			page, err = s.cfg.Client.SyncDevices(ctx, s.cfg.Account, cur.Value, s.limit(acct, PathSyncDevices))
		}
		if err == nil {
			return page, nil
		}
		if !transient(err) || attempt >= s.cfg.MaxAttempts {
			return nil, err
		}
		delay := s.cfg.Backoff.Delay(attempt)
		if ra := retryAfter(err); ra > delay {
			delay = ra
		}
		s.cfg.Logger.WarnContext(ctx, "dep: transient error, retrying", "account", s.cfg.Account, "attempt", attempt, "retry_in", delay, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.cfg.Clock.After(delay):
		}
	}
}

// limit picks the page size for an endpoint.
func (s *Syncer) limit(acct *Account, uri string) int {
	if s.cfg.Limit > 0 {
		return s.cfg.Limit
	}
	return acct.Limit(uri, FallbackLimit)
}

// transient reports whether err is worth retrying within seconds: a
// network failure or a 408, 429, or 5xx answer. Sentinels, other 4xx
// answers, and cancellations are not.
func transient(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Status == http.StatusTooManyRequests || e.Status == http.StatusRequestTimeout || e.Status >= 500
	}
	for _, sentinel := range []error{ErrTokenExpired, ErrTokenInvalid, ErrTermsNotSigned, ErrNoTokens, ErrNotFound, ErrInvalid, ErrBodyTooLarge, ErrConfig, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, sentinel) {
			return false
		}
	}
	return true
}

// retryAfter returns the Retry-After of a *Error, or 0.
func retryAfter(err error) time.Duration {
	var e *Error
	if errors.As(err, &e) {
		return e.RetryAfter
	}
	return 0
}

// commit deduplicates the page, writes it with the next cursor in one
// transaction, and publishes one event per device afterwards, so a page
// that fails to commit is re-requested with the same cursor and produces
// no duplicate events.
func (s *Syncer) commit(ctx context.Context, phase Phase, page *DevicePage, next Cursor) (added, modified, deleted int, err error) {
	devs := Dedupe(page.Devices)
	now := s.cfg.Clock.Now()
	var evs []event.Event
	err = s.cfg.Store.Update(ctx, func(tx Tx) error {
		evs = evs[:0]
		for i := range devs {
			d := &devs[i]
			d.normalise()
			typ, err := s.eventType(ctx, tx, d)
			if err != nil {
				return err
			}
			evs = append(evs, event.Event{Type: typ, At: now, Actor: Actor, Data: DeviceEvent{Account: s.cfg.Account, Device: d.Clone(), Phase: phase}})
		}
		if err := tx.PutDevices(ctx, s.cfg.Account, devs, now); err != nil {
			return err
		}
		return tx.SetCursor(ctx, s.cfg.Account, next)
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: commit page: %w", ErrSync, err)
	}
	for _, ev := range evs {
		switch ev.Type {
		case EventDeviceAdded:
			added++
		case EventDeviceDeleted:
			deleted++
		default:
			modified++
		}
		if s.cfg.Bus == nil {
			continue
		}
		if err := s.cfg.Bus.Publish(ctx, ev); err != nil {
			s.cfg.Logger.WarnContext(ctx, "dep: publish", "type", string(ev.Type), "error", err)
		}
	}
	return added, modified, deleted, nil
}

// eventType maps a record to its event: sync ops by op_type, fetch
// records by whether the store already holds the serial.
func (s *Syncer) eventType(ctx context.Context, tx Tx, d *Device) (event.Type, error) {
	switch d.OpType {
	case OpAdded:
		return EventDeviceAdded, nil
	case OpDeleted:
		return EventDeviceDeleted, nil
	case OpModified:
		return EventDeviceModified, nil
	}
	existing, err := tx.GetDevice(ctx, s.cfg.Account, d.SerialNumber)
	switch {
	case errors.Is(err, ErrNotFound):
		return EventDeviceAdded, nil
	case err != nil:
		return "", err
	case existing.Deleted:
		return EventDeviceAdded, nil
	default:
		return EventDeviceModified, nil
	}
}

// Dedupe collapses repeated serials on one page, keeping the record with
// the latest op_date; on a tie a deleted op wins, otherwise the later
// record on the page (Apple sorts pages chronologically).
func Dedupe(devs []Device) []Device {
	index := make(map[string]int, len(devs))
	out := make([]Device, 0, len(devs))
	for _, d := range devs {
		i, seen := index[d.SerialNumber]
		if !seen {
			index[d.SerialNumber] = len(out)
			out = append(out, d)
			continue
		}
		if replaces(d, out[i]) {
			out[i] = d
		}
	}
	return out
}

// replaces reports whether the later record a should replace b.
func replaces(a, b Device) bool {
	at, bt := opDate(a), opDate(b)
	switch {
	case at.After(bt):
		return true
	case bt.After(at):
		return false
	default:
		return !(b.OpType == OpDeleted && a.OpType != OpDeleted)
	}
}

func opDate(d Device) time.Time {
	if d.OpDate == nil {
		return time.Time{}
	}
	return *d.OpDate
}
