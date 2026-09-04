package adminauthtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
)

// t0 is the instant every case starts from, so stored timestamps are
// comparable across backends.
var t0 = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// NewStore builds an empty store for one subtest.
type NewStore func(t *testing.T) adminauth.Store

// RunSuite runs every contract case against newStore.
func RunSuite(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("Principals", func(t *testing.T) { runPrincipals(t, newStore) })
	t.Run("Tokens", func(t *testing.T) { runTokens(t, newStore) })
	t.Run("Policies", func(t *testing.T) { runPolicies(t, newStore) })
}

func principal(name string, roles ...string) adminauth.Principal {
	return adminauth.Principal{Name: name, Roles: roles, TokenID: name + "-id"}
}

func runPrincipals(t *testing.T, newStore NewStore) {
	ctx := context.Background()

	t.Run("RoundTrip", func(t *testing.T) {
		s := newStore(t)
		want := principal("alice", "reader", "operator")
		want.Root = true
		got, err := s.CreatePrincipal(ctx, want, "digest-alice", t0)
		if err != nil {
			t.Fatalf("CreatePrincipal: %v", err)
		}
		if got.Name != "alice" || !got.Root || len(got.Roles) != 2 {
			t.Fatalf("created = %+v", got)
		}
		read, err := s.Principal(ctx, "alice")
		if err != nil {
			t.Fatalf("Principal: %v", err)
		}
		if read.Name != "alice" || !read.Root || read.TokenID != "alice-id" {
			t.Fatalf("read back = %+v", read)
		}
		// Roles come back sorted and complete on every backend.
		if read.Roles[0] != "operator" || read.Roles[1] != "reader" {
			t.Fatalf("roles = %v, want sorted", read.Roles)
		}
		if !read.CreatedAt.Equal(t0) || !read.UpdatedAt.Equal(t0) {
			t.Fatalf("timestamps = %v / %v, want %v", read.CreatedAt, read.UpdatedAt, t0)
		}
	})

	t.Run("DuplicateIsConflict", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.CreatePrincipal(ctx, principal("alice"), "d1", t0); err != nil {
			t.Fatal(err)
		}
		_, err := s.CreatePrincipal(ctx, principal("alice"), "d2", t0)
		if !errors.Is(err, adminauth.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("UnknownIsNotFound", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Principal(ctx, "nobody"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("Principal: err = %v, want ErrNotFound", err)
		}
		if _, err := s.UpdatePrincipal(ctx, "nobody", nil, false, t0); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("UpdatePrincipal: err = %v, want ErrNotFound", err)
		}
		if err := s.RevokeToken(ctx, "nobody", t0); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("RevokeToken: err = %v, want ErrNotFound", err)
		}
		if err := s.DeletePrincipal(ctx, "nobody"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("DeletePrincipal: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdateAndDelete", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.CreatePrincipal(ctx, principal("alice", "reader"), "d1", t0); err != nil {
			t.Fatal(err)
		}
		later := t0.Add(time.Hour)
		got, err := s.UpdatePrincipal(ctx, "alice", []string{"admin"}, true, later)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Roles) != 1 || got.Roles[0] != "admin" || !got.Root {
			t.Fatalf("updated = %+v", got)
		}
		if !got.UpdatedAt.Equal(later) || !got.CreatedAt.Equal(t0) {
			t.Fatalf("timestamps after update = %v / %v", got.CreatedAt, got.UpdatedAt)
		}
		if err := s.DeletePrincipal(ctx, "alice"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Principal(ctx, "alice"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("after delete: %v", err)
		}
	})

	t.Run("CountRoot", func(t *testing.T) {
		s := newStore(t)
		root := principal("root")
		root.Root = true
		if _, err := s.CreatePrincipal(ctx, root, "d1", t0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreatePrincipal(ctx, principal("reader", "reader"), "d2", t0); err != nil {
			t.Fatal(err)
		}
		n, err := s.CountRoot(ctx)
		if err != nil || n != 1 {
			t.Fatalf("CountRoot = %d, %v; want 1", n, err)
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		s := newStore(t)
		for _, n := range []string{"a", "b", "c", "d", "e"} {
			if _, err := s.CreatePrincipal(ctx, principal(n), "digest-"+n, t0); err != nil {
				t.Fatal(err)
			}
		}
		var names []string
		cursor := ""
		pages := 0
		for {
			res, err := s.Principals(ctx, adminauth.Page{Cursor: cursor, Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			pages++
			for _, p := range res.Items {
				names = append(names, p.Name)
			}
			if res.NextCursor == "" {
				break
			}
			cursor = res.NextCursor
			if pages > 10 {
				t.Fatal("pagination did not terminate")
			}
		}
		if pages != 3 || len(names) != 5 || names[0] != "a" || names[4] != "e" {
			t.Fatalf("pages=%d names=%v", pages, names)
		}
	})
}

func runTokens(t *testing.T, newStore NewStore) {
	ctx := context.Background()

	t.Run("ByDigest", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.CreatePrincipal(ctx, principal("alice", "reader"), "digest-alice", t0); err != nil {
			t.Fatal(err)
		}
		got, err := s.PrincipalByDigest(ctx, "digest-alice")
		if err != nil {
			t.Fatalf("PrincipalByDigest: %v", err)
		}
		if got.Name != "alice" {
			t.Fatalf("resolved %q", got.Name)
		}
		if _, err := s.PrincipalByDigest(ctx, "no-such-digest"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("unknown digest: %v", err)
		}
	})

	// Rotation must invalidate the old value at once. A store that added a
	// second live digest instead would leave a compromised token working.
	t.Run("RotateInvalidatesTheOldDigest", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.CreatePrincipal(ctx, principal("alice"), "old-digest", t0); err != nil {
			t.Fatal(err)
		}
		later := t0.Add(time.Minute)
		got, err := s.SetToken(ctx, "alice", "new-digest", "new-id", time.Time{}, later)
		if err != nil {
			t.Fatalf("SetToken: %v", err)
		}
		if got.TokenID != "new-id" || !got.TokenAt.Equal(later) {
			t.Fatalf("after rotate = %+v", got)
		}
		if _, err := s.PrincipalByDigest(ctx, "old-digest"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("the old digest still resolves: %v", err)
		}
		if _, err := s.PrincipalByDigest(ctx, "new-digest"); err != nil {
			t.Fatalf("the new digest does not resolve: %v", err)
		}
	})

	// A revoked principal stores no digest. An empty digest must not match it,
	// or an empty Authorization header would authenticate as that principal.
	t.Run("RevokedIsNotFindable", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.CreatePrincipal(ctx, principal("alice"), "digest-alice", t0); err != nil {
			t.Fatal(err)
		}
		if err := s.RevokeToken(ctx, "alice", t0.Add(time.Minute)); err != nil {
			t.Fatalf("RevokeToken: %v", err)
		}
		if _, err := s.PrincipalByDigest(ctx, "digest-alice"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("a revoked digest still resolves: %v", err)
		}
		if _, err := s.PrincipalByDigest(ctx, ""); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("an empty digest resolved: %v", err)
		}
		// The principal itself survives, so audit history still resolves the name.
		p, err := s.Principal(ctx, "alice")
		if err != nil {
			t.Fatalf("the principal was deleted by a revoke: %v", err)
		}
		if p.TokenID != "" || !p.TokenAt.IsZero() {
			t.Fatalf("revoked principal keeps token state: %+v", p)
		}
	})

	// Several principals may be revoked at once: the unique index on the
	// digest must admit more than one row without a live credential.
	t.Run("ManyRevokedCoexist", func(t *testing.T) {
		s := newStore(t)
		for _, n := range []string{"a", "b", "c"} {
			if _, err := s.CreatePrincipal(ctx, principal(n), "digest-"+n, t0); err != nil {
				t.Fatal(err)
			}
			if err := s.RevokeToken(ctx, n, t0); err != nil {
				t.Fatalf("revoke %s: %v", n, err)
			}
		}
		res, err := s.Principals(ctx, adminauth.Page{})
		if err != nil || len(res.Items) != 3 {
			t.Fatalf("principals = %d, %v; want 3", len(res.Items), err)
		}
	})

	t.Run("ExpiryRoundTrips", func(t *testing.T) {
		s := newStore(t)
		exp := t0.Add(24 * time.Hour)
		p := principal("ci")
		p.ExpiresAt = exp
		if _, err := s.CreatePrincipal(ctx, p, "digest-ci", t0); err != nil {
			t.Fatal(err)
		}
		got, err := s.PrincipalByDigest(ctx, "digest-ci")
		if err != nil {
			t.Fatal(err)
		}
		if !got.ExpiresAt.Equal(exp) {
			t.Fatalf("expiry = %v, want %v", got.ExpiresAt, exp)
		}
		if err := got.Active(exp.Add(-time.Second)); err != nil {
			t.Fatalf("active before expiry: %v", err)
		}
		if err := got.Active(exp); !errors.Is(err, adminauth.ErrExpired) {
			t.Fatalf("at expiry: %v, want ErrExpired", err)
		}
	})
}

func runPolicies(t *testing.T, newStore NewStore) {
	ctx := context.Background()
	const src = `permit (principal, action == MDM::Action::"listEnrollments", resource);`

	t.Run("RoundTrip", func(t *testing.T) {
		s := newStore(t)
		got, err := s.PutPolicy(ctx, adminauth.Policy{Name: "readers", Source: src, Description: "read only"}, t0)
		if err != nil {
			t.Fatalf("PutPolicy: %v", err)
		}
		if got.Name != "readers" || got.Source != src {
			t.Fatalf("stored = %+v", got)
		}
		read, err := s.GetPolicy(ctx, "readers")
		if err != nil {
			t.Fatal(err)
		}
		// The source is served back byte for byte, so an operator sees what
		// they wrote rather than a reformatting.
		if read.Source != src || read.Description != "read only" {
			t.Fatalf("read back = %+v", read)
		}
		if _, err := s.GetPolicy(ctx, "nope"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("unknown policy: %v", err)
		}
	})

	t.Run("ReplacePreservesCreatedAt", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "p", Source: src}, t0); err != nil {
			t.Fatal(err)
		}
		later := t0.Add(time.Hour)
		got, err := s.PutPolicy(ctx, adminauth.Policy{Name: "p", Source: src + "\n"}, later)
		if err != nil {
			t.Fatal(err)
		}
		if !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(later) {
			t.Fatalf("timestamps = %v / %v", got.CreatedAt, got.UpdatedAt)
		}
		all, err := s.Policies(ctx)
		if err != nil || len(all) != 1 {
			t.Fatalf("policies = %d, %v; want one replaced row", len(all), err)
		}
	})

	t.Run("ListedByName", func(t *testing.T) {
		s := newStore(t)
		for _, n := range []string{"c", "a", "b"} {
			if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: n, Source: src}, t0); err != nil {
				t.Fatal(err)
			}
		}
		all, err := s.Policies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 3 || all[0].Name != "a" || all[2].Name != "c" {
			t.Fatalf("policies = %+v, want sorted by name", all)
		}
	})

	// A compiled policy set is cached against this number, so every write has
	// to move it or a stale compilation would keep answering.
	t.Run("VersionMovesOnEveryWrite", func(t *testing.T) {
		s := newStore(t)
		v0, err := s.PolicyVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "p", Source: src}, t0); err != nil {
			t.Fatal(err)
		}
		v1, err := s.PolicyVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if v1 == v0 {
			t.Fatalf("version did not move on insert: %d", v1)
		}
		if _, err := s.PutPolicy(ctx, adminauth.Policy{Name: "p", Source: src + "\n"}, t0); err != nil {
			t.Fatal(err)
		}
		v2, _ := s.PolicyVersion(ctx)
		if v2 == v1 {
			t.Fatalf("version did not move on replace: %d", v2)
		}
		if err := s.DeletePolicy(ctx, "p"); err != nil {
			t.Fatal(err)
		}
		v3, _ := s.PolicyVersion(ctx)
		if v3 == v2 {
			t.Fatalf("version did not move on delete: %d", v3)
		}
	})

	t.Run("DeleteUnknownIsNotFound", func(t *testing.T) {
		s := newStore(t)
		if err := s.DeletePolicy(ctx, "nope"); !errors.Is(err, adminauth.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}
