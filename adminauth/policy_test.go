package adminauth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/deploymenttheory/go-apple-dm/adminauth"
	"github.com/deploymenttheory/go-apple-dm/adminauth/inmem"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
)

var t0 = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// registry mirrors the shape the admin route table will register.
func registry(t *testing.T) *adminauth.Registry {
	t.Helper()
	reg, err := adminauth.NewRegistry(
		adminauth.Action{ID: "listEnrollments", Help: "List enrolled devices.", Resource: adminauth.EntitySystem},
		adminauth.Action{ID: "enqueueCommand", Help: "Queue a command to a device.", Resource: adminauth.EntityEnrollment},
		adminauth.Action{ID: "exportEnrollments", Help: "Export enrollments including unlock and bootstrap tokens.", Resource: adminauth.EntitySystem},
		adminauth.Action{ID: "putDeclaration", Help: "Publish a declaration.", Resource: adminauth.EntityDeclaration},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

func manager(t *testing.T) (*adminauth.Manager, *inmem.Store, *clock.Fake) {
	t.Helper()
	st := inmem.New()
	fake := clock.NewFake(t0)
	m, err := adminauth.New(st, registry(t), adminauth.WithClock(fake))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m, st, fake
}

// put stores a policy as the bootstrap root actor.
func put(t *testing.T, m *adminauth.Manager, name, src string) {
	t.Helper()
	if _, err := m.PutPolicy(context.Background(), adminauth.Root, adminauth.Policy{Name: name, Source: src}); err != nil {
		t.Fatalf("PutPolicy %s: %v", name, err)
	}
}

func TestAuthorize(t *testing.T) {
	ctx := context.Background()

	t.Run("DefaultDeny", func(t *testing.T) {
		m, _, _ := manager(t)
		p := adminauth.Principal{Name: "nobody"}
		d, err := m.Authorize(ctx, p, "listEnrollments", adminauth.SystemResource, nil)
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			t.Fatal("an empty policy set allowed a request")
		}
	})

	t.Run("RoleGrant", func(t *testing.T) {
		m, _, _ := manager(t)
		put(t, m, "readers", `permit (principal in MDM::Role::"reader", action == MDM::Action::"listEnrollments", resource);`)
		reader := adminauth.Principal{Name: "alice", Roles: []string{"reader"}}
		other := adminauth.Principal{Name: "bob"}
		if d, _ := m.Authorize(ctx, reader, "listEnrollments", adminauth.SystemResource, nil); !d.Allowed {
			t.Fatal("reader was refused")
		}
		if d, _ := m.Authorize(ctx, other, "listEnrollments", adminauth.SystemResource, nil); d.Allowed {
			t.Fatal("a principal with no role was allowed")
		}
	})

	// The rule a coarse scope cannot express: a CI credential that may run
	// inventory commands but not destructive ones.
	t.Run("ContextNarrowsAnAction", func(t *testing.T) {
		m, _, _ := manager(t)
		put(t, m, "ci", `permit (
			principal == MDM::Principal::"ci",
			action == MDM::Action::"enqueueCommand",
			resource
		) when { context.requestType == "DeviceInformation" };`)
		ci := adminauth.Principal{Name: "ci"}
		allowed := map[string]bool{"DeviceInformation": true, "EraseDevice": false}
		for reqType, want := range allowed {
			d, err := m.Authorize(ctx, ci, "enqueueCommand", adminauth.SystemResource,
				map[string]types.Value{"requestType": types.String(reqType)})
			if err != nil {
				t.Fatal(err)
			}
			if d.Allowed != want {
				t.Fatalf("enqueue %s = %v, want %v", reqType, d.Allowed, want)
			}
		}
	})

	t.Run("ForbidOverridesPermit", func(t *testing.T) {
		m, _, _ := manager(t)
		put(t, m, "grant", `permit (principal in MDM::Role::"admin", action, resource);`)
		put(t, m, "guard", `forbid (
			principal, action == MDM::Action::"exportEnrollments", resource
		) unless { principal in MDM::Role::"break-glass" };`)
		admin := adminauth.Principal{Name: "root", Roles: []string{"admin"}}
		if d, _ := m.Authorize(ctx, admin, "exportEnrollments", adminauth.SystemResource, nil); d.Allowed {
			t.Fatal("forbid did not override permit")
		}
		breakGlass := adminauth.Principal{Name: "root", Roles: []string{"admin", "break-glass"}}
		if d, _ := m.Authorize(ctx, breakGlass, "exportEnrollments", adminauth.SystemResource, nil); !d.Allowed {
			t.Fatal("the break-glass exception did not apply")
		}
	})

	t.Run("DecisionNamesThePolicy", func(t *testing.T) {
		m, _, _ := manager(t)
		put(t, m, "readers", `permit (principal in MDM::Role::"reader", action == MDM::Action::"listEnrollments", resource);`)
		d, _ := m.Authorize(ctx, adminauth.Principal{Name: "a", Roles: []string{"reader"}}, "listEnrollments", adminauth.SystemResource, nil)
		if !strings.HasPrefix(d.Policy, "readers/") {
			t.Fatalf("decision policy = %q, want the readers document", d.Policy)
		}
	})

	t.Run("UnknownActionIsAnError", func(t *testing.T) {
		m, _, _ := manager(t)
		_, err := m.Authorize(ctx, adminauth.Root, "noSuchAction", adminauth.SystemResource, nil)
		if !errors.Is(err, adminauth.ErrUnknownAction) {
			t.Fatalf("err = %v, want ErrUnknownAction", err)
		}
	})

	t.Run("PolicyChangeIsPickedUp", func(t *testing.T) {
		m, _, _ := manager(t)
		p := adminauth.Principal{Name: "alice", Roles: []string{"reader"}}
		if d, _ := m.Authorize(ctx, p, "listEnrollments", adminauth.SystemResource, nil); d.Allowed {
			t.Fatal("allowed before any policy existed")
		}
		put(t, m, "readers", `permit (principal in MDM::Role::"reader", action == MDM::Action::"listEnrollments", resource);`)
		if d, _ := m.Authorize(ctx, p, "listEnrollments", adminauth.SystemResource, nil); !d.Allowed {
			t.Fatal("a new policy was not picked up")
		}
		if err := m.DeletePolicy(ctx, adminauth.Root, "readers"); err != nil {
			t.Fatal(err)
		}
		if d, _ := m.Authorize(ctx, p, "listEnrollments", adminauth.SystemResource, nil); d.Allowed {
			t.Fatal("a deleted policy still granted")
		}
	})
}

// A policy naming an action nobody serves parses cleanly in Cedar and then
// silently never grants. Refusing it at write time is the whole reason the
// registry exists.
func TestPutPolicyRejectsUnknownAction(t *testing.T) {
	ctx := context.Background()
	m, _, _ := manager(t)
	_, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{
		Name:   "typo",
		Source: `permit (principal, action == MDM::Action::"lstEnrollments", resource);`,
	})
	if !errors.Is(err, adminauth.ErrUnknownAction) {
		t.Fatalf("err = %v, want ErrUnknownAction", err)
	}
	if !strings.Contains(err.Error(), "listEnrollments") {
		t.Fatalf("the error should name the known actions, got %v", err)
	}
	if _, err := m.GetPolicy(ctx, adminauth.Root, "typo"); !errors.Is(err, adminauth.ErrNotFound) {
		t.Fatalf("a refused policy was stored: %v", err)
	}
}

func TestPutPolicyRejectsMalformedSource(t *testing.T) {
	ctx := context.Background()
	m, _, _ := manager(t)
	for _, tc := range []struct{ name, src string }{
		{"garbage", "this is not cedar"},
		{"truncated", `permit (principal, action ==`},
	} {
		if _, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{Name: tc.name, Source: tc.src}); !errors.Is(err, adminauth.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
	if _, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{Name: "bad name!", Source: `permit (principal, action, resource);`}); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatal("a malformed policy name was accepted")
	}
}

func TestRegistry(t *testing.T) {
	if _, err := adminauth.NewRegistry(adminauth.Action{ID: "a"}, adminauth.Action{ID: "a"}); !errors.Is(err, adminauth.ErrConflict) {
		t.Fatal("a duplicate action id was accepted")
	}
	if _, err := adminauth.NewRegistry(adminauth.Action{ID: "not a name"}); !errors.Is(err, adminauth.ErrInvalid) {
		t.Fatal("a malformed action id was accepted")
	}
	reg := registry(t)
	if got := reg.IDs(); len(got) != 4 || got[0] != "enqueueCommand" {
		t.Fatalf("IDs = %v, want four sorted ids", got)
	}
	if _, ok := reg.Lookup("nope"); ok {
		t.Fatal("Lookup found an action that was never registered")
	}
	// Every action carries operator-facing prose, so `mdmctl policy actions`
	// can say what granting it means.
	for _, a := range reg.Actions() {
		if a.Help == "" {
			t.Fatalf("action %q has no help text", a.ID)
		}
	}
}
