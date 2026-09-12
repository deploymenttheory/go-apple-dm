//go:build schema_seed_os_27

package service_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func TestSeedOS27MixedFleet(t *testing.T) {
	// Identity policy has separate coverage. This test isolates version and
	// channel routing through one core and one enqueue request for the fleet.
	h := newHarness(t, service.Config{Pinning: service.PinOff})
	var devices, users []mdm.EnrollmentID
	for _, version := range []string{"15.7", "26.4", "27.0"} {
		device := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: version}
		user := mdm.EnrollmentID{
			Channel:  mdm.ChannelUser,
			ID:       version + ":alice",
			ParentID: version,
		}
		for _, id := range []mdm.EnrollmentID{device, user} {
			e := storage.Enrollment{
				ID:      id,
				Enabled: true,
				Device:  storage.DeviceInfo{ProductName: "Mac16,1", OSVersion: version},
				Capabilities: storage.Capabilities{
					Supervised:   storage.CapabilityTrue,
					DEP:          storage.CapabilityTrue,
					UserApproved: storage.CapabilityTrue,
				},
			}
			if err := h.store.Import(
				t.Context(),
				storage.EnrollmentExport{Enrollment: e},
			); err != nil {
				t.Fatal(err)
			}
		}
		devices = append(devices, device)
		users = append(users, user)
	}
	for _, tc := range []struct {
		name    string
		payload commands.Command
		ids     []mdm.EnrollmentID
		allowed []bool
	}{
		{"shared inventory", &commands.DeviceInformation{Queries: []string{"OSVersion"}}, devices, []bool{true, true, true}},
		{"legacy updates", &commands.AvailableOSUpdates{}, devices, []bool{true, true, false}},
		{"OS27 logging", &commands.TriggerEnhancedLogCollection{AppleCareToken: "test-token"}, users, []bool{false, false, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newCmd(t, tc.payload)
			res, err := h.core.Enqueue(t.Context(), tc.ids, cmd, storage.EnqueueOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range tc.ids {
				if !tc.allowed[i] {
					if !errors.Is(res.Skipped[id], service.ErrUnsupportedTarget) {
						t.Fatalf("%s accepted: %+v", id.ID, res)
					}
					continue
				}
				idle := response(id.Device().ID, "", mdm.StatusIdle)
				idle.ID = id
				got, err := h.core.Connect(t.Context(), req(h.cert), idle)
				if err != nil || got == nil || !bytes.Equal(got.Raw, cmd.Raw) {
					t.Fatalf("%s delivery: %+v %v", id.ID, got, err)
				}
				ack := response(id.Device().ID, cmd.UUID, mdm.StatusAcknowledged)
				ack.ID = id
				if next, err := h.core.Connect(
					t.Context(),
					req(h.cert),
					ack,
				); err != nil ||
					next != nil {
					t.Fatalf("%s completion: %+v %v", id.ID, next, err)
				}
			}
		})
	}
}

func TestSeedOS27UpgradeRechecksQueuedCommands(t *testing.T) {
	for _, validate := range []bool{true, false} {
		h := newHarness(t, service.Config{ValidateTargets: new(validate)})
		id := seedDevice(t, h, "Mac16,1", "26.4", true, false)
		inventory := newCmd(t, &commands.DeviceInformation{Queries: []string{"OSVersion"}})
		legacy := newCmd(t, &commands.AvailableOSUpdates{})
		shared := newCmd(t, &commands.ProfileList{})
		for _, cmd := range []*mdm.Command{inventory, legacy, shared} {
			if res, err := h.core.Enqueue(
				t.Context(),
				[]mdm.EnrollmentID{id},
				cmd,
				storage.EnqueueOptions{},
			); err != nil ||
				len(res.Queued) != 1 {
				t.Fatalf("enqueue: %+v %v", res, err)
			}
		}
		if got, err := h.core.Connect(
			t.Context(),
			req(h.cert),
			response(id.ID, "", mdm.StatusIdle),
		); err != nil || got == nil ||
			got.UUID != inventory.UUID {
			t.Fatalf("inventory delivery: %+v %v", got, err)
		}
		raw, err := plist.Marshal(
			map[string]any{
				"UDID":           id.ID,
				"CommandUUID":    inventory.UUID,
				"Status":         "Acknowledged",
				"QueryResponses": map[string]any{"OSVersion": "27.0"},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		ack, err := mdm.DecodeResponse(raw, "DeviceInformation")
		if err != nil {
			t.Fatal(err)
		}
		got, err := h.core.Connect(t.Context(), req(h.cert), ack)
		want := shared.UUID
		if !validate {
			want = legacy.UUID
		}
		if err != nil || got == nil || got.UUID != want {
			t.Fatalf("post-upgrade delivery: %+v %v, want %s", got, err, want)
		}
		rows, err := h.store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows.Items {
			if row.Command.UUID == legacy.UUID && validate &&
				(row.State != storage.StateCleared || row.Result != nil) {
				t.Fatalf(
					"server rejection must retain an audit row without inventing a device result: %+v",
					row,
				)
			}
		}
		rejected := 0
		for _, ev := range h.events {
			if ev.Type == event.CommandRejected {
				rejected++
			}
		}
		if validate && rejected != 1 || !validate && rejected != 0 {
			t.Fatalf("rejections: %d", rejected)
		}
	}
}
