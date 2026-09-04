package adminauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/adminauthtest"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/inmem"
)

// newRoot creates the first root principal, as a deployment's bootstrap does.
func newRoot(t *testing.T, m *adminauth.Manager, name string) (adminauth.Principal, adminauth.Token) {
	t.Helper()
	p, tok, err := m.CreatePrincipal(context.Background(), adminauth.Root,
		adminauth.Principal{Name: name, Root: true}, time.Time{})
	if err != nil {
		t.Fatalf("CreatePrincipal %s: %v", name, err)
	}
	return p, tok
}

func TestNew(t *testing.T) {
	reg := registry(t)
	if _, err := adminauth.New(nil, reg); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("nil store: %v", err)
	}
	if _, err := adminauth.New(inmem.New(), nil); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatalf("nil registry: %v", err)
	}
	m, err := adminauth.New(inmem.New(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if m.Registry() != reg {
		t.Fatal("Registry did not return the registry it was built with")
	}
	// A nil clock is ignored rather than installed.
	if _, err := adminauth.New(inmem.New(), reg, adminauth.WithClock(nil)); err != nil {
		t.Fatalf("WithClock(nil): %v", err)
	}
}

func TestAuthenticate(t *testing.T) {
	ctx := context.Background()

	t.Run("RoundTrip", func(t *testing.T) {
		m, _, _ := manager(t)
		_, tok := newRoot(t, m, "root")
		p, err := m.Authenticate(ctx, tok)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if p.Name != "root" || !p.Root {
			t.Fatalf("authenticated as %+v", p)
		}
		if p.TokenID == "" {
			t.Fatal("the principal carries no credential id for an audit line")
		}
	})

	// A malformed token is refused on its checksum, so a scanner spraying the
	// endpoint costs no database round trips.
	t.Run("MalformedNeedsNoStore", func(t *testing.T) {
		st := &adminauthtest.Failing{Store: inmem.New(), Fail: "PrincipalByDigest"}
		m, err := adminauth.New(st, registry(t))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Authenticate(ctx, "not-a-token"); !errors.Is(err, adminauth.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid without reaching the store", err)
		}
	})

	t.Run("UnknownToken", func(t *testing.T) {
		m, _, _ := manager(t)
		other, err := adminauth.Mint()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Authenticate(ctx, other); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("RevokedAndExpired", func(t *testing.T) {
		m, _, fake := manager(t)
		newRoot(t, m, "root")
		root, _ := m.Principal(ctx, "root")

		_, tok, err := m.CreatePrincipal(ctx, root, adminauth.Principal{Name: "ci"}, t0.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Authenticate(ctx, tok); err != nil {
			t.Fatalf("before expiry: %v", err)
		}
		fake.Advance(2 * time.Hour)
		if _, err := m.Authenticate(ctx, tok); !errors.Is(err, adminauth.ErrExpired) {
			t.Fatalf("after expiry: %v, want ErrExpired", err)
		}
		fake.Advance(-2 * time.Hour)
		if err := m.Revoke(ctx, root, "ci"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if _, err := m.Authenticate(ctx, tok); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("after revoke: %v, want ErrNotFound", err)
		}
	})

	// Rotation invalidates the previous value at once, which is what makes a
	// leaked credential recoverable without a restart.
	t.Run("RotateInvalidatesOld", func(t *testing.T) {
		m, _, _ := manager(t)
		newRoot(t, m, "root")
		root, _ := m.Principal(ctx, "root")
		_, old, err := m.CreatePrincipal(ctx, root, adminauth.Principal{Name: "ci"}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		_, fresh, err := m.Rotate(ctx, root, "ci", time.Time{})
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if old == fresh {
			t.Fatal("Rotate returned the same token")
		}
		if _, err := m.Authenticate(ctx, old); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("the old token still authenticates: %v", err)
		}
		if _, err := m.Authenticate(ctx, fresh); err != nil {
			t.Fatalf("the new token does not authenticate: %v", err)
		}
	})
}

// The invariants that stop an operator escalating or locking themselves out.
func TestAdministration(t *testing.T) {
	ctx := context.Background()

	t.Run("PolicyEditingNeedsRoot", func(t *testing.T) {
		m, _, _ := manager(t)
		plain := adminauth.Principal{Name: "reader", Roles: []string{"reader"}}
		doc := adminauth.Policy{Name: "p", Source: `permit (principal, action == MDM::Action::"listEnrollments", resource);`}
		if _, err := m.PutPolicy(ctx, plain, doc); !errors.Is(err, adminauth.ErrDenied) {
			t.Fatalf("PutPolicy: %v, want ErrDenied", err)
		}
		if _, err := m.GetPolicy(ctx, plain, "p"); !errors.Is(err, adminauth.ErrDenied) {
			t.Fatalf("GetPolicy: %v", err)
		}
		if _, err := m.Policies(ctx, plain); !errors.Is(err, adminauth.ErrDenied) {
			t.Fatalf("Policies: %v", err)
		}
		if err := m.DeletePolicy(ctx, plain, "p"); !errors.Is(err, adminauth.ErrDenied) {
			t.Fatalf("DeletePolicy: %v", err)
		}
	})

	// A root principal cannot grant a policy that would make it root: the
	// capability lives outside the policy system that bounds everything else.
	t.Run("RootIsNotPolicyGrantable", func(t *testing.T) {
		m, _, _ := manager(t)
		newRoot(t, m, "root")
		root, _ := m.Principal(ctx, "root")
		put(t, m, "everything", `permit (principal, action, resource);`)
		// Even with a permit-all policy, a non-root principal cannot
		// administer policies.
		plain := adminauth.Principal{Name: "reader"}
		if _, err := m.PutPolicy(ctx, plain, adminauth.Policy{Name: "x", Source: `permit (principal, action, resource);`}); !errors.Is(err, adminauth.ErrDenied) {
			t.Fatalf("a permit-all policy granted policy administration: %v", err)
		}
		if _, err := m.Policies(ctx, root); err != nil {
			t.Fatalf("root cannot list policies: %v", err)
		}
	})

	// Ported from Zentral's can_issue_credentials_for.
	t.Run("SubsetOnlyIssuance", func(t *testing.T) {
		m, _, _ := manager(t)
		newRoot(t, m, "root")
		root, _ := m.Principal(ctx, "root")
		if _, _, err := m.CreatePrincipal(ctx, root, adminauth.Principal{Name: "ops", Roles: []string{"a", "b"}}, time.Time{}); err != nil {
			t.Fatal(err)
		}
		ops, _ := m.Principal(ctx, "ops")
		// ops holds a and b, so it may create a principal with a subset.
		if _, _, err := m.CreatePrincipal(ctx, ops, adminauth.Principal{Name: "sub", Roles: []string{"a"}}, time.Time{}); err != nil {
			t.Fatalf("subset grant refused: %v", err)
		}
		// It may not grant a role it does not hold.
		if _, _, err := m.CreatePrincipal(ctx, ops, adminauth.Principal{Name: "up", Roles: []string{"c"}}, time.Time{}); !errors.Is(err, adminauth.ErrEscalation) {
			t.Fatalf("granting an unheld role: %v, want ErrEscalation", err)
		}
		// Nor may it make anyone root, which would hand over policy editing.
		if _, _, err := m.CreatePrincipal(ctx, ops, adminauth.Principal{Name: "up2", Root: true}, time.Time{}); !errors.Is(err, adminauth.ErrEscalation) {
			t.Fatalf("granting root: %v, want ErrEscalation", err)
		}
		// Nor act on a credential more privileged than its own.
		sub, _ := m.Principal(ctx, "sub")
		if _, _, err := m.Rotate(ctx, sub, "ops", time.Time{}); !errors.Is(err, adminauth.ErrEscalation) {
			t.Fatalf("rotating a superior credential: %v, want ErrEscalation", err)
		}
		if err := m.Revoke(ctx, sub, "ops"); !errors.Is(err, adminauth.ErrEscalation) {
			t.Fatalf("revoking a superior credential: %v", err)
		}
		if err := m.DeletePrincipal(ctx, sub, "ops"); !errors.Is(err, adminauth.ErrEscalation) {
			t.Fatalf("deleting a superior credential: %v", err)
		}
		if _, err := m.UpdatePrincipal(ctx, sub, "ops", []string{"a", "b"}, false); !errors.Is(err, adminauth.ErrEscalation) {
			t.Fatalf("updating a superior credential: %v", err)
		}
	})

	// A principal a policy names directly no longer derives its authority
	// from its roles, so the role subset test cannot bound it.
	t.Run("PrincipalNamedByPolicyIsRefused", func(t *testing.T) {
		m, _, _ := manager(t)
		newRoot(t, m, "root")
		root, _ := m.Principal(ctx, "root")
		if _, _, err := m.CreatePrincipal(ctx, root, adminauth.Principal{Name: "special"}, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := m.CreatePrincipal(ctx, root, adminauth.Principal{Name: "peer"}, time.Time{}); err != nil {
			t.Fatal(err)
		}
		peer, _ := m.Principal(ctx, "peer")
		// Before any policy names it, a peer may rotate it.
		if _, _, err := m.Rotate(ctx, peer, "special", time.Time{}); err != nil {
			t.Fatalf("rotate before the policy: %v", err)
		}
		put(t, m, "special", `permit (principal == MDM::Principal::"special", action == MDM::Action::"enqueueCommand", resource);`)
		if _, _, err := m.Rotate(ctx, peer, "special", time.Time{}); !errors.Is(err, adminauth.ErrEscalation) {
			t.Fatalf("rotate after the policy names it: %v, want ErrEscalation", err)
		}
		// The principal may still rotate its own credential.
		special, _ := m.Principal(ctx, "special")
		if _, _, err := m.Rotate(ctx, special, "special", time.Time{}); err != nil {
			t.Fatalf("self rotation refused: %v", err)
		}
	})

	t.Run("LastRootCannotBeRemoved", func(t *testing.T) {
		m, _, _ := manager(t)
		newRoot(t, m, "root")
		root, _ := m.Principal(ctx, "root")
		if err := m.DeletePrincipal(ctx, root, "root"); !errors.Is(err, adminauth.ErrLastRoot) {
			t.Fatalf("delete: %v, want ErrLastRoot", err)
		}
		if err := m.Revoke(ctx, root, "root"); !errors.Is(err, adminauth.ErrLastRoot) {
			t.Fatalf("revoke: %v, want ErrLastRoot", err)
		}
		if _, err := m.UpdatePrincipal(ctx, root, "root", nil, false); !errors.Is(err, adminauth.ErrLastRoot) {
			t.Fatalf("demote: %v, want ErrLastRoot", err)
		}
		// With a second root, the first may go.
		if _, _, err := m.CreatePrincipal(ctx, root, adminauth.Principal{Name: "root2", Root: true}, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if err := m.DeletePrincipal(ctx, root, "root"); err != nil {
			t.Fatalf("delete with a second root: %v", err)
		}
	})

	t.Run("BadInput", func(t *testing.T) {
		m, _, _ := manager(t)
		for name, p := range map[string]adminauth.Principal{
			"bad name": {Name: "not a name!"},
			"bad role": {Name: "ok", Roles: []string{"not a role!"}},
		} {
			if _, _, err := m.CreatePrincipal(ctx, adminauth.Root, p, time.Time{}); !errors.Is(err, adminauth.ErrInvalid) {
				t.Fatalf("%s: %v, want ErrInvalid", name, err)
			}
		}
		if _, err := m.Principal(ctx, "nobody"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("unknown principal: %v", err)
		}
		if _, _, err := m.Rotate(ctx, adminauth.Root, "nobody", time.Time{}); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("rotate unknown: %v", err)
		}
	})

	t.Run("Listing", func(t *testing.T) {
		m, _, _ := manager(t)
		newRoot(t, m, "root")
		root, _ := m.Principal(ctx, "root")
		for _, n := range []string{"a", "b"} {
			if _, _, err := m.CreatePrincipal(ctx, root, adminauth.Principal{Name: n}, time.Time{}); err != nil {
				t.Fatal(err)
			}
		}
		res, err := m.Principals(ctx, adminauth.Page{})
		if err != nil || len(res.Items) != 3 {
			t.Fatalf("principals = %d, %v; want 3", len(res.Items), err)
		}
	})
}

// Store failures surface rather than being mistaken for a denial: an
// authorization system that fails open, or that reports a database outage as
// "forbidden", is worse than one that errors.
func TestStoreFailuresSurface(t *testing.T) {
	ctx := context.Background()
	for _, method := range []string{"Policies", "PolicyVersion", "CountRoot", "Principal"} {
		t.Run(method, func(t *testing.T) {
			base := inmem.New()
			seed, err := adminauth.New(base, registry(t))
			if err != nil {
				t.Fatal(err)
			}
			newRoot(t, seed, "root")
			root, _ := seed.Principal(ctx, "root")
			if _, _, err := seed.CreatePrincipal(ctx, root, adminauth.Principal{Name: "other", Root: true}, time.Time{}); err != nil {
				t.Fatal(err)
			}

			m, err := adminauth.New(&adminauthtest.Failing{Store: base, Fail: method}, registry(t))
			if err != nil {
				t.Fatal(err)
			}
			var gotErr error
			switch method {
			case "Policies", "PolicyVersion":
				_, gotErr = m.Authorize(ctx, root, "listEnrollments", adminauth.SystemResource, nil)
			case "CountRoot":
				gotErr = m.DeletePrincipal(ctx, root, "other")
			case "Principal":
				_, _, gotErr = m.Rotate(ctx, root, "other", time.Time{})
			}
			if !errors.Is(gotErr, adminauthtest.ErrFailing) {
				t.Fatalf("err = %v, want the injected failure", gotErr)
			}
		})
	}
}

func TestFakeClockIsUsed(t *testing.T) {
	st := inmem.New()
	fake := clock.NewFake(t0)
	m, err := adminauth.New(st, registry(t), adminauth.WithClock(fake))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := newRoot(t, m, "root")
	if !p.CreatedAt.Equal(t0) {
		t.Fatalf("CreatedAt = %v, want the injected clock's %v", p.CreatedAt, t0)
	}
}
