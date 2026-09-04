package sqlstore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/v3/server/acmestore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/sqlite"
)

// TestCorruptRowsSurface: a record another process mangled is reported and
// named, not decoded into a zero value. A zero record would look to the
// server like an order with no status or a challenge whose attestation
// vanished, and it would answer a device on that basis.
func TestCorruptRowsSurface(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		stmt  string
		table string
		id    string
		read  func(s *sqlstore.Store) error
	}{
		{"account", "UPDATE acme_accounts SET record = X'00'", "acme_accounts", "acct", func(s *sqlstore.Store) error {
			_, err := s.GetAccount(ctx, "acct")
			return err
		}},
		{"account by thumbprint", "UPDATE acme_accounts SET record = X'00'", "acme_accounts", "acct", func(s *sqlstore.Store) error {
			_, err := s.AccountByThumbprint(ctx, "tp-acct")
			return err
		}},
		{"order", "UPDATE acme_orders SET record = X'00'", "acme_orders", "o1", func(s *sqlstore.Store) error {
			_, err := s.GetOrder(ctx, "o1")
			return err
		}},
		{"order list", "UPDATE acme_orders SET record = X'00'", "acme_orders", "o1", func(s *sqlstore.Store) error {
			_, err := s.ListOrders(ctx, "acct", paging.Page{})
			return err
		}},
		{"authorization", "UPDATE acme_authorizations SET record = X'00'", "acme_authorizations", "az-o1", func(s *sqlstore.Store) error {
			_, err := s.GetAuthorization(ctx, "az-o1")
			return err
		}},
		{"challenge", "UPDATE acme_challenges SET record = X'00'", "acme_challenges", "ch-o1", func(s *sqlstore.Store) error {
			_, err := s.GetChallenge(ctx, "ch-o1")
			return err
		}},
		{"certificate", "UPDATE acme_certificates SET record = X'00'", "acme_certificates", "c1", func(s *sqlstore.Store) error {
			_, err := s.GetCertificate(ctx, "c1")
			return err
		}},
		{"certificate list", "UPDATE acme_certificates SET record = X'00'", "acme_certificates", "c1", func(s *sqlstore.Store) error {
			_, err := s.ListCertificates(ctx, acme.CertificateQuery{}, paging.Page{})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
			if err != nil {
				t.Fatal(err)
			}
			seedOrder(t, s, "acct", "o1")
			if err := s.Update(ctx, func(tx acme.Tx) error {
				return tx.PutCertificate(ctx, &acme.Certificate{ID: "c1", AccountID: "acct", OrderID: "o1", Serial: "AA", IssuedAt: t0, NotAfter: t0})
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, tc.stmt); err != nil {
				t.Fatal(err)
			}
			err = tc.read(s)
			if err == nil {
				t.Fatal("corrupt row decoded without error")
			}
			if !strings.Contains(err.Error(), tc.table) || !strings.Contains(err.Error(), tc.id) {
				t.Fatalf("error %q names neither %s nor %s", err, tc.table, tc.id)
			}
		})
	}
}

// TestUnsupportedDialectAndConflictingSchema: a dialect with no embedded
// migrations is refused everywhere it could be used, and a migration over
// somebody else's table of the same name fails rather than half-applying.
func TestUnsupportedDialectAndConflictingSchema(t *testing.T) {
	ctx := context.Background()
	t.Run("UnsupportedDialect", func(t *testing.T) {
		db := openDB(t)
		bad := sqlite.Dialect
		bad.Name = "oracle"
		if _, err := sqlstore.Migrate(ctx, db, bad); err == nil {
			t.Error("Migrate accepted an unknown dialect")
		}
		if _, err := sqlstore.Rollback(ctx, db, bad, 0); err == nil {
			t.Error("Rollback accepted an unknown dialect")
		}
		if _, err := sqlstore.Version(ctx, db, bad); err == nil {
			t.Error("Version accepted an unknown dialect")
		}
		if _, err := sqlstore.Open(ctx, db, bad, sqlstore.Options{}); err == nil {
			t.Error("Open accepted an unknown dialect")
		}
	})
	t.Run("ConflictingSchema", func(t *testing.T) {
		db := openDB(t)
		if _, err := db.ExecContext(ctx, "CREATE TABLE acme_accounts (x INTEGER)"); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err == nil {
			t.Error("migration over a conflicting table succeeded")
		}
	})
	t.Run("MissingTable", func(t *testing.T) {
		db := openDB(t)
		s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "DROP TABLE acme_challenges"); err != nil {
			t.Fatal(err)
		}
		// Prune runs several statements; the first one to hit the missing
		// table fails the sweep rather than reporting a partial count.
		if n, err := s.Prune(ctx, t0); err == nil || n != 0 {
			t.Errorf("prune over a missing table = %d %v", n, err)
		}
	})
}
