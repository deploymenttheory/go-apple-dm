package sqlstore_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/server/depstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestAccountMutationFailuresRollback rejects unavailable locks and failed writes
// without partially changing credentials, key slots, inventory or profile state.
func TestAccountMutationFailuresRollback(t *testing.T) {
	lock := "BEFORE UPDATE ON dep_account_locks"
	cases := []struct {
		name, trigger string
		mutate        func(context.Context, *sqlstore.Store) error
	}{
		{"renewal lock", lock, func(ctx context.Context, s *sqlstore.Store) error {
			return s.PutAccount(ctx, &dep.Account{Name: "a", ConsumerKey: "new", CreatedAt: time.Now(), UpdatedAt: time.Now()})
		}},
		{"delete lock", lock, func(ctx context.Context, s *sqlstore.Store) error { return s.DeleteAccount(ctx, "a") }},
		{"state lock", lock, func(ctx context.Context, s *sqlstore.Store) error {
			return s.SetAccountState(ctx, "a", dep.AccountState{TokenInvalid: true})
		}},
		{"staging lock", lock, func(ctx context.Context, s *sqlstore.Store) error {
			return s.PutKeypair(ctx, "a", dep.StageStaged, &dep.Keypair{CertPEM: []byte("new-cert"), KeyPEM: []byte("new-key")})
		}},
		{"promotion lock", lock, func(ctx context.Context, s *sqlstore.Store) error { return s.UpstageKeypair(ctx, "a") }},
		{"account deletion", "BEFORE DELETE ON dep_accounts", func(ctx context.Context, s *sqlstore.Store) error { return s.DeleteAccount(ctx, "a") }},
		{"account state", "BEFORE UPDATE OF token_invalid ON dep_accounts", func(ctx context.Context, s *sqlstore.Store) error {
			return s.SetAccountState(ctx, "a", dep.AccountState{TokenInvalid: true})
		}},
		{"key promotion", "BEFORE INSERT ON dep_keypairs WHEN NEW.stage = 'current'", func(ctx context.Context, s *sqlstore.Store) error { return s.UpstageKeypair(ctx, "a") }},
		{"inventory batch", "BEFORE INSERT ON dep_devices WHEN NEW.serial_number = 'T'", func(ctx context.Context, s *sqlstore.Store) error {
			return s.PutDevices(ctx, "a", []dep.Device{{SerialNumber: "S", ProfileUUID: "changed"}, {SerialNumber: "T"}}, time.Now())
		}},
		{"generation batch", "BEFORE UPDATE OF fetch_generation ON dep_devices WHEN NEW.serial_number = 'T'", func(ctx context.Context, s *sqlstore.Store) error {
			return s.MarkFetched(ctx, "a", "new", []string{"S", "T"})
		}},
		{"profile deletion", "BEFORE DELETE ON dep_profiles", func(ctx context.Context, s *sqlstore.Store) error { return s.DeleteProfile(ctx, "a", "p") }},
		{"profile definition", "BEFORE INSERT ON dep_profiles", func(ctx context.Context, s *sqlstore.Store) error {
			return s.PutProfile(ctx, "a", &dep.Profile{ProfileUUID: "p", ProfileName: "changed", URL: "https://example.com/new"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newWorkerStateStore(t)
			seed(t, s)
			ctx := t.Context()
			if err := s.PutDevices(ctx, "a", []dep.Device{{SerialNumber: "T"}}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := s.PutKeypair(ctx, "a", dep.StageStaged, &dep.Keypair{CertPEM: []byte("staged-cert"), KeyPEM: []byte("staged-key")}); err != nil {
				t.Fatal(err)
			}
			before := mutationSnapshot(t, s)
			// #nosec G202 -- trigger clauses are fixed test cases, never user input.
			if _, err := s.DB().ExecContext(ctx, "CREATE TRIGGER fail_mutation "+tc.trigger+" BEGIN SELECT RAISE(ABORT, 'injected mutation failure'); END"); err != nil {
				t.Fatal(err)
			}
			if err := tc.mutate(ctx, s); err == nil || !strings.Contains(err.Error(), "injected mutation failure") {
				t.Fatalf("lost the injected SQL failure: %v", err)
			}
			if after := mutationSnapshot(t, s); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed mutation changed account state: before=%v after=%v", before, after)
			}
			if _, err := s.DB().ExecContext(ctx, "DROP TRIGGER fail_mutation"); err != nil {
				t.Fatal(err)
			}
			if err := tc.mutate(ctx, s); err != nil {
				t.Fatalf("retry after removing the failure did not recover: %v", err)
			}
		})
	}
}

// mutationSnapshot observes public account state and generation membership so a
// failed multi-row write cannot conceal changes to rows before the failing row.
func mutationSnapshot(t *testing.T, s *sqlstore.Store) []any {
	t.Helper()
	ctx := t.Context()
	account, accountErr := s.GetAccount(ctx, "a")
	secrets, secretErr := s.RawSecrets(ctx, "a")
	devices, deviceErr := s.ListDevices(ctx, "a", dep.DeviceQuery{}, paging.Page{})
	unseen, unseenErr := s.ListDevices(ctx, "a", dep.DeviceQuery{NotSeenInGeneration: "new"}, paging.Page{})
	profiles, profileErr := s.ListProfiles(ctx, "a", paging.Page{})
	assignments, assignmentErr := s.ListAssignments(ctx, "a", dep.AssignmentQuery{}, paging.Page{})
	if err := errors.Join(accountErr, secretErr, deviceErr, unseenErr, profileErr, assignmentErr); err != nil {
		t.Fatal(err)
	}
	return []any{account, secrets, devices, unseen, profiles, assignments}
}

// TestAccountWritesJoinOuterRollback proves ordinary account mutations use the
// enclosing SQL unit of work, even though each store operation uses a savepoint.
func TestAccountWritesJoinOuterRollback(t *testing.T) {
	s := newWorkerStateStore(t)
	seed(t, s)
	before := mutationSnapshot(t, s)
	abort := errors.New("enclosing operation failed")
	uow := sqlcommon.UnitOfWork{DB: s.DB(), Dialect: sqlite.Dialect}
	err := uow.Run(t.Context(), func(ctx context.Context) error {
		account, err := s.GetAccount(ctx, "a")
		if err != nil {
			return err
		}
		account.AccessToken = "new-token"
		if err := s.PutAccount(ctx, account); err != nil {
			return err
		}
		if err := s.SetSession(ctx, "a", "new-session"); err != nil {
			return err
		}
		if err := s.SetAccountState(ctx, "a", dep.AccountState{TokenInvalid: true}); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("outer failure lost: %v", err)
	}
	if after := mutationSnapshot(t, s); !reflect.DeepEqual(after, before) {
		t.Fatal("account or session escaped the enclosing rollback")
	}
}

// TestRawSecretsRejectsPartialResults never returns an incomplete credential
// inspection when a later session or keypair query fails.
func TestRawSecretsRejectsPartialResults(t *testing.T) {
	for _, table := range []string{"dep_sessions", "dep_keypairs"} {
		t.Run(table, func(t *testing.T) {
			s := newWorkerStateStore(t)
			seed(t, s)
			// #nosec G202 -- table names are fixed test fixtures.
			if _, err := s.DB().ExecContext(t.Context(), "DROP TABLE "+table); err != nil {
				t.Fatal(err)
			}
			if values, err := s.RawSecrets(t.Context(), "a"); err == nil || values != nil {
				t.Fatalf("partial secret inventory returned: %v %v", values, err)
			}
		})
	}
}
