package ddmtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// errRollback is the sentinel a failing Update callback returns.
var errRollback = errors.New("ddmtest: roll back")

// populate gives id a set assignment, a direct assignment, a snapshot, a
// status report with declarations, values, and errors, and a change row.
func populate(t *testing.T, s ddm.Tx, id mdm.EnrollmentID) {
	t.Helper()
	ctx := context.Background()
	assignSet(t, s, id, "s")
	assignDecl(t, s, id, "b")
	if err := s.PutSnapshot(ctx, &ddm.Snapshot{ID: id, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0, Items: []ddm.SnapshotItem{item(schemaddm.KindConfiguration, "a", "ta")}}); err != nil {
		t.Fatal(err)
	}
	u := report(true, t0, []ddm.DeclarationStatus{status(schemaddm.KindConfiguration, "a", "ta", true, "valid")}, []ddm.StatusValue{value("p.a", "1")})
	u.Errors = []ddm.StatusError{{StatusItem: "x", Reasons: []byte(`[]`)}}
	putStatus(t, s, id, u)
	if err := s.RecordChanges(ctx, []mdm.EnrollmentID{id}, "r", t0); err != nil {
		t.Fatal(err)
	}
}

// footprint counts what the store holds for id: sets, direct assignments,
// snapshot, status rows, values, errors, reports, and pending changes.
func footprint(t *testing.T, s ddm.Tx, id mdm.EnrollmentID) int {
	t.Helper()
	ctx := context.Background()
	n := 0
	sets, err := s.EnrollmentSets(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	n += len(sets)
	direct, err := s.EnrollmentDeclarations(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	n += len(direct)
	if _, err := s.Snapshot(ctx, id); err == nil {
		n++
	} else if !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal(err)
	}
	n += len(declStatus(t, s, id))
	n += len(valuePaths(t, s, id, ""))
	errs, err := s.StatusErrors(ctx, id, storage.Page{})
	if err != nil {
		t.Fatal(err)
	}
	n += len(errs.Items)
	reports, err := s.StatusReports(ctx, id, storage.Page{})
	if err != nil {
		t.Fatal(err)
	}
	n += len(reports.Items)
	for _, c := range pending(t, s, t0, 0) {
		if c.ID == id {
			n++
		}
	}
	return n
}

// RunClearSuite covers Tx.ClearEnrollment.
func RunClearSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("RemovesEverythingForEnrollmentOnly", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		put(t, s, Decl("b", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		addMember(t, s, "s", "a")
		dev1, dev2, usr := Device(1), Device(2), User(1, "alice")
		for _, id := range []mdm.EnrollmentID{dev1, dev2, usr} {
			populate(t, s, id)
		}
		const full = 8
		for _, id := range []mdm.EnrollmentID{dev1, dev2, usr} {
			if n := footprint(t, s, id); n != full {
				t.Fatalf("%s footprint before clear: %d", id.ID, n)
			}
		}
		if err := s.ClearEnrollment(ctx, dev1); err != nil {
			t.Fatal(err)
		}
		if n := footprint(t, s, dev1); n != 0 {
			t.Fatalf("cleared footprint: %d", n)
		}
		// Its user channel and the other device are untouched, as are the
		// declarations and sets themselves.
		for _, id := range []mdm.EnrollmentID{dev2, usr} {
			if n := footprint(t, s, id); n != full {
				t.Fatalf("%s footprint after clearing %s: %d", id.ID, dev1.ID, n)
			}
		}
		if _, err := s.GetDeclaration(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		members, _ := s.SetDeclarations(ctx, "s")
		wantStrings(t, "members", members, []string{"a"})
		affected, _ := s.AffectedEnrollments(ctx, nil, []string{"s"})
		wantStrings(t, "affected", ids(affected), []string{dev2.ID, usr.ID})
		byID, _ := s.DeclarationStatusByIdentifier(ctx, "a", storage.Page{})
		if len(byID.Items) != 2 {
			t.Fatalf("status by identifier: %+v", byID.Items)
		}
		if err := s.ClearEnrollment(ctx, dev1); err != nil {
			t.Fatalf("clear again: %v", err)
		}
		if err := s.ClearEnrollment(ctx, Device(7)); err != nil {
			t.Fatalf("clear unknown: %v", err)
		}
	})
}

// RunUpdateSuite covers Store.Update.
func RunUpdateSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("RollbackOnError", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("keep", schemaddm.KindConfiguration, `{}`))
		dev := Device(1)
		err := s.Update(ctx, func(tx ddm.Tx) error {
			put(t, tx, Decl("a", schemaddm.KindConfiguration, `{}`))
			putSet(t, tx, "s")
			addMember(t, tx, "s", "a")
			assignSet(t, tx, dev, "s")
			if err := tx.DeleteDeclaration(ctx, "keep"); err != nil {
				return err
			}
			if err := tx.RecordChanges(ctx, []mdm.EnrollmentID{dev}, "r", t0); err != nil {
				return err
			}
			// The transaction sees its own writes.
			static, err := tx.StaticDeclarations(ctx, dev)
			if err != nil || len(static) != 1 {
				t.Errorf("inside tx: %v %v", static, err)
			}
			return errRollback
		})
		wantErr(t, "Update error", err, errRollback)
		_, err = s.GetDeclaration(ctx, "a")
		wantErr(t, "rolled back declaration", err, ddm.ErrNotFound)
		_, err = s.GetSet(ctx, "s")
		wantErr(t, "rolled back set", err, ddm.ErrNotFound)
		if _, err := s.GetDeclaration(ctx, "keep"); err != nil {
			t.Fatalf("rolled back delete: %v", err)
		}
		sets, _ := s.EnrollmentSets(ctx, dev)
		wantStrings(t, "rolled back assignment", sets, nil)
		if rows := pending(t, s, t0, 0); len(rows) != 0 {
			t.Fatalf("rolled back changes: %+v", rows)
		}
	})

	t.Run("CommitVisible", func(t *testing.T) {
		s := newStore(t)
		dev := Device(1)
		err := s.Update(ctx, func(tx ddm.Tx) error {
			put(t, tx, Decl("a", schemaddm.KindConfiguration, `{}`))
			putSet(t, tx, "s")
			addMember(t, tx, "s", "a")
			assignSet(t, tx, dev, "s")
			return tx.RecordChanges(ctx, []mdm.EnrollmentID{dev}, "r", t0)
		})
		if err != nil {
			t.Fatal(err)
		}
		static, err := s.StaticDeclarations(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		wantStrings(t, "committed static", identifiers(static), []string{"a"})
		if rows := pending(t, s, t0, 0); len(rows) != 1 || rows[0].ID != dev {
			t.Fatalf("committed changes: %+v", rows)
		}
		// A committed transaction does not leak later writes back.
		if err := s.Update(ctx, func(tx ddm.Tx) error { return nil }); err != nil {
			t.Fatalf("empty update: %v", err)
		}
		static, _ = s.StaticDeclarations(ctx, dev)
		wantStrings(t, "static after empty update", identifiers(static), []string{"a"})
	})

	t.Run("NestedUpdateInvalid", func(t *testing.T) {
		s := newStore(t)
		exposed := false
		err := s.Update(ctx, func(tx ddm.Tx) error {
			nested, ok := tx.(ddm.Store)
			if !ok {
				return nil
			}
			exposed = true
			err := nested.Update(ctx, func(ddm.Tx) error { return nil })
			if !errors.Is(err, ddm.ErrInvalid) {
				t.Errorf("nested Update: got %v, want ErrInvalid", err)
			}
			put(t, tx, Decl("a", schemaddm.KindConfiguration, `{}`))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !exposed {
			t.Skip("transaction view does not expose Update")
		}
		if _, err := s.GetDeclaration(ctx, "a"); err != nil {
			t.Fatalf("outer transaction still committed: %v", err)
		}
	})
}

// RunConcurrencySuite hammers the store from many goroutines; run it under
// the race detector.
func RunConcurrencySuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("ParallelPutSameIdentifier", func(t *testing.T) {
		s := newStore(t)
		const n = 16
		decls := make([]*ddm.Declaration, n)
		for i := range n {
			decls[i] = Decl("a", schemaddm.KindConfiguration, fmt.Sprintf(`{"Echo":"%d"}`, i%3))
		}
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				if _, err := s.PutDeclaration(ctx, decls[i]); err != nil {
					t.Errorf("put %d: %v", i, err)
				}
				if _, err := s.GetDeclaration(ctx, "a"); err != nil {
					t.Errorf("get %d: %v", i, err)
				}
			})
		}
		wg.Wait()
		got, err := s.GetDeclaration(ctx, "a")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetDeclarationVersion(ctx, "a", got.ServerToken); err != nil {
			t.Fatalf("current version missing: %v", err)
		}
		for _, d := range decls {
			if _, err := s.GetDeclarationVersion(ctx, "a", d.ServerToken); err != nil {
				t.Fatalf("version %s missing: %v", d.ServerToken, err)
			}
		}
	})

	t.Run("ParallelAssign", func(t *testing.T) {
		s := newStore(t)
		put(t, s, Decl("a", schemaddm.KindConfiguration, `{}`))
		putSet(t, s, "s")
		addMember(t, s, "s", "a")
		const n = 16
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				id := Device(i + 1)
				if _, err := s.AssignSet(ctx, id, "s", t0); err != nil {
					t.Errorf("assign set %d: %v", i, err)
				}
				err := s.Update(ctx, func(tx ddm.Tx) error {
					if _, err := tx.AssignDeclaration(ctx, id, "a", t0); err != nil {
						return err
					}
					return tx.RecordChanges(ctx, []mdm.EnrollmentID{id}, "r", t0)
				})
				if err != nil {
					t.Errorf("update %d: %v", i, err)
				}
				if _, err := s.StaticDeclarations(ctx, id); err != nil {
					t.Errorf("static %d: %v", i, err)
				}
				if _, err := s.AffectedEnrollments(ctx, []string{"a"}, nil); err != nil {
					t.Errorf("affected %d: %v", i, err)
				}
			})
		}
		wg.Wait()
		r, err := s.SetEnrollments(ctx, "s", storage.Page{})
		if err != nil || len(r.Items) != n {
			t.Fatalf("set enrollments: %d %v", len(r.Items), err)
		}
		affected, err := s.AffectedEnrollments(ctx, []string{"a"}, nil)
		if err != nil || len(affected) != n {
			t.Fatalf("affected: %d %v", len(affected), err)
		}
		if rows := pending(t, s, t0, 0); len(rows) != n {
			t.Fatalf("pending: %d", len(rows))
		}
	})
}
