package adminauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/deploymenttheory/go-apple-dm/adminauth"
	"github.com/deploymenttheory/go-apple-dm/adminauth/adminauthtest"
	"github.com/deploymenttheory/go-apple-dm/adminauth/inmem"
)

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"alice":     true,
		"ci-bot":    true,
		"a_b.c-1":   true,
		"A":         true,
		"":          false,
		"has space": false,
		"has/slash": false,
		`has"quote`: false,
		"has:colon": false,
		"emoji-✓":   false,
	} {
		if got := adminauth.ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v, want %v", name, got, want)
		}
	}
	// A name is bounded so it stays printable in an audit line.
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if adminauth.ValidName(string(long)) {
		t.Fatal("a 65-character name was accepted")
	}
	if !adminauth.ValidName(string(long[:64])) {
		t.Fatal("a 64-character name was refused")
	}
}

func TestParseRoles(t *testing.T) {
	got, err := adminauth.ParseRoles(" b , a ,, b ")
	if err != nil {
		t.Fatalf("ParseRoles: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("ParseRoles = %v, want sorted and deduplicated", got)
	}
	if _, err := adminauth.ParseRoles("ok,not a role"); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("malformed role: %v, want ErrInvalid", err)
	}
	if got, err := adminauth.ParseRoles(""); err != nil || got != nil {
		t.Fatalf("empty = %v, %v; want no roles and no error", got, err)
	}
}

func TestPrincipalCovers(t *testing.T) {
	root := adminauth.Principal{Name: "root", Root: true}
	ops := adminauth.Principal{Name: "ops", Roles: []string{"a", "b"}}
	sub := adminauth.Principal{Name: "sub", Roles: []string{"a"}}

	if !root.Covers(ops) || !root.Covers(root) {
		t.Fatal("root does not cover everything")
	}
	if !ops.Covers(sub) {
		t.Fatal("a superset of roles does not cover a subset")
	}
	if sub.Covers(ops) {
		t.Fatal("a subset covers a superset")
	}
	if ops.Covers(root) {
		t.Fatal("a non-root principal covers a root one")
	}
}

func TestPrincipalActive(t *testing.T) {
	now := t0
	revoked := adminauth.Principal{Name: "a"}
	if err := revoked.Active(now); !errors.Is(err, adminauth.ErrRevoked) {
		t.Fatalf("revoked: %v, want ErrRevoked", err)
	}
	never := adminauth.Principal{Name: "a", TokenID: "x"}
	if err := never.Active(now); err != nil {
		t.Fatalf("a token with no expiry: %v", err)
	}
	expiring := adminauth.Principal{Name: "a", TokenID: "x", ExpiresAt: now.Add(time.Minute)}
	if err := expiring.Active(now); err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	if err := expiring.Active(now.Add(time.Minute)); !errors.Is(err, adminauth.ErrExpired) {
		t.Fatalf("at expiry: %v, want ErrExpired", err)
	}
}

// Roles reach Cedar as entity parents, which is what makes `principal in
// MDM::Role::"x"` resolve.
func TestPrincipalEntity(t *testing.T) {
	p := adminauth.Principal{Name: "alice", Roles: []string{"reader", "ops"}}
	e := p.Entity()
	if e.UID != adminauth.PrincipalUID("alice") {
		t.Fatalf("UID = %v", e.UID)
	}
	if e.Parents.Len() != 2 {
		t.Fatalf("parents = %d, want one per role", e.Parents.Len())
	}
	if !e.Parents.Contains(adminauth.RoleUID("reader")) {
		t.Fatal("the reader role is not a parent")
	}
	if got := adminauth.ActionUID("x"); got.Type != adminauth.EntityAction {
		t.Fatalf("ActionUID type = %v", got.Type)
	}
}

// An empty policy set denies, and a nil one does too rather than panicking.
func TestPolicySetEdges(t *testing.T) {
	reg := registry(t)
	empty, err := adminauth.Compile(reg, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := empty.Authorize(adminauth.Principal{Name: "a"}, "listEnrollments", types.EntityUID{}, nil); d.Allowed {
		t.Fatal("an empty policy set allowed a request")
	}
	if empty.Version() != 1 {
		t.Fatalf("Version = %d", empty.Version())
	}
	var nilSet *adminauth.PolicySet
	if d := nilSet.Authorize(adminauth.Principal{Name: "a"}, "listEnrollments", adminauth.SystemResource, nil); d.Allowed {
		t.Fatal("a nil policy set allowed a request")
	}
}

// A store failure during compilation surfaces rather than becoming an empty
// policy set, which would deny everything and look like a policy bug.
func TestAuthorizeStoreFailure(t *testing.T) {
	ctx := context.Background()
	m, err := adminauth.New(&adminauthtest.Failing{Store: inmem.New(), Fail: "Policies"}, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Authorize(ctx, adminauth.Root, "listEnrollments", adminauth.SystemResource, nil); !errors.Is(err, adminauthtest.ErrFailing) {
		t.Fatalf("err = %v, want the injected failure", err)
	}
}

// A stored policy that no longer compiles is an error, not a silent deny.
func TestCompileRejectsStoredGarbage(t *testing.T) {
	ctx := context.Background()
	st := inmem.New()
	if _, err := st.PutPolicy(ctx, adminauth.Policy{Name: "bad", Source: "not cedar"}, t0); err != nil {
		t.Fatal(err)
	}
	m, err := adminauth.New(st, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Authorize(ctx, adminauth.Root, "listEnrollments", adminauth.SystemResource, nil); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestValidateNames(t *testing.T) {
	reg := registry(t)
	if err := adminauth.Validate(reg, adminauth.Policy{Name: "bad name", Source: "permit (principal, action, resource);"}); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("bad policy name: %v", err)
	}
	if err := adminauth.Validate(reg, adminauth.Policy{Name: "ok", Source: `permit (principal, action == MDM::Action::"listEnrollments", resource);`}); err != nil {
		t.Fatalf("a valid policy was refused: %v", err)
	}
}
