package inmem_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/ddm/ddmtest"
	"github.com/deploymenttheory/go-apple-mdm/ddm/inmem"
	schemaddm "github.com/deploymenttheory/go-apple-mdm/schema/ddm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

func TestContract(t *testing.T) {
	t.Parallel()
	ddmtest.RunAll(t, func(t *testing.T) ddm.Store { return inmem.New() })
}

var (
	errInjected = errors.New("injected")
	t0          = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

// TestUpdateRollbackRestoresState fails a method mid-transaction through
// the Failing wrapper and checks that nothing written before it survived.
func TestUpdateRollbackRestoresState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	inner := inmem.New()
	s := &ddmtest.Failing{Store: inner, Fail: map[string]error{"AssignSet": errInjected}}
	if _, err := s.PutDeclaration(ctx, ddmtest.Decl("keep", schemaddm.KindConfiguration, `{}`)); err != nil {
		t.Fatal(err)
	}
	err := s.Update(ctx, func(tx ddm.Tx) error {
		if _, err := tx.PutDeclaration(ctx, ddmtest.Decl("a", schemaddm.KindConfiguration, `{}`)); err != nil {
			return err
		}
		if _, err := tx.PutSet(ctx, "s", t0); err != nil {
			return err
		}
		if err := tx.DeleteDeclaration(ctx, "keep"); err != nil {
			return err
		}
		_, err := tx.AssignSet(ctx, ddmtest.Device(1), "s", t0)
		return err
	})
	if !errors.Is(err, errInjected) {
		t.Fatalf("Update: %v", err)
	}
	if _, err := inner.GetDeclaration(ctx, "a"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("declaration survived rollback: %v", err)
	}
	if _, err := inner.GetSet(ctx, "s"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("set survived rollback: %v", err)
	}
	if _, err := inner.GetDeclaration(ctx, "keep"); err != nil {
		t.Fatalf("delete survived rollback: %v", err)
	}
}

func TestUpdatePanicRestoresState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := inmem.New()
	if _, err := s.PutSet(ctx, "keep", t0); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic did not propagate")
			}
		}()
		_ = s.Update(ctx, func(tx ddm.Tx) error {
			if _, err := tx.PutSet(ctx, "s", t0); err != nil {
				return err
			}
			if err := tx.DeleteSet(ctx, "keep"); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	// The lock was released and the state is the pre-Update one.
	if _, err := s.GetSet(ctx, "s"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("set survived panic: %v", err)
	}
	if _, err := s.GetSet(ctx, "keep"); err != nil {
		t.Fatalf("delete survived panic: %v", err)
	}
	if err := s.Update(ctx, func(ddm.Tx) error { return nil }); err != nil {
		t.Fatalf("Update after panic: %v", err)
	}
	if err := s.Update(ctx, nil); !errors.Is(err, ddm.ErrInvalid) {
		t.Fatalf("nil callback: %v", err)
	}
}

// TestFailingWrapperPassesThrough runs the whole contract through a Failing
// wrapper with nothing configured to fail, then checks that a configured
// failure fires both outside and inside Update.
func TestFailingWrapperPassesThrough(t *testing.T) {
	t.Parallel()
	ddmtest.RunAll(t, func(t *testing.T) ddm.Store { return &ddmtest.Failing{Store: inmem.New()} })

	ctx := context.Background()
	s := &ddmtest.Failing{Store: inmem.New(), Fail: map[string]error{"ListSets": errInjected, "Update": nil}}
	if _, err := s.ListSets(ctx, storage.Page{}); !errors.Is(err, errInjected) {
		t.Fatalf("outside Update: %v", err)
	}
	err := s.Update(ctx, func(tx ddm.Tx) error {
		_, err := tx.ListSets(ctx, storage.Page{})
		return err
	})
	if !errors.Is(err, errInjected) {
		t.Fatalf("inside Update: %v", err)
	}
	s.Fail["Update"] = errInjected
	if err := s.Update(ctx, func(ddm.Tx) error { return nil }); !errors.Is(err, errInjected) {
		t.Fatalf("Update itself: %v", err)
	}
}
