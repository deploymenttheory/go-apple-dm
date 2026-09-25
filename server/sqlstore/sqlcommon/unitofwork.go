package sqlcommon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Queryer is a pool or the transaction shared by stores participating in Run.
type Queryer interface {
	// ExecContext executes a statement without returning result rows, honoring context
	// cancellation.
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	// QueryContext executes a query whose returned rows must be closed by the caller.
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	// QueryRowContext executes a single-row query whose result or error is consumed by Scan.
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// UnitOfWork coordinates stores using the same pool. Configure every store with
// that pool and use Query or CurrentTransaction for each operation. A callback
// must finish all database work before returning; it must not retain its context
// for background work or perform a remote side effect inside the transaction.
type UnitOfWork struct {
	DB      *sql.DB
	Dialect Dialect
}

var ErrTransaction = errors.New("sqlcommon: transaction coordination failed")

type (
	unitKey struct{}
	unit    struct {
		db       *sql.DB
		tx       *sql.Tx
		mu       sync.Mutex
		err      error
		commit   []func(context.Context)
		complete []func(context.Context, bool)
	}
)

// currentUnit finds the SQL unit of work carried by the context, if one is present.
func currentUnit(ctx context.Context) *unit {
	u, _ := ctx.Value(unitKey{}).(*unit)
	return u
}

// CurrentTransaction reports the transaction for db in this context.
func CurrentTransaction(ctx context.Context, db *sql.DB) (*sql.Tx, bool) {
	u := currentUnit(ctx)
	if u == nil || u.db != db {
		return nil, false
	}
	return u.tx, true
}

// Query returns the transaction for db, or db when no matching transaction exists.
func Query(ctx context.Context, db *sql.DB) Queryer {
	if tx, ok := CurrentTransaction(ctx, db); ok {
		return tx
	}
	return db
}

// Fail prevents a transaction from committing, even when an intermediate caller
// ignores the returned error. Outside a transaction it returns err unchanged.
func Fail(ctx context.Context, err error) error {
	if u := currentUnit(ctx); u != nil && err != nil {
		u.mu.Lock()
		u.err = errors.Join(u.err, err)
		u.mu.Unlock()
	}
	return err
}

var savepointSequence atomic.Uint64

// Savepoint preserves a participating store's Update contract inside an outer
// transaction. An ordinary callback error rolls back only that store operation;
// Fail still poisons the entire transaction. Callbacks must use the supplied
// context, and nested SQL operations must run sequentially.
func Savepoint(ctx context.Context, db *sql.DB, fn func(context.Context, *sql.Tx) error) (err error) {
	parent := currentUnit(ctx)
	if parent == nil || parent.db != db || fn == nil {
		return Fail(ctx, ErrTransaction)
	}
	name := fmt.Sprintf("dm_%d", savepointSequence.Add(1))
	if _, err := parent.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return Fail(ctx, fmt.Errorf("%w: savepoint: %w", ErrTransaction, err))
	}
	parent.mu.Lock()
	commitStart, completeStart := len(parent.commit), len(parent.complete)
	parent.mu.Unlock()
	completed := false
	defer func() {
		// A panic or cancellation must not leave writes or notifications from a
		// failed nested operation in a transaction the caller might recover.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if !completed || err != nil {
			if _, rollbackErr := parent.tx.ExecContext(cleanup, "ROLLBACK TO SAVEPOINT "+name); rollbackErr != nil {
				err = errors.Join(err, Fail(ctx, fmt.Errorf("%w: savepoint rollback: %w", ErrTransaction, rollbackErr)))
			}
		}
		if _, releaseErr := parent.tx.ExecContext(cleanup, "RELEASE SAVEPOINT "+name); releaseErr != nil {
			err = errors.Join(err, Fail(ctx, fmt.Errorf("%w: savepoint release: %w", ErrTransaction, releaseErr)))
		}
		parent.mu.Lock()
		if !completed || err != nil {
			parent.commit = parent.commit[:commitStart]
			for i := completeStart; i < len(parent.complete); i++ {
				callback := parent.complete[i]
				parent.complete[i] = func(ctx context.Context, _ bool) { callback(ctx, false) }
			}
		}
		parent.mu.Unlock()
	}()
	err = fn(ctx, parent.tx)
	completed = true
	return err
}

// AfterCommit schedules an in-process notification after commit. It runs
// immediately outside a transaction. Its context has no transaction or request
// cancellation, so the receiver must impose its own bounded lifetime.
func AfterCommit(ctx context.Context, fn func(context.Context)) {
	if u := currentUnit(ctx); u != nil {
		u.mu.Lock()
		u.commit = append(u.commit, fn)
		u.mu.Unlock()
		return
	}
	fn(context.WithoutCancel(ctx))
}

// AfterCompletion schedules work outside the transaction on either outcome.
// Security denials can use this to record an occurrence even after rollback.
func AfterCompletion(ctx context.Context, fn func(context.Context, bool)) {
	if u := currentUnit(ctx); u != nil {
		u.mu.Lock()
		u.complete = append(u.complete, fn)
		u.mu.Unlock()
		return
	}
	fn(context.WithoutCancel(ctx), true)
}

// Run commits local mutations together. A nested error poisons the enclosing
// transaction. Different pools cannot be combined into an atomic operation.
func (w UnitOfWork) Run(ctx context.Context, fn func(context.Context) error) (err error) {
	if w.DB == nil || fn == nil {
		return fmt.Errorf("%w: database and callback required", ErrTransaction)
	}
	if u := currentUnit(ctx); u != nil {
		if u.db != w.DB {
			return Fail(ctx, fmt.Errorf("%w: different database pools", ErrTransaction))
		}
		return Fail(ctx, fn(ctx))
	}
	opts := &sql.TxOptions{Isolation: sql.LevelReadCommitted}
	if w.Dialect.Name == "sqlite" {
		opts = nil
	}
	tx, err := w.DB.BeginTx(ctx, opts)
	if err != nil {
		// A context cancelled while BEGIN is already in flight is reported by the
		// driver in its own terms, SQLITE_INTERRUPT for SQLite, rather than as
		// ctx.Err(). Without this an orderly shutdown is indistinguishable from a
		// coordination failure. Preserve the driver error, as Commit below does.
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		return fmt.Errorf("%w: begin: %w", ErrTransaction, err)
	}
	u := &unit{db: w.DB, tx: tx}
	committed := false
	defer func() {
		_ = tx.Rollback()
		outside := context.WithoutCancel(context.WithValue(ctx, unitKey{}, (*unit)(nil)))
		if committed {
			for _, f := range u.commit {
				f(outside)
			}
		}
		for _, f := range u.complete {
			f(outside, committed)
		}
	}()
	inside := context.WithValue(ctx, unitKey{}, u)
	err = fn(inside)
	u.mu.Lock()
	err = errors.Join(err, u.err)
	u.mu.Unlock()
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		// database/sql can roll back a cancelled transaction before Commit
		// observes the cancellation, returning ErrTxDone instead of ctx.Err().
		// Preserve the SQL error while making that shutdown outcome identifiable.
		if errors.Is(err, sql.ErrTxDone) && ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		return fmt.Errorf("%w: commit: %w", ErrTransaction, err)
	}
	committed = true
	return nil
}
