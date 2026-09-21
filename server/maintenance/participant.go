package maintenance

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

// Participant covers a process's HTTP handlers and background workers. Setup
// clients register for their entire operation and Close when all writes finish.
type Participant struct {
	store     *Store
	id        string
	mu        sync.Mutex
	accepting bool
	requests  sync.WaitGroup
}

// ID returns the stable identifier assigned when this writer joined the maintenance
// protocol.
func (p *Participant) ID() string { return p.id }

// Wrap rejects new requests during maintenance, including on a control-store
// failure. An admitted request finishes before the participant acknowledges.
func (p *Participant) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		allowed := p.accepting
		if allowed {
			p.requests.Add(1)
		}
		p.mu.Unlock()
		if allowed {
			defer p.requests.Done()
			status, err := p.store.Status(r.Context())
			allowed = err == nil && status.Token == ""
		}
		if !allowed {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "server maintenance", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// admission sets whether the participant accepts new requests while holding its admission
// mutex.
func (p *Participant) admission(accepting bool) {
	p.mu.Lock()
	p.accepting = accepting
	p.mu.Unlock()
}

// Run supervises one worker bundle. A pause closes admission, cancels workers,
// and waits for both workers and admitted HTTP requests before acknowledging.
// The callback must honor cancellation and return only after its writes stop.
func (p *Participant) Run(ctx context.Context, workers func(context.Context) error) error {
	if workers == nil {
		return ErrInvalid
	}
	defer p.admission(false)
	for {
		if err := p.store.acknowledge(ctx, p.id, ""); err != nil {
			if !errors.Is(err, ErrOwner) {
				return err
			}
		} else {
			p.admission(true)
			if err := p.runActive(ctx, workers); err != nil {
				return err
			}
		}
		p.admission(false)
		p.requests.Wait()
		status, err := p.store.Status(ctx)
		if err != nil {
			return err
		}
		if status.Token == "" {
			continue
		}
		if err := p.store.acknowledge(
			ctx,
			p.id,
			status.Token,
		); err != nil &&
			!errors.Is(err, ErrOwner) {
			return err
		}
		for {
			if err := wait(ctx); err != nil {
				return wrap(err)
			}
			next, err := p.store.Status(ctx)
			if err != nil {
				return err
			}
			if next.Token != status.Token {
				break
			}
		}
	}
}

// runActive runs background workers until they stop or maintenance begins, then closes
// admission, cancels workers, and waits for outstanding requests.
func (p *Participant) runActive(
	ctx context.Context,
	workers func(context.Context) error,
) (err error) {
	workCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- workers(workCtx) }()
	joined := false
	defer func() {
		p.admission(false)
		cancel()
		if !joined {
			workerErr := <-done
			if !errors.Is(workerErr, context.Canceled) {
				err = errors.Join(err, workerErr)
			}
		}
		p.requests.Wait()
	}()
	for {
		select {
		case err = <-done:
			joined = true
			if err == nil {
				err = ErrParticipant
			}
			return wrap(err)
		default:
		}
		status, err := p.store.Status(ctx)
		if err != nil {
			return err
		}
		if status.Token != "" {
			return nil
		}
		if err := wait(ctx); err != nil {
			return wrap(err)
		}
	}
}

// Close is called after Run and all setup operations stop. A failed unregister
// deliberately leaves the member present, so recovery cannot assume it drained.
func (p *Participant) Close(ctx context.Context) error {
	p.admission(false)
	p.requests.Wait()
	_, err := p.store.db.ExecContext(
		ctx,
		p.store.d.Rebind("DELETE FROM maintenance_participants WHERE id = ?"),
		p.id,
	)
	return wrap(err)
}
