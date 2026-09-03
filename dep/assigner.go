package dep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// Defaults for AssignerConfig.
const (
	DefaultAssignInterval  = 15 * time.Minute
	DefaultAssignBatch     = 1000
	DefaultAssignPage      = 500
	DefaultNotAccessible   = time.Hour
	DefaultFailedBase      = 15 * time.Minute
	DefaultAssignMax       = 24 * time.Hour
	DefaultThrottleDelay   = 5 * time.Minute
	DefaultAccountBackoff  = time.Minute
	DefaultAccountBackMax  = time.Hour
	assignmentMissingError = "serial missing from the assign response"
)

// AssignerConfig configures NewAssigner. Client, Store, and Account are
// required.
type AssignerConfig struct {
	Client  *Client
	Store   Store
	Account string
	Clock   clock.Clock
	Bus     *event.Bus
	Logger  *slog.Logger
	// Filter keeps a device eligible when it returns true; nil keeps all.
	Filter func(Device) bool
	// BatchSize bounds serials per AssignProfile call. Default 1000, the
	// most Apple advises.
	BatchSize int
	// PageSize is the store page size when scanning devices. Default 500.
	PageSize int
	// Interval is how often Run assigns without a Kick. Default 15m.
	Interval time.Duration
	// NotAccessibleBackoff and FailedBackoff space retries of those
	// outcomes per serial. Defaults 1h and 15m, doubling to 24h with jitter.
	NotAccessibleBackoff Backoff
	FailedBackoff        Backoff
	// ThrottleDelay applies to THROTTLED when the response carries no
	// retry_after_seconds. Default 5m.
	ThrottleDelay time.Duration
	// AccountBackoff spaces runs after HTTP 429 without Retry-After.
	// Default 1m doubling to 1h.
	AccountBackoff Backoff
	// ReadBack refreshes assigned devices through DeviceDetails so the
	// stored record shows what Apple holds rather than what was requested.
	ReadBack bool
	// UsePUT sends the assignment as PUT (WithAssignPUT).
	UsePUT bool
}

// Assigner keeps every eligible device of one account on the account's
// current profile. Work is computed from state, not from op_type: a
// device whose profile_uuid differs from the account's profile or whose
// profile_status is empty or removed is assigned, after every synced
// page and after a full re-fetch alike (decision record 0026).
type Assigner struct {
	cfg  AssignerConfig
	kick chan struct{}

	notBefore time.Time
	failures  int
}

// AssignResult counts what one run did.
type AssignResult struct {
	Candidates    int
	Assigned      int
	Failed        int
	NotAccessible int
	Throttled     int
	// Deferred counts candidates whose next attempt is in the future.
	Deferred int
	// NotBefore is set when the account is backing off.
	NotBefore time.Time
}

// NewAssigner validates the configuration and applies defaults.
func NewAssigner(cfg AssignerConfig) (*Assigner, error) {
	if cfg.Client == nil || cfg.Store == nil || cfg.Account == "" {
		return nil, fmt.Errorf("%w: assigner needs Client, Store, and Account", ErrConfig)
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultAssignBatch
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = DefaultAssignPage
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultAssignInterval
	}
	if cfg.ThrottleDelay <= 0 {
		cfg.ThrottleDelay = DefaultThrottleDelay
	}
	cfg.NotAccessibleBackoff = cfg.NotAccessibleBackoff.withDefaults(DefaultNotAccessible, DefaultAssignMax)
	cfg.FailedBackoff = cfg.FailedBackoff.withDefaults(DefaultFailedBase, DefaultAssignMax)
	cfg.AccountBackoff = cfg.AccountBackoff.withDefaults(DefaultAccountBackoff, DefaultAccountBackMax)
	return &Assigner{cfg: cfg, kick: make(chan struct{}, 1)}, nil
}

// Kick wakes Run so the next run happens now. It never blocks; suitable
// for SyncerConfig.Wake.
func (a *Assigner) Kick() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// Run assigns until ctx is cancelled: every Interval, on Kick, or when an
// account backoff ends.
func (a *Assigner) Run(ctx context.Context) error {
	for {
		wait := a.cfg.Interval
		res, err := a.RunOnce(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
		case errors.Is(err, ErrBackoff):
			if d := res.NotBefore.Sub(a.cfg.Clock.Now()); d < wait {
				wait = d
			}
		case err != nil:
			a.cfg.Logger.WarnContext(ctx, "dep: assign failed", "account", a.cfg.Account, "error", err)
			if !res.NotBefore.IsZero() {
				if d := res.NotBefore.Sub(a.cfg.Clock.Now()); d < wait {
					wait = d
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.kick:
		case <-a.cfg.Clock.After(wait):
		}
	}
}

// RunOnce assigns every eligible device once. It returns ErrBackoff with
// NotBefore set while the account is backing off after HTTP 429, and the
// service error of a failed batch (earlier batches stay recorded).
func (a *Assigner) RunOnce(ctx context.Context) (AssignResult, error) {
	var res AssignResult
	now := a.cfg.Clock.Now()
	if now.Before(a.notBefore) {
		res.NotBefore = a.notBefore
		return res, fmt.Errorf("%w: until %s", ErrBackoff, a.notBefore.UTC().Format(time.RFC3339))
	}
	acct, err := a.cfg.Store.GetAccount(ctx, a.cfg.Account)
	if err != nil {
		return res, err
	}
	if acct.ProfileUUID == "" {
		return res, nil
	}
	serials, err := a.candidates(ctx, acct.ProfileUUID, now, &res)
	if err != nil {
		return res, err
	}
	var opts []AssignOption
	if a.cfg.UsePUT {
		opts = append(opts, WithAssignPUT())
	}
	for batch := range slices.Chunk(serials, a.cfg.BatchSize) {
		resp, err := a.cfg.Client.AssignProfile(ctx, a.cfg.Account, acct.ProfileUUID, batch, opts...)
		if err != nil {
			if statusIs(err, http.StatusTooManyRequests) {
				a.failures++
				// Retry-After is authoritative when Apple sends it; the
				// account backoff covers a bare 429.
				delay := a.cfg.AccountBackoff.Delay(a.failures)
				if ra := retryAfter(err); ra > 0 {
					delay = ra
				}
				a.notBefore = a.cfg.Clock.Now().Add(delay)
				res.NotBefore = a.notBefore
			}
			return res, err
		}
		if err := a.record(ctx, acct.ProfileUUID, batch, resp, &res); err != nil {
			return res, err
		}
	}
	a.failures = 0
	return res, nil
}

// candidates scans the account's live devices for the ones to assign.
func (a *Assigner) candidates(ctx context.Context, profileUUID string, now time.Time, res *AssignResult) ([]string, error) {
	var serials []string
	page := storage.Page{Limit: a.cfg.PageSize}
	for {
		r, err := a.cfg.Store.ListDevices(ctx, a.cfg.Account, DeviceQuery{}, page)
		if err != nil {
			return nil, err
		}
		for _, sd := range r.Items {
			if !NeedsAssignment(sd.Device, profileUUID) || (a.cfg.Filter != nil && !a.cfg.Filter(sd.Device)) {
				continue
			}
			state, err := a.state(ctx, &sd, profileUUID, now)
			if err != nil {
				return nil, err
			}
			if state == assignSatisfied {
				// A recorded SUCCESS for this profile that Apple has not
				// contradicted since: not a candidate at all.
				continue
			}
			res.Candidates++
			if state == assignDeferred {
				res.Deferred++
				continue
			}
			serials = append(serials, sd.SerialNumber)
		}
		if r.NextCursor == "" {
			return serials, nil
		}
		page.Cursor = r.NextCursor
	}
}

// NeedsAssignment reports whether the device is off the account's
// profile: its profile_uuid differs, or its profile_status is empty or
// removed.
func NeedsAssignment(d Device, profileUUID string) bool {
	return d.ProfileUUID != profileUUID || d.ProfileStatus == ProfileStatusEmpty || d.ProfileStatus == ProfileStatusRemoved
}

type assignState int

const (
	assignDue assignState = iota
	assignDeferred
	assignSatisfied
)

// state consults the recorded outcome: a future NextAttemptAt defers the
// serial, and a SUCCESS for this profile newer than the device record is
// trusted until Apple reports the device again.
func (a *Assigner) state(ctx context.Context, sd *StoredDevice, profileUUID string, now time.Time) (assignState, error) {
	asg, err := a.cfg.Store.GetAssignment(ctx, a.cfg.Account, sd.SerialNumber)
	if errors.Is(err, ErrNotFound) {
		return assignDue, nil
	}
	if err != nil {
		return assignDue, err
	}
	if !asg.NextAttemptAt.IsZero() && now.Before(asg.NextAttemptAt) {
		return assignDeferred, nil
	}
	if asg.Status == StatusSuccess && asg.ProfileUUID == profileUUID && !sd.UpdatedAt.After(asg.AttemptedAt) {
		return assignSatisfied, nil
	}
	return assignDue, nil
}

// record writes the per-serial outcome of one batch, schedules retries,
// reads successes back when configured, and publishes EventDeviceAssigned.
func (a *Assigner) record(ctx context.Context, profileUUID string, batch []string, resp *AssignResponse, res *AssignResult) error {
	now := a.cfg.Clock.Now()
	var success []string
	var events []event.Event
	err := a.cfg.Store.Update(ctx, func(tx Tx) error {
		success, events = success[:0], events[:0]
		for _, serial := range batch {
			asg, err := tx.GetAssignment(ctx, a.cfg.Account, serial)
			if errors.Is(err, ErrNotFound) {
				asg = &Assignment{Account: a.cfg.Account, SerialNumber: serial}
			} else if err != nil {
				return err
			}
			status := resp.Devices[serial]
			a.outcome(asg, status, profileUUID, resp.RetryAfterSeconds, now, res)
			if err := tx.PutAssignment(ctx, asg); err != nil {
				return err
			}
			if asg.Status == StatusSuccess {
				success = append(success, serial)
				events = append(events, event.Event{Type: EventDeviceAssigned, At: now, Actor: Actor, Data: AssignmentEvent{Account: a.cfg.Account, Assignment: *asg}})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, ev := range events {
		if a.cfg.Bus == nil {
			break
		}
		if err := a.cfg.Bus.Publish(ctx, ev); err != nil {
			a.cfg.Logger.WarnContext(ctx, "dep: publish", "type", string(ev.Type), "error", err)
		}
	}
	if a.cfg.ReadBack {
		// Every serial of the batch: Apple's profile_status is the truth
		// for NOT_ACCESSIBLE and FAILED devices too.
		return a.readBack(ctx, batch)
	}
	return nil
}

// outcome applies one response value to the assignment record.
func (a *Assigner) outcome(asg *Assignment, status, profileUUID string, retryAfterSeconds int, now time.Time, res *AssignResult) {
	asg.ProfileUUID = profileUUID
	asg.AttemptedAt = now
	asg.LastError = ""
	switch status {
	case StatusSuccess:
		asg.Status = StatusSuccess
		asg.Attempts = 0
		asg.NextAttemptAt = time.Time{}
		res.Assigned++
		return
	case StatusThrottled:
		asg.Status = StatusThrottled
		asg.Attempts++
		delay := time.Duration(retryAfterSeconds) * time.Second
		if delay <= 0 {
			delay = a.cfg.ThrottleDelay
		}
		asg.NextAttemptAt = now.Add(delay)
		res.Throttled++
	case StatusNotAccessible:
		asg.Status = StatusNotAccessible
		asg.Attempts++
		asg.NextAttemptAt = now.Add(a.cfg.NotAccessibleBackoff.Delay(asg.Attempts))
		res.NotAccessible++
	default:
		asg.Status = StatusFailed
		asg.Attempts++
		asg.NextAttemptAt = now.Add(a.cfg.FailedBackoff.Delay(asg.Attempts))
		if status == "" {
			asg.LastError = assignmentMissingError
		} else {
			asg.LastError = status
		}
		res.Failed++
	}
}

// readBack refreshes the stored records of the serials from
// DeviceDetails, keeping the op fields the details endpoint does not
// carry.
func (a *Assigner) readBack(ctx context.Context, serials []string) error {
	details, err := a.cfg.Client.DeviceDetails(ctx, a.cfg.Account, serials)
	if err != nil {
		return err
	}
	now := a.cfg.Clock.Now()
	return a.cfg.Store.Update(ctx, func(tx Tx) error {
		devs := make([]Device, 0, len(details))
		for _, serial := range serials {
			d, ok := details[serial]
			if !ok || d.ResponseStatus == StatusNotAccessible {
				continue
			}
			existing, err := tx.GetDevice(ctx, a.cfg.Account, serial)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if existing != nil {
				d.OpType, d.OpDate = existing.OpType, existing.OpDate
			}
			d.ResponseStatus = ""
			devs = append(devs, d)
		}
		if len(devs) == 0 {
			return nil
		}
		return tx.PutDevices(ctx, a.cfg.Account, devs, now)
	})
}
