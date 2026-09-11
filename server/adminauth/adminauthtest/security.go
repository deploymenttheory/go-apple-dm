package adminauthtest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

func runActiveRoot(t *testing.T, factory NewStore) {
	t.Run("InvalidChangesPreserveCredentials", func(t *testing.T) {
		s := factory(t)
		ctx := t.Context()
		p := principal("root")
		p.Root = true
		if _, err := s.CreatePrincipal(ctx, p, "root-digest", t0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyPrincipal(
			ctx,
			"missing",
			adminauth.PrincipalChange{Op: "revoke"},
			t0,
		); !errors.Is(
			err,
			adminauth.ErrNotFound,
		) {
			t.Fatalf("missing principal: %v", err)
		}
		for _, change := range []adminauth.PrincipalChange{
			{Op: "unknown"},
			{Op: "update", Roles: []string{"invalid role"}},
			{Op: "rotate", TokenID: "new"},
			{Op: "rotate", Digest: "new"},
		} {
			if _, err := s.ApplyPrincipal(
				ctx,
				p.Name,
				change,
				t0,
			); !errors.Is(
				err,
				adminauth.ErrInvalid,
			) {
				t.Fatalf("invalid change accepted: %v", err)
			}
			got, err := s.PrincipalByDigest(ctx, "root-digest")
			if err != nil || !got.Root || got.Active(t0) != nil {
				t.Fatalf("invalid change damaged root: %+v %v", got, err)
			}
		}
	})
	for _, op := range []string{"update", "revoke", "delete", "rotate"} {
		t.Run(op, func(t *testing.T) {
			s := factory(t)
			ctx := t.Context()
			for _, name := range []string{"one", "two", "expired"} {
				p := principal(name)
				p.Root = true
				if name == "expired" {
					p.ExpiresAt = t0.Add(-time.Hour)
				}
				if _, err := s.CreatePrincipal(ctx, p, "digest-"+name, t0); err != nil {
					t.Fatal(err)
				}
			}
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for _, name := range []string{"one", "two"} {
				wg.Go(func() {
					_, err := s.ApplyPrincipal(ctx, name, adminauth.PrincipalChange{
						Op:        op,
						Root:      false,
						Digest:    "new-" + name,
						TokenID:   "new-" + name,
						ExpiresAt: t0,
					}, t0)
					errs <- err
				})
			}
			wg.Wait()
			close(errs)
			allowed, denied := 0, 0
			for err := range errs {
				if err == nil {
					allowed++
				} else if errors.Is(err, adminauth.ErrLastRoot) {
					denied++
				} else {
					t.Fatal(err)
				}
			}
			if allowed != 1 || denied != 1 {
				t.Fatalf("mutations allowed=%d denied=%d", allowed, denied)
			}
			principals, err := s.Principals(ctx, adminauth.Page{})
			if err != nil {
				t.Fatal(err)
			}
			active := 0
			for _, p := range principals.Items {
				if p.Root && p.Active(t0) == nil {
					active++
				}
			}
			if active != 1 {
				t.Fatalf("active root count=%d", active)
			}
		})
	}
}
