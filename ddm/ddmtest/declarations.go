package ddmtest

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/paging"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

// RunDeclarationSuite covers DeclarationStore.
func RunDeclarationSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("PutGetRoundTrip", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.GetDeclaration(ctx, "a"); err == nil {
			t.Fatal("Get unknown: no error")
		} else {
			wantErr(t, "Get unknown", err, ddm.ErrNotFound)
		}
		d := Decl("a", schemaddm.KindConfiguration, `{"Echo":"x"}`)
		want := bytes.Clone(d.Canonical)
		put(t, s, d)
		// The store copied the input.
		d.Canonical[0] = '!'
		got, err := s.GetDeclaration(ctx, "a")
		if err != nil {
			t.Fatal(err)
		}
		if got.Identifier != "a" || got.Type != "com.apple.configuration.test" || got.Kind != schemaddm.KindConfiguration || got.ServerToken != d.ServerToken || !bytes.Equal(got.Canonical, want) {
			t.Fatalf("round trip: %+v", got)
		}
		wantTime(t, "CreatedAt", got.CreatedAt, t0)
		wantTime(t, "UpdatedAt", got.UpdatedAt, t0)
		// The store returned a copy.
		got.Canonical[0] = '!'
		again, _ := s.GetDeclaration(ctx, "a")
		if !bytes.Equal(again.Canonical, want) {
			t.Fatal("Get returned aliased canonical bytes")
		}
		v, err := s.GetDeclarationVersion(ctx, "a", d.ServerToken)
		if err != nil {
			t.Fatal(err)
		}
		if v.Identifier != "a" || v.Type != got.Type || v.ServerToken != d.ServerToken || !bytes.Equal(v.Canonical, want) {
			t.Fatalf("version: %+v", v)
		}
		wantTime(t, "version CreatedAt", v.CreatedAt, t0)
	})

	t.Run("PutUnchangedIsNoop", func(t *testing.T) {
		s := newStore(t)
		d := Decl("a", schemaddm.KindConfiguration, `{"Echo":"x"}`)
		put(t, s, d)
		later := Decl("a", schemaddm.KindConfiguration, `{"Echo":"x"}`)
		later.CreatedAt, later.UpdatedAt = t0.Add(time.Hour), t0.Add(time.Hour)
		changed, err := s.PutDeclaration(ctx, later)
		if err != nil || changed {
			t.Fatalf("same token: changed=%v err=%v", changed, err)
		}
		got, _ := s.GetDeclaration(ctx, "a")
		wantTime(t, "UpdatedAt untouched", got.UpdatedAt, t0)
		wantTime(t, "CreatedAt untouched", got.CreatedAt, t0)
	})

	t.Run("PutNewTokenWritesVersion", func(t *testing.T) {
		s := newStore(t)
		a := Decl("a", schemaddm.KindConfiguration, `{"Echo":"1"}`)
		b := Decl("a", schemaddm.KindConfiguration, `{"Echo":"2"}`)
		b.UpdatedAt = t0.Add(time.Minute)
		put(t, s, a)
		put(t, s, b)
		got, err := s.GetDeclaration(ctx, "a")
		if err != nil {
			t.Fatal(err)
		}
		if got.ServerToken != b.ServerToken || !bytes.Equal(got.Canonical, b.Canonical) {
			t.Fatalf("current: %+v", got)
		}
		wantTime(t, "CreatedAt kept", got.CreatedAt, t0)
		wantTime(t, "UpdatedAt moved", got.UpdatedAt, t0.Add(time.Minute))
		for _, d := range []*ddm.Declaration{a, b} {
			v, err := s.GetDeclarationVersion(ctx, "a", d.ServerToken)
			if err != nil {
				t.Fatalf("version %s: %v", d.ServerToken, err)
			}
			if !bytes.Equal(v.Canonical, d.Canonical) {
				t.Fatalf("version %s bytes: %s", d.ServerToken, v.Canonical)
			}
			wantTime(t, "version CreatedAt", v.CreatedAt, d.UpdatedAt)
		}
		v, _ := s.GetDeclarationVersion(ctx, "a", b.ServerToken)
		v.Canonical[0] = '!'
		v2, _ := s.GetDeclarationVersion(ctx, "a", b.ServerToken)
		if v2.Canonical[0] == '!' {
			t.Fatal("GetDeclarationVersion returned aliased bytes")
		}
		// Going back to a's bytes reuses the existing revision.
		put(t, s, a)
		n, err := s.PruneVersions(ctx)
		if err != nil || n != 1 {
			t.Fatalf("prune after revert: n=%d err=%v", n, err)
		}
	})

	t.Run("KindChangeConflicts", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		_, err := s.PutDeclaration(ctx, Decl("a", schemaddm.KindActivation, `{}`))
		wantErr(t, "kind change", err, ddm.ErrConflict)
		got, _ := s.GetDeclaration(ctx, "a")
		if got.Kind != schemaddm.KindConfiguration {
			t.Fatalf("kind changed to %s", got.Kind)
		}
	})

	t.Run("DeleteCascadesSetsAssignmentsVersions", func(t *testing.T) {
		s := newStore(t)
		d := Decl("a", schemaddm.KindConfiguration, `{}`)
		put(t, s, d)
		put(t, s, Decl("b", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		addMember(t, s, "s", "a")
		addMember(t, s, "s", "b")
		dev := Device(1)
		assignSet(t, s, dev, "s")
		assignDecl(t, s, dev, "a")
		assignDecl(t, s, dev, "b")
		if err := s.DeleteDeclaration(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		_, err := s.GetDeclaration(ctx, "a")
		wantErr(t, "Get after delete", err, ddm.ErrNotFound)
		_, err = s.GetDeclarationVersion(ctx, "a", d.ServerToken)
		wantErr(t, "version after delete", err, ddm.ErrNotFound)
		members, _ := s.SetDeclarations(ctx, "s")
		wantStrings(t, "set members", members, []string{"b"})
		sets, _ := s.DeclarationSets(ctx, "a")
		wantStrings(t, "declaration sets", sets, nil)
		direct, _ := s.EnrollmentDeclarations(ctx, dev)
		wantStrings(t, "direct", direct, []string{"b"})
		static, _ := s.StaticDeclarations(ctx, dev)
		wantStrings(t, "static", identifiers(static), []string{"b"})
	})

	t.Run("DeleteUnknownNotFound", func(t *testing.T) {
		s := newStore(t)
		wantErr(t, "delete unknown", s.DeleteDeclaration(ctx, "nope"), ddm.ErrNotFound)
	})

	t.Run("ListPaginatesByIdentifier", func(t *testing.T) {
		s := newStore(t)
		for i := 5; i >= 1; i-- {
			put(t, s, Decl(fmt.Sprintf("d-%02d", i), schemaddm.KindConfiguration, `{}`))
		}
		var got []string
		p := paging.Page{Limit: 2}
		for pages := 0; ; pages++ {
			r, err := s.ListDeclarations(ctx, ddm.DeclarationQuery{}, p)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, identifiers(r.Items)...)
			if r.NextCursor == "" {
				if pages != 2 || len(r.Items) != 1 {
					t.Fatalf("last page %d has %d items", pages, len(r.Items))
				}
				break
			}
			if len(r.Items) != 2 || r.NextCursor != r.Items[1].Identifier {
				t.Fatalf("page %d: %+v", pages, r)
			}
			p.Cursor = r.NextCursor
		}
		wantStrings(t, "paged", got, []string{"d-01", "d-02", "d-03", "d-04", "d-05"})
		all, err := s.ListDeclarations(ctx, ddm.DeclarationQuery{}, paging.Page{})
		if err != nil || len(all.Items) != 5 || all.NextCursor != "" {
			t.Fatalf("default page: %+v %v", all, err)
		}
		all.Items[0].Canonical[0] = '!'
		again, _ := s.ListDeclarations(ctx, ddm.DeclarationQuery{}, paging.Page{})
		if again.Items[0].Canonical[0] == '!' {
			t.Fatal("List returned aliased bytes")
		}
	})

	t.Run("ListFiltersKindTypeSet", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		put(t, s, Decl("b", schemaddm.KindActivation, `{}`))
		put(t, s, Decl("c", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		addMember(t, s, "s", "a")
		addMember(t, s, "s", "b")
		list := func(q ddm.DeclarationQuery) []string {
			t.Helper()
			r, err := s.ListDeclarations(ctx, q, paging.Page{})
			if err != nil {
				t.Fatalf("list %+v: %v", q, err)
			}
			return identifiers(r.Items)
		}
		wantStrings(t, "kind", list(ddm.DeclarationQuery{Kind: schemaddm.KindConfiguration}), []string{"a", "c"})
		wantStrings(t, "type", list(ddm.DeclarationQuery{Type: "com.apple.activation.simple"}), []string{"b"})
		wantStrings(t, "in set", list(ddm.DeclarationQuery{InSet: "s"}), []string{"a", "b"})
		wantStrings(t, "in set and kind", list(ddm.DeclarationQuery{InSet: "s", Kind: schemaddm.KindActivation}), []string{"b"})
		wantStrings(t, "unknown set", list(ddm.DeclarationQuery{InSet: "nope"}), nil)
	})

	t.Run("GetVersionByToken", func(t *testing.T) {
		s := newStore(t)
		d := Decl("a", schemaddm.KindAsset, `{"Reference":{"DataURL":"https://x"}}`)
		put(t, s, d)
		_, err := s.GetDeclarationVersion(ctx, "a", "nope")
		wantErr(t, "unknown token", err, ddm.ErrNotFound)
		_, err = s.GetDeclarationVersion(ctx, "b", d.ServerToken)
		wantErr(t, "unknown identifier", err, ddm.ErrNotFound)
		v, err := s.GetDeclarationVersion(ctx, "a", d.ServerToken)
		if err != nil {
			t.Fatal(err)
		}
		if v.Type != "com.apple.asset.data" || !bytes.Equal(v.Canonical, d.Canonical) {
			t.Fatalf("version: %+v", v)
		}
	})

	t.Run("PruneVersionsKeepsCurrentAndSnapshotted", func(t *testing.T) {
		s := newStore(t)
		a := Decl("a", schemaddm.KindConfiguration, `{"Echo":"1"}`)
		b := Decl("a", schemaddm.KindConfiguration, `{"Echo":"2"}`)
		c := Decl("a", schemaddm.KindConfiguration, `{"Echo":"3"}`)
		put(t, s, a)
		put(t, s, b)
		put(t, s, c)
		snap := &ddm.Snapshot{ID: Device(1), DeclarationsToken: "tok", TokenChangedAt: t0, RefreshedAt: t0, Items: []ddm.SnapshotItem{
			{DeclarationRef: ddm.DeclarationRef{Kind: schemaddm.KindConfiguration, Identifier: "a", ServerToken: b.ServerToken}, BaseToken: b.ServerToken},
		}}
		if err := s.PutSnapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
		n, err := s.PruneVersions(ctx)
		if err != nil || n != 1 {
			t.Fatalf("prune: n=%d err=%v", n, err)
		}
		_, err = s.GetDeclarationVersion(ctx, "a", a.ServerToken)
		wantErr(t, "pruned version", err, ddm.ErrNotFound)
		for _, keep := range []*ddm.Declaration{b, c} {
			if _, err := s.GetDeclarationVersion(ctx, "a", keep.ServerToken); err != nil {
				t.Fatalf("kept version %s: %v", keep.ServerToken, err)
			}
		}
		n, err = s.PruneVersions(ctx)
		if err != nil || n != 0 {
			t.Fatalf("second prune: n=%d err=%v", n, err)
		}
	})

	t.Run("InvalidArguments", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		bad := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "x"}
		good := Device(1)
		noID := Decl("", schemaddm.KindConfiguration, `{}`)
		noToken := Decl("b", schemaddm.KindConfiguration, `{}`)
		noToken.ServerToken = ""
		checks := map[string]func() error{
			"PutDeclaration nil":              func() error { _, err := s.PutDeclaration(ctx, nil); return err },
			"PutDeclaration empty identifier": func() error { _, err := s.PutDeclaration(ctx, noID); return err },
			"PutDeclaration empty token":      func() error { _, err := s.PutDeclaration(ctx, noToken); return err },
			"GetDeclaration":                  func() error { _, err := s.GetDeclaration(ctx, ""); return err },
			"GetDeclarationVersion identifier": func() error {
				_, err := s.GetDeclarationVersion(ctx, "", "t")
				return err
			},
			"GetDeclarationVersion token": func() error { _, err := s.GetDeclarationVersion(ctx, "a", ""); return err },
			"DeleteDeclaration":           func() error { return s.DeleteDeclaration(ctx, "") },
			"PutSet":                      func() error { _, err := s.PutSet(ctx, "", t0); return err },
			"DeleteSet":                   func() error { return s.DeleteSet(ctx, "") },
			"GetSet":                      func() error { _, err := s.GetSet(ctx, ""); return err },
			"AddSetDeclaration set":       func() error { _, err := s.AddSetDeclaration(ctx, "", "a", t0); return err },
			"AddSetDeclaration id":        func() error { _, err := s.AddSetDeclaration(ctx, "s", "", t0); return err },
			"RemoveSetDeclaration set":    func() error { _, err := s.RemoveSetDeclaration(ctx, "", "a"); return err },
			"RemoveSetDeclaration id":     func() error { _, err := s.RemoveSetDeclaration(ctx, "s", ""); return err },
			"SetDeclarations":             func() error { _, err := s.SetDeclarations(ctx, ""); return err },
			"DeclarationSets":             func() error { _, err := s.DeclarationSets(ctx, ""); return err },
			"AssignSet id":                func() error { _, err := s.AssignSet(ctx, bad, "s", t0); return err },
			"AssignSet set":               func() error { _, err := s.AssignSet(ctx, good, "", t0); return err },
			"UnassignSet id":              func() error { _, err := s.UnassignSet(ctx, bad, "s"); return err },
			"UnassignSet set":             func() error { _, err := s.UnassignSet(ctx, good, ""); return err },
			"EnrollmentSets":              func() error { _, err := s.EnrollmentSets(ctx, bad); return err },
			"SetEnrollments":              func() error { _, err := s.SetEnrollments(ctx, "", paging.Page{}); return err },
			"AssignDeclaration id":        func() error { _, err := s.AssignDeclaration(ctx, bad, "a", t0); return err },
			"AssignDeclaration decl":      func() error { _, err := s.AssignDeclaration(ctx, good, "", t0); return err },
			"UnassignDeclaration id":      func() error { _, err := s.UnassignDeclaration(ctx, bad, "a"); return err },
			"UnassignDeclaration decl":    func() error { _, err := s.UnassignDeclaration(ctx, good, ""); return err },
			"EnrollmentDeclarations":      func() error { _, err := s.EnrollmentDeclarations(ctx, bad); return err },
			"StaticDeclarations":          func() error { _, err := s.StaticDeclarations(ctx, bad); return err },
			"AffectedEnrollments id":      func() error { _, err := s.AffectedEnrollments(ctx, []string{""}, nil); return err },
			"AffectedEnrollments set":     func() error { _, err := s.AffectedEnrollments(ctx, nil, []string{""}); return err },
			"PutSnapshot nil":             func() error { return s.PutSnapshot(ctx, nil) },
			"PutSnapshot id":              func() error { return s.PutSnapshot(ctx, &ddm.Snapshot{ID: bad}) },
			"PutSnapshot item": func() error {
				return s.PutSnapshot(ctx, &ddm.Snapshot{ID: good, Items: []ddm.SnapshotItem{{}}})
			},
			"Snapshot":  func() error { _, err := s.Snapshot(ctx, bad); return err },
			"PutStatus": func() error { _, err := s.PutStatus(ctx, bad, ddm.StatusUpdate{}); return err },
			"PutStatus declaration": func() error {
				_, err := s.PutStatus(ctx, good, ddm.StatusUpdate{Declarations: []ddm.DeclarationStatus{{Kind: schemaddm.KindConfiguration}}})
				return err
			},
			"PutStatus value": func() error {
				_, err := s.PutStatus(ctx, good, ddm.StatusUpdate{Values: []ddm.StatusValue{{Value: []byte("1")}}})
				return err
			},
			"DeclarationStatus": func() error { _, err := s.DeclarationStatus(ctx, bad); return err },
			"DeclarationStatusByIdentifier": func() error {
				_, err := s.DeclarationStatusByIdentifier(ctx, "", paging.Page{})
				return err
			},
			"StatusValues":    func() error { _, err := s.StatusValues(ctx, bad, ddm.StatusValueQuery{}, paging.Page{}); return err },
			"StatusErrors":    func() error { _, err := s.StatusErrors(ctx, bad, paging.Page{}); return err },
			"StatusReports":   func() error { _, err := s.StatusReports(ctx, bad, paging.Page{}); return err },
			"RecordChanges":   func() error { return s.RecordChanges(ctx, []mdm.EnrollmentID{good, bad}, "r", t0) },
			"ClearEnrollment": func() error { return s.ClearEnrollment(ctx, bad) },
		}
		for name, fn := range checks {
			wantErr(t, name, fn(), ddm.ErrInvalid)
		}
		// A rejected RecordChanges wrote nothing.
		pending, err := s.PendingChanges(ctx, t0, 0)
		if err != nil || len(pending) != 0 {
			t.Fatalf("pending after rejected record: %v %v", pending, err)
		}
		// A rejected PutStatus wrote nothing.
		reports, err := s.StatusReports(ctx, good, paging.Page{})
		if err != nil || len(reports.Items) != 0 {
			t.Fatalf("reports after rejected status: %v %v", reports, err)
		}
	})
}
