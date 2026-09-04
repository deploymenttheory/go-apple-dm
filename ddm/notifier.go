package ddm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/push"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// DefaultDedupeKey is the dedupe key NotifierConfig.DedupeKey uses when it is
// not set. It suppresses a second DeclarativeManagement while one is still
// pending for the same enrollment.
//
// Suppression is safe for this command specifically, and only for it: the
// command is a doorbell, not a payload. A device that receives it fetches the
// current tokens and declaration items, so a pending command already carries
// every change made since it was queued. It would not be safe for a command
// whose payload is the instruction, which is why the key is opt-in per caller
// rather than a property of the queue.
const DefaultDedupeKey = "ddm"

// Defaults for NotifierConfig.
const (
	DefaultNotifyWindow = 2 * time.Second
	DefaultNotifyPoll   = time.Second
	DefaultNotifyBatch  = 500
)

// Enqueuer queues a command for enrollments; *service.Core satisfies it.
type Enqueuer interface {
	Enqueue(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, o storage.EnqueueOptions) (storage.EnqueueResult, error)
}

// Pusher sends APNs wake-ups; *pushnotify.Notifier satisfies it.
type Pusher interface {
	Notify(ctx context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]push.Result, error)
}

// TokenSource renders an enrollment's TokensResponse; *Engine satisfies it.
type TokenSource interface {
	Tokens(ctx context.Context, id mdm.EnrollmentID) ([]byte, error)
}

// NotifierConfig configures NewNotifier. Store, Tokens, and Enqueuer are
// required; Pusher, Bus, and Logger are optional.
type NotifierConfig struct {
	Store    ChangeStore
	Tokens   TokenSource
	Enqueuer Enqueuer
	Pusher   Pusher
	Bus      *event.Bus
	Clock    clock.Clock
	Logger   *slog.Logger
	// Window defers an enrollment while its newest change is younger than
	// this, so a burst of uploads becomes one command. Default 2s.
	Window time.Duration
	// Poll is how often Run drains without a Kick. Default 1s.
	Poll time.Duration
	// Batch bounds the change rows read per drain. Default 500.
	Batch int
	// Backoff maps the attempt count to the retry delay. Default
	// storage.NotNowBackoff.
	Backoff func(attempt int) time.Duration
	// DedupeKey suppresses a new DeclarativeManagement while one is still
	// pending for the same enrollment. Nil uses DefaultDedupeKey; an empty
	// string turns suppression off, so every drain queues a command.
	//
	// It is a pointer for the same reason service.Config.ValidateTargets is:
	// the zero value has to mean "not set" so that "" can mean "off".
	// Whether to suppress is the consumer's decision, not this package's.
	DedupeKey *string
	// OnDrain, when set, is called with the outcome of every drain. A
	// suppressed command is counted in DrainResult.Deduped and is otherwise
	// invisible, which is the whole problem with suppression: it has to be
	// observable to be defensible.
	OnDrain func(ctx context.Context, res DrainResult)
}

// Notifier turns committed change rows into DeclarativeManagement commands
// and pushes (decision record 0022).
type Notifier struct {
	cfg  NotifierConfig
	kick chan struct{}
}

// ErrNotifierConfig reports a missing required dependency.
var ErrNotifierConfig = errors.New("ddm: notifier needs Store, Tokens, and Enqueuer")

// NewNotifier validates the configuration and applies defaults.
func NewNotifier(cfg NotifierConfig) (*Notifier, error) {
	if cfg.Store == nil || cfg.Tokens == nil || cfg.Enqueuer == nil {
		return nil, ErrNotifierConfig
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultNotifyWindow
	}
	if cfg.Poll <= 0 {
		cfg.Poll = DefaultNotifyPoll
	}
	if cfg.Batch <= 0 {
		cfg.Batch = DefaultNotifyBatch
	}
	if cfg.Backoff == nil {
		cfg.Backoff = storage.NotNowBackoff
	}
	if cfg.DedupeKey == nil {
		k := DefaultDedupeKey
		cfg.DedupeKey = &k
	}
	return &Notifier{cfg: cfg, kick: make(chan struct{}, 1)}, nil
}

// Kick wakes Run so the next drain happens now rather than at the next
// poll. It never blocks; suitable for Engine Config.Wake.
func (n *Notifier) Kick() {
	select {
	case n.kick <- struct{}{}:
	default:
	}
}

// Run drains until ctx is cancelled, every Poll or on Kick. Drain errors
// are logged and retried at the next tick.
func (n *Notifier) Run(ctx context.Context) error {
	for {
		res, err := n.DrainOnce(ctx)
		if err != nil && ctx.Err() == nil {
			n.cfg.Logger.WarnContext(ctx, "ddm: notifier drain", "error", err)
		}
		n.report(ctx, res)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-n.kick:
		case <-n.cfg.Clock.After(n.cfg.Poll):
		}
	}
}

// report makes a drain's outcome visible. Run used to discard the result, so
// a suppressed or skipped command left no trace anywhere and an operator
// asking why a device was never woken had nothing to read.
func (n *Notifier) report(ctx context.Context, res DrainResult) {
	if n.cfg.OnDrain != nil {
		n.cfg.OnDrain(ctx, res)
	}
	if res.Empty() {
		return
	}
	n.cfg.Logger.DebugContext(ctx, "ddm: notifier drain",
		"queued", res.Queued, "deduped", res.Deduped, "skipped", res.Skipped,
		"deferred", res.Deferred, "failed", res.Failed, "pushed", res.Pushed)
}

// DrainResult counts what one drain did.// DrainResult counts what one drain did.
type DrainResult struct {
	// Deferred enrollments were left for a later drain because a change
	// arrived within Window.
	Deferred int
	// Queued enrollments received a new command; Deduped ones already had
	// one pending and were pushed anyway; Skipped ones cannot be
	// commanded (disabled or unknown) and were completed without a push.
	Queued, Deduped, Skipped int
	// Failed enrollments had their change rows scheduled for a retry.
	Failed int
	// Pushed counts enrollments handed to the Pusher.
	Pushed int
}

// Empty reports a drain that did nothing, so a caller can skip reporting it.
func (r DrainResult) Empty() bool {
	return r == DrainResult{}
}

type changeGroup struct {
	id     mdm.EnrollmentID
	seqs   []int64
	rows   []Change
	newest time.Time
	tries  int
	push   bool
}

// DrainOnce processes the due change rows once. Store failures are returned;
// per-enrollment failures are recorded on the rows with backoff.
func (n *Notifier) DrainOnce(ctx context.Context) (DrainResult, error) {
	var res DrainResult
	now := n.cfg.Clock.Now()
	rows, err := n.cfg.Store.PendingChanges(ctx, now, n.cfg.Batch)
	if err != nil {
		return res, fmt.Errorf("%w: pending: %w", ErrNotifier, err)
	}
	groups := groupChanges(rows)
	var ready []*changeGroup
	for _, g := range groups {
		if now.Sub(g.newest) < n.cfg.Window {
			res.Deferred++
			continue
		}
		ready = append(ready, g)
	}
	var toPush []*changeGroup
	var done []*changeGroup
	for _, g := range ready {
		outcome, err := n.command(ctx, g, now)
		if err != nil {
			res.Failed++
			if ferr := n.fail(ctx, g, err, now); ferr != nil {
				return res, ferr
			}
			continue
		}
		switch outcome {
		case outcomeQueued:
			res.Queued++
			g.push = true
		case outcomeDeduped:
			res.Deduped++
			g.push = true
		case outcomeSkipped:
			res.Skipped++
		}
		if g.push {
			toPush = append(toPush, g)
		} else {
			done = append(done, g)
		}
	}
	pushed, failed, err := n.push(ctx, toPush, now)
	if err != nil {
		return res, err
	}
	res.Pushed = len(pushed)
	res.Failed += failed
	done = append(done, pushed...)
	if err := n.complete(ctx, done); err != nil {
		return res, err
	}
	return res, nil
}

type commandOutcome int

const (
	outcomeQueued commandOutcome = iota
	outcomeDeduped
	outcomeSkipped
)

// command enqueues one DeclarativeManagement command carrying the
// enrollment's current tokens.
func (n *Notifier) command(ctx context.Context, g *changeGroup, now time.Time) (commandOutcome, error) {
	tokens, err := n.cfg.Tokens.Tokens(ctx, g.id)
	if err != nil {
		return 0, fmt.Errorf("tokens: %w", err)
	}
	cmd, err := mdm.NewCommand(&commands.DeclarativeManagement{Data: tokens})
	if err != nil {
		return 0, fmt.Errorf("command: %w", err)
	}
	r, err := n.cfg.Enqueuer.Enqueue(ctx, []mdm.EnrollmentID{g.id}, cmd,
		storage.EnqueueOptions{DedupeKey: *n.cfg.DedupeKey, Now: now})
	if err != nil {
		return 0, fmt.Errorf("enqueue: %w", err)
	}
	if skip, ok := r.Skipped[g.id]; ok {
		if errors.Is(skip, storage.ErrConflict) {
			return outcomeDeduped, nil
		}
		n.cfg.Logger.InfoContext(ctx, "ddm: change dropped, enrollment cannot be commanded", "enrollment", g.id.ID, "reason", skip.Error())
		return outcomeSkipped, nil
	}
	return outcomeQueued, nil
}

// push wakes every commanded enrollment once. A whole-batch push error
// fails every group; a per-target error fails that group; a token APNs
// says is dead completes the group, because retrying it can only fail the
// same way and the command waits for the next connect anyway.
//
// A rejected push is not that: APNs refused the request, usually for a
// reason shared by the whole topic, so it retries with back-off and leaves
// the cause on the change. Treating it as delivered would mark a fleet's
// worth of changes done that no device was ever woken for.
func (n *Notifier) push(ctx context.Context, groups []*changeGroup, now time.Time) (pushed []*changeGroup, failed int, err error) {
	if len(groups) == 0 {
		return nil, 0, nil
	}
	if n.cfg.Pusher == nil {
		return groups, 0, nil
	}
	ids := make([]mdm.EnrollmentID, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.id)
	}
	results, perr := n.cfg.Pusher.Notify(ctx, ids)
	for _, g := range groups {
		var cause error
		switch r := results[g.id]; {
		case perr != nil:
			cause = perr
		case r.Err != nil && !r.TokenInvalid():
			cause = r.Err
		}
		if cause == nil {
			pushed = append(pushed, g)
			continue
		}
		failed++
		if ferr := n.fail(ctx, g, fmt.Errorf("push: %w", cause), now); ferr != nil {
			return nil, failed, ferr
		}
	}
	return pushed, failed, nil
}

func (n *Notifier) fail(ctx context.Context, g *changeGroup, cause error, now time.Time) error {
	attempt := g.tries + 1
	next := now.Add(n.cfg.Backoff(attempt))
	n.cfg.Logger.WarnContext(ctx, "ddm: notify failed", "enrollment", g.id.ID, "attempt", attempt, "next", next, "error", cause)
	if err := n.cfg.Store.FailChanges(ctx, g.seqs, cause.Error(), next); err != nil {
		return fmt.Errorf("%w: fail: %w", ErrNotifier, err)
	}
	return nil
}

func (n *Notifier) complete(ctx context.Context, groups []*changeGroup) error {
	if len(groups) == 0 {
		return nil
	}
	var seqs []int64
	for _, g := range groups {
		seqs = append(seqs, g.seqs...)
	}
	if err := n.cfg.Store.CompleteChanges(ctx, seqs); err != nil {
		return fmt.Errorf("%w: complete: %w", ErrNotifier, err)
	}
	if n.cfg.Bus == nil {
		return nil
	}
	for _, g := range groups {
		ev := event.Event{Type: event.DDMChanged, At: n.cfg.Clock.Now(), Enrollment: g.id, Actor: "ddm", Data: g.rows}
		if err := n.cfg.Bus.Publish(ctx, ev); err != nil {
			n.cfg.Logger.WarnContext(ctx, "ddm: publish", "type", string(event.DDMChanged), "error", err)
		}
	}
	return nil
}

// groupChanges groups rows per enrollment in first-seen order.
func groupChanges(rows []Change) []*changeGroup {
	index := map[mdm.EnrollmentID]*changeGroup{}
	var out []*changeGroup
	for _, c := range rows {
		g, ok := index[c.ID]
		if !ok {
			g = &changeGroup{id: c.ID}
			index[c.ID] = g
			out = append(out, g)
		}
		g.seqs = append(g.seqs, c.Seq)
		g.rows = append(g.rows, c)
		if c.CreatedAt.After(g.newest) {
			g.newest = c.CreatedAt
		}
		if c.Attempts > g.tries {
			g.tries = c.Attempts
		}
	}
	return out
}
