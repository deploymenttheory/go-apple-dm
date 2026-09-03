package ddmtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// RunSetSuite covers SetStore.
func RunSetSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("PutIdempotent", func(t *testing.T) {
		s := newStore(t)
		_, err := s.GetSet(ctx, "s")
		wantErr(t, "Get unknown", err, ddm.ErrNotFound)
		created, err := s.PutSet(ctx, "s", t0)
		if err != nil || !created {
			t.Fatalf("first put: created=%v err=%v", created, err)
		}
		created, err = s.PutSet(ctx, "s", t0.Add(time.Hour))
		if err != nil || created {
			t.Fatalf("second put: created=%v err=%v", created, err)
		}
		got, err := s.GetSet(ctx, "s")
		if err != nil || got.Name != "s" {
			t.Fatalf("get: %+v %v", got, err)
		}
		wantTime(t, "CreatedAt", got.CreatedAt, t0)
		wantTime(t, "UpdatedAt", got.UpdatedAt, t0)
	})

	t.Run("DeleteCascades", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		addMember(t, s, "s", "a")
		dev := Device(1)
		assignSet(t, s, dev, "s")
		if err := s.DeleteSet(ctx, "s"); err != nil {
			t.Fatal(err)
		}
		_, err := s.GetSet(ctx, "s")
		wantErr(t, "Get after delete", err, ddm.ErrNotFound)
		_, err = s.SetDeclarations(ctx, "s")
		wantErr(t, "SetDeclarations after delete", err, ddm.ErrNotFound)
		_, err = s.SetEnrollments(ctx, "s", storage.Page{})
		wantErr(t, "SetEnrollments after delete", err, ddm.ErrNotFound)
		sets, _ := s.DeclarationSets(ctx, "a")
		wantStrings(t, "declaration sets", sets, nil)
		sets, _ = s.EnrollmentSets(ctx, dev)
		wantStrings(t, "enrollment sets", sets, nil)
		static, _ := s.StaticDeclarations(ctx, dev)
		wantStrings(t, "static", identifiers(static), nil)
		if _, err := s.GetDeclaration(ctx, "a"); err != nil {
			t.Fatalf("declaration survived: %v", err)
		}
		wantErr(t, "delete again", s.DeleteSet(ctx, "s"), ddm.ErrNotFound)
	})

	t.Run("AddRemoveMembership", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		changed, err := s.AddSetDeclaration(ctx, "s", "a", t0.Add(time.Minute))
		if err != nil || !changed {
			t.Fatalf("add: changed=%v err=%v", changed, err)
		}
		changed, err = s.AddSetDeclaration(ctx, "s", "a", t0.Add(2*time.Minute))
		if err != nil || changed {
			t.Fatalf("add again: changed=%v err=%v", changed, err)
		}
		got, _ := s.GetSet(ctx, "s")
		wantTime(t, "UpdatedAt after add", got.UpdatedAt, t0.Add(time.Minute))
		sets, _ := s.DeclarationSets(ctx, "a")
		wantStrings(t, "declaration sets", sets, []string{"s"})
		changed, err = s.RemoveSetDeclaration(ctx, "s", "a")
		if err != nil || !changed {
			t.Fatalf("remove: changed=%v err=%v", changed, err)
		}
		changed, err = s.RemoveSetDeclaration(ctx, "s", "a")
		if err != nil || changed {
			t.Fatalf("remove again: changed=%v err=%v", changed, err)
		}
		members, err := s.SetDeclarations(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		wantStrings(t, "members", members, nil)
		_, err = s.RemoveSetDeclaration(ctx, "nope", "a")
		wantErr(t, "remove from unknown set", err, ddm.ErrNotFound)
		_, err = s.SetDeclarations(ctx, "nope")
		wantErr(t, "SetDeclarations unknown", err, ddm.ErrNotFound)
	})

	t.Run("AddUnknownDeclarationNotFound", func(t *testing.T) {
		s := newStore(t)
		putSet(t, s, "s")
		_, err := s.AddSetDeclaration(ctx, "s", "nope", t0)
		wantErr(t, "add unknown declaration", err, ddm.ErrNotFound)
	})

	t.Run("AddToUnknownSetNotFound", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		_, err := s.AddSetDeclaration(ctx, "nope", "a", t0)
		wantErr(t, "add to unknown set", err, ddm.ErrNotFound)
	})

	t.Run("ListPaginates", func(t *testing.T) {
		s := newStore(t)
		for i := 5; i >= 1; i-- {
			putSet(t, s, fmt.Sprintf("s-%02d", i))
		}
		var got []string
		p := storage.Page{Limit: 2}
		for {
			r, err := s.ListSets(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range r.Items {
				got = append(got, set.Name)
			}
			if r.NextCursor == "" {
				break
			}
			if len(r.Items) != 2 || r.NextCursor != r.Items[1].Name {
				t.Fatalf("page: %+v", r)
			}
			p.Cursor = r.NextCursor
		}
		wantStrings(t, "paged", got, []string{"s-01", "s-02", "s-03", "s-04", "s-05"})
		all, err := s.ListSets(ctx, storage.Page{})
		if err != nil || len(all.Items) != 5 || all.NextCursor != "" {
			t.Fatalf("default page: %+v %v", all, err)
		}
	})

	t.Run("SetDeclarationsSorted", func(t *testing.T) {
		s := newStore(t)
		for _, id := range []string{"c", "a", "b"} {
			put(t, s, Decl(id, schemaddm.KindConfiguration, `{}`))
		}
		putSet(t, s, "z")
		putSet(t, s, "y")
		for _, id := range []string{"c", "a", "b"} {
			addMember(t, s, "z", id)
		}
		addMember(t, s, "y", "a")
		members, err := s.SetDeclarations(ctx, "z")
		if err != nil {
			t.Fatal(err)
		}
		wantStrings(t, "members", members, []string{"a", "b", "c"})
		members[0] = "!"
		again, _ := s.SetDeclarations(ctx, "z")
		wantStrings(t, "members after mutation", again, []string{"a", "b", "c"})
		sets, err := s.DeclarationSets(ctx, "a")
		if err != nil {
			t.Fatal(err)
		}
		wantStrings(t, "sets of a", sets, []string{"y", "z"})
		sets, err = s.DeclarationSets(ctx, "nope")
		if err != nil {
			t.Fatalf("unknown declaration: %v", err)
		}
		wantStrings(t, "sets of unknown", sets, nil)
	})
}
