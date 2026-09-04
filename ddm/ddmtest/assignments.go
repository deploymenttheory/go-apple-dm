package ddmtest

import (
	"bytes"
	"context"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/paging"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

// RunAssignmentSuite covers AssignmentStore.
func RunAssignmentSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("AssignSetAndDirect", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		dev := Device(1)
		_, err := s.AssignSet(ctx, dev, "nope", t0)
		wantErr(t, "assign unknown set", err, ddm.ErrNotFound)
		_, err = s.AssignDeclaration(ctx, dev, "nope", t0)
		wantErr(t, "assign unknown declaration", err, ddm.ErrNotFound)
		changed, err := s.AssignSet(ctx, dev, "s", t0)
		if err != nil || !changed {
			t.Fatalf("assign set: %v %v", changed, err)
		}
		changed, err = s.AssignSet(ctx, dev, "s", t0)
		if err != nil || changed {
			t.Fatalf("assign set again: %v %v", changed, err)
		}
		changed, err = s.AssignDeclaration(ctx, dev, "a", t0)
		if err != nil || !changed {
			t.Fatalf("assign declaration: %v %v", changed, err)
		}
		changed, err = s.AssignDeclaration(ctx, dev, "a", t0)
		if err != nil || changed {
			t.Fatalf("assign declaration again: %v %v", changed, err)
		}
		sets, _ := s.EnrollmentSets(ctx, dev)
		wantStrings(t, "sets", sets, []string{"s"})
		direct, _ := s.EnrollmentDeclarations(ctx, dev)
		wantStrings(t, "direct", direct, []string{"a"})
		r, err := s.SetEnrollments(ctx, "s", paging.Page{})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Items) != 1 || r.Items[0] != dev {
			t.Fatalf("set enrollments: %+v", r)
		}
		changed, err = s.UnassignSet(ctx, dev, "s")
		if err != nil || !changed {
			t.Fatalf("unassign set: %v %v", changed, err)
		}
		changed, err = s.UnassignDeclaration(ctx, dev, "a")
		if err != nil || !changed {
			t.Fatalf("unassign declaration: %v %v", changed, err)
		}
		sets, _ = s.EnrollmentSets(ctx, dev)
		wantStrings(t, "sets after unassign", sets, nil)
		direct, _ = s.EnrollmentDeclarations(ctx, dev)
		wantStrings(t, "direct after unassign", direct, nil)
	})

	t.Run("StaticDeclarationsUnionDeduped", func(t *testing.T) {
		s := newStore(t)
		d1 := Decl("d1", schemaddm.KindConfiguration, `{"Echo":"1"}`)
		put(t, s, d1)
		put(t, s, Decl("d2", schemaddm.KindActivation, `{}`))
		put(t, s, Decl("d3", schemaddm.KindAsset, `{}`))
		put(t, s, Decl("d4", schemaddm.KindAsset, `{}`))
		putSet(t, s, "s1")
		putSet(t, s, "s2")
		addMember(t, s, "s1", "d1")
		addMember(t, s, "s1", "d2")
		addMember(t, s, "s2", "d1")
		dev := Device(1)
		assignSet(t, s, dev, "s1")
		assignSet(t, s, dev, "s2")
		assignDecl(t, s, dev, "d1")
		assignDecl(t, s, dev, "d3")
		got, err := s.StaticDeclarations(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		wantStrings(t, "static", identifiers(got), []string{"d1", "d2", "d3"})
		if got[0].ServerToken != d1.ServerToken || !bytes.Equal(got[0].Canonical, d1.Canonical) || got[0].Kind != schemaddm.KindConfiguration {
			t.Fatalf("d1 copy: %+v", got[0])
		}
		got[0].Canonical[0] = '!'
		again, _ := s.StaticDeclarations(ctx, dev)
		if again[0].Canonical[0] == '!' {
			t.Fatal("StaticDeclarations returned aliased bytes")
		}
		none, err := s.StaticDeclarations(ctx, Device(9))
		if err != nil || len(none) != 0 {
			t.Fatalf("unknown enrollment: %v %v", none, err)
		}
	})

	t.Run("StaticDeclarationsSortedRegardlessOfInsertOrder", func(t *testing.T) {
		s := newStore(t)
		for _, id := range []string{"z", "m", "a", "q"} {
			put(t, s, Decl(id, schemaddm.KindConfiguration, `{}`))
		}
		putSet(t, s, "s")
		addMember(t, s, "s", "m")
		addMember(t, s, "s", "a")
		dev := Device(1)
		assignDecl(t, s, dev, "z")
		assignDecl(t, s, dev, "q")
		assignSet(t, s, dev, "s")
		got, err := s.StaticDeclarations(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		wantStrings(t, "static", identifiers(got), []string{"a", "m", "q", "z"})
	})

	t.Run("UserChannelIsNotDeviceChannel", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		put(t, s, Decl("b", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		addMember(t, s, "s", "a")
		dev, usr := Device(1), User(1, "alice")
		assignSet(t, s, dev, "s")
		assignDecl(t, s, usr, "b")
		devStatic, _ := s.StaticDeclarations(ctx, dev)
		wantStrings(t, "device static", identifiers(devStatic), []string{"a"})
		usrStatic, _ := s.StaticDeclarations(ctx, usr)
		wantStrings(t, "user static", identifiers(usrStatic), []string{"b"})
		usrSets, _ := s.EnrollmentSets(ctx, usr)
		wantStrings(t, "user sets", usrSets, nil)
		affected, _ := s.AffectedEnrollments(ctx, []string{"b"}, nil)
		wantStrings(t, "affected by b", ids(affected), []string{usr.ID})
		if affected[0] != usr {
			t.Fatalf("affected identity: %+v", affected[0])
		}
		if err := s.ClearEnrollment(ctx, dev); err != nil {
			t.Fatal(err)
		}
		usrStatic, _ = s.StaticDeclarations(ctx, usr)
		wantStrings(t, "user static after device clear", identifiers(usrStatic), []string{"b"})
	})

	t.Run("AffectedEnrollmentsByIdentifierAndSet", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("d1", schemaddm.KindConfiguration, `{}`))
		put(t, s, Decl("d2", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s1")
		putSet(t, s, "s2")
		addMember(t, s, "s1", "d1")
		addMember(t, s, "s2", "d2")
		dev1, dev2, dev3, usr := Device(1), Device(2), Device(3), User(1, "alice")
		assignDecl(t, s, dev1, "d1")
		assignSet(t, s, dev1, "s1")
		assignSet(t, s, dev2, "s1")
		assignSet(t, s, dev3, "s2")
		assignSet(t, s, usr, "s2")
		affected := func(identifiers, sets []string) []string {
			t.Helper()
			got, err := s.AffectedEnrollments(ctx, identifiers, sets)
			if err != nil {
				t.Fatalf("affected %q %q: %v", identifiers, sets, err)
			}
			return ids(got)
		}
		wantStrings(t, "by d1", affected([]string{"d1"}, nil), []string{dev1.ID, dev2.ID})
		// Device channels (no parent) sort before user channels.
		wantStrings(t, "by s2", affected(nil, []string{"s2"}), []string{dev3.ID, usr.ID})
		wantStrings(t, "by both", affected([]string{"d1"}, []string{"s2"}), []string{dev1.ID, dev2.ID, dev3.ID, usr.ID})
		wantStrings(t, "by d2", affected([]string{"d2"}, nil), []string{dev3.ID, usr.ID})
		wantStrings(t, "none", affected(nil, nil), nil)
		wantStrings(t, "unknown", affected([]string{"nope"}, []string{"nope"}), nil)
	})

	t.Run("SetEnrollmentsPaginates", func(t *testing.T) {
		s := newStore(t)
		putSet(t, s, "s")
		for i := 5; i >= 1; i-- {
			assignSet(t, s, Device(i), "s")
		}
		var got []string
		p := paging.Page{Limit: 2}
		for {
			r, err := s.SetEnrollments(ctx, "s", p)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range r.Items {
				if id.Channel != mdm.ChannelDevice {
					t.Fatalf("channel lost: %+v", id)
				}
				got = append(got, id.ID)
			}
			if r.NextCursor == "" {
				break
			}
			if len(r.Items) != 2 || r.NextCursor != r.Items[1].ID {
				t.Fatalf("page: %+v", r)
			}
			p.Cursor = r.NextCursor
		}
		wantStrings(t, "paged", got, []string{"DEVICE-01", "DEVICE-02", "DEVICE-03", "DEVICE-04", "DEVICE-05"})
		_, err := s.SetEnrollments(ctx, "nope", paging.Page{})
		wantErr(t, "unknown set", err, ddm.ErrNotFound)
	})

	t.Run("UnassignUnknownIsFalse", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		dev := Device(1)
		changed, err := s.UnassignSet(ctx, dev, "s")
		if err != nil || changed {
			t.Fatalf("unassign not assigned set: %v %v", changed, err)
		}
		changed, err = s.UnassignSet(ctx, dev, "nope")
		if err != nil || changed {
			t.Fatalf("unassign unknown set: %v %v", changed, err)
		}
		changed, err = s.UnassignDeclaration(ctx, dev, "a")
		if err != nil || changed {
			t.Fatalf("unassign not assigned declaration: %v %v", changed, err)
		}
		changed, err = s.UnassignDeclaration(ctx, dev, "nope")
		if err != nil || changed {
			t.Fatalf("unassign unknown declaration: %v %v", changed, err)
		}
	})
}
