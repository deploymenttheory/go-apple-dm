//go:build schema_seed_os_27

package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func seedDevice(t *testing.T, h *harness, product, version string, supervised bool, user bool) mdm.EnrollmentID {
	t.Helper()
	enroll(t, h, "D1")
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	e, err := h.store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	e.Device.ProductName, e.Device.OSVersion = product, version
	e.Capabilities = storage.Capabilities{Supervised: storage.CapabilityFalse, DEP: storage.CapabilityTrue, UserApproved: storage.CapabilityTrue}
	if supervised {
		e.Capabilities.Supervised = storage.CapabilityTrue
	}
	if err := h.store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	if user {
		if _, err := h.core.Checkin(t.Context(), req(h.cert), tokenUpdate(t, "D1", map[string]any{"UserID": "alice", "UserShortName": "alice", "UserLongName": "Alice"})); err != nil {
			t.Fatal(err)
		}
		return userID("D1", "alice")
	}
	return id
}

func TestSeedOS27EnhancedLogCommands(t *testing.T) {
	for _, payload := range []commands.Command{&commands.TriggerEnhancedLogCollection{AppleCareToken: "test-token-normal"}, &commands.CancelEnhancedLogCollection{}} {
		for _, tc := range []struct {
			name, product, version    string
			supervised, user, allowed bool
		}{
			{"ios27", "iPhone17,1", "27.0", true, false, true},
			{"ios26", "iPhone17,1", "26.4", true, false, false},
			{"unsupervised", "iPhone17,1", "27.0", false, false, false},
			{"mac-device", "Mac16,1", "27.0", true, false, false},
			{"mac-user", "Mac16,1", "27.0", true, true, true},
			{"tvos", "AppleTV14,1", "27.0", true, false, true},
			{"vision", "RealityDevice14,1", "27.0", true, false, false},
			{"watch", "Watch7,1", "27.0", true, false, false},
		} {
			for _, resultStatus := range []mdm.Status{mdm.StatusAcknowledged, mdm.StatusError} {
				t.Run(payload.RequestTypeName()+"/"+tc.name+"/"+string(resultStatus), func(t *testing.T) {
					h := newHarness(t, service.Config{})
					id := seedDevice(t, h, tc.product, tc.version, tc.supervised, tc.user)
					cmd := newCmd(t, payload)
					res, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{})
					if err != nil {
						t.Fatal(err)
					}
					if !tc.allowed {
						if len(res.Queued) != 0 || !errors.Is(res.Skipped[id], service.ErrUnsupportedTarget) {
							t.Fatal(res)
						}
						return
					}
					if len(res.Queued) != 1 {
						t.Fatalf("supported target rejected: %+v", res)
					}
					idle := response("D1", "", mdm.StatusIdle)
					idle.ID = id
					got, err := h.core.Connect(t.Context(), req(h.cert), idle)
					if err != nil || got == nil || string(got.Raw) != string(cmd.Raw) {
						t.Fatal(got, err)
					}
					decoded, err := mdm.DecodeCommand(got.Raw)
					if err != nil || decoded.Payload.RequestTypeName() != payload.RequestTypeName() {
						t.Fatal(decoded, err)
					}
					if trigger, ok := decoded.Payload.(*commands.TriggerEnhancedLogCollection); ok && trigger.AppleCareToken != "test-token-normal" {
						t.Fatal("token changed")
					}
					result := response("D1", cmd.UUID, resultStatus)
					result.ID = id
					if _, err := h.core.Connect(t.Context(), req(h.cert), result); err != nil {
						t.Fatal(err)
					}
					rows, err := h.store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{})
					if err != nil || len(rows.Items) != 1 {
						t.Fatal(rows, err)
					}
					want := storage.StateAcknowledged
					if resultStatus == mdm.StatusError {
						want = storage.StateError
					}
					if rows.Items[0].State != want {
						t.Fatal(rows.Items[0].State)
					}
				})
			}
		}
		for _, target := range []support.Target{
			{OS: support.IOS, Version: support.V(27, 0, 0), Channel: support.ChannelDevice, Supervised: true, SharedIPad: true},
			{OS: support.IOS, Version: support.V(27, 0, 0), Channel: support.ChannelUser, Supervised: true, SharedIPad: true},
			{OS: support.IOS, Version: support.V(27, 0, 0), Channel: support.ChannelDevice, Supervised: true, UserEnrollment: true},
		} {
			want := target.SharedIPad && target.Channel == support.ChannelDevice
			if got := commands.Support(payload.RequestTypeName()).Check(target); got.Supported != want {
				t.Fatalf("context %+v: %+v", target, got)
			}
		}
	}
	h := newHarness(t, service.Config{})
	id := seedDevice(t, h, "iPhone17,1", "27.0", true, false)
	if _, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, newCmd(t, &commands.TriggerEnhancedLogCollection{}), storage.EnqueueOptions{}); !errors.Is(err, validation.ErrValidation) {
		t.Fatalf("missing AppleCare token: %v", err)
	}
}

func TestSeedOS27SoftwareUpdateRemoval(t *testing.T) {
	for _, payload := range []commands.Command{&commands.AvailableOSUpdates{}, &commands.ScheduleOSUpdateScan{}, &commands.ScheduleOSUpdate{Updates: []commands.ScheduleOSUpdateUpdates{{InstallAction: "Default", ProductKey: new("test-update")}}}, &commands.OSUpdateStatus{}} {
		for _, version := range []string{"26.4", "27.0"} {
			t.Run(payload.RequestTypeName()+"/"+version, func(t *testing.T) {
				h := newHarness(t, service.Config{})
				id := seedDevice(t, h, "Mac16,1", version, true, false)
				v, err := support.ParseVersion(version)
				if err != nil {
					t.Fatal(err)
				}
				r := commands.Support(payload.RequestTypeName()).Check(support.Target{OS: support.MacOS, Version: v, Channel: support.ChannelDevice, Supervised: true, DEP: true, UserApproved: true})
				if version == "26.4" && (!r.Supported || !r.Deprecated) {
					t.Fatal(r)
				}
				res, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, newCmd(t, payload), storage.EnqueueOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if version == "26.4" {
					if len(res.Queued) != 1 {
						t.Fatal(res)
					}
				} else if r.Supported || len(res.Queued) != 0 || !errors.Is(res.Skipped[id], service.ErrUnsupportedTarget) {
					t.Fatal(res, r)
				}
			})
		}
	}
}

func TestSeedOS27ReturnToServiceRetry(t *testing.T) {
	for _, value := range []*bool{nil, new(false), new(true)} {
		for _, ownToken := range []bool{false, true} {
			h := newHarness(t, service.Config{ReturnToService: func(context.Context, *mdm.Request, *checkin.ReturnToService) (*checkin.ReturnToServiceResponse, error) {
				r := &checkin.ReturnToServiceResponse{ReturnToService: checkin.ReturnToServiceResponseReturnToService{Enabled: true, ShouldRetryEnrollment: value}}
				if ownToken {
					r.ReturnToService.BootstrapToken = []byte("handler")
				}
				return r, nil
			}})
			id := seedDevice(t, h, "iPhone17,1", "27.0", true, false)
			if err := h.store.StoreBootstrapToken(t.Context(), id, []byte("stored"), t0); err != nil {
				t.Fatal(err)
			}
			res, err := h.core.Checkin(t.Context(), req(h.cert), simple(t, "ReturnToService", "D1", nil))
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := plist.Unmarshal(res.Body, &wire); err != nil {
				t.Fatal(err)
			}
			dict, ok := wire["ReturnToService"].(map[string]any)
			if !ok {
				t.Fatal(wire)
			}
			got, present := dict["ShouldRetryEnrollment"]
			if present != (value != nil) || (value != nil && got != *value) {
				t.Fatalf("retry wire value: %+v", dict)
			}
			want := "stored"
			if ownToken {
				want = "handler"
			}
			if string(rtsResponse(t, res.Body).ReturnToService.BootstrapToken) != want {
				t.Fatal("bootstrap token changed")
			}
		}
	}
	entry := checkin.Support("ReturnToService.response.ReturnToService.ShouldRetryEnrollment")
	if entry == nil {
		t.Fatal("missing availability")
	}
	for _, os := range []support.OS{support.IOS, support.MacOS, support.TvOS, support.VisionOS, support.WatchOS} {
		for _, version := range []support.Version{support.V(26, 4, 0), support.V(27, 0, 0)} {
			got := entry.Check(support.Target{OS: os, Version: version, Channel: support.ChannelDevice, Supervised: true, DEP: true})
			want := os == support.IOS && version == support.V(27, 0, 0)
			if got.Supported != want {
				t.Fatalf("%s %v: %+v", os, version, got)
			}
		}
	}
}
