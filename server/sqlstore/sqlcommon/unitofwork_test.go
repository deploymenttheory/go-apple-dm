package sqlcommon_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

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
