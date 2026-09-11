package sqlstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

func TestPrincipalMutationRollsBackOnFailure(t *testing.T) {
	for name, damage := range map[string]string{
		"missing-lock-table":      "DROP TABLE admin_policy_version",
		"missing-lock-row":        "DELETE FROM admin_policy_version",
		"missing-principal-table": "DROP TABLE admin_principals",
		"write-failure": `CREATE TRIGGER refuse_mutation BEFORE UPDATE ON admin_principals
 BEGIN SELECT RAISE(ABORT, 'test write failure'); END`,
	} {
		t.Run(name, func(t *testing.T) {
			s, db := openWithDB(t)
			ctx, now := t.Context(), time.Now()
			for _, n := range []string{"one", "two"} {
				if _, err := s.CreatePrincipal(
					ctx,
					adminauth.Principal{Name: n, Root: true, TokenID: n},
					"digest-"+n,
					now,
				); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, damage); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ApplyPrincipal(
				ctx,
				"one",
				adminauth.PrincipalChange{Op: "revoke"},
				now,
			); err == nil {
				t.Fatal("mutation succeeded with unavailable lock or storage")
			}
			if name != "missing-principal-table" {
				p, err := s.PrincipalByDigest(ctx, "digest-one")
				if err != nil || p.Active(now) != nil {
					t.Fatalf("failed mutation revoked the credential: %+v %v", p, err)
				}
			}
		})
	}
}

func TestPrincipalRotationCannotShareDigest(t *testing.T) {
	s := openStore(t)
	ctx, now := t.Context(), time.Now()
	for _, n := range []string{"one", "two"} {
		if _, err := s.CreatePrincipal(
			ctx,
			adminauth.Principal{Name: n, TokenID: n},
			"digest-"+n,
			now,
		); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.ApplyPrincipal(
		ctx,
		"one",
		adminauth.PrincipalChange{Op: "rotate", Digest: "digest-two", TokenID: "new"},
		now,
	)
	if !errors.Is(err, adminauth.ErrConflict) {
		t.Fatalf("credential collision not rejected: %v", err)
	}
	p, err := s.PrincipalByDigest(ctx, "digest-one")
	if err != nil || p.TokenID != "one" {
		t.Fatalf("failed rotation invalidated the original token: %+v %v", p, err)
	}
}
