package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/sqlite"
)

// openWithDB returns a migrated store and the pool behind it, so a test can
// damage the schema while the pool stays usable. That reaches the failure
// paths inside a transaction, which a closed pool cannot: there, BeginTx
// fails first and everything after it is unreachable.
func openWithDB(t *testing.T) (*sqlstore.Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "c.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := sqlstore.Open(context.Background(), db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

// The version row is bumped inside the same transaction as the policy write,
// so losing it fails the write rather than committing a policy whose change no
// cached compilation would notice.
func TestPolicyWriteFailsWithoutTheVersionRow(t *testing.T) {
	ctx := context.Background()
	s, db := openWithDB(t)
	if _, err := db.ExecContext(ctx, "DROP TABLE admin_policy_version"); err != nil {
		t.Fatal(err)
	}
	doc := adminauth.Policy{Name: "p", Source: "permit (principal, action, resource);"}
	if _, err := s.PutPolicy(ctx, doc, time.Now()); err == nil {
		t.Fatal("PutPolicy committed without the version row")
	}
	// The insert rolled back with the failed bump, so nothing was stored.
	if _, err := s.GetPolicy(ctx, "p"); !errors.Is(err, adminauth.ErrNotFound) {
		t.Fatalf("a policy survived a failed version bump: %v", err)
	}
}

func TestPolicyDeleteFailsWithoutTheVersionRow(t *testing.T) {
	ctx := context.Background()
	s, db := openWithDB(t)
	if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "p", Source: "permit (principal, action, resource);"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE admin_policy_version"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePolicy(ctx, "p"); err == nil {
		t.Fatal("DeletePolicy committed without the version row")
	}
	// The delete rolled back, so the policy is still there.
	if _, err := s.GetPolicy(ctx, "p"); err != nil {
		t.Fatalf("the policy was deleted despite the failed bump: %v", err)
	}
}

// A replace reads the original creation time first; losing the table surfaces
// as an error rather than silently resetting it.
func TestPolicyReplaceFailsWithoutTheTable(t *testing.T) {
	ctx := context.Background()
	s, db := openWithDB(t)
	if _, err := db.ExecContext(ctx, "DROP TABLE admin_policies"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "p", Source: "permit (principal, action, resource);"}, time.Now()); err == nil {
		t.Fatal("PutPolicy succeeded with no policies table")
	}
}

// A row a previous version wrote, or a hand-edited one, must fail the read
// rather than yield a half-decoded policy.
func TestUnreadableRowSurfaces(t *testing.T) {
	ctx := context.Background()
	s, db := openWithDB(t)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO admin_policies (name, source, description, created_at, updated_at)
		 VALUES ('bad', 'permit (principal, action, resource);', '', 'not-a-timestamp', 'not-a-timestamp')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Policies(ctx); err == nil {
		t.Fatal("Policies decoded a row with an unparseable timestamp")
	}
	if _, err := s.GetPolicy(ctx, "bad"); err == nil {
		t.Fatal("GetPolicy decoded a row with an unparseable timestamp")
	}
}

// Open applies migrations, so a schema it cannot migrate onto is an Open
// error rather than a store that fails on first use.
func TestOpenFailsOnAConflictingSchema(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "x.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, "CREATE TABLE admin_principals (name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err == nil {
		t.Fatal("Open migrated over a conflicting table")
	}
}

func TestSetTokenOnUnknownPrincipal(t *testing.T) {
	ctx := context.Background()
	s, _ := openWithDB(t)
	if _, err := s.SetToken(ctx, "nobody", "d", "id", time.Time{}, time.Now()); !errors.Is(err, adminauth.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A principal created with no digest has no credential, and must never be
// reachable by an empty Authorization header.
func TestPrincipalWithoutADigest(t *testing.T) {
	ctx := context.Background()
	s, _ := openWithDB(t)
	if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "alice"}, "", time.Now()); err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	if _, err := s.PrincipalByDigest(ctx, ""); !errors.Is(err, adminauth.ErrNotFound) {
		t.Fatalf("an empty digest resolved a principal: %v", err)
	}
	// A second credential-less principal is fine: NULL digests do not collide.
	if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "bob"}, "", time.Now()); err != nil {
		t.Fatalf("a second principal with no digest: %v", err)
	}
	if p, err := s.Principal(ctx, "alice"); err != nil || p.TokenID != "" {
		t.Fatalf("principal = %+v, %v", p, err)
	}
}
