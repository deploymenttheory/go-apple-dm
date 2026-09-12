package service_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func TestEnqueueValidatesWirePayload(t *testing.T) {
	t.Parallel()
	for _, off := range []bool{false, true} {
		h := newHarness(t, service.Config{ValidateTargets: new(!off)})
		enroll(t, h, "D1")
		id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
		bad := newCmd(t, &commands.DeviceInformation{})
		// A valid convenience payload must not conceal invalid wire bytes.
		bad.Payload = &commands.DeviceInformation{Queries: []string{"OSVersion"}}
		res, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, bad, storage.EnqueueOptions{})
		if service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, validation.ErrValidation) || len(res.Queued) != 0 {
			t.Fatalf("invalid payload: %+v %v", res, err)
		}
		for _, bad := range []*mdm.Command{nil, {}, {UUID: "wrong", RequestType: "DeviceInformation", Raw: bad.Raw}, {UUID: bad.UUID, RequestType: "DeviceLock", Raw: bad.Raw}, {UUID: "x", RequestType: "DeviceInformation", Raw: []byte("bad plist")}} {
			if _, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, bad, storage.EnqueueOptions{}); service.CodeOf(err) != service.CodeBadRequest {
				t.Fatalf("invalid envelope: %v", err)
			}
		}
		if next, err := h.store.Next(t.Context(), id, false, t0); err != nil || next != nil {
			t.Fatal("invalid command queued", next, err)
		}
		valid := newCmd(t, &commands.DeviceInformation{Queries: []string{"OSVersion"}})
		valid.Payload = &commands.DeviceInformation{}
		if res, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, valid, storage.EnqueueOptions{}); err != nil || len(res.Queued) != 1 {
			t.Fatalf("wire truth: %+v %v", res, err)
		}
	}
}

func TestEnqueueChecksPresentFieldsPerTarget(t *testing.T) {
	t.Parallel()
	for _, off := range []bool{false, true} {
		h := newHarness(t, service.Config{ValidateTargets: new(!off)})
		var ids []mdm.EnrollmentID
		for _, version := range []string{"10.13", "10.14"} {
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: version}
			if err := h.store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, Device: storage.DeviceInfo{ProductName: "Mac15,3", OSVersion: version}}}); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		cmd := newCmd(t, &commands.DeviceLock{Message: new("Managed device")})
		res, err := h.core.Enqueue(t.Context(), ids, cmd, storage.EnqueueOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if off {
			if len(res.Queued) != 2 {
				t.Fatal(res)
			}
		} else {
			if len(res.Queued) != 1 || res.Queued[0] != ids[1] || !errors.Is(res.Skipped[ids[0]], service.ErrUnsupportedTarget) || !errors.Is(res.Skipped[ids[0]], validation.ErrValidation) {
				t.Fatalf("field boundary: %+v", res)
			}
			if next, err := h.store.Next(t.Context(), ids[0], false, t0); err != nil || next != nil {
				t.Fatal("unsupported target queued", next, err)
			}
		}
	}
}

func TestEnqueueRetainsUnknownCommandExtensions(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	enroll(t, h, "D1")
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	raw, err := plist.Marshal(map[string]any{"CommandUUID": "future", "Command": map[string]any{"RequestType": "FutureCommand", "Extension": "value"}})
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := mdm.DecodeCommand(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil || len(res.Queued) != 1 {
		t.Fatal(res, err)
	}
	got, err := h.core.Connect(t.Context(), req(h.cert), response(id.ID, "", mdm.StatusIdle))
	if err != nil || got == nil || string(got.Raw) != string(raw) {
		t.Fatalf("unknown bytes changed: %v %v", got, err)
	}
}

func TestEnqueueRequiresInventoryForAdvancedCommands(t *testing.T) {
	t.Parallel()
	for _, device := range []storage.DeviceInfo{
		{}, {ProductName: "Mac16,1"}, {ProductName: "Mac16,1", OSVersion: "invalid"},
	} {
		h := newHarness(t, service.Config{})
		id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "unknown"}
		if err := h.store.Import(
			t.Context(),
			storage.EnrollmentExport{
				Enrollment: storage.Enrollment{ID: id, Enabled: true, Device: device},
			},
		); err != nil {
			t.Fatal(err)
		}
		res, err := h.core.Enqueue(
			t.Context(),
			[]mdm.EnrollmentID{id},
			newCmd(t, &commands.ProfileList{}),
			storage.EnqueueOptions{},
		)
		if err != nil || len(res.Queued) != 0 ||
			!errors.Is(res.Skipped[id], service.ErrUnsupportedTarget) {
			t.Fatalf("unknown inventory authorized command: %+v %v", res, err)
		}
		for _, payload := range []commands.Command{&commands.DeviceInformation{Queries: []string{"OSVersion", "ProductName"}}, &commands.SecurityInfo{}} {
			res, err := h.core.Enqueue(
				t.Context(),
				[]mdm.EnrollmentID{id},
				newCmd(t, payload),
				storage.EnqueueOptions{},
			)
			if err != nil || len(res.Queued) != 1 {
				t.Fatalf("inventory bootstrap blocked: %+v %v", res, err)
			}
		}
	}
}
