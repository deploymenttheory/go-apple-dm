package inmem_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
)

func TestWorkerStateValidationAndAtomicity(t *testing.T) {
	s := inmem.New()
	ctx := t.Context()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, account := range []string{"one", "two"} {
		if err := s.PutAccount(ctx, &dep.Account{Name: account, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutDevices(ctx, account, []dep.Device{{SerialNumber: "A"}, {SerialNumber: "B"}}, now); err != nil {
			t.Fatal(err)
		}
	}
	state := dep.AssignmentState{Owner: "worker", LeaseUntil: now.Add(time.Minute), NotBefore: now.Add(time.Hour), Failures: 2}
	if err := s.PutAssignmentState(ctx, "one", state); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"mark without account":    func() error { return s.MarkFetched(ctx, "", "new", []string{"A"}) },
		"mark without generation": func() error { return s.MarkFetched(ctx, "one", "", []string{"A"}) },
		"read without account":    func() error { _, err := s.AssignmentState(ctx, ""); return err },
		"write without account":   func() error { return s.PutAssignmentState(ctx, "", state) },
		"negative failures":       func() error { return s.PutAssignmentState(ctx, "one", dep.AssignmentState{Failures: -1}) },
		"lock without account":    func() error { return s.Update(ctx, func(tx dep.Tx) error { return tx.LockAccount(ctx, "") }) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, dep.ErrInvalid) {
				t.Fatalf("got %v, want invalid argument", err)
			}
		})
	}
	if got, err := s.AssignmentState(ctx, "one"); err != nil || got != state {
		t.Fatalf("invalid update changed state: %+v %v", got, err)
	}
	if got, err := s.AssignmentState(ctx, "two"); err != nil || got != (dep.AssignmentState{}) {
		t.Fatalf("state leaked across accounts: %+v %v", got, err)
	}
	if err := s.Update(ctx, func(tx dep.Tx) error { return tx.LockAccount(ctx, "missing") }); !errors.Is(err, dep.ErrNotFound) {
		t.Fatalf("locked missing account: %v", err)
	}
	// A batch containing an unknown device must not mark the earlier valid device.
	if err := s.MarkFetched(ctx, "one", "new", []string{"A", "missing"}); !errors.Is(err, dep.ErrNotFound) {
		t.Fatalf("missing device: %v", err)
	}
	page, err := s.ListDevices(ctx, "one", dep.DeviceQuery{NotSeenInGeneration: "new"}, paging.Page{})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("partial fetch marks committed: %+v %v", page, err)
	}
	if err := s.MarkFetched(ctx, "one", "new", []string{"A"}); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListDevices(ctx, "one", dep.DeviceQuery{NotSeenInGeneration: "new"}, paging.Page{})
	if err != nil || len(page.Items) != 1 || page.Items[0].SerialNumber != "B" {
		t.Fatalf("generation filter: %+v %v", page, err)
	}
	page, err = s.ListDevices(ctx, "two", dep.DeviceQuery{NotSeenInGeneration: "new"}, paging.Page{})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("fetch marks leaked across accounts: %+v %v", page, err)
	}
	if err := s.PutAssignmentState(ctx, "one", dep.AssignmentState{}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AssignmentState(ctx, "one"); err != nil || got != (dep.AssignmentState{}) {
		t.Fatalf("state not cleared: %+v %v", got, err)
	}
}
