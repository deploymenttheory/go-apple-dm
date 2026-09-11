package adminauth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// Role subsets do not bound authority granted directly to a principal name.
// Creating a previously absent named principal must not mint that authority.
func TestDelegatedCreationCannotClaimNamedPolicyAuthority(t *testing.T) {
	m, _, _ := manager(t)
	root, _ := newRoot(t, m, "root")
	actor, _, err := m.CreatePrincipal(
		t.Context(),
		root,
		adminauth.Principal{Name: "operator"},
		time.Time{},
	)
	if err != nil {
		t.Fatal(err)
	}
	put(t, m, "reserved", `permit(principal == MDM::Principal::"reserved", action, resource);`)
	_, token, err := m.CreatePrincipal(
		t.Context(),
		actor,
		adminauth.Principal{Name: "reserved"},
		time.Time{},
	)
	if !errors.Is(err, adminauth.ErrDenied) && !errors.Is(err, adminauth.ErrEscalation) {
		t.Fatalf(
			"delegated caller minted named policy authority: token returned=%v error=%v",
			token != "",
			err,
		)
	}
}

func TestRevokedRootDoesNotPermitRevokingLastActiveRoot(t *testing.T) {
	m, _, _ := manager(t)
	root, _ := newRoot(t, m, "first")
	newRoot(t, m, "second")
	if err := m.Revoke(t.Context(), root, "second"); err != nil {
		t.Fatal(err)
	}
	if err := m.Revoke(t.Context(), root, "first"); !errors.Is(err, adminauth.ErrLastRoot) {
		t.Fatalf("last usable root credential was revoked: %v", err)
	}
}
