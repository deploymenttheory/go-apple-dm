package sqlstore_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func TestRoleMigrationPreservesCredentialsAndPolicies(t *testing.T) {
	ctx := t.Context()
	db := openDB(t)
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 1); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	token, err := adminauth.Mint()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO admin_principals (name,roles,root,token_digest,token_id,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", "legacy", "operator,reader", true, adminauth.Digest(token), "old-id", now, now); err != nil {
		t.Fatal(err)
	}
	source := `forbid(principal in MDM::Role::"policy-only",action == MDM::Action::"enqueueCommand",resource);`
	if _, err := db.ExecContext(ctx, "INSERT INTO admin_policies (name,source,created_at,updated_at) VALUES (?,?,?,?)", "legacy-forbid", source, now, now); err != nil {
		t.Fatal(err)
	}
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PrincipalByDigest(ctx, adminauth.Digest(token))
	if err != nil || p.Name != "legacy" || !p.Root || p.TokenID != "old-id" || !slices.Equal(p.Roles, []string{"operator", "reader"}) {
		t.Fatal(p, err)
	}
	for _, name := range []string{"operator", "reader", "policy-only"} {
		if _, err := s.Role(ctx, name); err != nil {
			t.Fatal(name, err)
		}
	}
	policy, err := s.GetPolicy(ctx, "legacy-forbid")
	if err != nil || policy.Source != source || !policy.Enabled() {
		t.Fatal(policy, err)
	}
	if _, err := s.BootstrapPrincipal(ctx, adminauth.Principal{Name: "new-root", Root: true, TokenID: "new"}, "new-digest", now); !errors.Is(err, adminauth.ErrConflict) {
		t.Fatalf("upgrade reopened bootstrap: %v", err)
	}
	if _, err := s.ApplyPrincipal(ctx, "legacy", adminauth.PrincipalChange{Op: "update", Root: true}, now); err != nil {
		t.Fatal(err)
	}
	// A stale legacy column cannot reintroduce membership on subsequent opens.
	if _, err := db.ExecContext(ctx, "UPDATE admin_principals SET roles = 'operator' WHERE name = 'legacy'"); err != nil {
		t.Fatal(err)
	}
	s, err = sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Principal(ctx, "legacy")
	if err != nil || len(p.Roles) != 0 {
		t.Fatalf("memberships reimported: %+v %v", p, err)
	}
}
