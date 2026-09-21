package adminauthtest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// runRoles checks role membership and policy references, role pagination, and permanently
// consumed bootstrap under concurrent attempts.
func runRoles(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("MembershipAndPolicyReferences", func(t *testing.T) {
		s := newStore(t)
		ctx := t.Context()
		if _, err := s.CreatePrincipal(ctx, principal("unknown", "absent"), "digest", t0); !errors.Is(err, adminauth.ErrInvalid) {
			t.Fatalf("unknown role accepted: %v", err)
		}
		role, err := s.PutRole(ctx, adminauth.Role{Name: "custom", Description: "initial"}, t0)
		if err != nil {
			t.Fatal(err)
		}
		role.Description = "updated"
		role, err = s.PutRole(ctx, role, t0.Add(time.Minute))
		if err != nil || !role.CreatedAt.Equal(t0) || !role.UpdatedAt.Equal(t0.Add(time.Minute)) {
			t.Fatal(role, err)
		}
		if _, err := s.CreatePrincipal(ctx, principal("member", "custom"), "member-digest", t0); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteRole(ctx, "custom"); !errors.Is(err, adminauth.ErrConflict) {
			t.Fatalf("deleted assigned role: %v", err)
		}
		if _, err := s.ApplyPrincipal(ctx, "member", adminauth.PrincipalChange{Op: "update"}, t0); err != nil {
			t.Fatal(err)
		}
		inactive := false
		policy := adminauth.Policy{Name: "inactive", Source: `permit(principal in MDM::Role::"custom",action,resource);`, Active: &inactive}
		stored, err := s.PutPolicy(ctx, policy, t0)
		if err != nil {
			t.Fatal(err)
		}
		*stored.Active = true
		got, err := s.GetPolicy(ctx, "inactive")
		if err != nil || got.Enabled() {
			t.Fatalf("inactive policy mutated through return value: %+v %v", got, err)
		}
		if err := s.DeleteRole(ctx, "custom"); !errors.Is(err, adminauth.ErrConflict) {
			t.Fatalf("deleted policy reference: %v", err)
		}
		if err := s.DeletePolicy(ctx, "inactive"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteRole(ctx, "custom"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Role(ctx, "custom"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatal(err)
		}
	})
	t.Run("PagedRoles", func(t *testing.T) {
		s := newStore(t)
		page, err := s.Roles(t.Context(), adminauth.Page{Limit: 1})
		if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
			t.Fatal(page, err)
		}
		next, err := s.Roles(t.Context(), adminauth.Page{Limit: 1, Cursor: page.NextCursor})
		if err != nil || len(next.Items) != 1 || next.Items[0].Name <= page.Items[0].Name {
			t.Fatal(next, err)
		}
	})
	t.Run("ConcurrentBootstrapConsumedForever", func(t *testing.T) {
		s := newStore(t)
		ctx := t.Context()
		var wg sync.WaitGroup
		results := make(chan error, 8)
		winners := make(chan adminauth.Principal, 8)
		for i := range 8 {
			wg.Go(func() {
				name := fmt.Sprintf("root-%d", i)
				p, err := s.BootstrapPrincipal(ctx, adminauth.Principal{Name: name, Root: true, TokenID: name, ExpiresAt: t0.Add(time.Minute)}, name, t0)
				results <- err
				if err == nil {
					winners <- p
				}
			})
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil && !errors.Is(err, adminauth.ErrConflict) {
				t.Fatal(err)
			}
		}
		if len(winners) != 1 {
			t.Fatalf("bootstrap winners: %d", len(winners))
		}
		winner := <-winners
		// Expired roots can be removed, but an empty database cannot reopen bootstrap.
		if _, err := s.ApplyPrincipal(ctx, winner.Name, adminauth.PrincipalChange{Op: "delete"}, t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if ready, err := s.Initialized(ctx); err != nil || !ready {
			t.Fatal(ready, err)
		}
		_, err := s.BootstrapPrincipal(ctx, adminauth.Principal{Name: "again", Root: true, TokenID: "again"}, "again", t0)
		if !errors.Is(err, adminauth.ErrConflict) {
			t.Fatalf("bootstrap reopened: %v", err)
		}
	})
}
