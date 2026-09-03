package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/service"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// enrollMac enrols a macOS device with a product name so support metadata
// resolves an OS, then a user channel for each user.
func enrollMac(t *testing.T, h *harness, udid string, users ...string) {
	t.Helper()
	ctx := context.Background()
	msg := checkinPlist(t, map[string]any{"MessageType": "Authenticate", "Topic": "com.apple.mgmt.t", "UDID": udid, "Model": "Mac", "ModelName": "MacBook", "DeviceName": "d", "SerialNumber": "S1", "ProductName": "Mac16,1", "OSVersion": "15.1"})
	if _, err := h.core.Checkin(ctx, req(h.cert), msg); err != nil {
		t.Fatal(err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, udid, nil)); err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, udid, map[string]any{"UserID": u, "UserShortName": u, "UserLongName": u})); err != nil {
			t.Fatalf("user %s: %v", u, err)
		}
	}
}

func userID(udid, u string) mdm.EnrollmentID {
	return mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: udid + ":" + u, ParentID: udid}
}

func newCmd(t *testing.T, payload commands.Command) *mdm.Command {
	t.Helper()
	c, err := mdm.NewCommand(payload)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEnqueue(t *testing.T) {
	ctx := context.Background()
	t.Run("ChannelValidatedAgainstMetadata", func(t *testing.T) {
		h := newHarness(t, service.Config{})
		enrollMac(t, h, "D1", "alice")
		dev, usr := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}, userID("D1", "alice")
		res, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{dev, usr}, newCmd(t, &commands.ProfileList{}), storage.EnqueueOptions{})
		if err != nil || len(res.Queued) != 2 || len(res.Skipped) != 0 {
			t.Fatalf("ProfileList to both channels: %+v %v", res, err)
		}
	})
	t.Run("DeviceOnlyToUserRejected", func(t *testing.T) {
		h := newHarness(t, service.Config{})
		enrollMac(t, h, "D1", "alice")
		dev, usr := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}, userID("D1", "alice")
		// DeviceConfigured is a device channel command on macOS.
		res, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{dev, usr}, newCmd(t, &commands.DeviceConfigured{}), storage.EnqueueOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Queued) != 1 || res.Queued[0] != dev {
			t.Fatalf("queued = %v, want the device only", res.Queued)
		}
		if serr := res.Skipped[usr]; !errors.Is(serr, service.ErrUnsupportedTarget) || !strings.Contains(serr.Error(), "user channel") {
			t.Fatalf("user skip = %v", serr)
		}
		if next, _ := h.store.Next(ctx, usr, false, h.clock.Now()); next != nil {
			t.Fatal("device-only command reached the user channel")
		}
		// Every target unsupported: nothing is queued and the store is not asked.
		res, err = h.core.Enqueue(ctx, []mdm.EnrollmentID{usr}, newCmd(t, &commands.DeviceConfigured{}), storage.EnqueueOptions{})
		if err != nil || len(res.Queued) != 0 || len(res.Skipped) != 1 {
			t.Fatalf("all unsupported: %+v %v", res, err)
		}
		// Validation can be switched off.
		off := false
		h2 := newHarness(t, service.Config{ValidateTargets: &off})
		enrollMac(t, h2, "D1", "alice")
		if res, err := h2.core.Enqueue(ctx, []mdm.EnrollmentID{usr}, newCmd(t, &commands.DeviceConfigured{}), storage.EnqueueOptions{}); err != nil || len(res.Queued) != 1 {
			t.Fatalf("validation off: %+v %v", res, err)
		}
	})
	t.Run("SharedIPadOnly", func(t *testing.T) {
		h := newHarness(t, service.Config{})
		ipad := checkinPlist(t, map[string]any{"MessageType": "Authenticate", "Topic": "com.apple.mgmt.t", "UDID": "P1", "Model": "iPad", "ModelName": "iPad", "DeviceName": "d", "SerialNumber": "S2", "ProductName": "iPad14,1", "OSVersion": "18.0"})
		if _, err := h.core.Checkin(ctx, req(h.cert), ipad); err != nil {
			t.Fatal(err)
		}
		if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "P1", nil)); err != nil {
			t.Fatal(err)
		}
		dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "P1"}
		// Not a Shared iPad yet: LogOutUser only applies to Shared iPad.
		res, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{dev}, newCmd(t, &commands.LogOutUser{}), storage.EnqueueOptions{})
		if err != nil || len(res.Queued) != 0 || !errors.Is(res.Skipped[dev], service.ErrUnsupportedTarget) {
			t.Fatalf("LogOutUser on a plain iPad: %+v %v", res, err)
		}
		// The logged-in user channel makes it a Shared iPad.
		if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "P1", map[string]any{"UserID": mdm.SharedIPadUserID, "UserShortName": "student", "UserLongName": "Student"})); err != nil {
			t.Fatal(err)
		}
		res, err = h.core.Enqueue(ctx, []mdm.EnrollmentID{dev}, newCmd(t, &commands.LogOutUser{}), storage.EnqueueOptions{})
		if err != nil || len(res.Queued) != 1 {
			t.Fatalf("LogOutUser on a Shared iPad: %+v %v", res, err)
		}
	})
}

func TestSharedIPad(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, service.Config{})
	ipad := checkinPlist(t, map[string]any{"MessageType": "Authenticate", "Topic": "com.apple.mgmt.t", "UDID": "P2", "Model": "iPad", "ModelName": "iPad", "DeviceName": "d", "SerialNumber": "S3", "ProductName": "iPad14,1", "OSVersion": "18.0"})
	if _, err := h.core.Checkin(ctx, req(h.cert), ipad); err != nil {
		t.Fatal(err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "P2", nil)); err != nil {
		t.Fatal(err)
	}
	t.Run("LoggedInUserChannel", func(t *testing.T) {
		if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "P2", map[string]any{"UserID": mdm.SharedIPadUserID, "UserShortName": "student", "UserLongName": "Student One"})); err != nil {
			t.Fatal(err)
		}
		id := mdm.EnrollmentID{Channel: mdm.ChannelSharedIPadUser, ID: "P2:student", ParentID: "P2"}
		e, err := h.store.Get(ctx, id)
		if err != nil || !e.Enabled || e.UserShortName != "student" || e.UserLongName != "Student One" {
			t.Fatalf("shared iPad user = %+v %v", e, err)
		}
	})
	t.Run("DeviceScopedCommands", func(t *testing.T) {
		dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "P2"}
		usr := mdm.EnrollmentID{Channel: mdm.ChannelSharedIPadUser, ID: "P2:student", ParentID: "P2"}
		// UserList is a device channel command; it never goes to the user channel.
		res, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{dev, usr}, newCmd(t, &commands.UserList{}), storage.EnqueueOptions{})
		if err != nil || len(res.Queued) != 1 || res.Queued[0] != dev || !errors.Is(res.Skipped[usr], service.ErrUnsupportedTarget) {
			t.Fatalf("UserList: %+v %v", res, err)
		}
	})
}

func TestCheckOut(t *testing.T) {
	t.Run("UserChannel", func(t *testing.T) {
		ctx := context.Background()
		h := newHarness(t, service.Config{})
		enrollMac(t, h, "D3", "alice", "bob")
		if _, err := h.core.Checkin(ctx, req(h.cert), checkinPlist(t, map[string]any{"MessageType": "CheckOut", "Topic": "com.apple.mgmt.t", "UDID": "D3", "UserID": "alice"})); err != nil {
			t.Fatal(err)
		}
		for id, want := range map[mdm.EnrollmentID]bool{{Channel: mdm.ChannelDevice, ID: "D3"}: true, userID("D3", "alice"): false, userID("D3", "bob"): true} {
			e, err := h.store.Get(ctx, id)
			if err != nil || e.Enabled != want {
				t.Fatalf("%s enabled = %v, want %v (%v)", id.ID, e.Enabled, want, err)
			}
		}
	})
}

func TestUserAuthenticatePolicy(t *testing.T) {
	ctx := context.Background()
	ua := func(udid, user string) *mdm.Checkin {
		return checkinPlist(t, map[string]any{"MessageType": "UserAuthenticate", "UDID": udid, "UserID": user})
	}
	t.Run("DefaultAccepts", func(t *testing.T) {
		h := newHarness(t, service.Config{})
		enrollMac(t, h, "D4")
		res, err := h.core.Checkin(ctx, req(h.cert), ua("D4", "alice"))
		if err != nil || res == nil || len(res.Body) == 0 {
			t.Fatalf("default policy: %+v %v", res, err)
		}
	})
	t.Run("AcceptEmpty", func(t *testing.T) {
		h := newHarness(t, service.Config{UserAuthenticate: func(context.Context, *mdm.Request, *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error) {
			return &mdm.UserAuthenticateResponse{}, nil
		}})
		enrollMac(t, h, "D4")
		if _, err := h.core.Checkin(ctx, req(h.cert), ua("D4", "alice")); err != nil {
			t.Fatal(err)
		}
		if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "D4", map[string]any{"UserID": "alice", "UserShortName": "alice", "UserLongName": "Alice"})); err != nil {
			t.Fatalf("user channel after empty accept: %v", err)
		}
	})
	t.Run("Digest", func(t *testing.T) {
		h := newHarness(t, service.Config{UserAuthenticate: func(context.Context, *mdm.Request, *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error) {
			challenge := "Digest realm=\"mdm\""
			return &mdm.UserAuthenticateResponse{DigestChallenge: &challenge}, nil
		}})
		enrollMac(t, h, "D4")
		res, err := h.core.Checkin(ctx, req(h.cert), ua("D4", "alice"))
		if err != nil || !strings.Contains(string(res.Body), "DigestChallenge") {
			t.Fatalf("digest challenge: %s %v", res.Body, err)
		}
	})
	t.Run("Decline410", func(t *testing.T) {
		h := newHarness(t, service.Config{UserAuthenticate: func(context.Context, *mdm.Request, *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error) {
			return nil, service.ErrUserNotManaged
		}})
		enrollMac(t, h, "D4")
		if _, err := h.core.Checkin(ctx, req(h.cert), ua("D4", "alice")); service.CodeOf(err) != service.CodeGone {
			t.Fatalf("decline = %v, want CodeGone", err)
		}
	})
	t.Run("TokenGatesTokenUpdate", func(t *testing.T) {
		h := newHarness(t, service.Config{RequireUserAuth: true})
		enrollMac(t, h, "D4")
		id := userID("D4", "alice")
		tu := tokenUpdate(t, "D4", map[string]any{"UserID": "alice", "UserShortName": "alice", "UserLongName": "Alice"})
		if _, err := h.core.Checkin(ctx, req(h.cert), tu); service.CodeOf(err) != service.CodeForbidden || !errors.Is(err, service.ErrUserAuthRequired) {
			t.Fatalf("TokenUpdate before UserAuthenticate = %v, want forbidden", err)
		}
		if err := h.store.StoreUserAuthChallenge(ctx, id, "nonce", nil, h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.core.Checkin(ctx, req(h.cert), tu); service.CodeOf(err) != service.CodeForbidden {
			t.Fatalf("challenge without token = %v, want forbidden", err)
		}
		if err := h.store.StoreUserAuthToken(ctx, id, "tok", nil, h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.core.Checkin(ctx, req(h.cert), tu); err != nil {
			t.Fatalf("TokenUpdate after UserAuthenticate: %v", err)
		}
		// Shared iPad users never UserAuthenticate: exempt.
		if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "D4", map[string]any{"UserID": mdm.SharedIPadUserID, "UserShortName": "s", "UserLongName": "S"})); err != nil {
			t.Fatalf("shared iPad exempt: %v", err)
		}
	})
}
