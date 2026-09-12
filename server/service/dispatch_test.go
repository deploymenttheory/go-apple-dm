package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// Hides extensions to model an existing third-party backend.
type legacyQueue struct{ storage.Store }

type dispatchQueue struct {
	storage.Store
	clearErr, lookupErr error
	afterNext           bool
	cancel              context.CancelFunc
}

func (q *dispatchQueue) Next(
	ctx context.Context,
	id mdm.EnrollmentID,
	skip bool,
	now time.Time,
) (*mdm.Command, error) {
	cmd, err := q.Store.Next(ctx, id, skip, now)
	q.afterNext = true
	return cmd, err
}

func (q *dispatchQueue) Get(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	if q.afterNext && q.lookupErr != nil {
		return nil, q.lookupErr
	}
	return q.Store.Get(ctx, id)
}

func (q *dispatchQueue) ClearCommand(
	ctx context.Context,
	id mdm.EnrollmentID,
	uuid string,
) (int64, error) {
	if q.clearErr != nil {
		return 0, q.clearErr
	}
	n, err := q.Store.(storage.CommandClearer).ClearCommand(ctx, id, uuid)
	if q.cancel != nil {
		q.cancel()
	}
	return n, err
}

func TestDispatchRejectsInvalidStoredCommandWithoutClearingOtherWork(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	enroll(t, h, "D1")
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	bad := newCmd(t, &commands.DeviceInformation{})
	good := newCmd(t, &commands.DeviceInformation{Queries: []string{"OSVersion"}})
	for _, cmd := range []*mdm.Command{bad, good} {
		if _, err := h.store.Enqueue(
			t.Context(),
			[]mdm.EnrollmentID{id},
			cmd,
			storage.EnqueueOptions{},
		); err != nil {
			t.Fatal(err)
		}
	}
	got, err := h.core.Connect(t.Context(), req(h.cert), response(id.ID, "", mdm.StatusIdle))
	if err != nil || got == nil || got.UUID != good.UUID {
		t.Fatalf("delivery: %+v %v", got, err)
	}
	rows, err := h.store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows.Items {
		if row.Command.UUID == bad.UUID && row.State != storage.StateCleared {
			t.Fatal(row)
		}
	}
}

func TestDispatchFailsSafelyWhenQueueCannotDiscardWork(t *testing.T) {
	t.Parallel()
	boom := errors.New("store failure")
	for _, name := range []string{"legacy backend", "clear failure", "lookup failure", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, service.Config{})
			enroll(t, h, "D1")
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
			e, err := h.store.Get(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			e.Device.ProductName, e.Device.OSVersion = "Mac16,1", "10.13"
			if err := h.store.Import(
				t.Context(),
				storage.EnrollmentExport{Enrollment: *e},
			); err != nil {
				t.Fatal(err)
			}
			cmd := newCmd(t, &commands.DeviceLock{Message: new("requires 10.14")})
			if _, err := h.store.Enqueue(
				t.Context(),
				[]mdm.EnrollmentID{id},
				cmd,
				storage.EnqueueOptions{},
			); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wrapper := &dispatchQueue{Store: h.store}
			var backend storage.Store = wrapper
			want := boom
			switch name {
			case "legacy backend":
				backend = &legacyQueue{Store: h.store}
				want = service.ErrUnsupportedTarget
			case "clear failure":
				wrapper.clearErr = boom
			case "lookup failure":
				wrapper.lookupErr = boom
			case "cancelled":
				wrapper.cancel = cancel
				want = context.Canceled
			}
			core, err := service.New(service.Config{Store: backend, Clock: h.clock})
			if err != nil {
				t.Fatal(err)
			}
			got, err := core.Connect(ctx, req(h.cert), response(id.ID, "", mdm.StatusIdle))
			if got != nil || !errors.Is(err, want) {
				t.Fatalf("returned command despite failed revalidation: %+v %v", got, err)
			}
		})
	}
}
