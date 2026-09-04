package sqlstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme"
	sqlstore "github.com/deploymenttheory/go-apple-dm/v3/server/acmestore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlite"
)

// TestClosedDatabaseSurfaces proves every method reports a driver failure
// instead of hiding it: the database is closed under the store. A store
// that answered ErrNotFound here would tell the server a record was never
// written when in truth it could not be read.
func TestClosedDatabaseSurfaces(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	seedOrder(t, s, "acct", "o1")
	if err := s.PutNonce(ctx, acme.Nonce{Value: "n1", IssuedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"GetAccount":          func() error { _, err := s.GetAccount(ctx, "acct"); return err },
		"AccountByThumbprint": func() error { _, err := s.AccountByThumbprint(ctx, "tp-acct"); return err },
		"GetOrder":            func() error { _, err := s.GetOrder(ctx, "o1"); return err },
		"GetAuthorization":    func() error { _, err := s.GetAuthorization(ctx, "az-o1"); return err },
		"GetChallenge":        func() error { _, err := s.GetChallenge(ctx, "ch-o1"); return err },
		"GetCertificate":      func() error { _, err := s.GetCertificate(ctx, "c1"); return err },
		"ListOrders":          func() error { _, err := s.ListOrders(ctx, "acct", paging.Page{}); return err },
		"ListCertificates": func() error {
			_, err := s.ListCertificates(ctx, acme.CertificateQuery{AccountID: "acct"}, paging.Page{})
			return err
		},
		"PutNonce":  func() error { return s.PutNonce(ctx, acme.Nonce{Value: "n2", IssuedAt: t0}) },
		"TakeNonce": func() error { _, err := s.TakeNonce(ctx, "n1"); return err },
		"Prune":     func() error { _, err := s.Prune(ctx, t0); return err },
		"Update":    func() error { return s.Update(ctx, func(acme.Tx) error { return nil }) },
		"PutAccount": func() error {
			return s.Update(ctx, func(tx acme.Tx) error {
				return tx.PutAccount(ctx, &acme.Account{ID: "a", Thumbprint: "t", CreatedAt: time.Now()})
			})
		},
		"PutOrder": func() error {
			return s.Update(ctx, func(tx acme.Tx) error {
				return tx.PutOrder(ctx, &acme.Order{ID: "o", AccountID: "a"})
			})
		},
		"PutAuthorization": func() error {
			return s.Update(ctx, func(tx acme.Tx) error {
				return tx.PutAuthorization(ctx, &acme.Authorization{ID: "az", OrderID: "o"})
			})
		},
		"PutChallenge": func() error {
			return s.Update(ctx, func(tx acme.Tx) error {
				return tx.PutChallenge(ctx, &acme.Challenge{ID: "ch", AuthzID: "az"})
			})
		},
		"PutCertificate": func() error {
			return s.Update(ctx, func(tx acme.Tx) error {
				return tx.PutCertificate(ctx, &acme.Certificate{ID: "c", OrderID: "o", AccountID: "a"})
			})
		},
		"ClaimIdentifier": func() error {
			return s.Update(ctx, func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "ci", "o") })
		},
	}
	for name, call := range calls {
		if err := call(); err == nil || errors.Is(err, acme.ErrNotFound) {
			t.Errorf("%s: err = %v, want a driver error", name, err)
		}
	}
	if _, err := sqlstore.Version(ctx, db, sqlite.Dialect); err == nil {
		t.Error("Version on a closed database succeeded")
	}
	if _, err := sqlstore.Migrate(ctx, db, sqlite.Dialect); err == nil {
		t.Error("Migrate on a closed database succeeded")
	}
	if _, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0); err == nil {
		t.Error("Rollback on a closed database succeeded")
	}
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err == nil {
		t.Error("Open on a closed database succeeded")
	}
}

func TestMigrateDirect(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	applied, err := sqlstore.Migrate(ctx, db, sqlite.Dialect)
	if err != nil || len(applied) != 1 {
		t.Fatalf("migrate = %v %v", applied, err)
	}
	if v, err := sqlstore.Version(ctx, db, sqlite.Dialect); err != nil || v != 1 {
		t.Fatalf("version = %d %v", v, err)
	}
}

// TestCancelledTransaction: a context cancelled inside Update fails the
// statements that follow and the commit, and every failure is reported.
func TestCancelledTransaction(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedOrder(t, s, "acct", "o1")
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var inside []error
	err := s.Update(cctx, func(tx acme.Tx) error {
		cancel()
		inside = append(inside,
			tx.PutOrder(cctx, &acme.Order{ID: "o2", AccountID: "acct"}),
			tx.ClaimIdentifier(cctx, "ci-o2", "o2"),
			readErr(tx.GetOrder(cctx, "o1")),
			readErr(tx.ListOrders(cctx, "acct", paging.Page{})),
		)
		return nil
	})
	if err == nil {
		t.Fatal("commit after cancellation succeeded")
	}
	for i, e := range inside {
		if e == nil {
			t.Errorf("statement %d after cancellation succeeded", i)
		}
	}
	if _, err := s.GetOrder(ctx, "o2"); !errors.Is(err, acme.ErrNotFound) {
		t.Fatalf("order from a cancelled transaction = %v", err)
	}
}

// readErr drops the value of a read so a table of calls can hold one.
func readErr[T any](_ T, err error) error { return err }
