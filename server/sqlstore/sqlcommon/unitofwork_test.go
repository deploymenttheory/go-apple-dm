package sqlcommon_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// TestUnitOfWorkSharesStateAndRejectsIgnoredNestedFailure checks that unit of work shares state
// and rejects ignored nested failure.
func TestUnitOfWorkSharesStateAndRejectsIgnoredNestedFailure(t *testing.T) {
	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "unit.sqlite"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	st, err := statestore.Open(t.Context(), s.DB(), sqlite.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	u := sqlcommon.UnitOfWork{DB: s.DB(), Dialect: sqlite.Dialect}
	fault := errors.New("capture failed")
	for _, fail := range []bool{true, false} {
		notified := false
		completed := false
		err = u.Run(t.Context(), func(ctx context.Context) error {
			if err := st.Update(ctx, []string{"test/one"}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: "test/one", Value: []byte("one")}) }); err != nil {
				return err
			}
			if _, err := st.Get(ctx, "test/one"); err != nil {
				return err
			}
			sqlcommon.AfterCommit(ctx, func(outside context.Context) {
				notified = true
				if _, ok := sqlcommon.CurrentTransaction(outside, s.DB()); ok {
					t.Error("notification retained transaction")
				}
				if _, err := st.Get(outside, "test/one"); err != nil {
					t.Error(err)
				}
			})
			sqlcommon.AfterCompletion(ctx, func(_ context.Context, committed bool) {
				completed = true
				if committed == fail {
					t.Error("incorrect completion outcome")
				}
			})
			_ = u.Run(ctx, func(ctx context.Context) error {
				if fail {
					return fault
				}
				return nil
			})
			return nil
		})
		if !completed || notified == fail {
			t.Fatal("callback boundary failed", completed, notified)
		}
		_, readErr := st.Get(t.Context(), "test/one")
		if fail {
			if !errors.Is(err, fault) || !errors.Is(readErr, state.ErrNotFound) {
				t.Fatal("failed operation committed", err, readErr)
			}
		} else if err != nil || readErr != nil {
			t.Fatal(err, readErr)
		}
	}
}

// TestSavepointRollsBackHandledFailureWithoutLosingOuterWork checks savepoint rolls back handled
// failure without losing outer work.
func TestSavepointRollsBackHandledFailureWithoutLosingOuterWork(t *testing.T) {
	db := openRaw(t)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE savepoint_test (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	u := sqlcommon.UnitOfWork{DB: db, Dialect: sqlite.Dialect}
	notified, completed := false, false
	handled := errors.New("expected missing record")
	err := u.Run(t.Context(), func(ctx context.Context) error {
		if _, err := sqlcommon.Query(ctx, db).ExecContext(ctx, "INSERT INTO savepoint_test VALUES (1)"); err != nil {
			return err
		}
		err := sqlcommon.Savepoint(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "INSERT INTO savepoint_test VALUES (2)"); err != nil {
				return err
			}
			sqlcommon.AfterCommit(ctx, func(context.Context) { notified = true })
			sqlcommon.AfterCompletion(ctx, func(outside context.Context, committed bool) {
				completed = true
				if committed {
					t.Error("rolled-back savepoint reported committed")
				}
				if _, ok := sqlcommon.CurrentTransaction(outside, db); ok {
					t.Error("completion retained transaction")
				}
			})
			return handled
		})
		if !errors.Is(err, handled) {
			return err
		}
		return sqlcommon.Savepoint(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "INSERT INTO savepoint_test VALUES (3)")
			return err
		})
	})
	if err != nil || notified || !completed {
		t.Fatal(err, notified, completed)
	}
	var count, sum int
	if err = db.QueryRowContext(t.Context(), "SELECT COUNT(*), SUM(id) FROM savepoint_test").Scan(&count, &sum); err != nil || count != 2 || sum != 4 {
		t.Fatal("savepoint or outer commit lost", count, sum, err)
	}
}

// TestSavepointCannotHideRequiredFailureAndRecoversPanic checks that savepoint cannot hide
// required failure and recovers panic.
func TestSavepointCannotHideRequiredFailureAndRecoversPanic(t *testing.T) {
	db := openRaw(t)
	u := sqlcommon.UnitOfWork{DB: db, Dialect: sqlite.Dialect}
	fault := errors.New("required capture failed")
	err := u.Run(t.Context(), func(ctx context.Context) error {
		_ = sqlcommon.Savepoint(ctx, db, func(ctx context.Context, _ *sql.Tx) error {
			return sqlcommon.Fail(ctx, fault)
		})
		return nil
	})
	if !errors.Is(err, fault) {
		t.Fatal("required failure lost", err)
	}
	if err = u.Run(t.Context(), func(ctx context.Context) error {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("missing panic")
				}
			}()
			_ = sqlcommon.Savepoint(ctx, db, func(context.Context, *sql.Tx) error { panic("stop") })
		}()
		return sqlcommon.Savepoint(ctx, db, func(context.Context, *sql.Tx) error { return nil })
	}); err != nil {
		t.Fatal("transaction unusable after recovered panic", err)
	}
	if err = sqlcommon.Savepoint(t.Context(), db, nil); !errors.Is(err, sqlcommon.ErrTransaction) {
		t.Fatal("savepoint without enclosing transaction", err)
	}
}

// TestUnitOfWorkRejectsDifferentPoolsAndRollsBackPanic checks that unit of work rejects different
// pools and rolls back panic.
func TestUnitOfWorkRejectsDifferentPoolsAndRollsBackPanic(t *testing.T) {
	a := openRaw(t)
	b := openRaw(t)
	u := sqlcommon.UnitOfWork{DB: a, Dialect: sqlite.Dialect}
	v := sqlcommon.UnitOfWork{DB: b, Dialect: sqlite.Dialect}
	if err := u.Run(t.Context(), func(ctx context.Context) error {
		_ = v.Run(ctx, func(context.Context) error { return nil })
		return nil
	}); !errors.Is(err, sqlcommon.ErrTransaction) {
		t.Fatal(err)
	}
	completed := false
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		_ = u.Run(t.Context(), func(ctx context.Context) error {
			sqlcommon.AfterCompletion(ctx, func(_ context.Context, committed bool) {
				completed = true
				if committed {
					t.Error("panic committed")
				}
			})
			panic("interrupted")
		})
	}()
	if !completed {
		t.Fatal("panic did not complete rollback")
	}
	if err := u.Run(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatal("pool unusable after panic", err)
	}
	if err := (sqlcommon.UnitOfWork{}).Run(t.Context(), nil); !errors.Is(err, sqlcommon.ErrTransaction) {
		t.Fatal(err)
	}
}

// TestUnitOfWorkRecognizesCancellationAfterAutomaticRollback checks unit of work recognizes
// cancellation after automatic rollback.
func TestUnitOfWorkRecognizesCancellationAfterAutomaticRollback(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		name := "explicit rollback"
		if cancelled {
			name = "automatic rollback on cancellation"
		}
		t.Run(name, func(t *testing.T) {
			db := openRaw(t)
			db.SetMaxOpenConns(1)
			if _, err := db.ExecContext(t.Context(), "CREATE TABLE cancelled_work (id INTEGER)"); err != nil {
				t.Fatal(err)
			}
			u := sqlcommon.UnitOfWork{DB: db, Dialect: sqlite.Dialect}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			notified, completed := false, false
			err := u.Run(ctx, func(inside context.Context) error {
				tx, _ := sqlcommon.CurrentTransaction(inside, db)
				if _, err := tx.ExecContext(inside, "INSERT INTO cancelled_work VALUES (1)"); err != nil {
					return err
				}
				sqlcommon.AfterCommit(inside, func(context.Context) { notified = true })
				sqlcommon.AfterCompletion(inside, func(outside context.Context, committed bool) {
					completed = true
					if committed || outside.Err() != nil {
						t.Error("rollback callback reported commit or retained cancellation")
					}
				})
				if cancelled {
					cancel()
				} else if err := tx.Rollback(); err != nil {
					return err
				}
				// With one connection, this waits until database/sql has finished
				// its asynchronous rollback. Commit must then see ErrTxDone.
				conn, err := db.Conn(t.Context())
				if err != nil {
					return err
				}
				return conn.Close()
			})
			if !errors.Is(err, sqlcommon.ErrTransaction) || !errors.Is(err, sql.ErrTxDone) {
				t.Fatal("lost transaction error", err)
			}
			if errors.Is(err, context.Canceled) != cancelled || notified || !completed {
				t.Fatal("incorrect cancellation or callback outcome", err, notified, completed)
			}
			var count int
			if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM cancelled_work").Scan(&count); err != nil || count != 0 {
				t.Fatal("cancelled work survived rollback", count, err)
			}
		})
	}
}

// interruptOnBeginConn fails BEGIN the way a driver does when the statement is interrupted
// mid-flight, having first cancelled the caller's context. Real drivers report that
// cancellation in their own terms rather than as ctx.Err(), and the window is a race that
// cannot be hit reliably against a real database, so it is reproduced here exactly.
type interruptOnBeginConn struct{ cancel context.CancelFunc }

// Open returns the connection itself, so one stub serves as driver and connection.
func (c *interruptOnBeginConn) Open(string) (driver.Conn, error) { return c, nil }

// Prepare is never reached: the transaction fails before any statement is prepared.
func (c *interruptOnBeginConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("sqlcommon_test: unexpected Prepare")
}

// Close satisfies driver.Conn.
func (c *interruptOnBeginConn) Close() error { return nil }

// Begin satisfies the legacy driver.Conn contract; database/sql prefers BeginTx.
func (c *interruptOnBeginConn) Begin() (driver.Tx, error) {
	return nil, errors.New("sqlcommon_test: unexpected Begin")
}

// BeginTx cancels the caller's context and then reports SQLite's interrupt error, which is
// the ordering an orderly shutdown produces.
func (c *interruptOnBeginConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.cancel()
	return nil, errors.New("interrupted (9)")
}

// TestUnitOfWorkRecognizesCancellationDuringBegin checks that a context cancelled while BEGIN
// is in flight stays identifiable as a cancellation, so an orderly shutdown is not reported as
// a transaction coordination failure. The driver error is kept as well, because it says what
// the database actually reported.
func TestUnitOfWorkRecognizesCancellationDuringBegin(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sql.Register("sqlcommon_test_interrupt_on_begin", &interruptOnBeginConn{cancel: cancel})
	db, err := sql.Open("sqlcommon_test_interrupt_on_begin", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ran := false
	err = (sqlcommon.UnitOfWork{DB: db, Dialect: sqlite.Dialect}).
		Run(ctx, func(context.Context) error { ran = true; return nil })
	if ran {
		t.Fatal("callback ran despite a failed begin")
	}
	if !errors.Is(err, sqlcommon.ErrTransaction) || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation during begin was not identifiable", err)
	}
	if !strings.Contains(err.Error(), "interrupted (9)") {
		t.Fatal("driver error was discarded", err)
	}
}

// TestLostSavepointPoisonsOuterTransactionAndSuppressesNotification checks lost savepoint poisons
// outer transaction and suppresses notification.
func TestLostSavepointPoisonsOuterTransactionAndSuppressesNotification(t *testing.T) {
	for _, cancelBefore := range []bool{false, true} {
		db := openRaw(t)
		for _, query := range []string{
			"CREATE TABLE lost_savepoint (id INTEGER PRIMARY KEY)",
			"CREATE TRIGGER abort_operation BEFORE INSERT ON lost_savepoint WHEN NEW.id = 2 BEGIN SELECT RAISE(ROLLBACK, 'transaction terminated'); END",
		} {
			if _, err := db.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
		}
		u := sqlcommon.UnitOfWork{DB: db, Dialect: sqlite.Dialect}
		notified, completed := false, false
		err := u.Run(t.Context(), func(ctx context.Context) error {
			if _, err := sqlcommon.Query(ctx, db).ExecContext(ctx, "INSERT INTO lost_savepoint VALUES (1)"); err != nil {
				return err
			}
			sqlcommon.AfterCommit(ctx, func(context.Context) { notified = true })
			sqlcommon.AfterCompletion(ctx, func(_ context.Context, committed bool) {
				completed = true
				if committed {
					t.Error("lost transaction reported committed")
				}
			})
			nested, cancel := context.WithCancel(ctx)
			defer cancel()
			if cancelBefore {
				cancel()
			}
			_ = sqlcommon.Savepoint(nested, db, func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, "INSERT INTO lost_savepoint VALUES (2)")
				return err
			})
			// Even if a caller handles this failure, failed savepoint creation,
			// rollback or release must prevent the enclosing operation committing.
			return nil
		})
		if !errors.Is(err, sqlcommon.ErrTransaction) || notified || !completed {
			t.Fatal("transaction loss was hidden", err, notified, completed)
		}
		var count int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM lost_savepoint").Scan(&count); err != nil || count != 0 {
			t.Fatal("partial work survived", count, err)
		}
	}
}
