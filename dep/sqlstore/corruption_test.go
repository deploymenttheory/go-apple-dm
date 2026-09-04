package sqlstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/dep/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlite"
)

// seed writes one of everything for account "a".
func seed(t *testing.T, s *sqlstore.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	if err := s.PutAccount(ctx, &dep.Account{Name: "a", ConsumerKey: "ck", ConsumerSecret: "cs", AccessToken: "at", AccessSecret: "as", Limits: map[string]dep.Limit{"/x": {Default: 1, Maximum: 2}}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSession(ctx, "a", "tok"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutKeypair(ctx, "a", dep.StageCurrent, &dep.Keypair{CertPEM: []byte("c"), KeyPEM: []byte("k"), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCursor(ctx, "a", dep.Cursor{Value: "c", Phase: dep.PhaseSync, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDevices(ctx, "a", []dep.Device{{SerialNumber: "S"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.PutProfile(ctx, "a", &dep.Profile{ProfileUUID: "p", ProfileName: "n", URL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAssignment(ctx, &dep.Assignment{Account: "a", SerialNumber: "S", ProfileUUID: "p", Status: dep.StatusSuccess, AttemptedAt: now}); err != nil {
		t.Fatal(err)
	}
}

// TestCorruptRowsSurface: a row another process mangled is reported, not
// decoded into a zero record.
func TestCorruptRowsSurface(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		stmt string
		read func(s *sqlstore.Store) error
	}{
		{"account timestamp", "UPDATE dep_accounts SET created_at = 'garbage'", func(s *sqlstore.Store) error { _, err := s.GetAccount(ctx, "a"); return err }},
		{"account limits", "UPDATE dep_accounts SET limits = X'00'", func(s *sqlstore.Store) error { _, err := s.GetAccount(ctx, "a"); return err }},
		{"account list", "UPDATE dep_accounts SET updated_at = 'garbage'", func(s *sqlstore.Store) error { _, err := s.ListAccounts(ctx, paging.Page{}); return err }},
		{"device record", "UPDATE dep_devices SET record = X'00'", func(s *sqlstore.Store) error { _, err := s.GetDevice(ctx, "a", "S"); return err }},
		{"device list", "UPDATE dep_devices SET updated_at = 'garbage'", func(s *sqlstore.Store) error {
			_, err := s.ListDevices(ctx, "a", dep.DeviceQuery{}, paging.Page{})
			return err
		}},
		{"profile record", "UPDATE dep_profiles SET record = X'00'", func(s *sqlstore.Store) error { _, err := s.GetProfile(ctx, "a", "p"); return err }},
		{"profile list", "UPDATE dep_profiles SET record = X'00'", func(s *sqlstore.Store) error { _, err := s.ListProfiles(ctx, "a", paging.Page{}); return err }},
		{"assignment", "UPDATE dep_assignments SET attempted_at = 'garbage'", func(s *sqlstore.Store) error { _, err := s.GetAssignment(ctx, "a", "S"); return err }},
		{"assignment list", "UPDATE dep_assignments SET attempted_at = 'garbage'", func(s *sqlstore.Store) error {
			_, err := s.ListAssignments(ctx, "a", dep.AssignmentQuery{}, paging.Page{})
			return err
		}},
		{"cursor", "UPDATE dep_cursors SET updated_at = 'garbage'", func(s *sqlstore.Store) error { _, err := s.Cursor(ctx, "a"); return err }},
		{"keypair", "UPDATE dep_keypairs SET created_at = 'garbage'", func(s *sqlstore.Store) error { _, err := s.Keypair(ctx, "a", dep.StageCurrent); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
			if err != nil {
				t.Fatal(err)
			}
			seed(t, s)
			if _, err := db.ExecContext(ctx, tc.stmt); err != nil {
				t.Fatal(err)
			}
			if err := tc.read(s); err == nil {
				t.Fatal("corrupt row decoded without error")
			}
		})
	}
}

// TestStrictKeyringRefusesPlaintext: secrets written without a keyring are
// refused by a strict one, and a tampered sealed value is reported.
func TestStrictKeyringRefusesPlaintext(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	plain, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, plain)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	strict, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "k1", Strict: true}, Provider: secrets.Static{"k1": key}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true, Keyring: strict})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAccount(ctx, "a"); err == nil {
		t.Error("plaintext account secrets accepted by a strict keyring")
	}
	if _, err := s.Session(ctx, "a"); err == nil {
		t.Error("plaintext session accepted by a strict keyring")
	}
	if _, err := s.Keypair(ctx, "a", dep.StageCurrent); err == nil {
		t.Error("plaintext key accepted by a strict keyring")
	}
	if _, err := s.ListAccounts(ctx, paging.Page{}); err == nil {
		t.Error("plaintext account list accepted by a strict keyring")
	}
	// Sealed rows work through the same keyring; a moved blob does not.
	if err := s.SetSession(ctx, "a", "sealed"); err != nil {
		t.Fatal(err)
	}
	if tok, err := s.Session(ctx, "a"); err != nil || tok != "sealed" {
		t.Fatalf("sealed session = %q %v", tok, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE dep_sessions SET account = 'b'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, "b"); err == nil {
		t.Error("session moved to another account still opened")
	}
}

func TestMoreFailurePaths(t *testing.T) {
	ctx := context.Background()
	t.Run("SealedRowWithoutKeyring", func(t *testing.T) {
		db := openDB(t)
		key := make([]byte, 32)
		k, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "k1"}, Provider: secrets.Static{"k1": key}})
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{Keyring: k})
		if err != nil {
			t.Fatal(err)
		}
		seed(t, sealed)
		plain, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plain.Session(ctx, "a"); err == nil {
			t.Error("sealed session opened without a keyring")
		}
		if _, err := plain.GetAccount(ctx, "a"); err == nil {
			t.Error("sealed account secrets opened without a keyring")
		}
		if raw, err := sealed.RawSecrets(ctx, "a"); err != nil || len(raw) == 0 {
			t.Fatalf("raw secrets = %v %v", raw, err)
		}
		if _, err := sealed.RawSecrets(ctx, ""); err == nil {
			t.Error("empty account name accepted")
		}
	})
	t.Run("CascadeDeleteFailure", func(t *testing.T) {
		db := openDB(t)
		s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		seed(t, s)
		if _, err := db.ExecContext(ctx, "DROP TABLE dep_assignments"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteAccount(ctx, "a"); err == nil {
			t.Error("cascade failure swallowed")
		}
	})
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
		if _, err := db.ExecContext(ctx, "CREATE TABLE dep_accounts (x INTEGER)"); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err == nil {
			t.Error("migration over a conflicting table succeeded")
		}
	})
}

// TestCancelledTransaction: a context cancelled inside Update fails the
// statements that follow and the commit, and every failure is reported.
func TestCancelledTransaction(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, s)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var inside []error
	err = s.Update(cctx, func(tx dep.Tx) error {
		cancel()
		inside = append(inside,
			tx.DeleteAccount(cctx, "a"),
			tx.PutDevices(cctx, "a", []dep.Device{{SerialNumber: "S"}}, time.Now()),
			tx.UpstageKeypair(cctx, "a"),
			tx.SetAccountState(cctx, "a", dep.AccountState{TokenInvalid: true}),
			tx.DeleteProfile(cctx, "a", "p"),
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
	// The raw view fails the same way once the database is gone.
	if _, err := s.RawSecrets(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := s.RawSecrets(ctx, "a"); err == nil {
		t.Fatal("raw view on a closed database succeeded")
	}
}

// TestEmptySecretsWithKeyring: empty secrets are stored as NULL, never
// sealed, and read back empty.
func TestEmptySecretsWithKeyring(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	k, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "k1", Strict: true}, Provider: secrets.Static{"k1": key}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqlstore.Open(ctx, openDB(t), sqlite.Dialect, sqlstore.Options{Keyring: k})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.PutAccount(ctx, &dep.Account{Name: "e", ConsumerKey: "ck", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAccount(ctx, "e")
	if err != nil {
		t.Fatal(err)
	}
	if got.ConsumerSecret != "" || got.AccessToken != "" || got.AccessSecret != "" || got.HasTokens() {
		t.Fatalf("empty secrets round-tripped as %+v", got)
	}
	raw, err := s.RawSecrets(ctx, "e")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range raw {
		if len(v) != 0 {
			t.Fatalf("%s stored as %x", k, v)
		}
	}
}
