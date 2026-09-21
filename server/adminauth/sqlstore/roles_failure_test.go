package sqlstore_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func TestAuthorityWritesRollbackOnStorageFailure(t *testing.T) {
	for _, tc := range []struct{ name, damage, operation string }{
		{"create-lock", "DROP TABLE admin_policy_version", "create"},
		{"role-put-lock", "DROP TABLE admin_policy_version", "put-role"},
		{"role-delete-lock", "DROP TABLE admin_policy_version", "delete-role"},
		{"principal-insert", "CREATE TRIGGER fail BEFORE INSERT ON admin_principals BEGIN SELECT RAISE(ABORT, 'injected'); END", "create"},
		{"membership-delete", "CREATE TRIGGER fail BEFORE DELETE ON admin_principal_roles BEGIN SELECT RAISE(ABORT, 'injected'); END", "update"},
		{"membership-insert", "CREATE TRIGGER fail BEFORE INSERT ON admin_principal_roles BEGIN SELECT RAISE(ABORT, 'injected'); END", "create"},
		{"initialization", "CREATE TRIGGER fail BEFORE UPDATE OF initialized ON admin_policy_version BEGIN SELECT RAISE(ABORT, 'injected'); END", "create"},
		{"role-update", "CREATE TRIGGER fail BEFORE UPDATE ON admin_roles BEGIN SELECT RAISE(ABORT, 'injected'); END", "put-role"},
		{"role-delete", "CREATE TRIGGER fail BEFORE DELETE ON admin_roles BEGIN SELECT RAISE(ABORT, 'injected'); END", "delete-role"},
		{"policy-insert", "CREATE TRIGGER fail BEFORE INSERT ON admin_policies BEGIN SELECT RAISE(ABORT, 'injected'); END", "create-policy"},
		{"policy-update", "CREATE TRIGGER fail BEFORE UPDATE ON admin_policies BEGIN SELECT RAISE(ABORT, 'injected'); END", "put-policy"},
		{"policy-delete", "CREATE TRIGGER fail BEFORE DELETE ON admin_policies BEGIN SELECT RAISE(ABORT, 'injected'); END", "delete-policy"},
		{"policy-version", "CREATE TRIGGER fail BEFORE UPDATE OF version ON admin_policy_version WHEN NEW.version != OLD.version BEGIN SELECT RAISE(ABORT, 'injected'); END", "put-policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := openWithDB(t)
			ctx, now := t.Context(), time.Now().UTC()
			for _, name := range []string{"assigned", "unused"} {
				if _, err := s.PutRole(ctx, adminauth.Role{Name: name, Description: "original"}, now); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "existing", Roles: []string{"assigned"}, TokenID: "original"}, "old-digest", now); err != nil {
				t.Fatal(err)
			}
			policy := adminauth.Policy{Name: "existing", Source: "permit(principal, action, resource);", Description: "original"}
			if _, err := s.PutPolicy(ctx, policy, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, tc.damage); err != nil {
				t.Fatal(err)
			}
			var err error
			switch tc.operation {
			case "create":
				_, err = s.CreatePrincipal(ctx, adminauth.Principal{Name: "new", Roles: []string{"assigned"}, TokenID: "new"}, "new-digest", now)
			case "update":
				_, err = s.ApplyPrincipal(ctx, "existing", adminauth.PrincipalChange{Op: "update", Roles: []string{"unused"}}, now)
			case "put-role":
				_, err = s.PutRole(ctx, adminauth.Role{Name: "unused", Description: "changed"}, now)
			case "delete-role":
				err = s.DeleteRole(ctx, "unused")
			case "create-policy":
				policy.Name = "new"
				_, err = s.PutPolicy(ctx, policy, now)
			case "put-policy":
				policy.Description = "changed"
				_, err = s.PutPolicy(ctx, policy, now)
			case "delete-policy":
				err = s.DeletePolicy(ctx, "existing")
			}
			if err == nil || errors.Is(err, adminauth.ErrNotFound) {
				t.Fatalf("storage failure hidden: %v", err)
			}
			if got, err := s.PrincipalByDigest(ctx, "old-digest"); err != nil || got.TokenID != "original" || len(got.Roles) != 1 || got.Roles[0] != "assigned" {
				t.Fatalf("credential changed after rollback: %+v %v", got, err)
			}
			if _, err := s.Principal(ctx, "new"); !errors.Is(err, adminauth.ErrNotFound) {
				t.Fatalf("new principal survived rollback: %v", err)
			}
			if got, err := s.Role(ctx, "unused"); err != nil || got.Description != "original" {
				t.Fatalf("role changed after rollback: %+v %v", got, err)
			}
			if got, err := s.GetPolicy(ctx, "existing"); err != nil || got.Description != "original" {
				t.Fatalf("policy changed after rollback: %+v %v", got, err)
			}
			if _, err := s.GetPolicy(ctx, "new"); !errors.Is(err, adminauth.ErrNotFound) {
				t.Fatalf("new policy survived rollback: %v", err)
			}
		})
	}
}

func TestRoleOperationsRejectUnreadableAuthority(t *testing.T) {
	for _, tc := range []struct{ name, damage, operation string }{
		{"role-table", "DROP TABLE admin_roles", "put"},
		{"list-table", "DROP TABLE admin_roles", "list"},
		{"role-timestamp", "UPDATE admin_roles SET created_at = 'invalid'", "list"},
		{"membership-count", "DROP TABLE admin_principal_roles", "delete"},
		{"policy-table", "DROP TABLE admin_policies", "delete"},
		{"policy-source", "UPDATE admin_policies SET source = 'invalid Cedar'", "delete"},
		{"principal-membership", "DROP TABLE admin_principal_roles", "principals"},
		{"principal-timestamp", "UPDATE admin_principals SET created_at = 'invalid'", "principals"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := openWithDB(t)
			ctx, now := t.Context(), time.Now().UTC()
			if _, err := s.PutRole(ctx, adminauth.Role{Name: "reader"}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "operator"}, "digest", now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "policy", Source: "permit(principal,action,resource);"}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, tc.damage); err != nil {
				t.Fatal(err)
			}
			var err error
			switch tc.operation {
			case "put":
				_, err = s.PutRole(ctx, adminauth.Role{Name: "reader"}, now)
			case "list":
				_, err = s.Roles(ctx, adminauth.Page{})
			case "delete":
				err = s.DeleteRole(ctx, "reader")
			case "principals":
				_, err = s.Principals(ctx, adminauth.Page{})
			}
			if err == nil || errors.Is(err, adminauth.ErrNotFound) {
				t.Fatalf("unreadable authority reported success or absence: %v", err)
			}
		})
	}
}

func TestBootstrapStorageFailuresDoNotIssueCredential(t *testing.T) {
	for _, damage := range []string{"DROP TABLE admin_policy_version", "UPDATE admin_policy_version SET initialized = 'invalid'", "DROP TABLE admin_principals"} {
		t.Run(damage, func(t *testing.T) {
			s, db := openWithDB(t)
			if _, err := db.ExecContext(t.Context(), damage); err != nil {
				t.Fatal(err)
			}
			p, err := s.BootstrapPrincipal(t.Context(), adminauth.Principal{Name: "root", Root: true, TokenID: "token"}, "digest", time.Now())
			if err == nil || p.TokenID != "" {
				t.Fatalf("bootstrap issued credential with unreadable state: %+v %v", p, err)
			}
		})
	}
}

func TestRoleMigrationFailurePreservesLegacyMemberships(t *testing.T) {
	for _, tc := range []struct{ name, damage string }{
		{"invalid-role", "UPDATE admin_principals SET roles = 'invalid/name'"},
		{"missing-authority", "DELETE FROM admin_policy_version"},
		{"invalid-marker", "UPDATE admin_policy_version SET roles_migrated = 'invalid'"},
		{"missing-principals", "DROP TABLE admin_principals"},
		{"missing-policies", "DROP TABLE admin_policies"},
		{"role-write", "CREATE TRIGGER fail BEFORE INSERT ON admin_roles BEGIN SELECT RAISE(ABORT, 'injected'); END"},
		{"membership-write", "CREATE TRIGGER fail BEFORE INSERT ON admin_principal_roles BEGIN SELECT RAISE(ABORT, 'injected'); END"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := openWithDB(t)
			ctx, now := t.Context(), time.Now().UTC()
			if _, err := s.CreatePrincipal(ctx, adminauth.Principal{Name: "legacy", TokenID: "token"}, "digest", now); err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{"UPDATE admin_policy_version SET roles_migrated = false", "UPDATE admin_principals SET roles = 'legacy-role'", tc.damage} {
				if _, err := db.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err == nil {
				t.Fatal("migration succeeded despite invalid authority")
			}
			if !strings.Contains(tc.damage, "admin_principals") {
				var roles, token string
				if err := db.QueryRowContext(ctx, "SELECT roles,token_id FROM admin_principals WHERE name = 'legacy'").Scan(&roles, &token); err != nil || roles != "legacy-role" || token != "token" {
					t.Fatalf("migration damaged legacy credential: %q %q %v", roles, token, err)
				}
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_roles").Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial role migration committed: %d %v", count, err)
			}
		})
	}
}
