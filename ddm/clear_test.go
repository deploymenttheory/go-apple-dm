package ddm_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/ddm/ddmtest"
	ddminmem "github.com/deploymenttheory/go-apple-dm/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/service"
	"github.com/deploymenttheory/go-apple-dm/storage"
	storeinmem "github.com/deploymenttheory/go-apple-dm/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/storage/storagetest"
)

// seed gives id a set, a direct assignment, a snapshot, status, and a
// pending change so a clear has everything to remove.
func seed(t *testing.T, h *harness, id mdm.EnrollmentID) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.engine.AssignSet(ctx, id, "lab"); err != nil {
		t.Fatal(err)
	}
	h.assign(id, "com.example.direct")
	if _, err := h.engine.Manifest(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.Status(ctx, id, report(t, boolp(true), declarationsItem(nil, []map[string]any{row("com.example.cfg", "t", true, "valid")}), nil)); err != nil {
		t.Fatal(err)
	}
}

// state summarises what the engine still holds for id.
func state(t *testing.T, h *harness, id mdm.EnrollmentID) string {
	t.Helper()
	ctx := context.Background()
	sets, err := h.engine.EnrollmentSets(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	decls, err := h.engine.EnrollmentDeclarations(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := "snapshot"
	if _, err := h.store.Snapshot(ctx, id); errors.Is(err, ddm.ErrNotFound) {
		snapshot = "-"
	} else if err != nil {
		t.Fatal(err)
	}
	rows, err := h.engine.DeclarationStatus(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := h.engine.StatusValues(ctx, id, ddm.StatusValueQuery{}, paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, c := range h.pending() {
		if c.ID == id {
			pending++
		}
	}
	return strings.Join([]string{
		"sets=" + strings.Join(sets, "+"), "decls=" + strings.Join(decls, "+"), snapshot,
		"rows=" + itoa(len(rows)), "values=" + itoa(len(vals.Items)), "pending=" + itoa(pending),
	}, " ")
}

func itoa(n int) string { return string(rune('0' + n)) }

const (
	seeded  = "sets=lab decls=com.example.direct snapshot rows=1 values=1 pending=2"
	cleared = "sets= decls= - rows=0 values=0 pending=0"
)

func clearHarness(t *testing.T, opts ...func(*ddm.Config)) *harness {
	t.Helper()
	h := newHarness(t, opts...)
	ctx := context.Background()
	h.put(configTest("com.example.cfg", "hi"))
	h.put(configTest("com.example.direct", "hi"))
	if _, err := h.engine.PutSet(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.AddToSet(ctx, "lab", "com.example.cfg"); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestClearEnrollment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("RemovesState", func(t *testing.T) {
		t.Parallel()
		h := clearHarness(t)
		dev, other := ddmtest.Device(1), ddmtest.Device(2)
		seed(t, h, dev)
		seed(t, h, other)
		if got := state(t, h, dev); got != seeded {
			t.Fatalf("seeded state %q", got)
		}
		if err := h.engine.ClearEnrollment(ctx, dev); err != nil {
			t.Fatal(err)
		}
		if got := state(t, h, dev); got != cleared {
			t.Fatalf("after clear %q", got)
		}
		if got := state(t, h, other); got != seeded {
			t.Fatalf("other enrollment touched: %q", got)
		}
		// Declarations and sets themselves survive.
		if _, err := h.engine.GetDeclaration(ctx, "com.example.direct"); err != nil {
			t.Fatal(err)
		}
		if members, err := h.engine.SetDeclarations(ctx, "lab"); err != nil || len(members) != 1 {
			t.Fatalf("set members %v, %v", members, err)
		}
		// Clearing an enrollment the engine never saw is not an error.
		if err := h.engine.ClearEnrollment(ctx, ddmtest.Device(9)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("InvalidID", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		if err := h.engine.ClearEnrollment(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice}); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
	})
	t.Run("StoreFailure", func(t *testing.T) {
		t.Parallel()
		failing := &ddmtest.Failing{Store: ddminmem.New(), Fail: map[string]error{"ClearEnrollment": errBoom}}
		h := newHarness(t, func(c *ddm.Config) { c.Store = failing })
		if err := h.engine.ClearEnrollment(ctx, ddmtest.Device(1)); !errors.Is(err, errBoom) {
			t.Fatalf("ClearEnrollment: %v", err)
		}
	})
}

// enrollments builds an MDM store holding DEVICE-01 with user channels u1
// and u2, plus DEVICE-02.
func enrollments(t *testing.T) *storeinmem.Store {
	t.Helper()
	st := storeinmem.New()
	for _, id := range []mdm.EnrollmentID{ddmtest.Device(1), ddmtest.User(1, "u1"), ddmtest.User(1, "u2"), ddmtest.Device(2)} {
		if err := st.UpsertAuthenticate(context.Background(), id, &checkin.Authenticate{MessageType: "Authenticate"}, nil, t0); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestServiceHook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dev, u1, u2, other := ddmtest.Device(1), ddmtest.User(1, "u1"), ddmtest.User(1, "u2"), ddmtest.Device(2)
	all := []mdm.EnrollmentID{dev, u1, u2, other}
	call := func(op string, id mdm.EnrollmentID) *service.Call {
		return &service.Call{Op: op, Request: &mdm.Request{ID: id}}
	}
	setup := func(t *testing.T, store storage.EnrollmentStore) (*harness, *ddm.ServiceHook) {
		t.Helper()
		h := clearHarness(t)
		for _, id := range all {
			seed(t, h, id)
		}
		return h, ddm.NewServiceHook(h.engine, store, nil)
	}
	expect := func(t *testing.T, h *harness, want map[mdm.EnrollmentID]string) {
		t.Helper()
		for _, id := range all {
			if got := state(t, h, id); got != want[id] {
				t.Errorf("%s: %q, want %q", id.ID, got, want[id])
			}
		}
	}
	t.Run("CheckOutClears", func(t *testing.T) {
		t.Parallel()
		h, hook := setup(t, enrollments(t))
		hook.After(ctx, call("checkin:CheckOut", dev), nil)
		expect(t, h, map[mdm.EnrollmentID]string{dev: cleared, u1: cleared, u2: cleared, other: seeded})
	})
	t.Run("ReauthenticateClearsUserChannels", func(t *testing.T) {
		t.Parallel()
		h, hook := setup(t, enrollments(t))
		hook.After(ctx, call("checkin:Authenticate", dev), nil)
		expect(t, h, map[mdm.EnrollmentID]string{dev: cleared, u1: cleared, u2: cleared, other: seeded})
		if got := h.logs.String(); strings.Contains(got, "WARN") {
			t.Fatalf("unexpected warnings: %s", got)
		}
	})
	t.Run("UserCheckOutClearsOnlyUser", func(t *testing.T) {
		t.Parallel()
		h, hook := setup(t, enrollments(t))
		hook.After(ctx, call("checkin:CheckOut", u1), nil)
		expect(t, h, map[mdm.EnrollmentID]string{dev: seeded, u1: cleared, u2: seeded, other: seeded})
		hook.After(ctx, call("checkin:Authenticate", u2), nil)
		expect(t, h, map[mdm.EnrollmentID]string{dev: seeded, u1: cleared, u2: cleared, other: seeded})
	})
	t.Run("IgnoresErrors", func(t *testing.T) {
		t.Parallel()
		h, hook := setup(t, enrollments(t))
		hook.After(ctx, call("checkin:CheckOut", dev), errBoom)
		hook.After(ctx, call("checkin:Authenticate", dev), errBoom)
		expect(t, h, map[mdm.EnrollmentID]string{dev: seeded, u1: seeded, u2: seeded, other: seeded})
	})
	t.Run("IgnoresOtherOps", func(t *testing.T) {
		t.Parallel()
		h, hook := setup(t, enrollments(t))
		for _, op := range []string{"checkin:TokenUpdate", "checkin:DeclarativeManagement", "connect", "enqueue", ""} {
			hook.After(ctx, call(op, dev), nil)
		}
		hook.After(ctx, nil, nil)
		hook.After(ctx, &service.Call{Op: "checkin:CheckOut"}, nil)
		if got, err := hook.Before(ctx, call("checkin:CheckOut", dev)); err != nil || got != ctx {
			t.Fatalf("Before = %v, %v", got, err)
		}
		expect(t, h, map[mdm.EnrollmentID]string{dev: seeded, u1: seeded, u2: seeded, other: seeded})
	})
	t.Run("ListFailureLogged", func(t *testing.T) {
		t.Parallel()
		failing := &storagetest.Failing{Store: enrollments(t), Fail: map[string]error{"List": errBoom}}
		h := clearHarness(t)
		for _, id := range all {
			seed(t, h, id)
		}
		var logs bytes.Buffer
		hook := ddm.NewServiceHook(h.engine, failing, slog.New(slog.NewTextHandler(&logs, nil)))
		hook.After(ctx, call("checkin:CheckOut", dev), nil)
		// The device is still cleared; its user channels could not be found.
		expect(t, h, map[mdm.EnrollmentID]string{dev: cleared, u1: seeded, u2: seeded, other: seeded})
		if got := logs.String(); !strings.Contains(got, "list user channels") || !strings.Contains(got, "boom") {
			t.Fatalf("log %q", got)
		}
		// With no logger the engine's own is used.
		quiet := ddm.NewServiceHook(h.engine, failing, nil)
		quiet.After(ctx, call("checkin:CheckOut", other), nil)
		if got := h.logs.String(); !strings.Contains(got, "list user channels") {
			t.Fatalf("engine log %q", got)
		}
	})
	t.Run("ClearFailureLogged", func(t *testing.T) {
		t.Parallel()
		failing := &ddmtest.Failing{Store: ddminmem.New(), Fail: map[string]error{}}
		h := newHarness(t, func(c *ddm.Config) { c.Store = failing })
		failing.Fail["ClearEnrollment"] = errBoom
		hook := ddm.NewServiceHook(h.engine, nil, nil)
		hook.After(ctx, call("checkin:CheckOut", dev), nil)
		if got := h.logs.String(); !strings.Contains(got, "clear enrollment") || !strings.Contains(got, "boom") {
			t.Fatalf("log %q", got)
		}
	})
	t.Run("NoEnrollmentStore", func(t *testing.T) {
		t.Parallel()
		h, hook := setup(t, nil)
		hook.After(ctx, call("checkin:CheckOut", dev), nil)
		expect(t, h, map[mdm.EnrollmentID]string{dev: cleared, u1: seeded, u2: seeded, other: seeded})
	})
	t.Run("PagesUserChannels", func(t *testing.T) {
		t.Parallel()
		// Many user channels span more than one page of the enrollment
		// listing; every page is cleared.
		st := storeinmem.New()
		h := clearHarness(t)
		users := make([]mdm.EnrollmentID, 0, paging.DefaultPageSize+3)
		for i := range paging.DefaultPageSize + 3 {
			users = append(users, ddmtest.User(3, "u"+itoa3(i)))
		}
		for _, id := range append(users, ddmtest.Device(3)) {
			if err := st.UpsertAuthenticate(ctx, id, &checkin.Authenticate{MessageType: "Authenticate"}, nil, t0); err != nil {
				t.Fatal(err)
			}
			if _, err := h.engine.AssignSet(ctx, id, "lab"); err != nil {
				t.Fatal(err)
			}
		}
		ddm.NewServiceHook(h.engine, st, nil).After(ctx, call("checkin:CheckOut", ddmtest.Device(3)), nil)
		for _, id := range append(users, ddmtest.Device(3)) {
			if sets, err := h.engine.EnrollmentSets(ctx, id); err != nil || len(sets) != 0 {
				t.Fatalf("%s not cleared: %v %v", id.ID, sets, err)
			}
		}
	})
}

func itoa3(n int) string {
	return string([]byte{byte('0' + n/100), byte('0' + n/10%10), byte('0' + n%10)})
}
