package sqlstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/server/depstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestAccountNameLocks exercises account creation and keypair promotion against
// independent SQL transactions, including names without an account yet.
func TestAccountNameLocks(t *testing.T) {
	db := openDB(t)
	if _, err := sqlstore.Open(t.Context(), db, sqlite.Dialect, sqlstore.Options{}); err != nil {
		t.Fatal(err)
	}
	checkAccountNameLocks(t, db, sqlite.Dialect)
}

// checkAccountNameLocks runs the same concurrency checks on SQLite and the
// PostgreSQL/MySQL integration backends, using separate pooled connections.
func checkAccountNameLocks(t *testing.T, db *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	s, err := sqlstore.Open(t.Context(), db, dialect, sqlstore.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	t.Run("ConcurrentCreation", func(t *testing.T) {
		const name = "lock-concurrent-create"
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		locked, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		first := asyncStoreCall(t, func() error {
			return s.Update(ctx, func(tx dep.Tx) error {
				if err := tx.LockAccount(ctx, name); !errors.Is(err, dep.ErrNotFound) {
					return errors.Join(errors.New("expected an absent account"), err)
				}
				close(locked)
				<-release
				return tx.PutAccount(ctx, &dep.Account{Name: name, ServerUUID: "first-server", CreatedAt: now, UpdatedAt: now})
			})
		})
		select {
		case <-locked:
		case <-ctx.Done():
			t.Fatal("first creation did not acquire its lock")
		}
		attempting := make(chan struct{})
		second := asyncStoreCall(t, func() error {
			close(attempting)
			return s.Update(ctx, func(tx dep.Tx) error {
				if err := tx.LockAccount(ctx, name); err != nil && !errors.Is(err, dep.ErrNotFound) {
					return err
				}
				if _, err := tx.GetAccount(ctx, name); err == nil {
					return dep.ErrConflict
				} else if !errors.Is(err, dep.ErrNotFound) {
					return err
				}
				return tx.PutAccount(ctx, &dep.Account{Name: name, ServerUUID: "second-server", CreatedAt: now, UpdatedAt: now})
			})
		})
		<-attempting
		assertStoreCallBlocked(t, second)
		unblock()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if err := <-second; !errors.Is(err, dep.ErrConflict) {
			t.Fatal("second creation overwrote the winner", err)
		}
		account, err := s.GetAccount(ctx, name)
		if err != nil || account.ServerUUID != "first-server" {
			t.Fatal(account, err)
		}
	})
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "StagingBeforeAccount", true: "StagingDuringRenewal"}[existing], func(t *testing.T) {
			name := "lock-stage-before-account"
			if existing {
				name = "lock-stage-renewal"
				if err := s.PutAccount(t.Context(), &dep.Account{Name: name, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
			}
			old := &dep.Keypair{CertPEM: []byte("validated certificate"), KeyPEM: []byte("validated key"), CreatedAt: now}
			newer := &dep.Keypair{CertPEM: []byte("next certificate"), KeyPEM: []byte("next key"), CreatedAt: now}
			if err := s.PutKeypair(t.Context(), name, dep.StageStaged, old); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			validated, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			imported := asyncStoreCall(t, func() error {
				return s.Update(ctx, func(tx dep.Tx) error {
					if err := tx.LockAccount(ctx, name); err != nil && !errors.Is(err, dep.ErrNotFound) {
						return err
					}
					kp, err := tx.Keypair(ctx, name, dep.StageStaged)
					if err != nil || !bytes.Equal(kp.CertPEM, old.CertPEM) {
						return errors.Join(errors.New("missing validation keypair"), err)
					}
					close(validated)
					<-release
					if err := tx.PutAccount(ctx, &dep.Account{Name: name, CreatedAt: now, UpdatedAt: now}); err != nil {
						return err
					}
					return tx.UpstageKeypair(ctx, name)
				})
			})
			select {
			case <-validated:
			case <-ctx.Done():
				t.Fatal("token validation did not read its staged key")
			}
			attempting := make(chan struct{})
			staged := asyncStoreCall(t, func() error {
				close(attempting)
				return s.PutKeypair(ctx, name, dep.StageStaged, newer)
			})
			<-attempting
			assertStoreCallBlocked(t, staged)
			unblock()
			if err := <-imported; err != nil {
				t.Fatal(err)
			}
			if err := <-staged; err != nil {
				t.Fatal(err)
			}
			for stage, want := range map[dep.Stage]*dep.Keypair{dep.StageCurrent: old, dep.StageStaged: newer} {
				got, err := s.Keypair(ctx, name, stage)
				if err != nil || !bytes.Equal(got.CertPEM, want.CertPEM) || !bytes.Equal(got.KeyPEM, want.KeyPEM) {
					t.Fatal("token import promoted or deleted the wrong keypair", stage, got, err)
				}
			}
		})
	}
	t.Run("DeletionRetainsNameLock", func(t *testing.T) {
		const name = "lock-deleted-account"
		account := &dep.Account{Name: name, CreatedAt: now, UpdatedAt: now}
		if err := s.PutAccount(t.Context(), account); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteAccount(t.Context(), name); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRowContext(t.Context(), dialect.Rebind("SELECT COUNT(*) FROM dep_account_locks WHERE account = ?"), name).Scan(&count); err != nil || count != 1 {
			t.Fatal("deletion removed the stable lock", count, err)
		}
		if err := s.Update(t.Context(), func(tx dep.Tx) error {
			if err := tx.LockAccount(t.Context(), name); !errors.Is(err, dep.ErrNotFound) {
				return errors.Join(errors.New("deleted account was found"), err)
			}
			return tx.PutAccount(t.Context(), account)
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("StateUpdateSharesNameLock", func(t *testing.T) {
		const name = "lock-account-state"
		if err := s.PutAccount(t.Context(), &dep.Account{Name: name, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		read, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		policy := asyncStoreCall(t, func() error {
			return s.Update(ctx, func(tx dep.Tx) error {
				if err := tx.LockAccount(ctx, name); err != nil {
					return err
				}
				account, err := tx.GetAccount(ctx, name)
				if err != nil {
					return err
				}
				close(read)
				<-release
				account.ProfileUUID = "desired-profile"
				return tx.PutAccount(ctx, account)
			})
		})
		select {
		case <-read:
		case <-ctx.Done():
			t.Fatal("policy update did not read account")
		}
		attempting := make(chan struct{})
		state := asyncStoreCall(t, func() error {
			close(attempting)
			return s.SetAccountState(ctx, name, dep.AccountState{TokenInvalid: true})
		})
		<-attempting
		assertStoreCallBlocked(t, state)
		unblock()
		if err := <-policy; err != nil {
			t.Fatal(err)
		}
		if err := <-state; err != nil {
			t.Fatal(err)
		}
		account, err := s.GetAccount(ctx, name)
		if err != nil || account.ProfileUUID != "desired-profile" || !account.State.TokenInvalid {
			t.Fatal("concurrent account state or policy was lost", account, err)
		}
	})
	t.Run("UnrelatedCreationDoesNotWait", func(t *testing.T) {
		if dialect.Name == "sqlite" {
			t.Skip("SQLite serializes database writers")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		locked, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		first := asyncStoreCall(t, func() error {
			return s.Update(ctx, func(tx dep.Tx) error {
				if err := tx.LockAccount(ctx, "lock-unrelated-first"); !errors.Is(err, dep.ErrNotFound) {
					return errors.Join(errors.New("expected an absent account"), err)
				}
				close(locked)
				<-release
				return nil
			})
		})
		select {
		case <-locked:
		case <-ctx.Done():
			t.Fatal("absent name was not locked")
		}
		second := asyncStoreCall(t, func() error {
			return s.PutAccount(ctx, &dep.Account{Name: "lock-unrelated-second", CreatedAt: now, UpdatedAt: now})
		})
		select {
		case err := <-second:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("locking an absent account blocked a different account name")
		}
		unblock()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
	})
}

// asyncStoreCall runs a competing operation and ensures its goroutine exits
// before the test releases its shared database.
func asyncStoreCall(t *testing.T, fn func() error) <-chan error {
	t.Helper()
	done, exited := make(chan error, 1), make(chan struct{})
	go func() {
		done <- fn()
		close(exited)
	}()
	t.Cleanup(func() {
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("competing store operation did not finish")
		}
	})
	return done
}

// assertStoreCallBlocked checks a competing mutation cannot finish while the
// first transaction deliberately holds its account-name lock.
func assertStoreCallBlocked(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatal("competing account mutation escaped its name lock", err)
	case <-time.After(50 * time.Millisecond):
	}
}
